package scheduler_test

import (
	"testing"

	"relay/internal/scheduler"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLabelMatch(t *testing.T) {
	tests := []struct {
		name     string
		requires string
		labels   string
		want     bool
	}{
		{
			name:     "empty requires matches anything",
			requires: `{}`,
			labels:   `{"zone": "studio-a"}`,
			want:     true,
		},
		{
			name:     "exact single match",
			requires: `{"zone": "studio-a"}`,
			labels:   `{"zone": "studio-a", "tier": "high"}`,
			want:     true,
		},
		{
			name:     "missing required key",
			requires: `{"zone": "studio-a"}`,
			labels:   `{"tier": "high"}`,
			want:     false,
		},
		{
			name:     "value mismatch",
			requires: `{"zone": "studio-a"}`,
			labels:   `{"zone": "studio-b"}`,
			want:     false,
		},
		{
			name:     "all required keys must match",
			requires: `{"zone": "studio-a", "tier": "high"}`,
			labels:   `{"zone": "studio-a"}`,
			want:     false,
		},
		{
			name:     "both empty",
			requires: `{}`,
			labels:   `{}`,
			want:     true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := scheduler.LabelMatch([]byte(tc.requires), []byte(tc.labels))
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
