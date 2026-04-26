//go:build integration

package worker_test

import (
	"context"
	"testing"
	"time"

	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"
	"relay/internal/tokenhash"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEnrollAndRegister_AtomicityOnSetTokenFailure verifies that if SetWorkerAgentToken
// fails (UNIQUE violation: another worker already holds the same agent_token_hash),
// the enrollment is NOT marked consumed. Without the transaction, the consume would
// be committed and the enrollment token would be permanently bricked.
func TestEnrollAndRegister_AtomicityOnSetTokenFailure(t *testing.T) {
	fx := newWorkerTestFixture(t)
	ctx := context.Background()

	// Seed an enrollment token.
	rawEnroll := seedEnrollment(t, ctx, fx.Q, fx.AdminID, time.Hour)
	enrollHash := tokenhash.Hash(rawEnroll)

	// Pre-create a worker that already holds a known agent_token_hash.
	collidingHash := tokenhash.Hash("collision-canary")
	pre, err := fx.Q.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
		Name: "preexisting", Hostname: "preexisting",
		CpuCores: 1, RamGb: 1, Os: "linux",
	})
	require.NoError(t, err)
	require.NoError(t, fx.Q.SetWorkerAgentToken(ctx, store.SetWorkerAgentTokenParams{
		ID: pre.ID, AgentTokenHash: &collidingHash,
	}))

	// Inject: make the enrollment flow generate the same hash → UNIQUE violation on SetWorkerAgentToken.
	worker.SetAgentTokenGeneratorForTest(t, func() (string, string) {
		return "collision-canary", collidingHash
	})

	stream := newMockConnectStream(t)
	stream.SendToServer(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Register{
			Register: &relayv1.RegisterRequest{
				Hostname: "atomic-host", CpuCores: 4, RamGb: 8, Os: "linux",
				Credential: &relayv1.RegisterRequest_EnrollmentToken{EnrollmentToken: rawEnroll},
			},
		},
	})
	done := make(chan error, 1)
	go func() { done <- fx.Handler.Connect(stream) }()

	connectErr := <-done
	require.Error(t, connectErr, "Connect must fail when SetWorkerAgentToken hits a UNIQUE violation")

	// Atomicity assertion: enrollment must NOT be consumed.
	enroll, err := fx.Q.GetAgentEnrollmentByTokenHash(ctx, enrollHash)
	require.NoError(t, err)
	assert.False(t, enroll.ConsumedAt.Valid,
		"enrollment must NOT be marked consumed when the transaction rolls back")

	// The "atomic-host" worker should not exist (upsert was also in the transaction).
	rows, err := fx.Pool.Query(ctx, `SELECT 1 FROM workers WHERE hostname=$1`, "atomic-host")
	require.NoError(t, err)
	defer rows.Close()
	assert.False(t, rows.Next(),
		"upserted worker must NOT exist when the transaction rolls back")
}

func TestEnrollAndRegister_HappyPathStillCommits(t *testing.T) {
	fx := newWorkerTestFixture(t)
	ctx := context.Background()

	rawEnroll := seedEnrollment(t, ctx, fx.Q, fx.AdminID, time.Hour)
	enrollHash := tokenhash.Hash(rawEnroll)

	stream := newMockConnectStream(t)
	stream.SendToServer(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Register{
			Register: &relayv1.RegisterRequest{
				Hostname: "happy-host", CpuCores: 4, RamGb: 8, Os: "linux",
				Credential: &relayv1.RegisterRequest_EnrollmentToken{EnrollmentToken: rawEnroll},
			},
		},
	})
	done := make(chan error, 1)
	go func() { done <- fx.Handler.Connect(stream) }()

	msg := stream.RecvFromServer(t, 5*time.Second)
	resp := msg.GetRegisterResponse()
	require.NotNil(t, resp)
	require.NotEmpty(t, resp.AgentToken)

	stream.CloseSend()
	<-done

	// Both rows must exist.
	enroll, err := fx.Q.GetAgentEnrollmentByTokenHash(ctx, enrollHash)
	require.NoError(t, err)
	assert.True(t, enroll.ConsumedAt.Valid, "enrollment must be consumed on success")

	agentHash := tokenhash.Hash(resp.AgentToken)
	w, err := fx.Q.GetWorkerByAgentTokenHash(ctx, &agentHash)
	require.NoError(t, err)
	assert.NotNil(t, w)

	// Worker ID must match the response.
	var wID pgtype.UUID
	require.NoError(t, wID.Scan(resp.WorkerId))
	assert.Equal(t, wID, w.ID)
}
