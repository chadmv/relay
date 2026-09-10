package worker

import (
	"errors"
	"fmt"
	"strings"
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
// is their SUM rather than each separately. short_id reaches neither index, so it
// cannot produce the index failure this constructor was written for; it is
// bounded because it is stored per row and echoed to admins, which makes an
// unbounded one a row-size and response-size cost.
const (
	maxWorkspaceSourceTypeBytes   = 64
	maxWorkspaceSourceKeyBytes    = 512
	maxWorkspaceShortIDBytes      = 128
	maxWorkspaceBaselineHashBytes = 128
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
// the row. It is the one place this package decides what is storable, so the
// policy lives at one function rather than at each call site.
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
	if err := checkStorableText("source_type", u.SourceType, maxWorkspaceSourceTypeBytes); err != nil {
		return store.UpsertWorkerWorkspaceParams{}, err
	}
	if err := checkStorableText("source_key", u.SourceKey, maxWorkspaceSourceKeyBytes); err != nil {
		return store.UpsertWorkerWorkspaceParams{}, err
	}
	if err := checkStorableText("short_id", u.ShortId, maxWorkspaceShortIDBytes); err != nil {
		return store.UpsertWorkerWorkspaceParams{}, err
	}
	if err := checkStorableText("baseline_hash", u.BaselineHash, maxWorkspaceBaselineHashBytes); err != nil {
		return store.UpsertWorkerWorkspaceParams{}, err
	}
	// THE PARSE ALONE IS NOT THE CHECK. An unparseable value yields the zero time,
	// which binds as SQL NULL against a TIMESTAMPTZ NOT NULL column and fails the
	// statement - and a zero time.Time formats as the year-one RFC3339 instant,
	// which parses, so a value that got past the parse can still be NULL by the
	// time it is bound.
	//
	// time.Parse's own error message echoes its input, so it is deliberately not
	// wrapped: the returned error carries a column name and nothing agent-supplied.
	ts, err := time.Parse(time.RFC3339, u.LastUsedAt)
	if err != nil {
		return store.UpsertWorkerWorkspaceParams{}, fmt.Errorf(
			"%w: last_used_at is not RFC3339", errUnstorableInventoryRow)
	}
	if ts.IsZero() {
		return store.UpsertWorkerWorkspaceParams{}, fmt.Errorf(
			"%w: last_used_at is the zero time", errUnstorableInventoryRow)
	}
	return store.UpsertWorkerWorkspaceParams{
		WorkerID:     workerID,
		SourceType:   u.SourceType,
		SourceKey:    u.SourceKey,
		ShortID:      u.ShortId,
		BaselineHash: u.BaselineHash,
		LastUsedAt:   pgtype.Timestamptz{Time: ts, Valid: true},
	}, nil
}

// checkStorableText refuses a string this table cannot hold: over its column's
// byte bound, or carrying a NUL. column and max are compile-time literals and s
// never enters the returned error.
//
// A NUL is legal in a proto3 string and illegal in TEXT (SQLSTATE 22021). There
// is no invalid-UTF-8 arm and there must not be one: proto.Unmarshal rejects a
// proto3 string field carrying invalid UTF-8 and the stream dies before Connect's
// message loop runs, so such a value cannot reach here over the wire.
func checkStorableText(column, s string, max int) error {
	if len(s) > max {
		return fmt.Errorf("%w: %s exceeds %d bytes", errUnstorableInventoryRow, column, max)
	}
	if strings.IndexByte(s, 0) >= 0 {
		return fmt.Errorf("%w: %s carries a NUL", errUnstorableInventoryRow, column)
	}
	return nil
}
