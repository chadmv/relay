package api

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
)

// jobFilters carries the four optional GET /v1/jobs predicates in the exact
// types the generated sqlc Params fields use, so a call site spreads them
// without conversion. The zero value means "no filter active": a nil Q and an
// invalid OwnerID/Since/Until each send SQL NULL, which the predicates read as
// "match everything".
type jobFilters struct {
	Q       *string
	OwnerID pgtype.UUID
	Since   pgtype.Timestamptz
	Until   pgtype.Timestamptz
}

// maxJobFilterQRunes bounds the substring needle in runes, not bytes.
const maxJobFilterQRunes = 200

// maxJobFilterQMessage is derived from the constant so the two cannot drift.
var maxJobFilterQMessage = fmt.Sprintf("q is too long; maximum %d characters", maxJobFilterQRunes)

// jobFilterParams are the four query parameters parseJobFilters reads.
// handleListJobs passes them to rejectRepeatedParams before calling in.
var jobFilterParams = []string{"q", "mine", "since", "until"}

// parseJobFilters produces the four optional GET /v1/jobs predicates. On
// invalid input it writes the response itself and returns ok=false. The caller
// spreads the result into every list and count Params struct on its path; a
// call site that omits a field disables that filter for its arm alone, with no
// error.
//
// qs is the query string parsePage already parsed and arity-checked. Taking
// it as an argument rather than re-reading r.URL.Query() keeps one parse per
// request: r.URL.Query() discards percent-decoding errors, so a second parse
// can disagree with the one that was validated.
func parseJobFilters(w http.ResponseWriter, qs url.Values, u AuthUser) (jobFilters, bool) {
	var f jobFilters

	if raw := qs.Get("q"); raw != "" {
		// Postgres text cannot hold a NUL byte and rejects one with SQLSTATE
		// 22021, so a needle carrying one must be refused here rather than
		// reaching the query as a parameter. utf8.ValidString does not cover
		// it: NUL is valid UTF-8.
		if !utf8.ValidString(raw) || strings.ContainsRune(raw, 0) {
			writeError(w, http.StatusBadRequest, "q is not valid UTF-8")
			return jobFilters{}, false
		}
		needle := strings.TrimSpace(raw)
		if utf8.RuneCountInString(needle) > maxJobFilterQRunes {
			writeError(w, http.StatusBadRequest, maxJobFilterQMessage)
			return jobFilters{}, false
		}
		// A cleared search box sends q=; empty after trimming means absent,
		// matching how status="" is already treated. It also keeps an empty
		// needle away from strpos, which returns 1 for one and would match
		// every row.
		if needle != "" {
			f.Q = &needle
		}
	}

	if raw := qs.Get("mine"); raw != "" {
		b, err := strconv.ParseBool(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid mine; expected true or false")
			return jobFilters{}, false
		}
		if b {
			if !u.ID.Valid {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return jobFilters{}, false
			}
			f.OwnerID = u.ID
		}
	}

	since, ok := parseJobFilterTime(w, qs.Get("since"), "since")
	if !ok {
		return jobFilters{}, false
	}
	f.Since = since

	until, ok := parseJobFilterTime(w, qs.Get("until"), "until")
	if !ok {
		return jobFilters{}, false
	}
	f.Until = until

	// until == since is a legal empty window; only until < since is rejected.
	if f.Since.Valid && f.Until.Valid && f.Until.Time.Before(f.Since.Time) {
		writeError(w, http.StatusBadRequest, "until is earlier than since")
		return jobFilters{}, false
	}

	return f, true
}

// parseJobFilterTime parses one RFC3339 bound. The layout requires an offset
// or Z, so a zone-less timestamp is rejected rather than silently read as UTC.
func parseJobFilterTime(w http.ResponseWriter, raw, name string) (pgtype.Timestamptz, bool) {
	if raw == "" {
		return pgtype.Timestamptz{}, true
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid "+name+"; expected an RFC3339 timestamp")
		return pgtype.Timestamptz{}, false
	}
	return pgtype.Timestamptz{Time: t, Valid: true}, true
}
