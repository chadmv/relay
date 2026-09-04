package scheduler

import (
	"reflect"
	"testing"

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
