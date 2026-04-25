package api

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateJobSpec_NormalizesLegacyCommand(t *testing.T) {
	spec := JobSpec{
		Name: "j",
		Tasks: []TaskSpec{{
			Name:    "t",
			Command: []string{"echo", "hello"},
		}},
	}
	require.NoError(t, ValidateJobSpec(spec))
	require.Nil(t, spec.Tasks[0].Command, "legacy Command must be cleared after normalization")
	require.Equal(t, [][]string{{"echo", "hello"}}, spec.Tasks[0].Commands)
}

func TestValidateJobSpec_AcceptsCommands(t *testing.T) {
	spec := JobSpec{
		Name: "j",
		Tasks: []TaskSpec{{
			Name:     "t",
			Commands: [][]string{{"go", "test"}, {"aws", "s3", "cp", "x", "y"}},
		}},
	}
	require.NoError(t, ValidateJobSpec(spec))
	require.Len(t, spec.Tasks[0].Commands, 2)
}

func TestValidateJobSpec_RejectsBothCommandAndCommands(t *testing.T) {
	spec := JobSpec{
		Name: "j",
		Tasks: []TaskSpec{{
			Name:     "t",
			Command:  []string{"echo"},
			Commands: [][]string{{"echo"}},
		}},
	}
	err := ValidateJobSpec(spec)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "either command or commands"),
		"error must explain conflict, got: %v", err)
}

func TestValidateJobSpec_RejectsEmptyCommands(t *testing.T) {
	spec := JobSpec{
		Name: "j",
		Tasks: []TaskSpec{{
			Name: "t",
			// neither command nor commands
		}},
	}
	require.Error(t, ValidateJobSpec(spec))
}

func TestValidateJobSpec_RejectsEmptyArgvInCommands(t *testing.T) {
	spec := JobSpec{
		Name: "j",
		Tasks: []TaskSpec{{
			Name:     "t",
			Commands: [][]string{{"echo", "ok"}, {}},
		}},
	}
	err := ValidateJobSpec(spec)
	require.Error(t, err)
	require.Contains(t, err.Error(), "commands[1]")
}
