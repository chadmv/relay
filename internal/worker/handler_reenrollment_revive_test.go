//go:build integration

package worker_test

import (
	"context"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"
	"relay/internal/tokenhash"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConnect_EnrollmentTokenRevivesRevokedWorker drives a revoked worker back
// through the enrollment-token Connect path and asserts end-to-end that the
// worker leaves the revoked state with revoked_at cleared. This closes the gap
// between the store-layer test (TestSetWorkerAgentToken_ClearsRevokedAt) and the
// gRPC register path.
//
// Shape:
//  1. Enroll a fresh worker via enrollment token; capture workerID.
//  2. Revoke via ClearWorkerAgentToken; assert precondition (revoked_at non-null).
//  3. Re-enroll the same hostname via a second enrollment token (the revive path).
//  4. Assert revoked_at is now NULL and status is not 'revoked'.
func TestConnect_EnrollmentTokenRevivesRevokedWorker(t *testing.T) {
	fx := newWorkerTestFixture(t)
	ctx := context.Background()

	// --- Step 1: initial enrollment ---
	rawEnroll1 := seedEnrollment(t, ctx, fx.Q, fx.AdminID, time.Hour)

	stream1 := newMockConnectStream(t)
	stream1.SendToServer(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Register{
			Register: &relayv1.RegisterRequest{
				Hostname: "revive-host",
				CpuCores: 4, RamGb: 8, Os: "linux",
				Credential: &relayv1.RegisterRequest_EnrollmentToken{EnrollmentToken: rawEnroll1},
			},
		},
	})

	done1 := make(chan error, 1)
	go func() { done1 <- fx.Handler.Connect(stream1) }()

	msg1 := stream1.RecvFromServer(t, 5*time.Second)
	resp1 := msg1.GetRegisterResponse()
	require.NotNil(t, resp1)
	require.NotEmpty(t, resp1.WorkerId)

	stream1.CloseSend()
	require.NoError(t, <-done1)

	var workerID pgtype.UUID
	require.NoError(t, workerID.Scan(resp1.WorkerId))

	// --- Step 2: revoke and assert precondition ---
	_, err := fx.Q.ClearWorkerAgentToken(ctx, workerID)
	require.NoError(t, err)

	preRevive, err := fx.Q.GetWorker(ctx, workerID)
	require.NoError(t, err)
	require.Equal(t, "revoked", preRevive.Status, "precondition: worker must be revoked before the revive attempt")
	require.True(t, preRevive.RevokedAt.Valid, "precondition: revoked_at must be non-null before the revive attempt")

	// --- Step 3: re-enroll via a fresh enrollment token (the revive path) ---
	// seedEnrollment derives its raw token from t.Name(), so a second call in the
	// same test would hit the unique-hash constraint. Create a second token with a
	// distinct raw value directly via the store.
	rawEnroll2 := "enroll-" + t.Name() + "-revive"
	h2 := tokenhash.Hash(rawEnroll2)
	_, err = fx.Q.CreateAgentEnrollment(ctx, store.CreateAgentEnrollmentParams{
		TokenHash: h2,
		CreatedBy: fx.AdminID,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	require.NoError(t, err)

	stream2 := newMockConnectStream(t)
	stream2.SendToServer(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Register{
			Register: &relayv1.RegisterRequest{
				Hostname: "revive-host", // same hostname - triggers UpsertWorkerByHostname for the same row
				CpuCores: 4, RamGb: 8, Os: "linux",
				Credential: &relayv1.RegisterRequest_EnrollmentToken{EnrollmentToken: rawEnroll2},
			},
		},
	})

	done2 := make(chan error, 1)
	go func() { done2 <- fx.Handler.Connect(stream2) }()

	msg2 := stream2.RecvFromServer(t, 5*time.Second)
	resp2 := msg2.GetRegisterResponse()
	require.NotNil(t, resp2, "re-enrollment of a revoked worker must succeed and return a RegisterResponse")
	require.NotEmpty(t, resp2.WorkerId, "revived worker must have an id")
	require.NotEmpty(t, resp2.AgentToken, "revived worker must receive a fresh agent token")
	assert.Equal(t, resp1.WorkerId, resp2.WorkerId, "revived worker must reuse the same row (same hostname)")

	stream2.CloseSend()
	require.NoError(t, <-done2)

	// --- Step 4: assert revoke state is cleared ---
	postRevive, err := fx.Q.GetWorker(ctx, workerID)
	require.NoError(t, err)
	assert.False(t, postRevive.RevokedAt.Valid,
		"revoked_at must be NULL after re-enrollment through the gRPC enrollment-token path")
	assert.NotEqual(t, "revoked", postRevive.Status,
		"status must not remain 'revoked' after re-enrollment")
}
