package api

import (
	"net/http"
	"net/url"
	"strconv"
	"time"

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

// jobFilterParams are the four query parameters parseJobFilters reads.
// handleListJobs passes them to rejectRepeatedParams before calling in.
var jobFilterParams = []string{"q", "mine", "since", "until"}

// parseJobFilters produces the four optional GET /v1/jobs predicates. On
// invalid input it writes the response itself and returns ok=false. The caller
// spreads the result into every list and count Params struct on its path; a
// call site that omits a field disables that filter for its arm alone, with no
// error.
//
// qs is the query string parsePage already parsed and arity-checked; see
// parseFilterQ for why it is passed rather than re-read.
func parseJobFilters(w http.ResponseWriter, qs url.Values, u AuthUser) (jobFilters, bool) {
	var f jobFilters

	q, ok := parseFilterQ(w, qs)
	if !ok {
		return jobFilters{}, false
	}
	f.Q = q

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
