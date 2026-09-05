package perforce

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"

	relayv1 "relay/internal/proto/relayv1"
)

// BaselineHash returns a 16-char canonical hash of the resolved sync spec +
// unshelves. If resolvedHead is provided and a sync entry's rev is "#head",
// the resolved value (e.g. "@12345") is used; otherwise the literal "#head"
// is hashed (server-side estimate before sync).
func BaselineHash(p *relayv1.PerforceSource, resolvedHead map[string]string) string {
	if p == nil {
		return ""
	}
	type entry struct {
		path, rev string
		exclude   bool
	}
	es := make([]entry, 0, len(p.Sync))
	for _, e := range p.Sync {
		rev := e.Rev
		if e.Rev == "#head" && resolvedHead != nil {
			if r, ok := resolvedHead[e.Path]; ok {
				rev = r
			}
		}
		es = append(es, entry{e.Path, rev, e.GetExclude()})
	}
	sort.Slice(es, func(i, j int) bool {
		if es[i].path != es[j].path {
			return es[i].path < es[j].path
		}
		if es[i].rev != es[j].rev {
			return es[i].rev < es[j].rev
		}
		// The flag joins the sort key because two entries sharing a path and a
		// rev otherwise sort unstably against each other.
		return !es[i].exclude && es[j].exclude
	})
	us := append([]int64(nil), p.Unshelves...)
	sort.Slice(us, func(i, j int) bool { return us[i] < us[j] })

	h := sha256.New()
	h.Write([]byte(p.Stream))
	h.Write([]byte{0})
	for _, e := range es {
		h.Write([]byte(e.path))
		h.Write([]byte{0})
		h.Write([]byte(e.rev))
		h.Write([]byte{0})
		// WRITTEN ONLY FOR AN EXCLUDED ENTRY, so a spec with no exclusions
		// hashes to the byte sequence it always has. That encoding is a
		// cross-process contract (scheduler.BaselineHashFromAPISpec computes it
		// server-side) and moving it re-syncs every warm workspace in the fleet
		// once; TestBaselineHash_NoExclusionsIsUnchanged is the guard.
		//
		// The marker cannot be mistaken for the start of the next entry's path:
		// every path reaching this function has passed validateSourceSpec's
		// "//" prefix rule, so no path can begin with this byte.
		if e.exclude {
			h.Write([]byte{2})
		}
	}
	h.Write([]byte{1})
	for _, u := range us {
		h.Write([]byte(strconv.FormatInt(u, 10)))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// PathPrefixOverlap reports whether two depot paths could touch the same files.
// Treats trailing "/..." as a wildcard prefix.
func PathPrefixOverlap(a, b string) bool {
	a = strings.TrimSuffix(a, "/...")
	b = strings.TrimSuffix(b, "/...")
	return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}
