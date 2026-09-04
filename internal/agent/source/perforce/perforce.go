package perforce

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"relay/internal/agent/source"
	relayv1 "relay/internal/proto/relayv1"
)

// ErrP4BinaryMissing indicates the p4 CLI is not on PATH on this host.
// Returned by (*Provider).Preflight; cmd/relay-agent uses errors.Is to
// recognize it and degrade gracefully.
var ErrP4BinaryMissing = errors.New("p4 binary not found on PATH")

// lookPath is exec.LookPath; overridable in tests via the package-level var
// pattern used elsewhere in this codebase.
var lookPath = exec.LookPath

// prepareAcquireHook, when non-nil, is invoked in Prepare after the pre-Acquire
// evicting pre-check and before ws.Acquire. It exists solely to let tests drive
// a concurrent EvictWorkspace into the gap between the pre-check and the
// acquire/re-check. Production keeps it nil. Package-level var pattern, as with
// lookPath above.
var prepareAcquireHook func(shortID string)

// Config holds constructor parameters for the Perforce provider.
type Config struct {
	Root     string  // RELAY_WORKSPACE_ROOT — directory for all workspaces
	Hostname string  // worker hostname, used in client name; sanitized on New()
	Client   *Client // override for tests; nil → exec real p4
}

// Provider implements source.Provider for Perforce.
type Provider struct {
	cfg        Config
	mu         sync.Mutex
	workspaces map[string]*Workspace // keyed by short_id
	evicting   map[string]bool       // short_ids reserved by an in-flight EvictWorkspace; guarded by mu
	reg        *Registry             // cached; loaded lazily
}

// New creates a Provider. cfg.Client may be nil (will use real p4).
func New(cfg Config) *Provider {
	if cfg.Client == nil {
		cfg.Client = NewClient()
	}
	cfg.Hostname = sanitizeHostname(cfg.Hostname)
	return &Provider{cfg: cfg, workspaces: map[string]*Workspace{}, evicting: map[string]bool{}}
}

func (p *Provider) Type() string { return "perforce" }

// Preflight verifies the agent host is configured for Perforce work.
// Currently checks only that the p4 binary exists on PATH. Does not contact
// the Perforce server, by design — startup must remain fast and offline.
//
// The ctx parameter is currently unused but is part of the signature so
// future preflight checks can be cancellable without a breaking change.
func (p *Provider) Preflight(ctx context.Context) error {
	_ = ctx
	if _, err := lookPath("p4"); err != nil {
		return fmt.Errorf("%w: install Perforce CLI on this worker or unset RELAY_WORKSPACE_ROOT: %v",
			ErrP4BinaryMissing, err)
	}
	return nil
}

// ListInventory returns all workspaces recorded in the on-disk registry,
// satisfying the source.InventoryLister interface.
func (p *Provider) ListInventory() ([]source.InventoryEntry, error) {
	reg, err := p.loadRegistry()
	if err != nil {
		return nil, err
	}
	ws := reg.Snapshot()
	out := make([]source.InventoryEntry, 0, len(ws))
	for _, w := range ws {
		out = append(out, source.InventoryEntry{
			SourceType:   "perforce",
			SourceKey:    w.SourceKey,
			ShortID:      w.ShortID,
			BaselineHash: w.BaselineHash,
			LastUsedAt:   w.LastUsedAt,
		})
	}
	return out, nil
}

func (p *Provider) loadRegistry() (*Registry, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.reg != nil {
		return p.reg, nil
	}
	r, err := LoadRegistry(filepath.Join(p.cfg.Root, ".relay-registry.json"))
	if err != nil {
		return nil, err
	}
	p.reg = r
	return r, nil
}

// Registry returns the single shared on-disk workspace registry instance for
// this provider, loading it on first call. Safe for concurrent use via the
// Registry's internal lock. Used by the sweeper to share state with the
// provider so eviction is immediately visible to subsequent ListInventory
// and Prepare calls.
func (p *Provider) Registry() (*Registry, error) {
	return p.loadRegistry()
}

// Prepare acquires a workspace for the given task and syncs it if needed.
// taskID is used to scope the per-task pending changelist for unshelves.
func (p *Provider) Prepare(ctx context.Context, taskID string, spec *relayv1.SourceSpec, progress func(line string)) (source.Handle, error) {
	pf := spec.GetPerforce()
	if pf == nil {
		return nil, fmt.Errorf("perforce: spec.perforce is nil")
	}

	reg, err := p.loadRegistry()
	if err != nil {
		return nil, err
	}

	// Find or allocate a workspace short_id for this stream.
	existing, found := reg.GetBySourceKey(pf.Stream)
	var shortID string
	if found {
		shortID = existing.ShortID
	} else {
		shortID = allocateShortID(pf.Stream, reg)
	}
	wsRoot := filepath.Join(p.cfg.Root, shortID)
	clientName := fmt.Sprintf("relay_%s_%s", p.cfg.Hostname, shortID)

	// Get or create the in-memory Workspace arbitrator.
	// This check runs ahead of the creation below so a workspace we have already
	// been told is going away costs no p4 work and its client -i cannot race the
	// evictor's client -d. Correctness does not depend on that ordering - the
	// post-Acquire re-check is what partitions the destructive window - but
	// TestEvictWorkspace_PrepareRefusedWhileReserved goes red the moment any p4
	// call moves ahead of this.
	p.mu.Lock()
	if p.evicting[shortID] {
		p.mu.Unlock()
		return nil, fmt.Errorf("perforce: workspace %s is being evicted", shortID)
	}
	ws, ok := p.workspaces[shortID]
	if !ok {
		ws = NewWorkspace(shortID)
		p.workspaces[shortID] = ws
	}
	p.mu.Unlock()

	// The p4 client must exist before any client-scoped call, and head resolution
	// is one: a virtual or import+ remap stream has no depot storage under the
	// stream name, so only the client's view can address it. The create runs on
	// every Prepare, not only the first, which is also what repairs a workspace
	// whose client spec was deleted while its registry row survived;
	// TestProvider_AWarmPrepareStillRewritesTheClientSpec is that guard.
	// Registration goes with creation because nothing else records that the
	// directory and client spec exist; a Prepare that fails between here and
	// ws.Acquire holds no workspace handle and must not try to release one.
	// TestProvider_AResolveHeadFailureOnFirstUseLeavesAReclaimableWorkspace and
	// TestProvider_AFailedPrepareLeavesNoUnregisteredWorkspaceDirectory.

	// A registry row can outlive its content - a crash between sweeper.evict's
	// os.RemoveAll and its reg.Remove, an os.RemoveAll that emptied the directory
	// and then failed on the rmdir itself, or an operator reclaiming disk by hand
	// - and the MkdirAll below silently recreates a missing directory EMPTY. Read
	// the state before destroying the evidence.
	// TestProvider_AWarmEntryWhoseDirectoryIsGoneStillSyncs and
	// TestProvider_AWarmEntryWhoseDirectoryIsEmptyStillSyncs.
	wsRootEmpty := isMissingOrEmptyDir(wsRoot)
	if err := os.MkdirAll(wsRoot, 0o755); err != nil {
		return nil, err
	}
	// The caller-supplied template is applied only when the workspace is cold.
	// Workspaces are keyed on the stream alone and ClientTemplate is in neither
	// that key nor BaselineHash, so two tasks carrying different templates hash
	// identically, are admitted together in ModeShared, and re-applying -t here
	// would let them flip one shared client spec against each other outside the
	// workspace lock - one of them possibly mid-sync.
	// TestProvider_TheClientTemplateIsAppliedOnlyToAColdWorkspace.
	tmpl := ""
	if !found && pf.ClientTemplate != nil {
		tmpl = *pf.ClientTemplate
	}
	if err := p.cfg.Client.CreateStreamClient(ctx, clientName, wsRoot, pf.Stream, tmpl); err != nil {
		return nil, classifyP4Error(fmt.Errorf("create client: %w", err))
	}
	// Upsert replaces the whole struct, so it runs only on the cold path: on a
	// warm one it would drop another task's OpenTaskChangelists and reset
	// BaselineHash to "". Registry.Mutate edits in place, and the warm branch
	// uses it to reconcile the row with what the create above actually did.
	// TestProvider_AWarmPrepareKeepsAnotherTasksOpenChangelist.
	if !found {
		reg.Upsert(WorkspaceEntry{
			ShortID:      shortID,
			SourceKey:    pf.Stream,
			ClientName:   clientName,
			BaselineHash: "",
			LastUsedAt:   time.Now(),
		})
		_ = reg.Save()
	} else {
		_ = reg.Mutate(shortID, func(e *WorkspaceEntry) {
			// clientName is recomputed from the hostname every call; the row
			// records what an earlier Prepare created. The sweeper deletes the
			// recorded name, so a disagreement orphans the client that exists.
			e.ClientName = clientName
			// DirtyDelete means the p4 client is already gone and sweeper.evict
			// must skip client -d. The create above put it back.
			e.DirtyDelete = false
			if wsRootEmpty {
				// Clearing the baseline rather than setting a local flag: it is
				// persisted, so a crash mid-sync still re-syncs, and a second
				// task admitted in ModeShared reads the same "" and syncs too
				// instead of running in the empty tree this one is filling.
				e.BaselineHash = ""
			}
		})
		_ = reg.Save()
	}

	// Resolve #head to a specific CL number.
	//
	// resolved and syncPaths are keyed on the DEPOT path. BaselineHash is a
	// cross-process contract: scheduler.BaselineHashFromAPISpec computes the same
	// function server-side to score warm-workspace affinity, and the coordinator
	// cannot know this agent's hostname or allocated short id, both of which feed
	// clientName. Only syncSpecs - the p4 argv - becomes client-form.
	resolved := make(map[string]string, len(pf.Sync))
	syncSpecs := make([]string, 0, len(pf.Sync))
	syncPaths := make([]string, 0, len(pf.Sync))
	for _, e := range pf.Sync {
		cp, err := toClientPath(clientName, pf.Stream, e.Path)
		if err != nil {
			return nil, err
		}
		rev := e.Rev
		if rev == "#head" {
			cl, err := p.cfg.Client.ResolveHead(ctx, clientName, cp)
			if err != nil {
				// The wrap names the DEPOT path: it is what the operator wrote and
				// can act on, where the argv carries the client path.
				// TestProvider_ANonP4CommandErrorCarryingASpecPathIsNotClassified.
				return nil, classifyP4Error(fmt.Errorf("resolve head for %s: %w", e.Path, err))
			}
			rev = fmt.Sprintf("@%d", cl)
			resolved[e.Path] = rev
		}
		syncSpecs = append(syncSpecs, cp+rev)
		syncPaths = append(syncPaths, e.Path)
	}

	baseline := BaselineHash(pf, resolved)

	if prepareAcquireHook != nil {
		prepareAcquireHook(shortID)
	}

	req := Request{
		BaselineHash:       baseline,
		SyncPaths:          syncPaths,
		Unshelves:          pf.Unshelves,
		WorkspaceExclusive: pf.WorkspaceExclusive,
	}
	handle, err := ws.Acquire(ctx, req)
	if err != nil {
		return nil, err
	}

	// Re-check the eviction reservation now that we hold a workspace handle.
	// EvictWorkspace's holder-check and reservation are one p.mu critical
	// section; ws.Acquire happens-before this re-check. So if an eviction
	// reserved this short ID in the gap between the pre-Acquire check above and
	// this Acquire (when it saw zero holders), we observe it here and back out -
	// releasing the handle so we never sync into a workspace being deleted. If
	// instead our Acquire landed first, EvictWorkspace sees holders > 0 and
	// refuses; exactly one of the two proceeds.
	//
	// It does not follow that the registry entry survived: a sweep that completed
	// entirely inside the window leaves p.evicting clear here. The registry check
	// below is what catches that case.
	p.mu.Lock()
	evicting := p.evicting[shortID]
	p.mu.Unlock()
	if evicting {
		handle.Release()
		return nil, fmt.Errorf("perforce: workspace %s is being evicted", shortID)
	}

	// A sweep that reserved this short id in the window between the registration
	// above and ws.Acquire can have run to completion and released before the
	// re-check read p.evicting, so the re-check passes and the entry is gone.
	// sweeper.evict reaches its reg.Remove only after client -d and os.RemoveAll
	// have both succeeded, so repairing the entry alone would hand the task a
	// WorkingDir that does not exist; refuse instead and let the retry rebuild
	// through the normal path.
	// TestSweeperClaim_ASweepThatCompletesBetweenRegistrationAndAcquireIsRefused.
	if _, ok := reg.Get(shortID); !ok {
		handle.Release()
		return nil, fmt.Errorf("perforce: workspace %s was evicted during prepare", shortID)
	}

	// Trigger recovery and sync when we hold exclusive access OR when the
	// registry shows a baseline mismatch on a shared-mode admit (which
	// happens on first use after a fresh process start, before syncedPaths
	// is populated). Both cases are functionally equivalent to gaining
	// exclusive workspace ownership for the sync operation.
	cur, curOK := reg.Get(shortID)
	needsSync := handle.Mode() == ModeExclusive || (curOK && cur.BaselineHash != baseline)

	// Crash-recovery: clean up any relay-owned pending CLs left by a
	// previous agent crash before we sync.
	if needsSync {
		if err := p.recoverOrphanedCLs(ctx, wsRoot, clientName); err != nil {
			progress(fmt.Sprintf("[recover] %v", err))
		}
	}

	if needsSync {
		// Brackets around the p4 output. The failure line carries NO cause: the
		// cause travels on the returned error, and repeating it here would put it
		// in the log twice in two spellings with nothing saying which is
		// authoritative.
		// TestProvider_ASyncFailureProgressLineDoesNotRepeatTheCause is the guard.
		progress(fmt.Sprintf("[sync] starting: %d path(s)", len(syncSpecs)))
		if err := p.cfg.Client.SyncStream(ctx, wsRoot, clientName, syncSpecs, progress); err != nil {
			// RELEASE FIRST. progress can park until agent shutdown (its flush
			// reaches Runner.send, which only selects on sendCh and the agent
			// context), and anything held across it strands every later task for
			// this stream in Workspace.Acquire.
			// TestProvider_ASyncFailureReleasesTheWorkspaceBeforeItReportsAnything.
			handle.Release()
			progress("[sync] failed; the cause is reported on the task's final status")
			return nil, classifyP4Error(fmt.Errorf("p4 sync: %w", err))
		}
		progress("[sync] complete")
		if curOK {
			_ = reg.Mutate(shortID, func(e *WorkspaceEntry) {
				e.BaselineHash = baseline
				e.LastUsedAt = time.Now()
			})
		}
		_ = reg.Save()
	}

	// Unshelves: create a per-task pending CL so Finalize can cleanly revert.
	var pendingCL int64
	if len(pf.Unshelves) > 0 {
		cl, err := p.cfg.Client.CreatePendingCL(ctx, wsRoot, clientName, "relay-task-"+taskID)
		if err != nil {
			handle.Release()
			return nil, classifyP4Error(fmt.Errorf("create pending CL: %w", err))
		}
		pendingCL = cl
		if err := reg.AddPendingCL(shortID, taskID, cl); err != nil {
			handle.Release()
			return nil, err
		}
		_ = reg.Save()
		for _, src := range pf.Unshelves {
			if err := p.cfg.Client.Unshelve(ctx, wsRoot, clientName, src, cl); err != nil {
				handle.Release()
				return nil, classifyP4Error(fmt.Errorf("unshelve %d: %w", src, err))
			}
		}
	}

	return &perforceHandle{
		provider:     p,
		workspaceDir: wsRoot,
		clientName:   clientName,
		sourceKey:    pf.Stream,
		shortID:      shortID,
		baselineHash: baseline,
		wsHandle:     handle,
		taskID:       taskID,
		pendingCL:    pendingCL,
	}, nil
}

// EvictWorkspace deletes the workspace identified by shortID if it is not in
// use. Under p.mu it verifies the workspace is neither held nor already being
// evicted, then reserves it in p.evicting; the holder-check and the reservation
// are one p.mu critical section. The reservation is held for the whole (slow)
// evict and cleared afterward; the lock is never held across the p4/disk I/O.
//
// This does not by itself fully exclude a concurrent Prepare, because Prepare
// registers its holder under ws.mu (not p.mu): an eviction can reserve in the
// window after Prepare's pre-Acquire check but before Prepare's ws.Acquire. The
// race is closed on the Prepare side, which re-checks p.evicting after Acquire
// and backs out if it lost. Net guarantee: a workspace is never deleted while a
// Prepare holds (or is about to hold) it, and exactly one of the two proceeds.
func (p *Provider) EvictWorkspace(ctx context.Context, shortID string) error {
	// This reservation block is the canonical twin of ReserveForEvict below;
	// they intentionally share the holder-check + p.evicting discipline and the
	// p.mu->ws.mu lock order. They are kept separate (not deduped) only so this
	// path can return the two distinct descriptive errors ("currently in use" vs
	// "already being evicted") that ReserveForEvict collapses to ok==false. If
	// the reservation discipline changes, update both.
	p.mu.Lock()
	if ws, ok := p.workspaces[shortID]; ok {
		ws.mu.Lock()
		held := len(ws.holders) > 0
		ws.mu.Unlock()
		if held {
			p.mu.Unlock()
			return fmt.Errorf("workspace %s is currently in use", shortID)
		}
	}
	if p.evicting[shortID] {
		p.mu.Unlock()
		return fmt.Errorf("workspace %s is already being evicted", shortID)
	}
	p.evicting[shortID] = true
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		delete(p.evicting, shortID)
		p.mu.Unlock()
	}()

	reg, err := p.loadRegistry()
	if err != nil {
		return fmt.Errorf("load registry: %w", err)
	}
	e, ok := reg.Get(shortID)
	if !ok {
		return fmt.Errorf("workspace %s not found in registry", shortID)
	}
	// ListLocked is intentionally omitted: sw.evict (the single-entry path used
	// here) never reads it - only SweepOnce does. The inline holder check above
	// plus the p.evicting reservation are the sole holder gate for this path.
	sw := &Sweeper{
		Root:        p.cfg.Root,
		Reg:         reg,
		Client:      p.cfg.Client,
		OnEvictedCB: p.InvalidateWorkspace,
	}
	return sw.evict(ctx, reg, e)
}

// ReserveForEvict atomically reserves shortID for an in-flight eviction, the
// same way EvictWorkspace does, for the background Sweeper's Claim hook. Under
// p.mu it verifies the workspace is neither held (inline holders re-check) nor
// already being evicted, then sets p.evicting[shortID] and returns a release
// closure that clears it under p.mu. The holder check and the reservation are
// one p.mu critical section, so Prepare's post-Acquire re-check observes the
// reservation and backs out if it loses the race. Lock order is p.mu then
// ws.mu, matching EvictWorkspace and lockedShortIDs. Returns ok=false (and a
// nil release) when the workspace is held or already reserved.
//
// The holder-check + reservation below is the canonical twin of the block in
// EvictWorkspace; keep the two in sync if the reservation discipline changes.
func (p *Provider) ReserveForEvict(shortID string) (func(), bool) {
	p.mu.Lock()
	if ws, ok := p.workspaces[shortID]; ok {
		ws.mu.Lock()
		held := len(ws.holders) > 0
		ws.mu.Unlock()
		if held {
			p.mu.Unlock()
			return nil, false
		}
	}
	if p.evicting[shortID] {
		p.mu.Unlock()
		return nil, false
	}
	p.evicting[shortID] = true
	p.mu.Unlock()

	release := func() {
		p.mu.Lock()
		delete(p.evicting, shortID)
		p.mu.Unlock()
	}
	return release, true
}

// Client returns the underlying Perforce client. Used by the sweeper in relay-agent main.
func (p *Provider) Client() *Client { return p.cfg.Client }

// LockedShortIDs returns the set of workspace short IDs currently held by active tasks.
// This is the public wrapper of lockedShortIDs for use by the sweeper.
func (p *Provider) LockedShortIDs() map[string]bool { return p.lockedShortIDs() }

// InvalidateWorkspace removes a workspace's per-task in-memory state after
// external eviction. Called by the sweeper's OnEvictedCB. The shared registry
// (p.reg) does not need to be nilled because the sweeper now mutates it in
// place via the shared *Registry pointer; this call only clears the per-task
// workspace cache.
func (p *Provider) InvalidateWorkspace(shortID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.workspaces, shortID)
}

// lockedShortIDs returns the set of shortIDs that are currently held by tasks.
func (p *Provider) lockedShortIDs() map[string]bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]bool)
	for id, ws := range p.workspaces {
		ws.mu.Lock()
		if len(ws.holders) > 0 {
			out[id] = true
		}
		ws.mu.Unlock()
	}
	return out
}

func (p *Provider) recoverOrphanedCLs(ctx context.Context, wsRoot, clientName string) error {
	cls, err := p.cfg.Client.PendingChangesByDescPrefix(ctx, wsRoot, clientName, "relay-task-")
	if err != nil {
		return classifyP4Error(err)
	}
	for _, cl := range cls {
		if err := p.cfg.Client.RevertCL(ctx, wsRoot, clientName, cl); err != nil {
			return classifyP4Error(fmt.Errorf("revert orphan CL %d: %w", cl, err))
		}
		if err := p.cfg.Client.DeleteCL(ctx, wsRoot, clientName, cl); err != nil {
			return classifyP4Error(fmt.Errorf("delete orphan CL %d: %w", cl, err))
		}
	}
	return nil
}

type perforceHandle struct {
	provider     *Provider
	workspaceDir string
	clientName   string
	sourceKey    string
	shortID      string
	baselineHash string
	wsHandle     *WorkspaceHandle
	taskID       string
	pendingCL    int64
}

func (h *perforceHandle) WorkingDir() string { return h.workspaceDir }
func (h *perforceHandle) Env() map[string]string {
	return map[string]string{"P4CLIENT": h.clientName}
}
func (h *perforceHandle) Inventory() source.InventoryEntry {
	return source.InventoryEntry{
		SourceType:   "perforce",
		SourceKey:    h.sourceKey,
		ShortID:      h.shortID,
		BaselineHash: h.baselineHash,
		LastUsedAt:   time.Now(),
	}
}

// Finalize reverts and deletes the per-task pending CL (if any), then releases the workspace lock.
func (h *perforceHandle) Finalize(ctx context.Context) error {
	defer h.wsHandle.Release()
	if h.pendingCL == 0 {
		return nil
	}
	revertErr := h.provider.cfg.Client.RevertCL(ctx, h.workspaceDir, h.clientName, h.pendingCL)
	delErr := h.provider.cfg.Client.DeleteCL(ctx, h.workspaceDir, h.clientName, h.pendingCL)
	reg, err := h.provider.loadRegistry()
	if err == nil {
		_ = reg.RemovePendingCL(h.shortID, h.taskID)
		_ = reg.Save()
	}
	if revertErr != nil {
		return classifyP4Error(fmt.Errorf("revert CL %d: %w", h.pendingCL, revertErr))
	}
	if delErr != nil {
		return classifyP4Error(fmt.Errorf("delete CL %d: %w", h.pendingCL, delErr))
	}
	return nil
}

// toClientPath rewrites a depot-form sync path into client syntax so p4 resolves
// it through the client's view. A virtual or import+ remap stream has no depot
// storage under the stream name, so the depot form addresses nothing. The spec
// this receives has passed jobspec.validateSourceSpec, which requires the path
// to equal the stream or sit under it; a path that does not is refused rather
// than rewritten, because emitting it unchanged is the defect this exists to
// fix and synthesizing a tail the operator never wrote is worse.
// TestToClientPath's sharesATextualPrefixButIsNotUnder row is why the prefix
// test is stream+"/".
func toClientPath(clientName, stream, depotPath string) (string, error) {
	if depotPath == stream {
		// //<client> with no wildcard names nothing, so an empty remainder maps
		// to the whole client.
		return "//" + clientName + "/...", nil
	}
	if strings.HasPrefix(depotPath, stream+"/") {
		return "//" + clientName + depotPath[len(stream):], nil
	}
	return "", fmt.Errorf("sync path %q is not under stream %q; this spec did not come through jobspec validation", depotPath, stream)
}

// isMissingOrEmptyDir reports whether path does not exist or holds no entries.
// An unreadable directory reports false: the caller uses this to decide whether
// to force a sync, and a read failure is not evidence that the tree is gone.
//
// Readdirnames(1) rather than os.ReadDir: this runs on every Prepare and a
// populated workspace root is the common case, so reading the whole listing to
// learn it is non-empty scales with the workspace.
func isMissingOrEmptyDir(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return errors.Is(err, os.ErrNotExist)
	}
	defer f.Close()
	names, err := f.Readdirnames(1)
	if errors.Is(err, io.EOF) {
		return true
	}
	if err != nil {
		return false
	}
	return len(names) == 0
}

// allocateShortID returns a short unique ID for a new workspace.
func allocateShortID(stream string, reg *Registry) string {
	sum := sha256.Sum256([]byte(stream))
	enc := strings.ToLower(base32.StdEncoding.EncodeToString(sum[:]))
	enc = strings.TrimRight(enc, "=")
	for n := 6; n <= len(enc); n += 2 {
		candidate := enc[:n]
		if !reg.ShortIDInUse(candidate, stream) {
			return candidate
		}
	}
	return enc
}

func sanitizeHostname(h string) string {
	var b strings.Builder
	for _, r := range h {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if len(out) > 32 {
		out = out[:32]
	}
	return out
}
