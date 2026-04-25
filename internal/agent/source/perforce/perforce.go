package perforce

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"relay/internal/agent/source"
	relayv1 "relay/internal/proto/relayv1"
)

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
	reg        *Registry             // cached; loaded lazily
}

// New creates a Provider. cfg.Client may be nil (will use real p4).
func New(cfg Config) *Provider {
	if cfg.Client == nil {
		cfg.Client = NewClient()
	}
	cfg.Hostname = sanitizeHostname(cfg.Hostname)
	return &Provider{cfg: cfg, workspaces: map[string]*Workspace{}}
}

func (p *Provider) Type() string { return "perforce" }

// ListInventory returns all workspaces recorded in the on-disk registry,
// satisfying the source.InventoryLister interface.
func (p *Provider) ListInventory() ([]source.InventoryEntry, error) {
	reg, err := p.loadRegistry()
	if err != nil {
		return nil, err
	}
	reg.mu.Lock()
	defer reg.mu.Unlock()
	out := make([]source.InventoryEntry, 0, len(reg.Workspaces))
	for _, w := range reg.Workspaces {
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
				return nil, fmt.Errorf("resolve head for %s: %w", e.Path, err)
			}
			rev = fmt.Sprintf("@%d", cl)
			resolved[e.Path] = rev
		}
		syncSpecs = append(syncSpecs, e.Path+rev)
		syncPaths = append(syncPaths, e.Path)
	}

	baseline := BaselineHash(pf, resolved)

	// Find or allocate a workspace short_id for this stream.
	existing := reg.GetBySourceKey(pf.Stream)
	var shortID string
	if existing != nil {
		shortID = existing.ShortID
	} else {
		shortID = allocateShortID(pf.Stream, reg)
	}
	wsRoot := filepath.Join(p.cfg.Root, shortID)
	clientName := fmt.Sprintf("relay_%s_%s", p.cfg.Hostname, shortID)

	// Get or create the in-memory Workspace arbitrator.
	p.mu.Lock()
	ws, ok := p.workspaces[shortID]
	if !ok {
		ws = NewWorkspace(shortID)
		p.workspaces[shortID] = ws
	}
	p.mu.Unlock()

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

	// First time: create on-disk dir and p4 client spec.
	if existing == nil {
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
			return nil, fmt.Errorf("create client: %w", err)
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
	cur := reg.Get(shortID)
	needsSync := handle.Mode() == ModeExclusive || (cur != nil && cur.BaselineHash != baseline)

	// Crash-recovery: clean up any relay-owned pending CLs left by a
	// previous agent crash before we sync.
	if needsSync {
		if err := p.recoverOrphanedCLs(ctx, clientName); err != nil {
			progress(fmt.Sprintf("[recover] %v", err))
		}
	}

	if needsSync {
		if err := p.cfg.Client.SyncStream(ctx, syncSpecs, progress); err != nil {
			handle.Release()
			return nil, fmt.Errorf("p4 sync: %w", err)
		}
		if cur != nil {
			cur.BaselineHash = baseline
			cur.LastUsedAt = time.Now()
			reg.Upsert(*cur)
		}
		_ = reg.Save()
	}

	// Unshelves: create a per-task pending CL so Finalize can cleanly revert.
	var pendingCL int64
	if len(pf.Unshelves) > 0 {
		cl, err := p.cfg.Client.CreatePendingCL(ctx, "relay-task-"+taskID)
		if err != nil {
			handle.Release()
			return nil, fmt.Errorf("create pending CL: %w", err)
		}
		pendingCL = cl
		if err := reg.AddPendingCL(shortID, taskID, cl); err != nil {
			handle.Release()
			return nil, err
		}
		_ = reg.Save()
		for _, src := range pf.Unshelves {
			if err := p.cfg.Client.Unshelve(ctx, src, cl); err != nil {
				handle.Release()
				return nil, fmt.Errorf("unshelve %d: %w", src, err)
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

func (p *Provider) recoverOrphanedCLs(ctx context.Context, clientName string) error {
	cls, err := p.cfg.Client.PendingChangesByDescPrefix(ctx, clientName, "relay-task-")
	if err != nil {
		return err
	}
	for _, cl := range cls {
		if err := p.cfg.Client.RevertCL(ctx, cl); err != nil {
			return fmt.Errorf("revert orphan CL %d: %w", cl, err)
		}
		if err := p.cfg.Client.DeleteCL(ctx, cl); err != nil {
			return fmt.Errorf("delete orphan CL %d: %w", cl, err)
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
	revertErr := h.provider.cfg.Client.RevertCL(ctx, h.pendingCL)
	delErr := h.provider.cfg.Client.DeleteCL(ctx, h.pendingCL)
	reg, err := h.provider.loadRegistry()
	if err == nil {
		_ = reg.RemovePendingCL(h.shortID, h.taskID)
		_ = reg.Save()
	}
	if revertErr != nil {
		return fmt.Errorf("revert CL %d: %w", h.pendingCL, revertErr)
	}
	if delErr != nil {
		return fmt.Errorf("delete CL %d: %w", h.pendingCL, delErr)
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
		if !shortIDInUse(reg, candidate, stream) {
			return candidate
		}
	}
	return enc
}

func shortIDInUse(reg *Registry, shortID, sourceKey string) bool {
	for _, w := range reg.Workspaces {
		if w.ShortID == shortID && w.SourceKey != sourceKey {
			return true
		}
	}
	return false
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
