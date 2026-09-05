package perforce

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"

	relayv1 "relay/internal/proto/relayv1"
)

// SourceKey returns the workspace identity for a Perforce source spec: the
// stream when nothing is excluded, and "x1|<16 hex>|<stream>" otherwise.
//
// THE EXCLUSION SET IS PART OF THE IDENTITY, and this is a precondition of the
// mechanism rather than a choice within it: the have-list preempt writes the
// CLIENT's have-list, which Prepare shares across every task on the stream, so
// a task with a different exclusion set must reach a different workspace or it
// observes files it asked for and did not get.
//
// A SPEC WITH NO EXCLUSIONS PRODUCES TODAY'S KEY BYTE FOR BYTE, which is why
// every existing registry row, worker_workspaces row and allocated short_id
// survives with no migration.
//
// The composite cannot collide with a bare stream: validateSourceSpec requires
// a stream to start with "//" and no legal stream starts with "x1|". The x1 tag
// is a version - a change to the canonicalisation moves to x2 rather than
// silently reusing a key for a different meaning. The 16-hex truncation is the
// same shape BaselineHash uses, so an operator sees one form twice.
//
// IT HASHES THE LITERAL STRINGS, and must keep doing so even though
// jobspec.DepotPathCovers reads "//s/x/C" and "//s/x/C/..." as one subtree. The
// preempt's filespec IS the literal string: `sync -k //c/C@N` marks one file and
// `sync -k //c/C/...@N` marks a subtree, so two specs the validator treats alike
// exclude different things. Folding them onto one key would put both in one
// workspace and let the broader preempt strip files the narrower task asked for
// - the poisoning hazard this key exists to close. Over-partitioning costs a
// workspace and the warm bias; TestSourceKey_ATrailingEllipsisIsPartOfTheString
// is the guard.
//
// KEEP IT SHORT. TestSourceKey_IsBoundedAtTwentyBytesOverTheStream is the guard
// and carries the reason.
func SourceKey(p *relayv1.PerforceSource) string {
	if p == nil {
		return ""
	}
	ex := make([]string, 0, len(p.Sync))
	for _, e := range p.Sync {
		if e.GetExclude() {
			ex = append(ex, e.GetPath())
		}
	}
	if len(ex) == 0 {
		return p.GetStream()
	}
	sort.Strings(ex)
	h := sha256.New()
	prev := ""
	for i, path := range ex {
		if i > 0 && path == prev {
			continue
		}
		prev = path
		h.Write([]byte(path))
		// The terminator is what makes the SET boundary part of the digest.
		// Without it two different exclusion sets whose concatenations coincide
		// hash alike and share one workspace;
		// TestSourceKey_ASetBoundaryIsPartOfTheEncoding is the discriminator.
		h.Write([]byte{0})
	}
	return "x1|" + hex.EncodeToString(h.Sum(nil))[:16] + "|" + p.GetStream()
}
