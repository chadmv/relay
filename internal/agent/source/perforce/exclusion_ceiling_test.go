package perforce

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// envOnly returns a getenv that answers only for the ceiling variable, so a
// table row cannot pass because of something else in the real environment.
func envOnly(value string) func(string) string {
	return func(k string) string {
		if k == maxExclusionSetsEnv {
			return value
		}
		return ""
	}
}

// THE ZERO ROW IS WHY THIS TABLE EXISTS. Its two neighbours in README's agent
// table mean "disabled" at zero, so the text is asserted, not just the number:
// a resolver that returned the default silently would leave an operator
// believing the control is off.
func TestResolveMaxExclusionSets(t *testing.T) {
	cases := []struct {
		name        string
		value       string
		want        int
		wantWarning bool
		wantText    string
	}{
		{"unset yields the default and says nothing", "", defaultMaxExclusionSets, false, ""},
		{"a value inside the range is used verbatim", "8", 8, false, ""},
		{"the maximum itself is used verbatim", "64", 64, false, ""},
		{"zero does not disable it", "0", defaultMaxExclusionSets, true, "0 does not disable this ceiling"},
		{"a negative value does not disable it", "-1", defaultMaxExclusionSets, true, "0 does not disable this ceiling"},
		{"an unparseable value falls back", "abc", defaultMaxExclusionSets, true, "cannot be switched off"},
		{"above the maximum clamps", "1000", maxMaxExclusionSets, true, "clamped"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, warnings := resolveMaxExclusionSets(envOnly(tc.value))
			require.Equal(t, tc.want, got)
			if !tc.wantWarning {
				require.Empty(t, warnings, "a value used verbatim must not warn")
				return
			}
			require.Len(t, warnings, 1)
			require.Contains(t, warnings[0], tc.wantText)
			require.Contains(t, warnings[0], maxExclusionSetsEnv,
				"a warning an operator cannot trace to a variable is noise")
		})
	}
}

// The ceiling is resolved in New, inside the package. A Config field would
// default to zero, and a zero here must never mean unlimited, so an unwired
// main.go has to fail CLOSED.
func TestNew_ResolvesTheCeilingInsideThePackage(t *testing.T) {
	t.Setenv(maxExclusionSetsEnv, "7")
	p := New(Config{Root: t.TempDir(), Hostname: "h", Client: &Client{r: newFakeP4Fixture(t)}})
	require.Equal(t, 7, p.maxExclusionSets)
}

// THE CONTROL, and the one that matters. With nothing wired anywhere, the
// ceiling is still in force at its default.
func TestNew_AnUnsetEnvironmentStillCarriesTheCeiling(t *testing.T) {
	t.Setenv(maxExclusionSetsEnv, "")
	p := New(Config{Root: t.TempDir(), Hostname: "h", Client: &Client{r: newFakeP4Fixture(t)}})
	require.Equal(t, defaultMaxExclusionSets, p.maxExclusionSets)
}

// A Provider whose field is zero must use the DEFAULT, never "unlimited" and
// never "refuse everything": a bare `count >= 0` comparison refuses every cold
// exclusion prepare, which is fail-closed and still a catastrophe.
func TestExclusionCeiling_AZeroFieldMeansTheDefault(t *testing.T) {
	require.Equal(t, defaultMaxExclusionSets, (&Provider{}).exclusionCeiling())
	require.Equal(t, 9, (&Provider{maxExclusionSets: 9}).exclusionCeiling())
}
