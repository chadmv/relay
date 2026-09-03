package api

import (
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// callParseReservationFilters drives parseReservationFilters over a raw query
// string. The arity refusal is the handler's, not this parser's, so a repeated
// parameter is not expressible here; it is pinned through the handler by
// TestListReservations_FilterErrorsAreReachedThroughTheHandler.
func callParseReservationFilters(t *testing.T, rawQuery string) (reservationFilters, bool, *httptest.ResponseRecorder) {
	t.Helper()
	qs, err := url.ParseQuery(rawQuery)
	require.NoError(t, err, "rawQuery must be decodable; malformed input is parsePage's case")
	rec := httptest.NewRecorder()
	f, ok := parseReservationFilters(rec, qs)
	return f, ok, rec
}

func TestParseReservationFilters_AbsentMeansNoFilter(t *testing.T) {
	f, ok, rec := callParseReservationFilters(t, "")
	require.True(t, ok)
	assert.Zero(t, rec.Body.Len(),
		"an absent filter must leave the response untouched; the recorder reports 200 "+
			"by default, so only an empty body distinguishes that from a refusal")
	assert.False(t, f.WorkerID.Valid)
}

func TestParseReservationFilters_EmptyWorkerIDIsAbsent(t *testing.T) {
	f, ok, _ := callParseReservationFilters(t, "worker_id=")
	require.True(t, ok)
	assert.False(t, f.WorkerID.Valid, "?worker_id= with no value must be treated as absent")
}

func TestParseReservationFilters_RejectsANonUUID(t *testing.T) {
	_, ok, rec := callParseReservationFilters(t, "worker_id=not-a-uuid")
	require.False(t, ok)
	assert.Equal(t, 400, rec.Code)
	assert.Equal(t, "invalid worker_id; expected a UUID", errBody(t, rec))
}

// The 400 must render nothing input-derived. handleCreateReservation echoes the
// supplied value into its own 400 body; this one deliberately does not, so a
// caller cannot get arbitrary text reflected back out of a query parameter.
func TestParseReservationFilters_ErrorDoesNotEchoTheInput(t *testing.T) {
	needle := "cafebabe-not-a-uuid-at-all"
	_, ok, rec := callParseReservationFilters(t, "worker_id="+needle)
	require.False(t, ok)
	body := errBody(t, rec)
	assert.NotContains(t, body, needle, "the 400 body must render nothing input-derived")
	assert.Equal(t, "invalid worker_id; expected a UUID", body)
}

func TestParseReservationFilters_AcceptsACanonicalUUID(t *testing.T) {
	const id = "3f0a1b2c-4d5e-4f60-8192-a3b4c5d6e7f8"
	f, ok, _ := callParseReservationFilters(t, "worker_id="+id)
	require.True(t, ok)
	require.True(t, f.WorkerID.Valid)
	assert.Equal(t, id, uuidStr(f.WorkerID))
}
