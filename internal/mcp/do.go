package mcp

import (
	"context"
	"errors"

	"relay/internal/relayclient"
)

// do issues an API request through the shared client and, on an HTTP 401,
// attempts a single token reload-and-retry. The reload reads the current token
// from config via s.reloadToken; the retry runs at most once. A 401 that is not
// recoverable (no reader, empty/identical/still-expired token, or a second 401)
// is returned unchanged so MapError surfaces auth_expired. Non-401 errors pass
// straight through. This is the single chokepoint every tool routes through.
func (s *Server) do(ctx context.Context, method, path string, body, out any) error {
	usedTok := s.client.Token()
	err := s.client.Do(ctx, method, path, body, out)
	if !is401(err) {
		return err
	}
	if s.reloadToken == nil {
		return err
	}
	newTok, rerr := s.reloadToken()
	if rerr != nil || newTok == "" {
		return err
	}
	// Compare against the token this call actually used, not the live client
	// token: under concurrent reloads another goroutine may already have swapped
	// in newTok, which would otherwise spuriously short-circuit this retry.
	if newTok == usedTok {
		return err
	}
	s.client.SetToken(newTok)
	return s.client.Do(ctx, method, path, body, out)
}

// is401 reports whether err is a relayclient.ResponseError with status 401.
func is401(err error) bool {
	var re *relayclient.ResponseError
	return errors.As(err, &re) && re.StatusCode == 401
}

// SetTokenReloader installs a config-backed token reloader used to recover from a
// mid-session 401. Call once after NewServer and before Run. Passing nil disables
// reload (the construction default).
func (s *Server) SetTokenReloader(fn func() (string, error)) {
	s.reloadToken = fn
}
