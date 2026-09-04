package api

import (
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
