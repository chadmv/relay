package scheduler

import (
	"reflect"
	"testing"

	"relay/internal/agent/source/perforce"
	"relay/internal/store"

	"relay/internal/api"
	relayv1 "relay/internal/proto/relayv1"

	"github.com/stretchr/testify/require"
)

func TestSourceSpecToProto_CarriesTheExcludeFlag(t *testing.T) {
	s := &api.SourceSpec{
		Type:   "perforce",
		Stream: "//s/x",
		Sync: []api.SyncEntry{
			{Path: "//s/x/...", Rev: "#head"},
			{Path: "//s/x/heavy/...", Exclude: true},
		},
	}
	got := sourceSpecToProto(s).GetPerforce().GetSync()
	require.Len(t, got, 2)
	require.False(t, got[0].GetExclude(), "an include must not arrive excluded")
	require.True(t, got[1].GetExclude(),
		"dropping exclude here syncs the subtree into a workspace whose identity says it is excluded")
}

// sourceSpecToProto copies api.SyncEntry into relayv1.SyncEntry field by field,
// and the compiler is blind to a field added on either side. Comparing arities
// makes that a RED here rather than a silent drop on the wire. The proto struct
// carries unexported protoimpl fields, so only tagged exported fields count.
func TestSourceSpecToProto_SyncEntryArityMatches(t *testing.T) {
	protoFields := 0
	pt := reflect.TypeOf(relayv1.SyncEntry{})
	for i := 0; i < pt.NumField(); i++ {
		f := pt.Field(i)
		if f.IsExported() && f.Tag.Get("protobuf") != "" {
			protoFields++
		}
	}
	require.Equal(t, reflect.TypeOf(api.SyncEntry{}).NumField(), protoFields,
		"api.SyncEntry and relayv1.SyncEntry have drifted; sourceSpecToProto copies them by hand")
}

// The coordinator and the agent must never compute different strings for one
// spec. They cannot, because this delegates to the agent's own function - and
// this test is what keeps a future "optimisation" from reimplementing it here.
func TestSourceKeyFromAPISpec_DelegatesToThePerforceFunction(t *testing.T) {
	s := &api.SourceSpec{
		Type:   "perforce",
		Stream: "//s/x",
		Sync: []api.SyncEntry{
			{Path: "//s/x/...", Rev: "#head"},
			{Path: "//s/x/heavy/...", Exclude: true},
		},
	}
	require.Equal(t, perforce.SourceKey(sourceSpecToProto(s).GetPerforce()), SourceKeyFromAPISpec(s))
	require.Equal(t, "", SourceKeyFromAPISpec(nil))
	require.Equal(t, "", SourceKeyFromAPISpec(&api.SourceSpec{Type: "git"}))
}

func TestSourceKeyFromAPISpec_NoExclusionsIsTheBareStream(t *testing.T) {
	s := &api.SourceSpec{Type: "perforce", Stream: "//s/x",
		Sync: []api.SyncEntry{{Path: "//s/x/...", Rev: "#head"}}}
	require.Equal(t, "//s/x", SourceKeyFromAPISpec(s))
}

// The candidate list the warm lookup is built from must use the SAME producer
// selectWorker compares against, or the lookup fetches rows the comparison can
// never match and the bias just silently stops firing.
func TestWarmKeysForTasks_UsesTheKeySelectWorkerCompares(t *testing.T) {
	tasks := []store.Task{
		{Source: []byte(`{"type":"perforce","stream":"//s/x","sync":[{"path":"//s/x/...","rev":"#head"}]}`)},
		{Source: []byte(`{"type":"perforce","stream":"//s/x","sync":[{"path":"//s/x/...","rev":"#head"},{"path":"//s/x/heavy/...","exclude":true}]}`)},
		{Source: nil},
		{Source: []byte(`{`)},
	}
	got := warmKeysForTasks(tasks)
	require.Len(t, got["perforce"], 2)
	require.Contains(t, got["perforce"], "//s/x")

	excl := &api.SourceSpec{Type: "perforce", Stream: "//s/x",
		Sync: []api.SyncEntry{
			{Path: "//s/x/...", Rev: "#head"},
			{Path: "//s/x/heavy/...", Exclude: true},
		}}
	require.Contains(t, got["perforce"], SourceKeyFromAPISpec(excl),
		"the excluded task's candidate key is the composite one, not the bare stream")
}
