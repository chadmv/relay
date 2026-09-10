package perforce

import (
	"context"
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

// admitExclusionWorkspace decides whether a COLD prepare may mint a new
// exclusion-derived workspace for stream. It returns nil to admit.
//
// IT MUST RUN BEFORE ANYTHING IS MINTED, and that is positional rather than
// incidental: the artifacts are the os.MkdirAll on the workspace root and
// Client.CreateStreamClient, both downstream of Prepare's found/not-found
// branch, and the first statement that can refuse a bogus exclusion is the
// PathHasFiles probe, far downstream of both.
//
// IT IS CALLED FROM THE NOT-FOUND ARM ONLY. Hoisting it above that branch - or
// putting it inside allocateShortID, where the found/not-found distinction is
// invisible - gates every WARM prepare too, which denies the feature to specs
// already using it instead of bounding new ones.
// TestProvider_AWarmExclusionPrepareAtTheCeilingIsNotGated.
//
// THE BOUND IS NOT A HARD CEILING. The count here and the mint downstream are
// not under one lock, so concurrent cold prepares for distinct new keys on one
// stream can all pass: the honest statement is ceiling + concurrent prepares on
// that stream - 1. That overshoot is bounded by the agent's slot configuration,
// which is the operator's and not the job author's, and closing it would add a
// reservation lifecycle with a leak-on-early-return failure mode.
//
// EVICTION GOES THROUGH Provider.EvictWorkspace AND NOTHING HERE DELETES. That
// call is the canonical twin of ReserveForEvict; a third copy of the holder
// check and the p.evicting reservation is the defect this avoids. It also means
// a candidate that becomes held between selection and eviction is refused there,
// so this function needs no holder test of its own.
//
// progress IS CALLED HERE BECAUSE NOTHING IS HELD HERE. It can park until agent
// shutdown, and this runs before Prepare's p.mu block and well before
// ws.Acquire, so there is no handle and no lock for it to strand.
func (p *Provider) admitExclusionWorkspace(
	ctx context.Context, reg *Registry, sourceKey, stream string, progress func(string),
) error {
	if !isExclusionKeyForStream(sourceKey, stream) {
		return nil
	}
	ceiling := p.exclusionCeiling()
	candidates := exclusionEvictionCandidates(reg.Snapshot(), stream)
	count := len(candidates)
	if count < ceiling {
		return nil
	}

	// At most one attempt per slot, which bounds ONE prepare's p4 client -d
	// calls: each is bounded only by RELAY_EVICTION_TIMEOUT, so an unbounded
	// series of them would let a single prepare stall for an arbitrary multiple
	// of it. This is a bound on work per prepare and NOT a convergence claim - a
	// registry far over the ceiling takes several prepares to come back under it.
	attempts := ceiling
	reclaimed := 0
	for _, c := range candidates {
		if count < ceiling || attempts <= 0 {
			break
		}
		attempts--
		if err := p.EvictWorkspace(ctx, c.ShortID); err != nil {
			// Held, already evicting, or a p4 or disk fault. Try the next slot.
			log.Printf("perforce: exclusion ceiling: evict %s: %v", c.ShortID, err)
			continue
		}
		log.Printf("perforce: exclusion ceiling: reclaimed %s", c.ShortID)
		reclaimed++
		count--
	}

	if reclaimed > 0 {
		// THE COUNT ONLY. GET /v1/tasks/{id}/logs is authenticated but not
		// admin-only and carries no per-owner gate, so anything on this line is
		// readable by any authenticated user - and a short id is derived from
		// another job author's exclusion set. The ids go to the agent's own log
		// above, where the reader already has host access.
		progress(fmt.Sprintf("[workspace] reclaimed %d exclusion workspace(s) for this stream",
			reclaimed))
	}
	if count >= ceiling {
		// NO OCCUPANT IS NAMED, for the reason the progress line gives: naming the
		// occupying workspaces would make this refusal a disclosure oracle for
		// another tenant's exclusion sets. The stream is the caller's own and is
		// rendered %q and LAST, per syncSummary's rule, so a forged path cannot
		// spell a convincing line of its own.
		//
		// It says none COULD be reclaimed rather than that all are in use: a
		// failed eviction can also be a p4 or disk fault, and a refusal that
		// asserted the wrong cause would send the operator after the wrong thing.
		return fmt.Errorf("exclusion workspace ceiling reached: this agent holds %d of at most %d "+
			"exclusion-derived workspaces for this stream and none could be reclaimed, which "+
			"normally means a running task holds every one; retry the task, or raise %s on the "+
			"agent. Stream %q",
			count, ceiling, maxExclusionSetsEnv, stream)
	}
	return nil
}
