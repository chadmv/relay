package api

import (
	"net/http"
	"strconv"
	"time"
)

// searchRateLimiter returns this Server's single read bucket, or nil when the
// control is unarmed. Constructed lazily because the limits arrive as exported
// fields set after New, and ONCE because a second instance would be a second
// budget and a second gcLoop goroutine that nothing stops.
func (s *Server) searchRateLimiter() *rateLimiter {
	s.searchLimiterOnce.Do(func() {
		if s.SearchLimitN <= 0 || s.SearchLimitWin <= 0 {
			return
		}
		rl := &rateLimiter{
			windows: make(map[string][]time.Time),
			limit:   s.SearchLimitN,
			window:  s.SearchLimitWin,
		}
		go rl.gcLoop()
		s.searchLimiter = rl
	})
	return s.searchLimiter
}

// allowSearch charges one q-carrying list request to its principal's bucket. It
// returns false with the response already written, and true otherwise, writing
// nothing.
//
// CALLED FROM INSIDE THE HANDLER, at the point parseFilterQ has already returned
// a non-nil needle, and NEVER as middleware. A middleware predicate deciding
// "does this request carry a needle" would be a second implementation of
// parseFilterQ's decision, reading r.URL.Query() again - which discards
// percent-decoding errors, so it can disagree with the parse that was validated.
// It would also disagree on the cases parseFilterQ normalizes: ?q= and ?q=%20%20
// are both ABSENT after the trim, and a middleware testing Get("q") != "" counts
// them. TestListJobs_WhitespaceOnlyQIsNotCounted is that input.
//
// THE 401 IS NOT A COURTESY. userRateLimitKey fails closed, so a q-carrying
// request with no resolved identity has no bucket to charge and cannot be let
// through - allowing it would be the one request nothing bounds. It also keeps
// the key space bounded by the user table: an unidentified caller creates no map
// entry at all.
//
// The body is deliberately NOT the "rate limit exceeded" that RateLimit and
// UserRateLimit share: a client and an operator reading a log must be able to
// tell which control fired.
func (s *Server) allowSearch(w http.ResponseWriter, u AuthUser) bool {
	rl := s.searchRateLimiter()
	if rl == nil {
		return true
	}
	key, ok := userRateLimitKey(u)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	retry, allowed := rl.allow(key)
	if !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "search rate limit exceeded")
		return false
	}
	return true
}
