package worker

import (
	"errors"
	"fmt"
	"time"

	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
)

// Byte bounds on the agent-supplied strings worker_workspaces stores. BYTES, not
// runes: a btree entry limit is a byte limit, and octet_length() in the CHECK
// behind these and len() here are the same measure, so a rune bound in Go would
// be a second implementation of one policy that agrees on ASCII and disagrees on
// everything else.
//
// THERE IS DELIBERATELY NO ENV KNOB, and the reason is one step further on than
// ingestLogLimiter's. An authenticated agent drives the refusal counter at one
// increment per inventory entry, and the remedy an operator reaches for on a
// climbing counter - raise the bound - is the attack. Changing one of these
// costs a rebuild and leaves a visible diff.
//
// source_type and source_key reach the primary key. source_type, source_key and
// baseline_hash share one worker_workspaces_lookup_idx entry, so what has to fit
// is their SUM rather than each separately.
const (
	maxWorkspaceSourceKeyBytes = 512
)

// errUnstorableInventoryRow is what inventoryUpsertParams returns for a row the
// coordinator will not store.
//
// IT CARRIES NO WIRE VALUE AND NO WRAPPER OF IT MAY. Every wrapper below formats
// a column name and a bound, both compile-time literals. A caller that renders
// this error into a log line therefore cannot be made to render an agent-chosen
// depot path.
var errUnstorableInventoryRow = errors.New("inventory row is not storable")

// inventoryUpsertParams builds the params UpsertWorkerWorkspace binds, or refuses
// the row. Both of this package's writers go through it, so the policy about what
// is storable lives at one function rather than at each call site.
//
// IT REFUSES; IT NEVER TRANSFORMS, which is why sanitizeAgentErrorMessage
// (handler.go) is deliberately not reused here. That value is human-readable
// prose, where losing a byte loses a byte. source_key is an IDENTITY: truncating
// it, or stripping a byte from it, makes two distinct workspaces canonicalise
// onto one primary-key row. That is the poisoning hazard perforce.SourceKey's NUL
// set-terminator exists to prevent, re-created at the coordinator by a sanitiser
// instead of at the agent by a missing separator.
//
// workerID is a PARAMETER and no identity is read out of u: it is resolved at
// registration from the credential, which is what scopes every statement on this
// path to the sending worker's own rows.
//
// Pinned by TestInventoryUpsertParams_MapsEveryFieldToItsOwnColumn and
// TestInventoryUpsertParams_SourceKeyIsBoundedInBytesNotRunes.
func inventoryUpsertParams(workerID pgtype.UUID, u *relayv1.WorkspaceInventoryUpdate) (store.UpsertWorkerWorkspaceParams, error) {
	if err := checkStorableText("source_key", u.SourceKey, maxWorkspaceSourceKeyBytes); err != nil {
		return store.UpsertWorkerWorkspaceParams{}, err
	}
	ts, _ := time.Parse(time.RFC3339, u.LastUsedAt)
	return store.UpsertWorkerWorkspaceParams{
		WorkerID:     workerID,
		SourceType:   u.SourceType,
		SourceKey:    u.SourceKey,
		ShortID:      u.ShortId,
		BaselineHash: u.BaselineHash,
		LastUsedAt:   pgtype.Timestamptz{Time: ts, Valid: !ts.IsZero()},
	}, nil
}

// checkStorableText refuses a string this table cannot hold. column and max are
// compile-time literals and s never enters the returned error.
func checkStorableText(column, s string, max int) error {
	if len(s) > max {
		return fmt.Errorf("%w: %s exceeds %d bytes", errUnstorableInventoryRow, column, max)
	}
	return nil
}
