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

// holdAll makes every seeded workspace un-evictable by giving it a live holder,
// the seam TestEvictWorkspace_RefusesHeldWorkspace uses. EvictWorkspace's holder
// check reads p.workspaces, so a registry row with no in-memory Workspace has no
// holder and is always evictable - which is why this is needed.
func holdAll(t *testing.T, p *Provider, entries []WorkspaceEntry) {
	t.Helper()
	for _, e := range entries {
		p.mu.Lock()
		w := NewWorkspace(e.ShortID)
		p.workspaces[e.ShortID] = w
		p.mu.Unlock()
		h, err := w.Acquire(context.Background(), Request{SyncPaths: []string{"//s/x/..."}})
		require.NoError(t, err)
		t.Cleanup(h.Release)
	}
}

// THE REFUSAL IS THE ASSERTION, AND THE ABSENCE OF THE MINT IS THE
// DISCRIMINATOR. A prepare that errored after creating the client spec has
// already produced the artifact this ceiling exists to bound, and "returned an
// error" is also what a dozen unrelated downstream failures produce.
//
// BOTH absences are asserted. The client name travels in `client -o -S <stream>
// <name>`'s argv; `client -i` carries the spec on STDIN, so its argv is two
// elements in every mint and cannot distinguish one from another. And
// os.MkdirAll on the workspace root runs before either, so the directory is the
// earliest artifact of all.
//
// Every slot is held by a running task, which is the only state in which this
// control refuses at all. The client -d fixtures are registered so a mutant that
// deletes a held workspace anyway is caught by the count, not by a fixture miss.
func TestProvider_TheCeilingRefusesWhenEverySlotIsHeld(t *testing.T) {
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
	holdAll(t, p, seeded)

	pf := seedSpec("//s/x", "fresh")
	client := setColdPrepareFixtures(fr, "h", "fresh", pf)
	newShortID := allocateShortID(SourceKey(pf), reg)

	_, err = p.Prepare(context.Background(), "task-1",
		&relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{Perforce: pf}}, func(string) {})

	require.Error(t, err, "with every slot held there is nothing to evict, so the prepare is refused")
	require.Contains(t, err.Error(), "ceiling")
	require.Contains(t, err.Error(), maxExclusionSetsEnv,
		"the refusal names the knob so the remedy is reachable from the task log")

	require.False(t, argvNames(fr, client),
		"NOTHING WAS MINTED. The client spec persists on the shared Perforce server, so a "+
			"refusal that still ran `client -o -S` has already produced the artifact the ceiling bounds.")
	_, statErr := os.Stat(filepath.Join(root, newShortID))
	require.True(t, os.IsNotExist(statErr), "and no workspace directory was created either")
	require.Empty(t, clientDeletes(fr), "a held slot is never deleted")
	require.Equal(t, 4, countExclusionEntries(reg, "//s/x"), "and the population is unchanged")
}

// THE REFUSAL MUST NOT NAME AN OCCUPANT. It reaches the task log on the stderr
// stream prefixed "[failed] ", and GET /v1/tasks/{id}/logs is authenticated but
// not admin-only and carries no per-owner gate - so an occupant's identifiers
// would make this control a disclosure oracle for another tenant's workspaces.
//
// The injected marker appears in no part of the expected message and is not a
// string this environment can produce on its own, so the assertion cannot go red
// or green for an unrelated reason.
func TestProvider_TheCeilingRefusalNamesNoOccupant(t *testing.T) {
	const marker = "qqzzoccupantqqzz"
	t.Setenv(maxExclusionSetsEnv, "2")
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
	reg, err := p.Registry()
	require.NoError(t, err)

	var occupants []WorkspaceEntry
	for i := 0; i < 2; i++ {
		id := fmt.Sprintf("%s%d", marker, i)
		e := WorkspaceEntry{
			ShortID:      id,
			SourceKey:    SourceKey(seedSpec("//s/x", fmt.Sprintf("occ%d", i))),
			ClientName:   "relay_h_" + id,
			BaselineHash: "bh-warm",
			LastUsedAt:   time.Now().Add(-time.Duration(i+1) * time.Hour),
		}
		reg.Upsert(e)
		require.NoError(t, os.MkdirAll(filepath.Join(root, id), 0o755))
		fr.set("client -d "+e.ClientName, "Client deleted.\n")
		occupants = append(occupants, e)
	}
	require.NoError(t, reg.Save())
	holdAll(t, p, occupants)

	pf := seedSpec("//s/x", "fresh")
	setColdPrepareFixtures(fr, "h", "fresh", pf)

	_, err = p.Prepare(context.Background(), "task-1",
		&relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{Perforce: pf}}, func(string) {})
	require.Error(t, err)

	require.NotContains(t, err.Error(), marker,
		"no occupant identifier may reach a task log any authenticated user can read")
	require.Contains(t, err.Error(), "2 of at most 2",
		"the count and the ceiling are what the refusal is allowed to say")
	require.Contains(t, err.Error(), `"//s/x"`,
		"the stream is the caller's own, rendered %q and LAST")
}

// THE BASE WORKSPACE IS NEVER EVICTED BY THE CEILING, and the decoy goes first:
// the base entry is the OLDEST row and carries a non-empty baseline, so it sorts
// ahead of every composite under both ordering arms and a pure-LRU candidate
// list picks it. A client -d fixture is registered for it too, so an
// implementation that picks it gets as far as the delete and is caught by this
// test's assertion rather than by a fixture miss.
//
// This is the attacker's best outcome: a handful of junk exclusion sets
// destroying the workspace every non-exclusion task on that stream shares.
func TestProvider_TheCeilingNeverEvictsTheStreamsBaseWorkspace(t *testing.T) {
	t.Setenv(maxExclusionSetsEnv, "4")
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
	reg, err := p.Registry()
	require.NoError(t, err)

	baseID := allocateShortID("//s/x", reg)
	baseClient := "relay_h_" + baseID
	reg.Upsert(WorkspaceEntry{
		ShortID:      baseID,
		SourceKey:    "//s/x",
		ClientName:   baseClient,
		BaselineHash: "bh-base",
		LastUsedAt:   time.Now().Add(-200 * time.Hour),
	})
	require.NoError(t, os.MkdirAll(filepath.Join(root, baseID), 0o755))
	require.NoError(t, reg.Save())
	fr.set("client -d "+baseClient, "Client deleted.\n")

	seeded := seedExclusionWorkspaces(t, reg, root, "h", "//s/x", 4, "bh-warm")
	for _, e := range seeded {
		fr.set("client -d "+e.ClientName, "Client deleted.\n")
	}

	pf := seedSpec("//s/x", "fresh")
	setColdPrepareFixtures(fr, "h", "fresh", pf)

	h, err := p.Prepare(context.Background(), "task-1",
		&relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{Perforce: pf}}, func(string) {})
	require.NoError(t, err)
	defer h.Finalize(context.Background())

	_, baseStill := reg.Get(baseID)
	require.True(t, baseStill,
		"the base workspace is outside the population and is never an eviction candidate")
	_, statErr := os.Stat(filepath.Join(root, baseID))
	require.NoError(t, statErr, "and its directory is untouched")

	require.Equal(t, []string{seeded[0].ClientName}, clientDeletes(fr),
		"exactly one composite slot was reclaimed, and the base workspace was not it")
}

// A WARM PREPARE IS NOT GATED. The registry is deliberately OVER the ceiling, so
// a check hoisted above Prepare's found/not-found branch - or buried inside
// allocateShortID, where that distinction is invisible - has to evict something,
// and the candidates include the very workspace this prepare is for.
//
// The client -d fixtures are all registered, so the assertion is the COUNT of
// deletes rather than a fixture miss.
func TestProvider_AWarmExclusionPrepareAtTheCeilingIsNotGated(t *testing.T) {
	t.Setenv(maxExclusionSetsEnv, "4")
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
	reg, err := p.Registry()
	require.NoError(t, err)

	seeded := seedExclusionWorkspaces(t, reg, root, "h", "//s/x", 5, "bh-warm")
	for _, e := range seeded {
		fr.set("client -d "+e.ClientName, "Client deleted.\n")
	}

	// The spec for seed2 - an entry that is ALREADY in the registry.
	pf := seedSpec("//s/x", "seed2")
	client := setColdPrepareFixtures(fr, "h", "seed2", pf)
	require.Equal(t, seeded[2].ClientName, client,
		"the premise: this prepare is warm, so it reuses the seeded short id and client name")

	h, err := p.Prepare(context.Background(), "task-1",
		&relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{Perforce: pf}}, func(string) {})
	require.NoError(t, err, "a warm prepare is never gated by the ceiling")
	defer h.Finalize(context.Background())

	require.Empty(t, clientDeletes(fr),
		"no eviction: the ceiling is checked only in the not-found arm")
	require.Equal(t, 5, countExclusionEntries(reg, "//s/x"),
		"and the over-ceiling population is left exactly as it was found")
}
