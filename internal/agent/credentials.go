package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Credentials manages the agent's authentication material. Two credentials
// exist:
//   - EnrollmentToken: a one-time bootstrap credential read from an env var,
//     sent only when no agent token has been persisted yet.
//   - AgentToken: a long-lived bearer persisted to <state-dir>/token after
//     the coordinator issues one in RegisterResponse.
type Credentials struct {
	tokenFilePath   string
	agentToken      string
	enrollmentToken string
}

// LoadCredentials reads the token file at <stateDir>/token if it exists.
// Missing file → empty credentials (no error). Unreadable file → error.
func LoadCredentials(stateDir string) (*Credentials, error) {
	path := filepath.Join(stateDir, "token")
	c := &Credentials{tokenFilePath: path}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return nil, fmt.Errorf("read token file %s: %w", path, err)
	}
	c.agentToken = strings.TrimSpace(string(b))
	return c, nil
}

// HasAgentToken reports whether a persisted agent token is available.
func (c *Credentials) HasAgentToken() bool { return c.agentToken != "" }

// AgentToken returns the long-lived agent bearer token, or "" if none.
func (c *Credentials) AgentToken() string { return c.agentToken }

// EnrollmentToken returns the in-memory enrollment token, or "" if none.
func (c *Credentials) EnrollmentToken() string { return c.enrollmentToken }

// SetEnrollmentToken sets the in-memory enrollment token. Used once at agent
// startup, from the RELAY_AGENT_ENROLLMENT_TOKEN env var.
func (c *Credentials) SetEnrollmentToken(t string) { c.enrollmentToken = t }

// Persist writes the given agent token to the state file with 0600 perms and
// clears any in-memory enrollment token.
func (c *Credentials) Persist(agentToken string) error {
	if err := os.MkdirAll(filepath.Dir(c.tokenFilePath), 0755); err != nil {
		return fmt.Errorf("mkdir state dir: %w", err)
	}
	if err := os.WriteFile(c.tokenFilePath, []byte(agentToken), 0600); err != nil {
		return fmt.Errorf("write token file: %w", err)
	}
	c.agentToken = agentToken
	c.enrollmentToken = ""
	return nil
}
