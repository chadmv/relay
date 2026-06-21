package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildRegisterRequest_SupportsWorkspaces(t *testing.T) {
	// Provider present -> reports true with explicit presence.
	a := &Agent{
		caps:     Capabilities{Hostname: "h1"},
		runners:  map[string]*Runner{},
		creds:    &Credentials{},
		provider: &fakeProvider{},
	}
	req := a.buildRegisterRequest()
	require.NotNil(t, req.SupportsWorkspaces, "field must be set with explicit presence")
	assert.True(t, req.GetSupportsWorkspaces())

	// Provider nil -> reports false (explicit presence, not absent).
	a.provider = nil
	req = a.buildRegisterRequest()
	require.NotNil(t, req.SupportsWorkspaces, "field must be set even when false")
	assert.False(t, req.GetSupportsWorkspaces())
}
