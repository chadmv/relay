package source_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"relay/internal/agent/source"
	relayv1 "relay/internal/proto/relayv1"
)

type fakeProvider struct{ typ string }

func (f *fakeProvider) Type() string { return f.typ }
func (f *fakeProvider) Prepare(ctx context.Context, taskID string, spec *relayv1.SourceSpec, progress func(string)) (source.Handle, error) {
	return nil, errors.New("nope")
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	reg := source.NewRegistry()
	reg.Register("perforce", func() source.Provider { return &fakeProvider{typ: "perforce"} })

	p, err := reg.Get("perforce")
	require.NoError(t, err)
	require.Equal(t, "perforce", p.Type())

	_, err = reg.Get("git")
	require.ErrorIs(t, err, source.ErrUnknownProvider)
}
