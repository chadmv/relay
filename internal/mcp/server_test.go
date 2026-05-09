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
	s, err := NewServer("http://localhost:8080", "tok")
	require.NoError(t, err)
	require.NotNil(t, s)
}
