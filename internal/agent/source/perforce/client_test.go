package perforce

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClient_CreateStreamClient_Default(t *testing.T) {
	fr := newFakeP4Fixture(t)
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
	fr := newFakeP4Fixture(t)
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
	fr := newFakeP4Fixture(t)
	fr.set("-c relay_h_abc changes -m1 //relay_h_abc/...#head", "Change 12345 on 2026-04-24 by relay@h '...'\n")
	c := &Client{r: fr}
	cl, err := c.ResolveHead(context.Background(), `D:\rw\abcdef`, "relay_h_abc", "//relay_h_abc/...")
	require.NoError(t, err)
	require.Equal(t, int64(12345), cl)
	// The cwd is not required by p4 - the global -c pins the client server-side -
	// so nothing else would notice a future edit dropping it, and the package's
	// cwd contract (assertCwdContract) would then be false for this call.
	require.Len(t, fr.calls, 1)
	require.Equal(t, `D:\rw\abcdef`, fr.calls[0].cwd)
}

func TestClient_RunFailureBubbles(t *testing.T) {
	fr := newFakeP4Fixture(t)
	fr.setErr("-c relay_h_abc changes -m1 //relay_h_abc/...#head",
		errors.New("Perforce password (P4PASSWD) invalid or unset."))
	c := &Client{r: fr}
	_, err := c.ResolveHead(context.Background(), `D:\rw\abcdef`, "relay_h_abc", "//relay_h_abc/...")
	require.ErrorContains(t, err, "P4PASSWD")
}
