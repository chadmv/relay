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
				// 7, NOT 3. FetchAllPages returns the FIRST page's total, and every
				// fixture in this file used to send one constant total on every
				// page - so no test could tell the first page's total from the
				// current one, and the `if first` guard was unpinned: measured,
				// replacing it with an unconditional `total = resp.Total` survived
				// the whole package (mutation GM_G). A server whose count moves
				// between requests is the ordinary case, not an adversarial one:
				// rows are being inserted and deleted while the walk runs.
				Total:      7,
			})
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	got, total, err := FetchAllPages[item](context.Background(), c, "/v1/things", url.Values{}, 0)
	require.NoError(t, err)
	assert.Equal(t, []item{{ID: "a"}, {ID: "b"}, {ID: "c"}}, got)
	assert.EqualValues(t, 3, total, "the FIRST page's total, not page 2's 7")
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
	// `strconv.Quote(resp.NextCursor)` at the REPEATED-CURSOR site survives the
	// whole suite without this test - measured, not assumed (mutation GM7).
	// That is the ONLY site this fixture reaches, because a self-loop stops
	// there; the empty-page site needs its own fixture and has one below.
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

func TestFetchAllPages_OverLongCursorIsTruncatedInTheEmptyPageError(t *testing.T) {
	// The SECOND site that interpolates a server-chosen cursor into a message,
	// and it was the unpinned one. The test above reaches only the
	// repeated-cursor site: its fixture is a self-loop, so that walk never gets
	// as far as the empty-page stop. Measured, swapping quoteCursor for
	// strconv.Quote at the EMPTY-PAGE site alone survived the whole package
	// (mutation GM8) - the same shape as GM7, which was fixed at one of its two
	// sites.
	//
	// TestFetchAllPages_EmptyPageAdvertisingMoreIsAnError's `"CUR-TWO"`
	// assertion is not a substitute: it proves the cursor is QUOTED, and
	// strconv.Quote quotes too. Only an over-long cursor separates the two
	// functions, so the bound has to be pinned at each raise site separately -
	// a helper that truncates proves nothing about a caller that does not call
	// it.
	//
	// Page 1 is non-empty with a SHORT cursor that differs from page 2's, so
	// neither the drained return nor the repeated-cursor stop is a second
	// possible explanation for the error; the "empty page" assertion is the
	// other half of that.
	huge := strings.Repeat("z", 5000)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch calls {
		case 1:
			_, _ = io.WriteString(w, `{"items":[{"id":"a"}],"next_cursor":"CUR-SHORT","total":99}`)
		case 2:
			fmt.Fprintf(w, `{"items":[],"next_cursor":%q,"total":99}`, huge)
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
	assert.Contains(t, err.Error(), "truncated from 5000 bytes")
	assert.NotContains(t, err.Error(), huge, "the whole server-chosen cursor must not reach the message")
	assert.Less(t, len(err.Error()), 500, "the message is bounded even though the cursor is not")
	assert.Nil(t, got)
	assert.Equal(t, 2, calls)
}

// maxListPages is package-global state. A test that shrinks it must NOT call
// t.Parallel(). Neither of the two below does; do not add it.

func TestFetchAllPages_PageCapBoundsTheRequestCount(t *testing.T) {
	original := maxListPages
	defer func() { maxListPages = original }()
	maxListPages = 3

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls > 3 {
			http.Error(w, `{"error":"past the cap"}`, http.StatusInternalServerError)
			return
		}
		// TWO rows per page, not one. With one row per page the fixture had
		// maxListPages == pages == len(out) == 3, so `3 rows collected` could not
		// tell the three apart and the assertion pinned the message's row count
		// only by numeric coincidence: measured, passing maxListPages where the
		// message passes len(out) survived the whole package (mutation GM_F).
		// That mutant tells an operator "10000 rows collected" when 2,000,000
		// were. 2 x 3 = 6 collides with neither of the other two numbers.
		// The total DIFFERS after page 1. The cap message says "the server's
		// first page reported %d", so the sentence is pinned where it is
		// written: a variant that keeps the `if first` guard for the return
		// value but interpolates resp.Total into the message survives
		// TestFetchAllPages_WalksTwoPages, and dies here.
		total := 1
		if calls == 1 {
			total = 9999
		}
		fmt.Fprintf(w, `{"items":[{"id":"a%d"},{"id":"b%d"}],"next_cursor":"CUR-%d","total":%d}`, calls, calls, calls, total)
	}))
	defer srv.Close()

	type item struct {
		ID string `json:"id"`
	}
	c := NewClient(srv.URL, "tok")
	got, _, err := FetchAllPages[item](context.Background(), c, "/v1/things", url.Values{}, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "page cap")
	// The message reports what it has and asserts NEITHER completeness nor
	// incompleteness. T is a bare type parameter with no id, so this package
	// cannot compute the distinct-row count the Python SDK's equivalent message
	// uses, and must not claim one.
	assert.Contains(t, err.Error(), "6 rows collected")
	assert.Contains(t, err.Error(), "9999", "the FIRST page's total, not page 3's 1")
	// The three negatives are the acceptance criterion, one per thing the
	// message must not say. Each quotes wording that IS in the Python SDK's
	// cap message, where a distinct-id count earns it:
	//   "every one was collected"                 - a completeness claim
	//   "N distinct row ids"                      - the count Go cannot compute
	//   "the server may never report it as drained" - blame
	// Copying any of those onto len(out) would be a claim the number cannot
	// support, because a server re-serving a page behind an advancing cursor
	// drives rows-appended and distinct-rows-received apart.
	assert.NotContains(t, err.Error(), "every one")
	assert.NotContains(t, err.Error(), "distinct")
	assert.NotContains(t, err.Error(), "may never")
	assert.Nil(t, got)
	assert.Equal(t, 3, calls)
}

func TestFetchAllPages_UserLimitSatisfiedOnPageTwoByAPageThatRepeatsACursor(t *testing.T) {
	// The userLimit short-circuit stays ABOVE every stop. A caller who asked for
	// 3 rows and has 3 rows has been served.
	//
	// TestFetchAllPages_RespectsUserLimit above LOOKS like it covers this and
	// does not: its userLimit=3 is satisfied on page 1, and neither the
	// repeated-cursor stop nor the cap can fire on request 1. The discriminating
	// case needs the limit satisfied on page 2 or later, by a page that also
	// trips a stop - hence CUR-A on both pages.
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls > 2 {
			http.Error(w, `{"error":"past the stop"}`, http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(w, `{"items":[{"id":"a%d"},{"id":"b%d"}],"next_cursor":"CUR-A","total":99}`, calls, calls)
	}))
	defer srv.Close()

	type item struct {
		ID string `json:"id"`
	}
	c := NewClient(srv.URL, "tok")
	got, _, err := FetchAllPages[item](context.Background(), c, "/v1/things", url.Values{}, 3)
	require.NoError(t, err)
	assert.Equal(t, []item{{ID: "a1"}, {ID: "b1"}, {ID: "a2"}}, got)
	assert.Equal(t, 2, calls)
}
