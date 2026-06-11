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

func TestValidate_SelfDependencyRejected(t *testing.T) {
	err := Validate(&JobSpec{
		Name: "x",
		Tasks: []TaskSpec{
			{Name: "a", Command: []string{"echo"}, DependsOn: []string{"a"}},
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "dependency cycle")
}

func TestValidate_TwoCycleRejected(t *testing.T) {
	err := Validate(&JobSpec{
		Name: "x",
		Tasks: []TaskSpec{
			{Name: "a", Command: []string{"echo"}, DependsOn: []string{"b"}},
			{Name: "b", Command: []string{"echo"}, DependsOn: []string{"a"}},
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "dependency cycle")
	require.Contains(t, err.Error(), "a")
	require.Contains(t, err.Error(), "b")
}

func TestValidate_ThreeCycleRejected(t *testing.T) {
	err := Validate(&JobSpec{
		Name: "x",
		Tasks: []TaskSpec{
			{Name: "a", Command: []string{"echo"}, DependsOn: []string{"b"}},
			{Name: "b", Command: []string{"echo"}, DependsOn: []string{"c"}},
			{Name: "c", Command: []string{"echo"}, DependsOn: []string{"a"}},
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "dependency cycle")
}

func TestValidate_DiamondDAGAccepted(t *testing.T) {
	// a -> {b, c} -> d. Legitimate DAG, must pass.
	err := Validate(&JobSpec{
		Name: "x",
		Tasks: []TaskSpec{
			{Name: "a", Command: []string{"echo"}},
			{Name: "b", Command: []string{"echo"}, DependsOn: []string{"a"}},
			{Name: "c", Command: []string{"echo"}, DependsOn: []string{"a"}},
			{Name: "d", Command: []string{"echo"}, DependsOn: []string{"b", "c"}},
		},
	})
	require.NoError(t, err)
}

func TestValidate_LinearChainAccepted(t *testing.T) {
	err := Validate(&JobSpec{
		Name: "x",
		Tasks: []TaskSpec{
			{Name: "a", Command: []string{"echo"}},
			{Name: "b", Command: []string{"echo"}, DependsOn: []string{"a"}},
			{Name: "c", Command: []string{"echo"}, DependsOn: []string{"b"}},
		},
	})
	require.NoError(t, err)
}
