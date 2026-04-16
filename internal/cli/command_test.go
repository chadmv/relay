// internal/cli/command_test.go
package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDispatch_RunsMatchingCommand(t *testing.T) {
	ran := false
	cmds := []Command{
		{Name: "ping", Usage: "test", Run: func(_ context.Context, _ []string, _ *Config) error {
			ran = true
			return nil
		}},
	}
	code := Dispatch(context.Background(), cmds, []string{"ping"}, &Config{})
	require.Equal(t, 0, code)
	require.True(t, ran)
}

func TestDispatch_UnknownCommandReturns1(t *testing.T) {
	code := Dispatch(context.Background(), nil, []string{"unknown"}, &Config{})
	require.Equal(t, 1, code)
}

func TestDispatch_EmptyArgsReturns1(t *testing.T) {
	code := Dispatch(context.Background(), nil, nil, &Config{})
	require.Equal(t, 1, code)
}

func TestDispatch_CommandErrorReturns1(t *testing.T) {
	cmds := []Command{
		{Name: "fail", Run: func(_ context.Context, _ []string, _ *Config) error {
			return errors.New("boom")
		}},
	}
	code := Dispatch(context.Background(), cmds, []string{"fail"}, &Config{})
	require.Equal(t, 1, code)
}

func TestDispatch_SilentErrorReturns1WithoutMessage(t *testing.T) {
	cmds := []Command{
		{Name: "silent", Run: func(_ context.Context, _ []string, _ *Config) error {
			return silentError{}
		}},
	}
	code := Dispatch(context.Background(), cmds, []string{"silent"}, &Config{})
	require.Equal(t, 1, code)
}
