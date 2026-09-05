package perforce

import (
	"fmt"

	"relay/internal/jobspec"
	relayv1 "relay/internal/proto/relayv1"
)

// preemptSpec is one exclusion resolved into what the p4 call and the log line
// each need: the CLIENT-form argv element, and the DEPOT path the operator
// wrote and can act on.
type preemptSpec struct {
	depotPath  string
	clientSpec string
}

// preemptSpecs resolves every excluded entry to the have-list preempt that
// implements it. revOf supplies the resolved revision of an include by its
// depot path - "@12345" where the spec said "#head".
//
// IT REFUSES RATHER THAN GUESSES, and the wording follows toClientPath's: a
// spec reaching here has passed jobspec.validateSourceSpec, which already
// requires exactly one covering include, so a violation means the spec did not
// come through validation and synthesising a revision would preempt at the
// wrong one - which fetches the excluded subtree rather than skipping it.
//
// It uses jobspec.DepotPathCovers, the SAME predicate the validator used to
// decide there is exactly one coverer. A second implementation here could
// disagree with the validator about which include supplies the revision.
func preemptSpecs(clientName, stream string, sync []*relayv1.SyncEntry, revOf map[string]string) ([]preemptSpec, error) {
	var out []preemptSpec
	for _, e := range sync {
		if !e.GetExclude() {
			continue
		}
		cover := ""
		for _, inc := range sync {
			if inc.GetExclude() || !jobspec.DepotPathCovers(inc.GetPath(), e.GetPath()) {
				continue
			}
			if cover != "" {
				return nil, fmt.Errorf("perforce: excluded path %s is covered by more than one sync path; "+
					"this spec did not come through jobspec validation", e.GetPath())
			}
			cover = inc.GetPath()
		}
		if cover == "" {
			return nil, fmt.Errorf("perforce: excluded path %s is covered by no sync path; "+
				"this spec did not come through jobspec validation", e.GetPath())
		}
		cp, err := toClientPath(clientName, stream, e.GetPath())
		if err != nil {
			return nil, err
		}
		out = append(out, preemptSpec{depotPath: e.GetPath(), clientSpec: cp + revOf[cover]})
	}
	return out, nil
}
