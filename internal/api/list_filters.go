package api

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

// maxFilterQRunes bounds the substring needle in runes, not bytes. strpos can be
// served by no index, so the needle length is one of the few things bounding a
// single request's scan cost.
const maxFilterQRunes = 200

// maxFilterQMessage is derived from the constant so the two cannot drift.
var maxFilterQMessage = fmt.Sprintf("q is too long; maximum %d characters", maxFilterQRunes)

// parseFilterQ validates the ?q= needle every list endpoint that accepts one
// shares. It returns nil when the filter is absent; on invalid input it writes
// the response itself and returns ok=false.
//
// The parameter name is fixed rather than an argument because the two 400 bodies
// spell it, and TestFilterQ_BodiesAreIdenticalAcrossEndpoints compares those
// bodies byte for byte across endpoints - a per-caller name would let them
// diverge. qs is the query string parsePage already parsed: r.URL.Query()
// discards percent-decoding errors, so a second parse can disagree with the one
// that was validated.
func parseFilterQ(w http.ResponseWriter, qs url.Values) (*string, bool) {
	raw := qs.Get("q")
	if raw == "" {
		return nil, true
	}
	// Go's query parser percent-decodes without validating UTF-8, so an invalid
	// byte sequence would otherwise reach Postgres as a text parameter. NUL is
	// handled at the chokepoint, not here.
	if !utf8.ValidString(raw) {
		writeError(w, http.StatusBadRequest, "q is not valid UTF-8")
		return nil, false
	}
	needle := strings.TrimSpace(raw)
	if utf8.RuneCountInString(needle) > maxFilterQRunes {
		writeError(w, http.StatusBadRequest, maxFilterQMessage)
		return nil, false
	}
	// A cleared search box sends q=; empty after trimming means absent. It also
	// keeps an empty needle away from strpos, which returns 1 for one and would
	// match every row.
	if needle == "" {
		return nil, true
	}
	return &needle, true
}
