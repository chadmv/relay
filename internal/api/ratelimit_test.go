package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestParseRateLimit(t *testing.T) {
	tests := []struct {
		in       string
		wantN    int
		wantWin  time.Duration
		wantErr  bool
	}{
		{"10:1m", 10, time.Minute, false},
		{"5:30s", 5, 30 * time.Second, false},
		{"100:1h", 100, time.Hour, false},
		{"0:1m", 0, 0, true},     // count must be > 0
		{"10:0s", 0, 0, true},    // window must be > 0
		{"nonsense", 0, 0, true},
		{"10", 0, 0, true},       // missing separator
		{"", 0, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			n, w, err := ParseRateLimit(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("wantErr=%v err=%v", tt.wantErr, err)
			}
			if tt.wantErr {
				return
			}
			if n != tt.wantN || w != tt.wantWin {
				t.Fatalf("got %d,%s want %d,%s", n, w, tt.wantN, tt.wantWin)
			}
		})
	}
}

func TestRateLimit_UnderLimitPasses(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := RateLimit(3, time.Minute)(next)

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("POST", "/x", nil)
		req.RemoteAddr = "10.0.0.1:12345"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: got %d want 200", i+1, rec.Code)
		}
	}
}

func TestRateLimit_OverLimitReturns429WithRetryAfter(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := RateLimit(2, time.Minute)(next)

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", "/x", nil)
		req.RemoteAddr = "10.0.0.1:12345"
		h.ServeHTTP(httptest.NewRecorder(), req)
	}

	req := httptest.NewRequest("POST", "/x", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
	ra := rec.Header().Get("Retry-After")
	if ra == "" {
		t.Fatalf("expected Retry-After header")
	}
	secs, err := strconv.Atoi(ra)
	if err != nil || secs < 1 {
		t.Fatalf("expected positive integer Retry-After, got %q", ra)
	}
}

func TestRateLimit_PerIPIsolation(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := RateLimit(1, time.Minute)(next)

	req1 := httptest.NewRequest("POST", "/x", nil)
	req1.RemoteAddr = "10.0.0.1:12345"
	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("IP1 first: got %d", rec1.Code)
	}

	req2 := httptest.NewRequest("POST", "/x", nil)
	req2.RemoteAddr = "10.0.0.2:12345"
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("IP2 first: got %d", rec2.Code)
	}

	// IP1 second should be blocked
	req1b := httptest.NewRequest("POST", "/x", nil)
	req1b.RemoteAddr = "10.0.0.1:54321"
	rec1b := httptest.NewRecorder()
	h.ServeHTTP(rec1b, req1b)
	if rec1b.Code != http.StatusTooManyRequests {
		t.Fatalf("IP1 second: expected 429, got %d", rec1b.Code)
	}
}

func TestRateLimit_WindowSlides(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := RateLimit(1, 50*time.Millisecond)(next)

	req := httptest.NewRequest("POST", "/x", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	h.ServeHTTP(httptest.NewRecorder(), req)

	// Immediately second should 429
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec2.Code)
	}

	// Wait past the window
	time.Sleep(75 * time.Millisecond)

	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, req)
	if rec3.Code != http.StatusOK {
		t.Fatalf("expected 200 after window slide, got %d", rec3.Code)
	}
}

func TestRateLimit_ConcurrentHitsDontRace(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := RateLimit(100, time.Minute)(next)

	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/x", nil)
			req.RemoteAddr = "10.0.0." + strconv.Itoa(i%10) + ":12345"
			h.ServeHTTP(httptest.NewRecorder(), req)
		}(i)
	}
	wg.Wait()
}

// TestUserRateLimitKey pins three properties of the bucket key, and the two
// fail-closed rows go first because they are the security half.
//
// THE LAST ROW IS THE TRANSPOSITION GUARD. AuthUser.ID and AuthUser.TokenID are
// adjacent fields of the same type, so uuidStr(u.TokenID) compiles and is
// per-token rather than per-user - and a fresh token per login would then be a
// fresh full bucket per login. Giving the two fields different values is what
// makes the assertion positional rather than a type coincidence.
func TestUserRateLimitKey(t *testing.T) {
	userID := pgtype.UUID{Bytes: [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}, Valid: true}
	const userIDStr = "01020304-0506-0708-090a-0b0c0d0e0f10"
	tokenID := pgtype.UUID{Bytes: [16]byte{0xaa, 0xbb, 0xcc, 0xdd}, Valid: true}

	tests := []struct {
		name   string
		u      AuthUser
		want   string
		wantOK bool
	}{
		{"zero AuthUser", AuthUser{}, "", false},
		// Bytes are non-zero and Valid is false: a key function that read Bytes
		// without consulting Valid would render a plausible uuid here.
		{"id present but not Valid", AuthUser{ID: pgtype.UUID{Bytes: [16]byte{1}, Valid: false}}, "", false},
		{"valid id", AuthUser{ID: userID}, userIDStr, true},
		{"key is the user id, not the token id", AuthUser{ID: userID, TokenID: tokenID}, userIDStr, true},
	}
	for _, tt := range tests {
		// t.Run, not a bare loop: a t.Fatalf in one row must not skip the rest.
		t.Run(tt.name, func(t *testing.T) {
			got, ok := userRateLimitKey(tt.u)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("got (%q, %v), want (%q, %v)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// TestUserRateLimit_ARequestWithNoRenderablePrincipalIsRefused pins the
// fail-closed half. This middleware is only correct inside the auth chain, so a
// request reaching it without a principal is a wiring fault; a pass-through
// would be a silent hole and a shared "" bucket would pool every such request
// into one budget.
//
// THE LIMIT IS 10, NOT 1, DELIBERATELY. At a limit of 1 the second request would
// be refused by the arithmetic whatever key it used, and the test would go green
// against the very mutation it exists to kill.
//
// `reached` is asserted, not only the status: a mutant that passes through and
// writes 401 afterwards would still have run the handler.
func TestUserRateLimit_ARequestWithNoRenderablePrincipalIsRefused(t *testing.T) {
	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	h := UserRateLimit(10, time.Minute)(next)

	cases := []struct {
		name string
		with func(context.Context) context.Context
	}{
		{"no AuthUser in context at all", func(ctx context.Context) context.Context { return ctx }},
		{"AuthUser whose id is not Valid", func(ctx context.Context) context.Context {
			return ctxWithUser(ctx, AuthUser{ID: pgtype.UUID{Bytes: [16]byte{9}, Valid: false}})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reached = false
			req := httptest.NewRequest("POST", "/v1/jobs", nil)
			req = req.WithContext(tc.with(req.Context()))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("got %d, want 401", rec.Code)
			}
			if reached {
				t.Fatal("the wrapped handler ran: a request with no renderable principal must be " +
					"refused, never passed through and never bucketed under \"\"")
			}
		})
	}
}

// TestUserRateLimit_TheSameUserFromTwoAddressesSharesOneBucket is the headline
// discriminator: the executable form of the doc comment's claim, and RED against
// the single most likely wrong implementation, which is reusing clientIP.
func TestUserRateLimit_TheSameUserFromTwoAddressesSharesOneBucket(t *testing.T) {
	// StatusAccepted, not StatusOK: 200 is httptest.NewRecorder's DEFAULT, so an
	// assertion of 200 is also satisfied by a middleware that writes nothing and
	// never calls next.
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusAccepted) })
	h := UserRateLimit(1, time.Minute)(next)

	u := AuthUser{ID: pgtype.UUID{Bytes: [16]byte{7}, Valid: true}}

	first := httptest.NewRequest("POST", "/v1/jobs", nil)
	first.RemoteAddr = "10.0.0.1:1111"
	first = first.WithContext(ctxWithUser(first.Context(), u))
	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, first)
	if rec1.Code != http.StatusAccepted {
		t.Fatalf("first request: got %d want 202, the wrapped handler's own code", rec1.Code)
	}

	// A DIFFERENT source address, the same principal. A studio artist moving
	// from a workstation to a laptop, or onto a VPN, must not get a fresh
	// budget: an IPv6 /64 makes that escape unlimited.
	second := httptest.NewRequest("POST", "/v1/jobs", nil)
	second.RemoteAddr = "203.0.113.9:2222"
	second = second.WithContext(ctxWithUser(second.Context(), u))
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, second)
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request from a different address: got %d want 429 - the bucket is keyed on "+
			"the address, not on the principal", rec2.Code)
	}
	if rec2.Header().Get("Retry-After") == "" {
		t.Fatal("a refusal must carry Retry-After")
	}
}

// TestUserRateLimit_TwoUsersFromOneAddressDoNotShareABucket is the mirror
// property and the one an operator feels: a studio behind one office egress is
// not collapsed into a single budget.
//
// THE THIRD REQUEST IS NOT OPTIONAL. Two 200s at a limit of 1 are also what a
// middleware that does nothing produces, so without the third assertion this
// test is vacuous against exactly the implementation it is supposed to describe.
func TestUserRateLimit_TwoUsersFromOneAddressDoNotShareABucket(t *testing.T) {
	// StatusAccepted, not StatusOK: 200 is httptest.NewRecorder's DEFAULT, so an
	// assertion of 200 is also satisfied by a middleware that writes nothing and
	// never calls next.
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusAccepted) })
	h := UserRateLimit(1, time.Minute)(next)

	const sharedAddr = "10.0.0.1:1111"
	alice := AuthUser{ID: pgtype.UUID{Bytes: [16]byte{1}, Valid: true}}
	bob := AuthUser{ID: pgtype.UUID{Bytes: [16]byte{2}, Valid: true}}

	send := func(u AuthUser) int {
		req := httptest.NewRequest("POST", "/v1/jobs", nil)
		req.RemoteAddr = sharedAddr
		req = req.WithContext(ctxWithUser(req.Context(), u))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	if got := send(alice); got != http.StatusAccepted {
		t.Fatalf("alice first: got %d want 202", got)
	}
	if got := send(bob); got != http.StatusAccepted {
		t.Fatalf("bob first, from alice's address: got %d want 202 - one egress must not collapse "+
			"unrelated callers into one bucket", got)
	}
	// The control: the limiter IS running and IS full for alice.
	if got := send(alice); got != http.StatusTooManyRequests {
		t.Fatalf("alice second: got %d want 429 - without this the two 200s above are also what a "+
			"middleware that does nothing produces", got)
	}
}

// TestUserRateLimit_ASustainableRateIsNotRefused is the "a normal submission
// rate is not refused" half of the acceptance criterion, in the only
// non-vacuous form: two requests under a limit of three would be green against a
// limiter that does nothing, while six at this spacing require the window to
// actually slide.
//
// THE TIMING IS SAFE IN ONE DIRECTION ONLY, AND IT IS THE RIGHT ONE. 30ms
// spacing under a 50ms window at limit 2 leaves one hit in the window per
// request. time.Sleep is guaranteed to sleep AT LEAST its duration, so a slow or
// coarse-grained scheduler only widens the gaps, which prunes more and admits
// more. It cannot make this test flaky-red.
func TestUserRateLimit_ASustainableRateIsNotRefused(t *testing.T) {
	// StatusAccepted, not StatusOK: 200 is httptest.NewRecorder's DEFAULT, so an
	// assertion of 200 is also satisfied by a middleware that writes nothing and
	// never calls next.
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusAccepted) })
	h := UserRateLimit(2, 50*time.Millisecond)(next)

	u := AuthUser{ID: pgtype.UUID{Bytes: [16]byte{3}, Valid: true}}
	for i := 1; i <= 6; i++ {
		if i > 1 {
			time.Sleep(30 * time.Millisecond)
		}
		req := httptest.NewRequest("POST", "/v1/jobs", nil)
		req = req.WithContext(ctxWithUser(req.Context(), u))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("request %d at a sustainable rate: got %d want 202", i, rec.Code)
		}
	}
}
