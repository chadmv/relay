package api

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// ParseCORSOrigins parses a comma-separated allowlist string. Entries are
// trimmed, deduplicated, and validated. Wildcard ("*") is rejected — wildcard
// plus the Authorization header is always a security mistake for this API.
// Empty entries are ignored. Non-HTTP(S) schemes are rejected.
func ParseCORSOrigins(s string) ([]string, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, raw := range strings.Split(s, ",") {
		origin := strings.TrimSpace(raw)
		if origin == "" {
			continue
		}
		if origin == "*" {
			return nil, fmt.Errorf("cors: wildcard origin (*) is not permitted")
		}
		u, err := url.Parse(origin)
		if err != nil {
			return nil, fmt.Errorf("cors: invalid origin %q: %w", origin, err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return nil, fmt.Errorf("cors: origin %q must use http or https scheme", origin)
		}
		if u.Host == "" {
			return nil, fmt.Errorf("cors: origin %q missing host", origin)
		}
		if _, ok := seen[origin]; ok {
			continue
		}
		seen[origin] = struct{}{}
		out = append(out, origin)
	}
	return out, nil
}

// CORS returns middleware emitting CORS headers only for origins in the
// allowlist. Empty allowlist → no CORS headers ever (same-origin only).
// Never emits Access-Control-Allow-Credentials (bearer tokens ride in
// Authorization headers, not cookies).
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	if len(allowedOrigins) == 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	set := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		set[o] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			_, allowed := set[origin]
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				if allowed {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
					w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
					w.Header().Set("Access-Control-Max-Age", "600")
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			if allowed {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			}
			next.ServeHTTP(w, r)
		})
	}
}
