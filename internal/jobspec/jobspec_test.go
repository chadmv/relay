package jobspec

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidate_HappyPath(t *testing.T) {
	spec := JobSpec{
		Name: "ok",
		Tasks: []TaskSpec{
			{Name: "t1", Command: []string{"echo", "hi"}},
		},
	}
	require.NoError(t, Validate(&spec))
	// Command should be normalized into Commands.
	require.Empty(t, spec.Tasks[0].Command)
	require.Equal(t, [][]string{{"echo", "hi"}}, spec.Tasks[0].Commands)
}

func TestValidate_NameRequired(t *testing.T) {
	err := Validate(&JobSpec{Tasks: []TaskSpec{{Name: "t", Command: []string{"x"}}}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "name is required")
}

func TestValidate_AtLeastOneTask(t *testing.T) {
	err := Validate(&JobSpec{Name: "x"})
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "at least one task"))
}

func TestValidate_DuplicateTaskName(t *testing.T) {
	err := Validate(&JobSpec{
		Name: "x",
		Tasks: []TaskSpec{
			{Name: "t", Command: []string{"a"}},
			{Name: "t", Command: []string{"b"}},
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate task name")
}

func TestValidate_UnknownDependsOn(t *testing.T) {
	err := Validate(&JobSpec{
		Name: "x",
		Tasks: []TaskSpec{
			{Name: "t1", Command: []string{"a"}, DependsOn: []string{"missing"}},
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown depends_on")
}

func TestValidate_BothCommandAndCommandsRejected(t *testing.T) {
	err := Validate(&JobSpec{
		Name: "x",
		Tasks: []TaskSpec{{
			Name:     "t",
			Command:  []string{"a"},
			Commands: [][]string{{"b"}},
		}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "either command or commands")
}
