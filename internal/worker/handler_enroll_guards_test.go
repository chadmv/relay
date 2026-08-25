package worker

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"relay/internal/events"
	"relay/internal/metrics"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// errRow is a pgx.Row that reports an error and scans nothing. pgx.ErrNoRows
// through this type is how the fixture says "no such row" - which strandDB could
// not express at all before, since its QueryRow returned strandWorkerRow{}
// unconditionally and EVERY :one found a row.
type errRow struct{ err error }

func (r errRow) Scan(...any) error { return r.err }

// rowScript answers a QueryRow FROM THE STATE THE FIXTURE IS CONFIGURED WITH,
// not from a per-statement script, and that is load-bearing rather than
// stylistic. A scripted fake must be reconfigured when the handler changes which
// statement it issues, so the test that goes green stops being the test that was
// red. Here a test says "a worker row for this hostname already exists" ONCE and
// every arm derives its answer, so one test body is RED against the
// ON CONFLICT DO UPDATE upsert HEAD issues and GREEN against the DO NOTHING
// insert that replaces it.
//
// MATCHING IS ON SQL TEXT, never on a generated Go symbol, so an arm may name a
// statement that does not exist yet without breaking the build.
type rowScript struct {
	mu sync.Mutex

	// existingHostname, when non-empty, is the single hostname the fake's
	// workers table already holds a row for.
	existingHostname string

	// unknownAgentToken makes the reconnect path's token lookup miss, which is
	// how a test drives reconnectAndRegister's own credential refusal.
	unknownAgentToken bool

	// existingHasLiveToken makes the existing worker row hold a non-NULL
	// agent_token_hash, i.e. a LIVE credential rather than a revoked one.
	existingHasLiveToken bool

	seen []strandExec
}

// answer resolves one QueryRow. First match wins; anything unmatched keeps the
// historical strandWorkerRow, which is what makes this inert for every test in
// the package that predates it.
func (s *rowScript) answer(sql string, args []any) pgx.Row {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, strandExec{sql: sql, args: args})

	switch {
	// GetAgentEnrollmentByTokenHash. strandWorkerRow scans this struct
	// successfully and makes every enrollment look consumed and expired.
	case strings.Contains(sql, "agent_enrollments"):
		return agentEnrollmentRow{}

	// GetWorkerByAgentTokenHash, used by the reconnect path.
	case strings.Contains(sql, "agent_token_hash = $1"):
		if s.unknownAgentToken {
			return errRow{pgx.ErrNoRows}
		}
		return strandWorkerRow{}

	// GetWorkerByHostname / GetWorkerByHostnameForUpdate: hostname is $1.
	case strings.Contains(sql, "FROM workers") && strings.Contains(sql, "hostname = $1"):
		if s.existingHostname != "" && strArg(args, 0) == s.existingHostname {
			if s.existingHasLiveToken {
				return liveWorkerRow{}
			}
			return strandWorkerRow{}
		}
		return errRow{pgx.ErrNoRows}

	// InsertWorkerForAutoEnroll: name is $1, hostname is $2, and DO NOTHING
	// returns NO ROW on conflict - the whole refusal signal.
	case strings.Contains(sql, "INSERT INTO workers") && strings.Contains(sql, "DO NOTHING"):
		if s.existingHostname != "" && strArg(args, 1) == s.existingHostname {
			return errRow{pgx.ErrNoRows}
		}
		return strandWorkerRow{}
	}
	return strandWorkerRow{}
}

// strArg reads a positional string argument, tolerating pgx's pointer spelling.
func strArg(args []any, i int) string {
	if i >= len(args) {
		return ""
	}
	switch v := args[i].(type) {
	case string:
		return v
	case *string:
		if v != nil {
			return *v
		}
	}
	return ""
}

// queryRowsSeen copies every statement this script answered. A SEPARATE list
// from strandDB.execs and fakeTx.execs, so "no INSERT was issued" stays an exact
// assertion rather than a substring hunt across mixed lists.
func (s *rowScript) queryRowsSeen() []strandExec {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]strandExec, len(s.seen))
	copy(out, s.seen)
	return out
}

func (s *rowScript) sawStatement(substr string) bool {
	for _, q := range s.queryRowsSeen() {
		if strings.Contains(q.sql, substr) {
			return true
		}
	}
	return false
}

// enrollFixture drives Connect down any of the three registration paths with no
// Postgres. It is a SIBLING of newSuccessFixture rather than an option on it:
// that fixture hardcodes a reconnect credential and every test using it depends
// on that, so extending it would give the enrollment paths and the reconnect
// path one constructor and one blast radius.
type enrollFixture struct {
	h       *Handler
	db      *strandDB
	tx      *fakeTx
	script  *rowScript
	stream  *scriptedStream
	events  <-chan events.Event
	release func()
}

type enrollConfig struct {
	hostname         string
	credential       any // nil = token-less auto-enroll
	existingHostname string
	// existingHasLiveToken: the existing row holds a non-NULL agent_token_hash.
	existingHasLiveToken bool
	allowAutoEnroll      bool
	execTag              string // "" keeps fakeTx's historical "DELETE 0"
}

func newEnrollFixture(t *testing.T, cfg enrollConfig) *enrollFixture {
	t.Helper()

	script := &rowScript{
		existingHostname:     cfg.existingHostname,
		existingHasLiveToken: cfg.existingHasLiveToken,
	}
	db := &strandDB{execTag: "UPDATE 1", script: script}
	tx := &fakeTx{script: script, execTag: cfg.execTag}
	pool := &fakePool{tx: tx}

	grace := NewGraceRegistry(time.Hour, func(string, int32) {})
	t.Cleanup(grace.Stop)

	broker := events.NewBroker()
	evs, unsubscribe := broker.Subscribe(events.Filter{})
	t.Cleanup(unsubscribe)

	h := &Handler{
		q:                   store.New(db),
		pool:                pool,
		registry:            NewRegistry(),
		broker:              broker,
		grace:               grace,
		triggerDispatch:     func() {},
		Metrics:             metrics.NewStore(8),
		AllowAutoEnroll:     cfg.allowAutoEnroll,
		RegistrationTimeout: 5 * time.Second,
	}

	reg := &relayv1.RegisterRequest{Hostname: cfg.hostname, CpuCores: 4, RamGb: 8, Os: "linux"}
	switch c := cfg.credential.(type) {
	case *relayv1.RegisterRequest_EnrollmentToken:
		reg.Credential = c
	case *relayv1.RegisterRequest_AgentToken:
		reg.Credential = c
	}

	s := &scriptedStream{
		ctx:     context.Background(),
		release: make(chan struct{}),
		msgs:    []*relayv1.AgentMessage{{Payload: &relayv1.AgentMessage_Register{Register: reg}}},
	}
	release := sync.OnceFunc(func() { close(s.release) })
	t.Cleanup(release)

	return &enrollFixture{h: h, db: db, tx: tx, script: script, stream: s, events: evs, release: release}
}

// connect runs Connect on its own goroutine and reports whichever outcome
// arrives first: a published worker event (registration SUCCEEDED and Connect is
// parked in the message loop) or Connect returning (registration REFUSED).
func (f *enrollFixture) connect(t *testing.T) (events.Event, error) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- f.h.Connect(f.stream) }()

	select {
	case ev := <-f.events:
		f.release()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Connect did not return after the stream was torn down")
		}
		return ev, nil
	case err := <-done:
		f.release()
		return events.Event{}, err
	case <-time.After(5 * time.Second):
		f.release()
		t.Fatal("Connect neither published a worker event nor returned within 5s: the registration is " +
			"wedged between authenticateAndRegister and finishRegister, so every assertion below would " +
			"be about a registration that never happened")
		return events.Event{}, nil
	}
}

// requireWorkerOnline asserts the event finishRegister publishes on a successful
// registration. THE TYPE ALONE IS NOT THE ASSERTION: the broker publishes
// Type "worker" for both the online publish in finishRegister and the offline
// publish in the teardown, and only the Data payload distinguishes them. The
// plan predicted a "worker.online" type, which does not exist in this tree.
func requireWorkerOnline(t *testing.T, ev events.Event) {
	t.Helper()
	require.Equal(t, "worker", ev.Type)
	require.Contains(t, string(ev.Data), `"status":"online"`)
}

// TestConnect_AutoEnrollStillCreatesAWorkerForAFreshHostname is the control for
// every refusal test in this file and the first default-lane test in this
// repository to drive a token-less enrollment to a successful return.
//
// tokensSent() == 1 IS THE DISCRIMINATING ASSERTION, not decoration. Asserting
// only that no raw token is retained passes just as well against a build that
// never minted one - the exact vacuity scriptedStream.Send's own comment
// predicted. This is that scrub arm's first consumer.
func TestConnect_AutoEnrollStillCreatesAWorkerForAFreshHostname(t *testing.T) {
	f := newEnrollFixture(t, enrollConfig{hostname: "fresh-host", allowAutoEnroll: true})

	ev, err := f.connect(t)
	require.NoError(t, err, "a hostname with no worker row must still auto-enroll")
	requireWorkerOnline(t, ev)
	require.Equal(t, 1, f.stream.tokensSent(),
		"auto-enrollment must MINT and SEND a fresh agent token; zero here means it was never issued, "+
			"and a test asserting only the secret's absence would pass against that build")

	var resp *relayv1.RegisterResponse
	for _, m := range f.stream.sentMsgs() {
		if rr := m.GetRegisterResponse(); rr != nil {
			resp = rr
		}
	}
	require.NotNil(t, resp)
	assert.Equal(t, "[redacted by scriptedStream]", resp.AgentToken,
		"the retained message must carry the placeholder, which distinguishes 'scrubbed' from 'never sent'")
}

// agentEnrollmentRow is a store.AgentEnrollment that is NEITHER consumed NOR
// expired - which strandWorkerRow structurally cannot be. Its type switch does
// cover every field of that struct, but it fills EVERY pgtype.Timestamptz with
// time.Unix(0, 0) and marks it Valid, so an enrollment read through it is
// simultaneously already-consumed and expired in 1970, and enrollAndRegister
// refuses it before its transaction opens.
//
// THE FILL IS POSITIONAL AND THE POSITION IS sqlc's TO CHOOSE.
// store.AgentEnrollment declares CreatedAt, ExpiresAt, ConsumedAt in that order
// and sqlc emits Scan in field order.
// TestAgentEnrollmentRow_IsNeitherConsumedNorExpired scans into the real struct
// and asserts both properties BY NAME, so a reordered or added column reddens
// rather than silently re-pointing these values.
type agentEnrollmentRow struct{}

func (agentEnrollmentRow) Scan(dest ...any) error {
	ts := 0
	for _, d := range dest {
		switch v := d.(type) {
		case *pgtype.UUID:
			*v = strandWorkerID
		case *string:
			*v = "enrollment-token-hash"
		case **string:
			*v = nil
		case *pgtype.Timestamptz:
			switch ts {
			case 0: // CreatedAt
				*v = pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true}
			case 1: // ExpiresAt - must be in the FUTURE
				*v = pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true}
			default: // ConsumedAt - must be INVALID (never consumed)
				*v = pgtype.Timestamptz{}
			}
			ts++
		default:
			return fmt.Errorf("agentEnrollmentRow: no fixture value for scan destination of type %T; "+
				"store.AgentEnrollment gained a column this stub does not model", d)
		}
	}
	return nil
}

func TestAgentEnrollmentRow_IsNeitherConsumedNorExpired(t *testing.T) {
	var e store.AgentEnrollment
	require.NoError(t, agentEnrollmentRow{}.Scan(
		&e.ID, &e.TokenHash, &e.HostnameHint, &e.CreatedBy, &e.CreatedAt, &e.ExpiresAt, &e.ConsumedAt, &e.ConsumedBy))

	assert.False(t, e.ConsumedAt.Valid,
		"a consumed enrollment is refused before enrollAndRegister's transaction opens")
	assert.True(t, e.ExpiresAt.Time.After(time.Now()),
		"an expired enrollment is refused before enrollAndRegister's transaction opens")
}

// TestConnect_EnrollmentTokenReachesASuccessfulRegistration is the enrollment
// path's GREEN control. Without a non-zero command tag ConsumeAgentEnrollment
// reports zero rows and errEnrollmentNotConsumable fires BY DEFAULT, so the
// fixture could drive that rejection and structurally could not drive a success.
func TestConnect_EnrollmentTokenReachesASuccessfulRegistration(t *testing.T) {
	f := newEnrollFixture(t, enrollConfig{
		hostname:   "token-host",
		credential: &relayv1.RegisterRequest_EnrollmentToken{EnrollmentToken: "raw-enrollment-token"},
		execTag:    "UPDATE 1",
	})

	ev, err := f.connect(t)
	require.NoError(t, err)
	requireWorkerOnline(t, ev)
	assert.Equal(t, 1, f.stream.tokensSent(), "enrollment must mint and send a fresh agent token")
}

// TestConnect_EnrollmentTokenIsRefusedWhenTheTokenIsNotConsumable pins
// errEnrollmentNotConsumable somewhere CI executes. The tag is set EXPLICITLY to
// zero rows so this asserts the branch rather than the fixture's default.
func TestConnect_EnrollmentTokenIsRefusedWhenTheTokenIsNotConsumable(t *testing.T) {
	f := newEnrollFixture(t, enrollConfig{
		hostname:   "token-host",
		credential: &relayv1.RegisterRequest_EnrollmentToken{EnrollmentToken: "raw-enrollment-token"},
		execTag:    "UPDATE 0",
	})

	_, err := f.connect(t)
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	assert.Equal(t, 0, f.stream.tokensSent(), "a refused enrollment must send no agent token")
}

// TestConnect_AutoEnrollRefusesAHostnameThatAlreadyHasAWorkerRow is item 1's
// criterion 1 in the default lane. AUTO-ENROLL MAY CREATE A WORKER AND MAY NEVER
// CLAIM ONE - whatever the existing row's status and whatever its token.
//
// Identity takeover is UPSTREAM of every per-task fence in the tree: a claimant
// that inherits a worker id passes AppendTaskLog's and UpdateTaskStatus's
// worker_id predicates as the task's genuine assignee. Those fences establish
// currency and identity; both are worthless if an attacker can BECOME the
// assignee.
func TestConnect_AutoEnrollRefusesAHostnameThatAlreadyHasAWorkerRow(t *testing.T) {
	f := newEnrollFixture(t, enrollConfig{
		hostname:         "taken-host",
		existingHostname: "taken-host",
		allowAutoEnroll:  true,
	})

	_, err := f.connect(t)
	require.Error(t, err, "auto-enroll must never bind to a hostname that already has a worker row")
	assert.Equal(t, codes.Unauthenticated, status.Code(err))

	assert.False(t, f.script.sawStatement("DO UPDATE"),
		"the auto-enroll path must not issue UpsertWorkerByHostname at all")
	for _, e := range f.tx.execsSeen() {
		assert.NotContains(t, e.sql, "agent_token_hash = $2",
			"SetWorkerAgentToken must never run: the existing worker's token stays intact")
	}

	// ASSERTING THAT NO STATEMENT WAS ISSUED IS NOT ASSERTING THE TRANSACTION
	// DID NOT COMMIT. Both halves, deliberately.
	commits, rollbacks := f.tx.outcome()
	assert.Equal(t, 0, commits)
	assert.GreaterOrEqual(t, rollbacks, 1)

	// The refusal returns above finishRegister, so no generation is acquired and
	// there is nothing to release. MarkWorkerOfflineIfEpoch is the only Exec on
	// this seam, so an empty list is the cheap check that this stays true.
	assert.Empty(t, f.db.execsSeen())
	assert.Equal(t, 0, f.stream.tokensSent())
}

// TestConnect_AutoEnrollRefusalIsIndistinguishableFromACredentialFailure.
// The two messages are COMPARED WITH EACH OTHER rather than each against a
// literal: comparing literals would still pass if both sites were changed to
// something disclosing.
func TestConnect_AutoEnrollRefusalIsIndistinguishableFromACredentialFailure(t *testing.T) {
	claimed := newEnrollFixture(t, enrollConfig{
		hostname: "taken-host", existingHostname: "taken-host", allowAutoEnroll: true,
	})
	_, claimedErr := claimed.connect(t)
	require.Error(t, claimedErr)

	unknown := newEnrollFixture(t, enrollConfig{
		hostname:   "any-host",
		credential: &relayv1.RegisterRequest_AgentToken{AgentToken: "no-such-token"},
	})
	unknown.script.unknownAgentToken = true
	_, unknownErr := unknown.connect(t)
	require.Error(t, unknownErr)

	assert.Equal(t, status.Code(unknownErr), status.Code(claimedErr))
	assert.Equal(t, status.Convert(unknownErr).Message(), status.Convert(claimedErr).Message())

	msg := status.Convert(claimedErr).Message()
	for _, leak := range []string{"taken-host", "revoked", "exists", "claimed", "ceiling"} {
		assert.NotContains(t, msg, leak,
			"the refusal must disclose nothing about the hostname beyond the refusal itself")
	}
}

// liveWorkerRow is strandWorkerRow with every nullable string column non-NULL,
// which is how this fixture says "this worker holds a LIVE agent token hash".
// strandWorkerRow sets every **string to nil - i.e. a REVOKED row, since
// ClearWorkerAgentToken is the only writer that nulls that column.
//
// It delegates first so it inherits strandWorkerRow's arity check rather than
// re-declaring it.
type liveWorkerRow struct{}

func (liveWorkerRow) Scan(dest ...any) error {
	if err := (strandWorkerRow{}).Scan(dest...); err != nil {
		return err
	}
	for _, d := range dest {
		if v, ok := d.(**string); ok {
			s := "live-agent-token-hash"
			*v = &s
		}
	}
	return nil
}

// TestConnect_EnrollmentTokenRefusesAHostnameWithALiveCredential. The enrollment
// path gets item 1's ORIGINAL predicate - a non-NULL agent_token_hash - because
// on THIS path it genuinely discriminates: NULL means revoked (recovery,
// allowed), non-NULL means a live credential (takeover, refused).
func TestConnect_EnrollmentTokenRefusesAHostnameWithALiveCredential(t *testing.T) {
	f := newEnrollFixture(t, enrollConfig{
		hostname:             "live-host",
		existingHostname:     "live-host",
		existingHasLiveToken: true,
		credential:           &relayv1.RegisterRequest_EnrollmentToken{EnrollmentToken: "raw-enrollment-token"},
		execTag:              "UPDATE 1",
	})

	_, err := f.connect(t)
	require.Error(t, err, "an enrollment token must never overwrite a live agent credential")
	assert.Equal(t, codes.Unauthenticated, status.Code(err))

	for _, e := range f.tx.execsSeen() {
		assert.NotContains(t, e.sql, "agent_token_hash = $2", "SetWorkerAgentToken must not run")
	}
	commits, rollbacks := f.tx.outcome()
	assert.Equal(t, 0, commits)
	assert.GreaterOrEqual(t, rollbacks, 1)
	assert.Empty(t, f.db.execsSeen())
	assert.Equal(t, 0, f.stream.tokensSent())
}

// TestConnect_EnrollmentTokenStillEnrollsARevokedHostname is the control, and it
// guards the recovery route the whole slice points operators at. WITHOUT IT the
// pair is defeated by swapping the predicate for a status check - see M9.
func TestConnect_EnrollmentTokenStillEnrollsARevokedHostname(t *testing.T) {
	f := newEnrollFixture(t, enrollConfig{
		hostname:         "revoked-host",
		existingHostname: "revoked-host", // exists, and its token hash is NULL
		credential:       &relayv1.RegisterRequest_EnrollmentToken{EnrollmentToken: "raw-enrollment-token"},
		execTag:          "UPDATE 1",
	})

	ev, err := f.connect(t)
	require.NoError(t, err, "revoke-then-re-enroll is the recovery route this slice documents")
	requireWorkerOnline(t, ev)
	assert.Equal(t, 1, f.stream.tokensSent())
}
