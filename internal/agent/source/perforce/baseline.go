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
	type entry struct{ path, rev string }
	es := make([]entry, 0, len(p.Sync))
	for _, e := range p.Sync {
		rev := e.Rev
		if e.Rev == "#head" && resolvedHead != nil {
			if r, ok := resolvedHead[e.Path]; ok {
				rev = r
			}
		}
		es = append(es, entry{e.Path, rev})
	}
	sort.Slice(es, func(i, j int) bool {
		if es[i].path != es[j].path {
			return es[i].path < es[j].path
		}
		return es[i].rev < es[j].rev
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
