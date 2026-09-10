package perforce

import (
	"fmt"
	"log"
	"sort"
	"strconv"
)

// maxExclusionSetsEnv is the operator knob for the per-stream, per-agent ceiling
// on exclusion-derived workspaces.
const maxExclusionSetsEnv = "RELAY_WORKSPACE_MAX_EXCLUSION_SETS"

// The ceiling's default and its hard maximum.
//
// 4: the design intent is one exclusion-derived workspace per stream per agent,
// and "with and without the heavy subtree" is two. Each slot costs a nearly
// full-size workspace, so this number multiplies disk directly - which is the
// reason not to default it higher.
//
// 64: the knob only ever LOOSENS a control, so it needs a bound of its own.
const (
	defaultMaxExclusionSets = 4
	maxMaxExclusionSets     = 64
)

// resolveMaxExclusionSets reads the knob and returns the effective ceiling plus
// any warning an operator must see.
//
// NO VALUE DISABLES THIS CEILING AND 0 IS NOT ONE. Two variables beside it in
// the same operator-facing table mean "disabled" at zero, so an operator will
// try it here; 0, a negative value and an unparseable value all resolve to the
// default and say so in the warning.
//
// THE WARNINGS ARE RETURN VALUES, not log calls, so the table test reads the
// text without capturing a logger.
func resolveMaxExclusionSets(getenv func(string) string) (int, []string) {
	raw := getenv(maxExclusionSetsEnv)
	if raw == "" {
		return defaultMaxExclusionSets, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return defaultMaxExclusionSets, []string{fmt.Sprintf(
			"%s is not an integer; using the default of %d. This ceiling cannot be switched off.",
			maxExclusionSetsEnv, defaultMaxExclusionSets)}
	}
	if n <= 0 {
		return defaultMaxExclusionSets, []string{fmt.Sprintf(
			"%s must be a positive integer; 0 does not disable this ceiling and neither does a "+
				"negative value. Using the default of %d. This ceiling cannot be switched off.",
			maxExclusionSetsEnv, defaultMaxExclusionSets)}
	}
	if n > maxMaxExclusionSets {
		return maxMaxExclusionSets, []string{fmt.Sprintf(
			"%s is above the hard maximum and was clamped to %d.",
			maxExclusionSetsEnv, maxMaxExclusionSets)}
	}
	return n, nil
}

// newMaxExclusionSets resolves the ceiling for a Provider and reports any
// warning on the agent's own log. New calls this so the value is resolved
// in-package: a Config field would default to the zero value, and a zero here
// must never mean "unlimited", so an unwired caller has to fail closed.
func newMaxExclusionSets(getenv func(string) string) int {
	n, warnings := resolveMaxExclusionSets(getenv)
	for _, w := range warnings {
		log.Printf("perforce: %s", w)
	}
	return n
}

// exclusionCeiling is the effective ceiling for this provider.
//
// A ZERO FIELD MEANS THE DEFAULT. It cannot mean "unlimited" - that is the
// failure this knob is resolved in-package to avoid - and it must not mean
// "refuse everything", which is what a bare comparison against zero would do to
// every cold exclusion prepare.
func (p *Provider) exclusionCeiling() int {
	if p.maxExclusionSets <= 0 {
		return defaultMaxExclusionSets
	}
	return p.maxExclusionSets
}

// exclusionEvictionCandidates returns stream's exclusion-derived entries,
// worst-first: an entry with no BaselineHash before any entry that has one, then
// by LastUsedAt ascending.
//
// THE EMPTY BASELINE ARM IS NOT A TIE-BREAK. The cold path registers a workspace
// with an empty baseline before anything is synced and the exclusion probe
// refuses before the sync runs, so an empty baseline on an unheld entry is what
// a failed bogus prepare leaves behind: an empty directory whose delete is free.
// Ordering it ahead of LRU reclaims that residue before any warm workspace an
// operator paid to fill. An entry left empty by an agent that died mid-sync is
// re-synced by Prepare anyway, so evicting one loses transferred bytes and no
// correctness. TestExclusionEvictionCandidates_AnUnsyncedEntryOutranksAnOlderSyncedOne
// carries the discriminating input.
//
// THE BASE WORKSPACE CANNOT APPEAR HERE, because isExclusionKeyForStream is
// false for a bare stream key. That is the single guard;
// TestExclusionEvictionCandidates_ExcludesTheBaseWorkspaceAndOtherStreams makes
// the base entry the oldest so a pure-LRU implementation picks it.
//
// It reads a snapshot and holds no lock, so nothing here can be a live pointer
// into registry memory.
func exclusionEvictionCandidates(snap []WorkspaceEntry, stream string) []WorkspaceEntry {
	out := make([]WorkspaceEntry, 0, len(snap))
	for _, e := range snap {
		if isExclusionKeyForStream(e.SourceKey, stream) {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		iUnsynced := out[i].BaselineHash == ""
		jUnsynced := out[j].BaselineHash == ""
		if iUnsynced != jUnsynced {
			return iUnsynced
		}
		return out[i].LastUsedAt.Before(out[j].LastUsedAt)
	})
	return out
}
