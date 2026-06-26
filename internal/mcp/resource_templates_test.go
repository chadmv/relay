package mcp

import (
	"context"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestResourceTemplates_Listed(t *testing.T) {
	b := newWhoamiBackend(t, true)
	s, err := NewServer(b.URL, "t")
	require.NoError(t, err)

	cs := connectClient(t, s)
	res, err := cs.ListResourceTemplates(context.Background(), &mcpsdk.ListResourceTemplatesParams{})
	require.NoError(t, err)

	got := map[string]bool{}
	for _, rt := range res.ResourceTemplates {
		got[rt.URITemplate] = true
	}
	require.True(t, got["relay://jobs/{id}"], "jobs template missing")
	require.True(t, got["relay://tasks/{id}"], "tasks template missing")
}
