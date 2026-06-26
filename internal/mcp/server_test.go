package mcp

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewServer_MissingCredentials(t *testing.T) {
	_, err := NewServer("", "")
	require.Error(t, err)
	_, err = NewServer("http://x", "")
	require.Error(t, err)
	_, err = NewServer("", "tok")
	require.Error(t, err)
}

func TestNewServer_ValidCredentials(t *testing.T) {
	backend := newWhoamiBackend(t, true)
	s, err := NewServer(backend.URL, "tok")
	require.NoError(t, err)
	require.NotNil(t, s)
}
