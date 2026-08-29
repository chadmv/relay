package relayclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetchAllPages_WalksTwoPages(t *testing.T) {
	type item struct {
		ID string `json:"id"`
	}

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "/v1/things", r.URL.Path)
		require.Equal(t, "200", r.URL.Query().Get("limit"))
		switch calls {
		case 1:
			require.Empty(t, r.URL.Query().Get("cursor"), "first call must have no cursor")
			json.NewEncoder(w).Encode(PageEnvelope[item]{
				Items:      []item{{ID: "a"}, {ID: "b"}},
				NextCursor: "next1",
				Total:      3,
			})
		case 2:
			require.Equal(t, "next1", r.URL.Query().Get("cursor"))
			json.NewEncoder(w).Encode(PageEnvelope[item]{
				Items:      []item{{ID: "c"}},
				NextCursor: "",
				Total:      3,
			})
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	got, total, err := FetchAllPages[item](context.Background(), c, "/v1/things", url.Values{}, 0)
	require.NoError(t, err)
	assert.Equal(t, []item{{ID: "a"}, {ID: "b"}, {ID: "c"}}, got)
	assert.EqualValues(t, 3, total)
	assert.Equal(t, 2, calls)
}

func TestFetchAllPages_RespectsUserLimit(t *testing.T) {
	type item struct {
		ID string `json:"id"`
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(PageEnvelope[item]{
			Items:      []item{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}, {ID: "e"}},
			NextCursor: "more",
			Total:      100,
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	got, total, err := FetchAllPages[item](context.Background(), c, "/v1/things", url.Values{}, 3)
	require.NoError(t, err)
	assert.Len(t, got, 3, "userLimit=3 caps output at 3 even when more available")
	assert.EqualValues(t, 100, total)
}

func TestFetchAllPages_ForwardsParams(t *testing.T) {
	type item struct {
		ID string `json:"id"`
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "running", r.URL.Query().Get("status"))
		json.NewEncoder(w).Encode(PageEnvelope[item]{Total: 0})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	params := url.Values{"status": []string{"running"}}
	_, _, err := FetchAllPages[item](context.Background(), c, "/v1/things", params, 0)
	require.NoError(t, err)
}

// The fixtures below write JSON BYTES. They deliberately do NOT marshal a
// PageEnvelope[T] the way TestFetchAllPages_ForwardsParams above does: a
// fixture encoded through the production envelope type agrees with the decoder
// by construction, on the envelope keys AND on the item fields, and can detect
// drift in neither direction.
//
// Each fixture also has a TERMINATOR - a 500 past the request count the correct
// implementation makes - so a mutant that drops the stop under test fails with
// a transport error instead of looping forever.
//
// The cursor assertions check the QUOTED form (with the double quotes), not the
// bare cursor. `Contains(err, "CUR-TWO")` would pass whether quoteCursor were on
// the message path or not; `Contains(err, "\"CUR-TWO\"")` is what proves it is.

func TestFetchAllPages_EmptyPageAdvertisingMoreIsAnError(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch calls {
		case 1:
			// Page 1 is NON-empty and its cursor DIFFERS from page 2's, so the
			// repeated-cursor stop is not a second possible explanation.
			_, _ = io.WriteString(w, `{"items":[{"id":"a"},{"id":"b"}],"next_cursor":"CUR-ONE","total":99}`)
		case 2:
			_, _ = io.WriteString(w, `{"items":[],"next_cursor":"CUR-TWO","total":99}`)
		default:
			http.Error(w, `{"error":"past the stop"}`, http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	type item struct {
		ID string `json:"id"`
	}
	c := NewClient(srv.URL, "tok")
	got, _, err := FetchAllPages[item](context.Background(), c, "/v1/things", url.Values{}, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty page")
	assert.Contains(t, err.Error(), `"CUR-TWO"`)
	assert.Contains(t, err.Error(), "paginate /v1/things")
	assert.Nil(t, got, "the partial slice is deliberately NOT returned; five renderers have nowhere to put it")
	assert.Equal(t, 2, calls)
}

func TestFetchAllPages_ZeroRowsIsNotAnError(t *testing.T) {
	// The drained return must stay ABOVE the empty-page stop. buildPage returns
	// ([]Out{}, "") for zero rows, so this is what a real list with no matching
	// rows sends, and inverting the order makes `relay list` fail on an empty
	// jobs table.
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = io.WriteString(w, `{"items":[],"next_cursor":"","total":0}`)
	}))
	defer srv.Close()

	type item struct {
		ID string `json:"id"`
	}
	c := NewClient(srv.URL, "tok")
	got, total, err := FetchAllPages[item](context.Background(), c, "/v1/things", url.Values{}, 0)
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.EqualValues(t, 0, total)
	assert.Equal(t, 1, calls)
}

func TestQuoteCursor_BoundsAnOverLongCursor(t *testing.T) {
	short := "eyJ0IjoiMjAyNi0wOC0yOCJ9"
	assert.Equal(t, `"`+short+`"`, quoteCursor(short))

	huge := strings.Repeat("z", 5000)
	quoted := quoteCursor(huge)
	assert.Less(t, len(quoted), 300)
	assert.Contains(t, quoted, "truncated from 5000 bytes")
	assert.NotContains(t, quoted, huge)
}

func TestFetchAllPages_RepeatedCursorIsAnError(t *testing.T) {
	// The repro shape: a server answering the same cursor forever. Membership is
	// tested BEFORE the cursor is recorded, so a self-loop fires on request 2.
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls > 3 {
			http.Error(w, `{"error":"past the stop"}`, http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(w, `{"items":[{"id":"a%d"}],"next_cursor":"CUR-SAME","total":99}`, calls)
	}))
	defer srv.Close()

	type item struct {
		ID string `json:"id"`
	}
	c := NewClient(srv.URL, "tok")
	got, _, err := FetchAllPages[item](context.Background(), c, "/v1/things", url.Values{}, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already requested")
	assert.Contains(t, err.Error(), `"CUR-SAME"`)
	assert.Nil(t, got)
	assert.Equal(t, 2, calls)
}

func TestFetchAllPages_TwoCycleOfCursorsIsAnError(t *testing.T) {
	// THIS is the test that discriminates the seen-SET from a comparison against
	// the immediately previous cursor. Under previous-cursor-only, A,B,A,B never
	// fires and runs to the page cap - 10000 requests and up to 2,000,000
	// retained rows later. Two replicas behind a load balancer with different
	// data, or a caching proxy alternating two cached bodies, produce exactly
	// this.
	cursors := []string{"CUR-A", "CUR-B", "CUR-A"}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls > len(cursors) {
			http.Error(w, `{"error":"past the stop"}`, http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(w, `{"items":[{"id":"a%d"}],"next_cursor":%q,"total":99}`, calls, cursors[calls-1])
	}))
	defer srv.Close()

	type item struct {
		ID string `json:"id"`
	}
	c := NewClient(srv.URL, "tok")
	_, _, err := FetchAllPages[item](context.Background(), c, "/v1/things", url.Values{}, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already requested")
	assert.Contains(t, err.Error(), `"CUR-A"`)
	assert.Equal(t, 3, calls)
}

func TestFetchAllPages_OverLongCursorIsTruncatedInTheWalksError(t *testing.T) {
	// ADDED BEYOND THE PLAN, which recorded that Go has no walk-level
	// truncation test and offered it as four lines if a reviewer wanted it.
	// Taking it, because the gap it leaves is a WIRING gap of exactly the kind
	// this repo has been bitten by before: TestQuoteCursor_BoundsAnOverLongCursor
	// proves the helper truncates, and nothing proves the helper is what the
	// walk calls. A mutant that swaps `quoteCursor(resp.NextCursor)` for
	// `strconv.Quote(resp.NextCursor)` at the two message sites survives the
	// whole suite without this test - measured, not assumed (mutation GM7).
	//
	// The quoted-form assertions in the two tests above are NOT a substitute:
	// they prove the cursor is QUOTED, which strconv.Quote also does. Only an
	// over-long cursor separates the two functions.
	//
	// Self-loop, so this fixture is also a second witness for deleting the
	// repeated-cursor stop - the same relationship Python's T9 has to its M1.
	huge := strings.Repeat("z", 5000)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls > 2 {
			http.Error(w, `{"error":"past the stop"}`, http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(w, `{"items":[{"id":"a%d"}],"next_cursor":%q,"total":99}`, calls, huge)
	}))
	defer srv.Close()

	type item struct {
		ID string `json:"id"`
	}
	c := NewClient(srv.URL, "tok")
	_, _, err := FetchAllPages[item](context.Background(), c, "/v1/things", url.Values{}, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already requested")
	assert.Contains(t, err.Error(), "truncated from 5000 bytes")
	assert.NotContains(t, err.Error(), huge, "the whole server-chosen cursor must not reach the message")
	assert.Less(t, len(err.Error()), 500, "the message is bounded even though the cursor is not")
	assert.Equal(t, 2, calls)
}
