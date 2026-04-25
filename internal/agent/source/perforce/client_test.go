package perforce

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClient_CreateStreamClient_Default(t *testing.T) {
	fr := newFakeP4Fixture()
	fr.set("client -o -S //streams/X/main relay_h_abc", `Client: relay_h_abc
Owner: relay
Root: D:\rw\abcdef
Stream: //streams/X/main
View: //streams/X/main/... //relay_h_abc/...
`)
	fr.set("client -i", "Client relay_h_abc saved.\n")
	c := &Client{r: fr}
	err := c.CreateStreamClient(context.Background(), "relay_h_abc", `D:\rw\abcdef`, "//streams/X/main", "")
	require.NoError(t, err)
	// Two calls: -o (read template) then -i (commit)
	require.Len(t, fr.calls, 2)
	require.Equal(t, []string{"client", "-o", "-S", "//streams/X/main", "relay_h_abc"}, fr.calls[0].args)
	require.Equal(t, []string{"client", "-i"}, fr.calls[1].args)
	require.Contains(t, fr.calls[1].stdin, "Root:")
}

func TestClient_CreateStreamClient_WithTemplate(t *testing.T) {
	fr := newFakeP4Fixture()
	fr.set("client -o -S //streams/X/main -t base relay_h_abc", `Client: relay_h_abc
Stream: //streams/X/main
Options: clobber
View: //streams/X/main/... //relay_h_abc/...
`)
	fr.set("client -i", "Client saved.\n")
	c := &Client{r: fr}
	err := c.CreateStreamClient(context.Background(), "relay_h_abc", `D:\rw\abcdef`, "//streams/X/main", "base")
	require.NoError(t, err)
	require.Equal(t, []string{"client", "-o", "-S", "//streams/X/main", "-t", "base", "relay_h_abc"}, fr.calls[0].args)
}

func TestClient_ResolveHead(t *testing.T) {
	fr := newFakeP4Fixture()
	fr.set("changes -m1 //streams/X/main/...#head", "Change 12345 on 2026-04-24 by relay@h '...'\n")
	c := &Client{r: fr}
	cl, err := c.ResolveHead(context.Background(), "//streams/X/main/...")
	require.NoError(t, err)
	require.Equal(t, int64(12345), cl)
}

func TestClient_RunFailureBubbles(t *testing.T) {
	fr := newFakeP4Fixture()
	fr.setErr("changes -m1 //x/...#head", errors.New("Perforce password (P4PASSWD) invalid or unset."))
	c := &Client{r: fr}
	_, err := c.ResolveHead(context.Background(), "//x/...")
	require.ErrorContains(t, err, "P4PASSWD")
}
