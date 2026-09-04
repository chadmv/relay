package perforce

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	relayv1 "relay/internal/proto/relayv1"
)

// warmStreamSpec is the fixed spec every test in this file prepares. The rev is
// a literal CL so BaselineHash needs no resolution map and the tests never
// depend on head resolution.
func warmStreamSpec() *relayv1.SourceSpec {
	return &relayv1.SourceSpec{Provider: &relayv1.SourceSpec_Perforce{
		Perforce: &relayv1.PerforceSource{
			Stream: "//s/x",
			Sync:   []*relayv1.SyncEntry{{Path: "//s/x/...", Rev: "@7"}},
		},
	}}
}

// warmFixtures registers every p4 call a warm Prepare of warmStreamSpec can
// make, including the sync path, so a test that asserts a sync did NOT happen
// fails on its own assertion rather than on a fixture miss.
func warmFixtures(fr *fakeRunner, client string) {
	fr.set("client -o -S //s/x "+client, "")
	fr.set("client -i", "Client saved.\n")
	fr.set("-c "+client+" changes -c "+client+" -s pending -l", "")
	fr.setStream("-c "+client+" sync -q --parallel=4 //"+client+"/...@7", "1 of 1 files\n")
}

// syncCall returns the p4 sync invocation from the runner's history, or nil.
func syncCall(fr *fakeRunner) []string {
	for _, c := range fr.argHistory() {
		if len(c) >= 3 && c[2] == "sync" {
			return c
		}
	}
	return nil
}

// hasArgs reports whether the runner issued a call whose argv is exactly want.
func hasArgs(fr *fakeRunner, want ...string) bool {
	for _, c := range fr.argHistory() {
		if len(c) != len(want) {
			continue
		}
		same := true
		for i := range c {
			if c[i] != want[i] {
				same = false
				break
			}
		}
		if same {
			return true
		}
	}
	return false
}

// A registry row can outlive its directory: a crash between sweeper.evict's
// os.RemoveAll and its reg.Remove leaves exactly that, and so does an operator
// reclaiming disk by hand. Prepare's os.MkdirAll then silently recreates the
// directory EMPTY, and the row's baseline still matches, so needsSync is false
// and the task runs to SUCCESS in an empty tree.
//
// The discriminating input is the missing directory alone: everything else here
// is a healthy warm workspace, so a sync can only be explained by the absence.
func TestProvider_AWarmEntryWhoseDirectoryIsGoneStillSyncs(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	client := expectedClientName("h", "//s/x")
	warmFixtures(fr, client)

	p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
	reg, err := p.Registry()
	require.NoError(t, err)
	spec := warmStreamSpec()
	shortID := allocateShortID("//s/x", &Registry{})
	reg.Upsert(WorkspaceEntry{
		ShortID:      shortID,
		SourceKey:    "//s/x",
		ClientName:   client,
		BaselineHash: BaselineHash(spec.GetPerforce(), nil),
		LastUsedAt:   time.Now(),
	})
	require.NoError(t, reg.Save())
	require.NoDirExists(t, filepath.Join(root, shortID), "the premise: the row survived, the directory did not")

	h, err := p.Prepare(context.Background(), "task-1", spec, func(string) {})
	require.NoError(t, err)
	t.Cleanup(func() { _ = h.Finalize(context.Background()) })

	require.NotNil(t, syncCall(fr), "a workspace whose directory was missing must be re-synced, not trusted")
}

// The control for the test above: the same warm entry with its directory intact
// must NOT re-sync. Without this, forcing a sync unconditionally would pass.
func TestProvider_AWarmEntryWithItsDirectoryIntactDoesNotResync(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	client := expectedClientName("h", "//s/x")
	warmFixtures(fr, client)

	p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
	reg, err := p.Registry()
	require.NoError(t, err)
	spec := warmStreamSpec()
	shortID := allocateShortID("//s/x", &Registry{})
	reg.Upsert(WorkspaceEntry{
		ShortID:      shortID,
		SourceKey:    "//s/x",
		ClientName:   client,
		BaselineHash: BaselineHash(spec.GetPerforce(), nil),
		LastUsedAt:   time.Now(),
	})
	require.NoError(t, reg.Save())
	require.NoError(t, os.MkdirAll(filepath.Join(root, shortID), 0o755))

	h, err := p.Prepare(context.Background(), "task-1", spec, func(string) {})
	require.NoError(t, err)
	t.Cleanup(func() { _ = h.Finalize(context.Background()) })

	require.Nil(t, syncCall(fr), "a warm workspace at the requested baseline must not re-sync")
}

// clientName is recomputed from the hostname on every Prepare while the row
// records the name used when the workspace was allocated. A rename of the agent
// host makes the two disagree, and the recorded name is the one the sweeper
// passes to client -d - so the client Prepare just created is never deleted,
// and handle.Env() hands the task a P4CLIENT the inventory does not report.
func TestProvider_AWarmEntryRecordsTheClientNameThatWasActuallyCreated(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	client := expectedClientName("h", "//s/x")
	warmFixtures(fr, client)

	p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
	reg, err := p.Registry()
	require.NoError(t, err)
	spec := warmStreamSpec()
	shortID := allocateShortID("//s/x", &Registry{})
	reg.Upsert(WorkspaceEntry{
		ShortID:      shortID,
		SourceKey:    "//s/x",
		ClientName:   "relay_oldhost_" + shortID,
		BaselineHash: BaselineHash(spec.GetPerforce(), nil),
		LastUsedAt:   time.Now(),
	})
	require.NoError(t, reg.Save())
	require.NoError(t, os.MkdirAll(filepath.Join(root, shortID), 0o755))

	h, err := p.Prepare(context.Background(), "task-1", spec, func(string) {})
	require.NoError(t, err)
	t.Cleanup(func() { _ = h.Finalize(context.Background()) })

	e, ok := reg.Get(shortID)
	require.True(t, ok)
	require.Equal(t, client, e.ClientName, "the row must name the client Prepare created")
	require.Equal(t, client, h.Env()["P4CLIENT"], "and the task must be handed that same client")
}

// DirtyDelete means "the p4 client is already gone; only the directory remains",
// and sweeper.evict skips client -d while it is set. Prepare recreates the
// client on every attempt, so a stale flag makes the next sweep leave that
// client on the p4 server with nothing that will ever delete it.
func TestProvider_RecreatingTheClientClearsDirtyDelete(t *testing.T) {
	root := t.TempDir()
	fr := newFakeP4Fixture(t)
	client := expectedClientName("h", "//s/x")
	warmFixtures(fr, client)

	p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
	reg, err := p.Registry()
	require.NoError(t, err)
	spec := warmStreamSpec()
	shortID := allocateShortID("//s/x", &Registry{})
	reg.Upsert(WorkspaceEntry{
		ShortID:      shortID,
		SourceKey:    "//s/x",
		ClientName:   client,
		BaselineHash: BaselineHash(spec.GetPerforce(), nil),
		LastUsedAt:   time.Now(),
		DirtyDelete:  true,
	})
	require.NoError(t, reg.Save())
	require.NoError(t, os.MkdirAll(filepath.Join(root, shortID), 0o755))

	h, err := p.Prepare(context.Background(), "task-1", spec, func(string) {})
	require.NoError(t, err)
	t.Cleanup(func() { _ = h.Finalize(context.Background()) })

	e, ok := reg.Get(shortID)
	require.True(t, ok)
	require.False(t, e.DirtyDelete, "the client exists again, so the sweeper must delete it")
}

// ClientTemplate is a caller-supplied field applied with `p4 client -o -t`.
// Workspaces are keyed on the stream alone and ClientTemplate is in neither
// that key nor BaselineHash, so two tasks carrying different templates hash
// identically, are admitted together in ModeShared, and would flip one shared
// client spec against each other - one of them possibly mid-sync - if the
// template were re-applied on every Prepare. On the warm path `client -o`
// returns the stored spec, so the fields the template contributed are already
// in it and nothing is lost by omitting -t.
//
// The cold subtest is the control: it is what stops "never pass -t" from
// passing, and it pins that a first Prepare still honours the field.
func TestProvider_TheClientTemplateIsAppliedOnlyToAColdWorkspace(t *testing.T) {
	const template = "base"
	newSpec := func() *relayv1.SourceSpec {
		s := warmStreamSpec()
		tmpl := template
		s.GetPerforce().ClientTemplate = &tmpl
		return s
	}

	t.Run("cold", func(t *testing.T) {
		root := t.TempDir()
		fr := newFakeP4Fixture(t)
		client := expectedClientName("h", "//s/x")
		warmFixtures(fr, client)
		fr.set("client -o -S //s/x -t "+template+" "+client, "")

		p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
		h, err := p.Prepare(context.Background(), "task-1", newSpec(), func(string) {})
		require.NoError(t, err)
		t.Cleanup(func() { _ = h.Finalize(context.Background()) })

		require.True(t, hasArgs(fr, "client", "-o", "-S", "//s/x", "-t", template, client),
			"a first Prepare must honour client_template")
	})

	t.Run("warm", func(t *testing.T) {
		root := t.TempDir()
		fr := newFakeP4Fixture(t)
		client := expectedClientName("h", "//s/x")
		warmFixtures(fr, client)
		fr.set("client -o -S //s/x -t "+template+" "+client, "")

		p := New(Config{Root: root, Hostname: "h", Client: &Client{r: fr}})
		reg, err := p.Registry()
		require.NoError(t, err)
		shortID := allocateShortID("//s/x", &Registry{})
		reg.Upsert(WorkspaceEntry{
			ShortID:      shortID,
			SourceKey:    "//s/x",
			ClientName:   client,
			BaselineHash: BaselineHash(newSpec().GetPerforce(), nil),
			LastUsedAt:   time.Now(),
		})
		require.NoError(t, reg.Save())
		require.NoError(t, os.MkdirAll(filepath.Join(root, shortID), 0o755))

		h, err := p.Prepare(context.Background(), "task-1", newSpec(), func(string) {})
		require.NoError(t, err)
		t.Cleanup(func() { _ = h.Finalize(context.Background()) })

		require.False(t, hasArgs(fr, "client", "-o", "-S", "//s/x", "-t", template, client),
			"a warm shared client must not have caller-supplied fields re-applied to it")
		require.True(t, hasArgs(fr, "client", "-o", "-S", "//s/x", client),
			"the spec is still re-read and re-written, which is what repairs a half-built workspace")
	})
}
