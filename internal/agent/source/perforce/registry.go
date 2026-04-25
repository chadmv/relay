package perforce

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// WorkspaceEntry describes a single on-disk workspace.
type WorkspaceEntry struct {
	ShortID             string               `json:"short_id"`
	SourceKey           string               `json:"source_key"`
	ClientName          string               `json:"client_name"`
	BaselineHash        string               `json:"baseline_hash"`
	LastUsedAt          time.Time            `json:"last_used_at"`
	OpenTaskChangelists []OpenTaskChangelist `json:"open_task_changelists,omitempty"`
	DirtyDelete         bool                 `json:"dirty_delete,omitempty"`
}

// OpenTaskChangelist tracks an open pending CL created for a specific task.
type OpenTaskChangelist struct {
	TaskID    string `json:"task_id"`
	PendingCL int64  `json:"pending_cl"`
}

// Registry is the in-memory view of .relay-registry.json.
type Registry struct {
	mu         sync.Mutex
	path       string
	Workspaces []WorkspaceEntry `json:"workspaces"`
}

// LoadRegistry reads the registry from disk. Returns an empty Registry if the
// file does not exist (not an error — first run).
func LoadRegistry(path string) (*Registry, error) {
	r := &Registry{path: path}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, r); err != nil {
		return nil, fmt.Errorf("parse registry: %w", err)
	}
	return r, nil
}

// Save writes the registry atomically to disk (temp+rename).
func (r *Registry) Save() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}

// Get returns a pointer to the entry with the given shortID, or nil.
func (r *Registry) Get(shortID string) *WorkspaceEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.Workspaces {
		if r.Workspaces[i].ShortID == shortID {
			return &r.Workspaces[i]
		}
	}
	return nil
}

// GetBySourceKey returns a pointer to the entry matching sourceKey, or nil.
func (r *Registry) GetBySourceKey(sourceKey string) *WorkspaceEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.Workspaces {
		if r.Workspaces[i].SourceKey == sourceKey {
			return &r.Workspaces[i]
		}
	}
	return nil
}

// Upsert inserts or replaces the entry by ShortID.
func (r *Registry) Upsert(e WorkspaceEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.Workspaces {
		if r.Workspaces[i].ShortID == e.ShortID {
			r.Workspaces[i] = e
			return
		}
	}
	r.Workspaces = append(r.Workspaces, e)
}

// Remove deletes the entry by ShortID.
func (r *Registry) Remove(shortID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.Workspaces[:0]
	for _, e := range r.Workspaces {
		if e.ShortID != shortID {
			out = append(out, e)
		}
	}
	r.Workspaces = out
}

// AddPendingCL records a new pending CL for the workspace with the given shortID.
func (r *Registry) AddPendingCL(shortID, taskID string, cl int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.Workspaces {
		if r.Workspaces[i].ShortID == shortID {
			r.Workspaces[i].OpenTaskChangelists = append(
				r.Workspaces[i].OpenTaskChangelists,
				OpenTaskChangelist{TaskID: taskID, PendingCL: cl},
			)
			return nil
		}
	}
	return fmt.Errorf("workspace %s not found", shortID)
}

// RemovePendingCL removes the pending CL entry for the given taskID.
func (r *Registry) RemovePendingCL(shortID, taskID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.Workspaces {
		if r.Workspaces[i].ShortID != shortID {
			continue
		}
		out := r.Workspaces[i].OpenTaskChangelists[:0]
		for _, c := range r.Workspaces[i].OpenTaskChangelists {
			if c.TaskID != taskID {
				out = append(out, c)
			}
		}
		r.Workspaces[i].OpenTaskChangelists = out
		return nil
	}
	return fmt.Errorf("workspace %s not found", shortID)
}

// MarkDirtyDelete sets or clears the DirtyDelete flag on a workspace.
func (r *Registry) MarkDirtyDelete(shortID string, dirty bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.Workspaces {
		if r.Workspaces[i].ShortID == shortID {
			r.Workspaces[i].DirtyDelete = dirty
			return nil
		}
	}
	return fmt.Errorf("workspace %s not found", shortID)
}
