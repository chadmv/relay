package scheduler

import (
	"relay/internal/api"
	relayv1 "relay/internal/proto/relayv1"
)

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
		p.Sync = append(p.Sync, &relayv1.SyncEntry{Path: e.Path, Rev: e.Rev})
	}
	if s.ClientTemplate != nil {
		ct := *s.ClientTemplate
		p.ClientTemplate = &ct
	}
	return &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{Perforce: p}}
}
