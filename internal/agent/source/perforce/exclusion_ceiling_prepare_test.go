package perforce

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/require"
)

// seedExclusionWorkspaces writes n exclusion-derived registry entries for
// stream, each from a distinct exclusion set so each gets a distinct composite
// key, and creates each one's directory. Returned OLDEST FIRST, so out[0] is the
// LRU victim whenever every baseline is the same.
func seedExclusionWorkspaces(
	t *testing.T, reg *Registry, root, hostname, stream string, n int, baseline string,
) []WorkspaceEntry {
	t.Helper()
	base := time.Now().Add(-24 * time.Hour)
	out := make([]WorkspaceEntry, 0, n)
	for i := 0; i < n; i++ {
		key := SourceKey(seedSpec(stream, fmt.Sprintf("seed%d", i)))
		id := allocateShortID(key, reg)
		e := WorkspaceEntry{
			ShortID:      id,
			SourceKey:    key,
			ClientName:   fmt.Sprintf("relay_%s_%s", hostname, id),
			BaselineHash: baseline,
			LastUsedAt:   base.Add(time.Duration(i) * time.Hour),
		}
		reg.Upsert(e)
		require.NoError(t, os.MkdirAll(filepath.Join(root, id), 0o755))
		out = append(out, e)
	}
	require.NoError(t, reg.Save())
	return out
}

// seedSpec is one include at @100 plus one exclusion named by tag.
func seedSpec(stream, tag string) *relayv1.PerforceSource {
	return &relayv1.PerforceSource{
		Stream: stream,
		Sync: []*relayv1.SyncEntry{
			{Path: stream + "/...", Rev: "@100"},
			{Path: fmt.Sprintf("%s/%s/...", stream, tag), Exclude: true},
		},
	}
}

// setColdPrepareFixtures registers every p4 call one successful cold prepare of
// pf makes, and returns the client name it will use. Registering them all is
// deliberate even in the refusal tests: it keeps those assertions about the MINT
// not happening rather than about a missing fixture, so removing the control
// entirely makes them fail on their own assertion.
func setColdPrepareFixtures(fr *fakeRunner, hostname, tag string, pf *relayv1.PerforceSource) string {
	client := expectedClientName(hostname, SourceKey(pf))
	fr.set("client -o -S "+pf.Stream+" "+client, "")
	fr.set("client -i", "Client saved.\n")
	fr.set("-c "+client+" changes -c "+client+" -s pending -l", "")
	fr.set("-c "+client+" files -m1 //"+client+"/"+tag+"/...@100",
		"//s/x/"+tag+"/a.ma#1 - add change 100 (text)\n")
	fr.setStream("-c "+client+" sync -k //"+client+"/"+tag+"/...@100",
		"//s/x/"+tag+"/a.ma#1 - added\n")
	fr.setStream("-c "+client+" sync --parallel=4 //"+client+"/...@100", "1 of 1 files\n")
	return client
}

// clientDeletes returns the deleted client names, in the order p4 was asked.
func clientDeletes(fr *fakeRunner) []string {
	var out []string
	for _, c := range fr.argHistory() {
		if len(c) == 3 && c[0] == "client" && c[1] == "-d" {
			out = append(out, c[2])
		}
	}
	return out
}

// argvNames reports whether any recorded argv element equals name. The client
// name travels in `client -o -S <stream> <name>`'s argv; `client -i` carries it
// on STDIN, so matching on that argv cannot distinguish one mint from another.
func argvNames(fr *fakeRunner, name string) bool {
	for _, c := range fr.argHistory() {
		for _, a := range c {
			if a == name {
				return true
			}
		}
	}
	return false
}

// countExclusionEntries counts stream's exclusion-derived registry rows.
func countExclusionEntries(reg *Registry, stream string) int {
	n := 0
	for _, e := range reg.Snapshot() {
		if isExclusionKeyForStream(e.SourceKey, stream) {
			n++
		}
	}
	return n
}

// AT THE CEILING A COLD PREPARE EVICTS AND IS ADMITTED. Both halves are the
// assertion: "admitted" alone is also what an absent control produces, and
// "something was evicted" alone says nothing about how many or which.
//
// Every seeded entry is unheld and carries the same non-empty baseline, so the
// ordering in force is LRU and the expected victim is the oldest. A client -d
// fixture is registered for ALL of them so an implementation that picks the
// wrong one is caught by the assertion below rather than by a fixture miss.
func TestProvider_AtTheCeilingAColdExclusionPrepareEvictsAndIsAdmitted(t *testing.T) {
	t.Setenv(maxExclusionSetsEnv, "4")
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
	reg, err := p.Registry()
	require.NoError(t, err)

	seeded := seedExclusionWorkspaces(t, reg, root, "h", "//s/x", 4, "bh-warm")
	for _, e := range seeded {
		fr.set("client -d "+e.ClientName, "Client deleted.\n")
	}

	pf := seedSpec("//s/x", "fresh")
	client := setColdPrepareFixtures(fr, "h", "fresh", pf)

	var lines []string
	h, err := p.Prepare(context.Background(), "task-1",
		&relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{Perforce: pf}},
		func(s string) { lines = append(lines, s) })
	require.NoError(t, err, "at the ceiling a cold prepare evicts and is admitted, not refused")
	defer h.Finalize(context.Background())

	require.Equal(t, []string{seeded[0].ClientName}, clientDeletes(fr),
		"exactly one slot is reclaimed - not all of them, not none - and it is the least "+
			"recently used")
	require.True(t, argvNames(fr, client), "and the new workspace's client is then created")

	_, victimStill := reg.Get(seeded[0].ShortID)
	require.False(t, victimStill, "the victim's registry row is gone")
	require.Equal(t, 4, countExclusionEntries(reg, "//s/x"),
		"this single-goroutine sequence ends back at the ceiling")

	require.Contains(t, lines, "[workspace] reclaimed 1 exclusion workspace(s) for this stream")
	for _, l := range lines {
		require.NotContains(t, l, seeded[0].ShortID,
			"a short id is derived from another author's exclusion set: it goes to the agent's "+
				"own log, never to the task log")
	}
}
