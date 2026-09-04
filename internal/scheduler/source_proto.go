package scheduler

import (
	"relay/internal/agent/source/perforce"
	"relay/internal/api"
	relayv1 "relay/internal/proto/relayv1"
)

// BaselineHashFromAPISpec computes the workspace baseline hash from an API
// SourceSpec. Returns "" for non-perforce or nil specs. Uses literal sync revs
// (no #head resolution) for server-side estimation.
func BaselineHashFromAPISpec(s *api.SourceSpec) string {
	if s == nil || s.Type != "perforce" {
		return ""
	}
	proto := sourceSpecToProto(s)
	if proto == nil {
		return ""
	}
	return perforce.BaselineHash(proto.GetPerforce(), nil)
}

// SourceKeyFromAPISpec computes the workspace source key from an API
// SourceSpec: the sibling of BaselineHashFromAPISpec, and the server-side
// producer of the string compared against worker_workspaces.source_key.
//
// IT DELEGATES rather than reimplements. The agent registers the key
// perforce.SourceKey produced, so a second implementation here would be a
// silent disagreement between two processes about which workspace a task's
// files are in. Returns "" for non-perforce or nil specs.
func SourceKeyFromAPISpec(s *api.SourceSpec) string {
	if s == nil || s.Type != "perforce" {
		return ""
	}
	proto := sourceSpecToProto(s)
	if proto == nil {
		return ""
	}
	return perforce.SourceKey(proto.GetPerforce())
}

func sourceSpecToProto(s *api.SourceSpec) *relayv1.SourceSpec {
	if s == nil || s.Type != "perforce" {
		return nil
	}
	p := &relayv1.PerforceSource{
		Stream:             s.Stream,
		Unshelves:          s.Unshelves,
		WorkspaceExclusive: s.WorkspaceExclusive,
	}
	for _, e := range s.Sync {
		p.Sync = append(p.Sync, &relayv1.SyncEntry{Path: e.Path, Rev: e.Rev, Exclude: e.Exclude})
	}
	if s.ClientTemplate != nil {
		ct := *s.ClientTemplate
		p.ClientTemplate = &ct
	}
	return &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{Perforce: p}}
}
