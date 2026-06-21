package perforce

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
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

	// Resolve #head to a specific CL number.
	resolved := make(map[string]string, len(pf.Sync))
	syncSpecs := make([]string, 0, len(pf.Sync))
	syncPaths := make([]string, 0, len(pf.Sync))
	for _, e := range pf.Sync {
		rev := e.Rev
		if rev == "#head" {
			cl, err := p.cfg.Client.ResolveHead(ctx, e.Path)
			if err != nil {
				return nil, classifyP4Error(fmt.Errorf("resolve head for %s: %w", e.Path, err))
			}
			rev = fmt.Sprintf("@%d", cl)
			resolved[e.Path] = rev
		}
		syncSpecs = append(syncSpecs, e.Path+rev)
		syncPaths = append(syncPaths, e.Path)
	}

	baseline := BaselineHash(pf, resolved)

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
	p.mu.Lock()
	evicting := p.evicting[shortID]
	p.mu.Unlock()
	if evicting {
		handle.Release()
		return nil, fmt.Errorf("perforce: workspace %s is being evicted", shortID)
	}

	// First time: create on-disk dir and p4 client spec.
	if !found {
		if err := os.MkdirAll(wsRoot, 0o755); err != nil {
			handle.Release()
			return nil, err
		}
		tmpl := ""
		if pf.ClientTemplate != nil {
			tmpl = *pf.ClientTemplate
		}
		if err := p.cfg.Client.CreateStreamClient(ctx, clientName, wsRoot, pf.Stream, tmpl); err != nil {
			handle.Release()
			return nil, classifyP4Error(fmt.Errorf("create client: %w", err))
		}
		reg.Upsert(WorkspaceEntry{
			ShortID:      shortID,
			SourceKey:    pf.Stream,
			ClientName:   clientName,
			BaselineHash: "",
			LastUsedAt:   time.Now(),
		})
		_ = reg.Save()
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
		if err := p.cfg.Client.SyncStream(ctx, wsRoot, clientName, syncSpecs, progress); err != nil {
			handle.Release()
			return nil, classifyP4Error(fmt.Errorf("p4 sync: %w", err))
		}
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
