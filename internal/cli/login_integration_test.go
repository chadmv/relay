//go:build integration

package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIntegration_LoginAgainstTheRealEndpoint drives the real relay login
// command against a live internal/api server.
//
// It is the pin on CLI-to-server compatibility for the auth body: the harness
// seeds tokens through the store, so no other test in this lane calls
// POST /v1/auth/login at all. doLogin decodes into a local anonymous struct
// carrying token and expires_at only, and encoding/json ignores unknown keys -
// this is what turns that argument into evidence.
func TestIntegration_LoginAgainstTheRealEndpoint(t *testing.T) {
	s := startRelayServer(t)

	origPass := readPasswordFn
	readPasswordFn = func(out io.Writer, prompt string) (string, error) {
		return cliLanePassword, nil
	}
	t.Cleanup(func() { readPasswordFn = origPass })

	var saved *Config
	origSave := saveConfigFn
	saveConfigFn = func(cfg *Config) error {
		copied := *cfg
		saved = &copied
		return nil
	}
	t.Cleanup(func() { saveConfigFn = origSave })

	// doLogin reads the server URL on the first line and the email on the
	// second; a bare newline accepts the configured URL.
	input := strings.NewReader("\n" + s.AdminEmail + "\n")
	var out bytes.Buffer
	cfg := &Config{ServerURL: s.BaseURL}
	require.NoError(t, doLogin(testCtx(t), cfg, input, &out))

	require.NotNil(t, saved, "a successful login must save the config")
	assert.Equal(t, s.BaseURL, saved.ServerURL)
	require.NotEmpty(t, saved.Token, "the token must be read out of the real response body")
	assert.NotEqual(t, s.AdminToken, saved.Token, "login must mint a NEW token, not echo the seeded one")
	assert.Contains(t, out.String(), "Logged in")

	// The saved token must actually authenticate a subsequent call, which is
	// what makes this a compatibility pin rather than a string assertion.
	var listOut bytes.Buffer
	require.NoError(t, doWorkers(testCtx(t), &Config{ServerURL: s.BaseURL, Token: saved.Token},
		[]string{"list"}, &listOut))
	assert.Contains(t, listOut.String(), "Total: 0")
}

// A wrong password must fail, so the success case above is not passing for some
// reason other than the credentials.
func TestIntegration_LoginRejectsAWrongPassword(t *testing.T) {
	s := startRelayServer(t)

	origPass := readPasswordFn
	readPasswordFn = func(out io.Writer, prompt string) (string, error) {
		return cliLanePassword + "-wrong", nil
	}
	t.Cleanup(func() { readPasswordFn = origPass })

	origSave := saveConfigFn
	saveConfigFn = func(cfg *Config) error {
		t.Errorf("a failed login must not save a config")
		return nil
	}
	t.Cleanup(func() { saveConfigFn = origSave })

	input := strings.NewReader("\n" + s.AdminEmail + "\n")
	var out bytes.Buffer
	err := doLogin(testCtx(t), &Config{ServerURL: s.BaseURL}, input, &out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid email or password")
}
