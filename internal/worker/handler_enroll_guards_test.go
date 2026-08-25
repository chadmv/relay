package worker

import (
	"context"
	"errors"
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

// countRow answers a COUNT(*) :one.
type countRow struct{ n int64 }

func (r countRow) Scan(dest ...any) error {
	for _, d := range dest {
		if v, ok := d.(*int64); ok {
			*v = r.n
		}
	}
	return nil
}

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

	// workerCount is what CountWorkers answers with.
	workerCount int64

	// storeErr, when set, is returned by the two statements that can fail with a
	// RAW POSTGRES ERROR on a caller-controlled input: the create-only insert and
	// the FOR UPDATE lookup. It is neither pgx.ErrNoRows nor a refusal - it stands
	// in for a genuine store fault.
	storeErr error

	seen []scriptedQuery
}

// scriptedQuery is what rowScript records, and it carries an OWNER that
// strandExec has no room for.
//
// WITHOUT IT NO ASSERTION CAN SAY WHERE A STATEMENT WAS ISSUED, only that it
// was, because strandDB.QueryRow and fakeTx.QueryRow share one rowScript and
// appended to one undifferentiated list. Measured: changing any of
// autoEnrollAndRegister's or enrollAndRegister's three txq.* calls to h.q.* -
// i.e. hoisting it OUT of the transaction onto the pool - left every test in
// this package green. That is not a cosmetic gap: enrollAndRegister's own
// comment claims "the lock is what makes this non-racy for the case that
// matters, an existing row", and a FOR UPDATE taken outside the transaction
// holds nothing by the time the upsert runs. For InsertWorkerForAutoEnroll the
// production consequence is worse than the fake's: a SetWorkerAgentToken failure
// would no longer roll the insert back, leaving an orphan row with a NULL hash
// that blocks its hostname permanently while not counting against the
// non-revoked ceiling budget either.
type scriptedQuery struct {
	owner string // "tx" for fakeTx, "pool" for strandDB
	sql   string
	args  []any
}

// answer resolves one QueryRow. First match wins; anything unmatched keeps the
// historical strandWorkerRow, which is what makes this inert for every test in
// the package that predates it.
func (s *rowScript) answer(owner, sql string, args []any) pgx.Row {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, scriptedQuery{owner: owner, sql: sql, args: args})

	switch {
	// GetAgentEnrollmentByTokenHash. strandWorkerRow scans this struct
	// successfully and makes every enrollment look consumed and expired.
	case strings.Contains(sql, "agent_enrollments"):
		return agentEnrollmentRow{}

	// CountWorkers. Matched on the full predicate so it cannot also catch
	// CountRevokedWorkers, which differs only in the comparison operator.
	case strings.Contains(sql, "COUNT(*) FROM workers WHERE status != 'revoked'"):
		return countRow{n: s.workerCount}

	// GetWorkerByAgentTokenHash, used by the reconnect path.
	case strings.Contains(sql, "agent_token_hash = $1"):
		if s.unknownAgentToken {
			return errRow{pgx.ErrNoRows}
		}
		return strandWorkerRow{}

	// GetWorkerByHostname / GetWorkerByHostnameForUpdate: hostname is $1.
	case strings.Contains(sql, "FROM workers") && strings.Contains(sql, "hostname = $1"):
		if s.storeErr != nil {
			return errRow{s.storeErr}
		}
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
		if s.storeErr != nil {
			return errRow{s.storeErr}
		}
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
func (s *rowScript) queryRowsSeen() []scriptedQuery {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]scriptedQuery, len(s.seen))
	copy(out, s.seen)
	return out
}

// sawStatement asks only WHETHER a statement was issued. Use sawStatementOn when
// the question is where - "no INSERT at all" and "no INSERT inside the
// transaction" are different claims and only the fixture can tell them apart.
func (s *rowScript) sawStatement(substr string) bool {
	for _, q := range s.queryRowsSeen() {
		if strings.Contains(q.sql, substr) {
			return true
		}
	}
	return false
}

// sawStatementOn asks whether a statement was issued on a PARTICULAR handle -
// "tx" for the enrollment transaction, "pool" for a bare h.q call outside it.
// It is what pins transactional placement, which nothing else in this package
// can see.
func (s *rowScript) sawStatementOn(owner, substr string) bool {
	for _, q := range s.queryRowsSeen() {
		if q.owner == owner && strings.Contains(q.sql, substr) {
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
	// workerCount: what CountWorkers answers with, for the ceiling tests.
	workerCount int64
	// storeErr: a non-ErrNoRows store fault on the insert or the lookup.
	storeErr        error
	allowAutoEnroll bool
	execTag         string // "" keeps fakeTx's historical "DELETE 0"
}

func newEnrollFixture(t *testing.T, cfg enrollConfig) *enrollFixture {
	t.Helper()

	script := &rowScript{
		existingHostname:     cfg.existingHostname,
		existingHasLiveToken: cfg.existingHasLiveToken,
		workerCount:          cfg.workerCount,
		storeErr:             cfg.storeErr,
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

	// THE INSERT MUST BE ON THE TRANSACTION, not on the pool. Hoisting it out
	// leaves this test green on every other assertion while a SetWorkerAgentToken
	// failure would strand an orphan row with a NULL agent_token_hash - blocking
	// that hostname forever and counting against nothing.
	assert.True(t, f.script.sawStatementOn("tx", "INSERT INTO workers"),
		"InsertWorkerForAutoEnroll must be issued inside the auto-enroll transaction")
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
	assert.True(t, f.script.sawStatementOn("tx", "INSERT INTO workers"),
		"the refusal must come from the INSERT inside the transaction - a create-only guard "+
			"hoisted onto the pool refuses identically here and rolls nothing back in production")
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

// TestConnect_EveryCredentialRefusalIsIndistinguishable drives every distinct
// refusal OUTCOME on the gRPC registration surface and compares the produced
// messages WITH EACH OTHER rather than each against a literal - comparing
// literals would still pass if every site were changed to something disclosing.
//
// IT USED TO COVER TWO HAND-PICKED ARMS, and that is how "auto-enroll disabled"
// survived: README claims every credential failure here returns the identical
// status and the identical string, and that arm returned "auto-enroll disabled",
// so an unauthenticated peer could fingerprint whether RELAY_ALLOW_AUTO_ENROLL
// is set - free, side-effect-free, and no worker row touched. It is also cause
// (1) of the three the agent's exit message tells an operator to check, i.e. the
// one the design says is indistinguishable was the one that was not.
//
// THIS TABLE IS NOT EXHAUSTIVE OVER THE REFUSAL SITES, and the previous revision
// of this comment claimed it was "BY CONSTRUCTION" while its own next sentence
// admitted a new arm is only caught if added here too. handler.go carries ELEVEN
// codes.Unauthenticated returns; five distinct outcomes reach this table, because
// several sites produce the same one (enrollAndRegister alone has five ways to
// say "bad enrollment token"). An exhaustiveness claim is a claim about the
// COMPLEMENT and cannot be checked by reading the test that makes it.
//
// What actually holds the property is TestRegistrationRefusals_AllUseTheSharedConstant,
// which parses handler.go and requires every one of those eleven to pass the
// msgAuthFailed CONSTANT rather than its own literal - so a twelfth site is
// indistinguishable whether or not anyone remembers this table. What this table
// adds on top is end-to-end evidence that the five outcomes are actually
// REACHABLE and actually agree, which a source-level guard cannot show.
func TestConnect_EveryCredentialRefusalIsIndistinguishable(t *testing.T) {
	ceiling := 1
	arms := []struct {
		name  string
		build func(t *testing.T) *enrollFixture
	}{
		{"auto-enroll DISABLED, no credential", func(t *testing.T) *enrollFixture {
			return newEnrollFixture(t, enrollConfig{hostname: "any-host"}) // allowAutoEnroll false
		}},
		{"auto-enroll, hostname already claimed", func(t *testing.T) *enrollFixture {
			return newEnrollFixture(t, enrollConfig{
				hostname: "taken-host", existingHostname: "taken-host", allowAutoEnroll: true,
			})
		}},
		{"auto-enroll, fleet at the ceiling", func(t *testing.T) *enrollFixture {
			f := newEnrollFixture(t, enrollConfig{hostname: "fresh-host", allowAutoEnroll: true, workerCount: 9})
			f.h.AutoEnrollWorkerCeiling = &ceiling
			return f
		}},
		{"reconnect, unknown agent token", func(t *testing.T) *enrollFixture {
			f := newEnrollFixture(t, enrollConfig{
				hostname:   "any-host",
				credential: &relayv1.RegisterRequest_AgentToken{AgentToken: "no-such-token"},
			})
			f.script.unknownAgentToken = true
			return f
		}},
		{"enrollment token, hostname holds a live credential", func(t *testing.T) *enrollFixture {
			return newEnrollFixture(t, enrollConfig{
				hostname: "live-host", existingHostname: "live-host", existingHasLiveToken: true,
				credential: &relayv1.RegisterRequest_EnrollmentToken{EnrollmentToken: "raw-enrollment-token"},
				execTag:    "UPDATE 1",
			})
		}},
	}

	type outcome struct {
		name string
		code codes.Code
		msg  string
	}
	var got []outcome
	for _, a := range arms {
		f := a.build(t)
		_, err := f.connect(t)
		require.Error(t, err, "%s must be refused", a.name)
		got = append(got, outcome{a.name, status.Code(err), status.Convert(err).Message()})
	}

	first := got[0]
	for _, o := range got[1:] {
		assert.Equal(t, first.code, o.code,
			"%q and %q must return the same status code", first.name, o.name)
		assert.Equal(t, first.msg, o.msg,
			"%q and %q must return the same message; a caller that can tell them apart can "+
				"fingerprint server configuration and hostname state without a credential", first.name, o.name)
	}

	for _, o := range got {
		for _, leak := range []string{"taken-host", "live-host", "revoked", "exists", "claimed", "ceiling", "disabled", "auto-enroll"} {
			assert.NotContains(t, o.msg, leak,
				"%q discloses %q: a refusal must say nothing beyond the refusal itself", o.name, leak)
		}
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
		// THE GUARD MUST PRECEDE ConsumeAgentEnrollment, and rollback makes the two
		// orderings observationally identical everywhere else - moving the guard
		// below the consume survives the whole mutation battery without this line.
		// A consumed one-shot admin credential that bought nothing is a real cost
		// even when the transaction unwinds it, because a retry needs a NEW token.
		assert.NotContains(t, e.sql, "consumed_at",
			"the live-credential guard must refuse BEFORE the enrollment token is consumed")
	}

	// FOR UPDATE, ON THE TRANSACTION. enrollAndRegister's comment claims the lock
	// is what makes this non-racy for an existing row; a lookup hoisted onto the
	// pool takes a lock that is released before the upsert runs, and until this
	// assertion existed that claim had no witness at all.
	assert.True(t, f.script.sawStatementOn("tx", "FOR UPDATE"),
		"the worker lookup must be issued inside the enrollment transaction")

	// BOTH HALVES, AND THEY ARE NOT REDUNDANT. commits == 0 says the transaction
	// did not succeed; rollbacks >= 1 says a transaction was OPENED AND UNWOUND at
	// all, which is what fails when the guard is hoisted above BeginTxFunc - the
	// refusal then returns before any transaction exists and this count is 0. Do
	// not delete either one as duplicative of the other.
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

// TestConnect_AutoEnrollRefusesWhenTheFleetIsAtTheCeiling is item 2's criterion 1.
func TestConnect_AutoEnrollRefusesWhenTheFleetIsAtTheCeiling(t *testing.T) {
	ceiling := 3
	f := newEnrollFixture(t, enrollConfig{hostname: "fresh-host", allowAutoEnroll: true, workerCount: 3})
	f.h.AutoEnrollWorkerCeiling = &ceiling

	_, err := f.connect(t)
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))

	// THE CHECK MUST PRECEDE THE WRITE, which is what makes the refusal free of
	// side effects. Asserting only the refusal cannot see a check moved after it.
	assert.False(t, f.script.sawStatement("INSERT INTO workers"),
		"the ceiling check must run BEFORE the insert, so a refused auto-enroll writes nothing")
	assert.True(t, f.script.sawStatementOn("tx", "COUNT(*) FROM workers"),
		"CountWorkers must be read inside the same transaction as the insert it gates; on the pool "+
			"it is a separate snapshot and the check no longer bounds the write it is guarding")
	assert.Equal(t, 0, f.stream.tokensSent())
}

// TestConnect_AutoEnrollAdmitsOneBelowTheCeiling is the BOUNDARY test, and it is
// what distinguishes >= from >. The at-the-ceiling test alone cannot see that
// one-character mutation.
func TestConnect_AutoEnrollAdmitsOneBelowTheCeiling(t *testing.T) {
	ceiling := 3
	f := newEnrollFixture(t, enrollConfig{hostname: "fresh-host", allowAutoEnroll: true, workerCount: 2})
	f.h.AutoEnrollWorkerCeiling = &ceiling

	ev, err := f.connect(t)
	require.NoError(t, err)
	requireWorkerOnline(t, ev)
}

// TestConnect_AutoEnrollCeilingOfZeroIsDisabled pins the knob's meaningful zero.
func TestConnect_AutoEnrollCeilingOfZeroIsDisabled(t *testing.T) {
	zero := 0
	f := newEnrollFixture(t, enrollConfig{hostname: "fresh-host", allowAutoEnroll: true, workerCount: 100000})
	f.h.AutoEnrollWorkerCeiling = &zero

	_, err := f.connect(t)
	require.NoError(t, err, "0 disables the ceiling; it must not mean a ceiling of zero")
}

// TestConnect_EnrollmentTokenIsNotSubjectToTheCeiling is item 2's criterion 2.
// The ceiling gates the auto-enroll path ONLY, which is what makes "use
// enrollment tokens" the without-downtime answer when the budget is exhausted.
func TestConnect_EnrollmentTokenIsNotSubjectToTheCeiling(t *testing.T) {
	ceiling := 1
	f := newEnrollFixture(t, enrollConfig{
		hostname:    "token-host",
		credential:  &relayv1.RegisterRequest_EnrollmentToken{EnrollmentToken: "raw-enrollment-token"},
		execTag:     "UPDATE 1",
		workerCount: 100000,
	})
	f.h.AutoEnrollWorkerCeiling = &ceiling

	ev, err := f.connect(t)
	require.NoError(t, err)
	requireWorkerOnline(t, ev)
	assert.False(t, f.script.sawStatement("COUNT(*) FROM workers"),
		"the enrollment path must not even ASK the ceiling question")
}

// TestConnect_ReconnectIsRefusedByNeitherGuard is item 2's criterion 3. THE
// "NO INSERT" HALF IS WHAT PROVES the path is row-free rather than merely
// succeeding.
func TestConnect_ReconnectIsRefusedByNeitherGuard(t *testing.T) {
	ceiling := 1
	f := newEnrollFixture(t, enrollConfig{
		hostname:         "strand-host",
		existingHostname: "strand-host",
		credential:       &relayv1.RegisterRequest_AgentToken{AgentToken: "strand-agent-token"},
		workerCount:      100000,
	})
	f.h.AutoEnrollWorkerCeiling = &ceiling

	ev, err := f.connect(t)
	require.NoError(t, err)
	requireWorkerOnline(t, ev)
	assert.False(t, f.script.sawStatement("INSERT INTO workers"))
	assert.False(t, f.script.sawStatement("COUNT(*) FROM workers"))
}

// TestConnect_EachEnrollmentRefusalMovesItsOwnCounter. Three refusals that are
// INDISTINGUISHABLE to the caller by design must still be distinguishable to the
// operator, and this is the only place that is true.
func TestConnect_EachEnrollmentRefusalMovesItsOwnCounter(t *testing.T) {
	claimed := newEnrollFixture(t, enrollConfig{
		hostname: "taken-host", existingHostname: "taken-host", allowAutoEnroll: true,
	})
	_, err := claimed.connect(t)
	require.Error(t, err)
	assert.Equal(t, EnrollmentRefusalCounts{HostnameClaimed: 1}, claimed.h.EnrollmentRefusals())

	ceiling := 1
	full := newEnrollFixture(t, enrollConfig{hostname: "fresh-host", allowAutoEnroll: true, workerCount: 9})
	full.h.AutoEnrollWorkerCeiling = &ceiling
	_, err = full.connect(t)
	require.Error(t, err)
	assert.Equal(t, EnrollmentRefusalCounts{FleetAtCeiling: 1}, full.h.EnrollmentRefusals())

	live := newEnrollFixture(t, enrollConfig{
		hostname: "live-host", existingHostname: "live-host", existingHasLiveToken: true,
		credential: &relayv1.RegisterRequest_EnrollmentToken{EnrollmentToken: "raw-enrollment-token"},
		execTag:    "UPDATE 1",
	})
	_, err = live.connect(t)
	require.Error(t, err)
	assert.Equal(t, EnrollmentRefusalCounts{CredentialLive: 1}, live.h.EnrollmentRefusals())
}

// TestConnect_EnrollmentRefusalWritesNoLogLine. The whole captured log must be
// EMPTY across repeated refusals, so any wording added later reddens this rather
// than passing a substring check. Mirrors
// TestRegisterWorker_ReconcileEchoesAnUnparseableRunningTaskIdAndLogsNothing.
func TestConnect_EnrollmentRefusalWritesNoLogLine(t *testing.T) {
	logged := captureUnitLog(t)

	for i := 0; i < 5; i++ {
		f := newEnrollFixture(t, enrollConfig{
			hostname: "taken-host", existingHostname: "taken-host", allowAutoEnroll: true,
		})
		_, err := f.connect(t)
		require.Error(t, err)
	}

	assert.Empty(t, logged(),
		"a refusal is unboundedly repeatable by the same caller with the same hostname, and the "+
			"per-connection log limiter is not even allocated until after registration. Refusals are "+
			"COUNTED (Handler.EnrollmentRefusals), never logged.")
}

// TestConnect_AutoEnrollSuccessStillWritesExactlyOneAuditLine. The success line
// survives unbudgeted BY DECISION, and after the create-only guard the argument
// is stronger than it was: a token-less enrollment is now one line per hostname
// FOREVER, because that hostname can never be auto-enrolled again.
func TestConnect_AutoEnrollSuccessStillWritesExactlyOneAuditLine(t *testing.T) {
	logged := captureUnitLog(t)

	f := newEnrollFixture(t, enrollConfig{hostname: "fresh-host", allowAutoEnroll: true})
	_, err := f.connect(t)
	require.NoError(t, err)

	out := logged()
	assert.Equal(t, 1, strings.Count(out, "auto-enrolled worker"))
	assert.Contains(t, out, `hostname="fresh-host"`, "%q is the injection defence and must stay")
}

// TestAutoEnrollWorkerCeiling_ResolvesTheUnsetAndNegativeCasesToTheDefault exists
// because the mutation battery found the hole: changing this resolver's fallback
// from DefaultAutoEnrollWorkerCeiling to 1 left every test in three packages
// GREEN. Nothing pinned it. TestParseAutoEnrollCeiling looks like it does and
// does not - it compares the parser's output against the same constant, so it is
// self-referential and moves with any change to the constant OR to this
// fallback.
//
// The three arms are not interchangeable. nil is "cmd/relay-server never set the
// field", which is every bare &Handler{} in this package; a non-nil ZERO is
// DISABLED and must NOT resolve to the default, which is the whole reason this
// field is a *int.
//
// THE NEGATIVE ARM IS DEFENSIVE AND UNREACHABLE FROM main, and saying so is the
// point rather than a caveat on it. parseAutoEnrollCeiling folds a negative or
// unparseable value to the default BEFORE assignment, so nothing
// cmd/relay-server can be configured to do produces a pointer to a negative
// number here. It is reachable only by a caller constructing a Handler directly
// - a test, or a future embedder - and it exists so such a caller fails BOUNDED
// rather than with a ceiling that refuses every enrollment.
func TestAutoEnrollWorkerCeiling_ResolvesTheUnsetAndNegativeCasesToTheDefault(t *testing.T) {
	neg, zero, pos := -1, 0, 7

	assert.Equal(t, DefaultAutoEnrollWorkerCeiling, (&Handler{}).autoEnrollWorkerCeiling(),
		"a nil field means UNSET and must resolve to the default, not to some other number")
	assert.Equal(t, DefaultAutoEnrollWorkerCeiling,
		(&Handler{AutoEnrollWorkerCeiling: &neg}).autoEnrollWorkerCeiling(),
		"a negative value must resolve to the default rather than disabling the bound or refusing everything")
	assert.Equal(t, 0, (&Handler{AutoEnrollWorkerCeiling: &zero}).autoEnrollWorkerCeiling(),
		"a non-nil zero means DISABLED and must never be folded into the default")
	assert.Equal(t, 7, (&Handler{AutoEnrollWorkerCeiling: &pos}).autoEnrollWorkerCeiling())
}

// pgFaultOnACallerControlledHostname is the shape a real Postgres error takes
// when an unvalidated hostname exceeds the btree entry limit on the
// workers.hostname unique index. reg.Hostname is a caller-supplied proto string
// bounded only by gRPC's 4 MiB receive limit, so this is reachable by a peer
// that has presented no credential at all.
var pgFaultOnACallerControlledHostname = errors.New(
	`ERROR: index row size 3000 exceeds btree version 4 maximum 2704 for index "workers_hostname_key" (SQLSTATE 54000)`)

// TestConnect_AStoreFaultDuringEnrollmentDisclosesNothingToTheCaller. Both
// enrollment transactions used to wrap a non-ErrNoRows store error and return it
// VERBATIM. There is no sanitizing interceptor on this server, so grpc-go sends
// the whole text to the peer as codes.Unknown - table name, index name and
// SQLSTATE included, to a caller that authenticated with nothing.
//
// The two arms are driven together because the two paths reach a raw error
// through DIFFERENT statements: auto-enroll through the create-only insert,
// enrollment through the FOR UPDATE lookup. Fixing one and not the other is the
// obvious half-fix and this is what catches it.
func TestConnect_AStoreFaultDuringEnrollmentDisclosesNothingToTheCaller(t *testing.T) {
	cases := []struct {
		name string
		cfg  enrollConfig
	}{
		{"auto-enroll, failing on the create-only insert", enrollConfig{
			hostname: "fresh-host", allowAutoEnroll: true,
			storeErr: pgFaultOnACallerControlledHostname,
		}},
		{"enrollment token, failing on the FOR UPDATE lookup", enrollConfig{
			hostname:   "fresh-host",
			credential: &relayv1.RegisterRequest_EnrollmentToken{EnrollmentToken: "raw-enrollment-token"},
			execTag:    "UPDATE 1",
			storeErr:   pgFaultOnACallerControlledHostname,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newEnrollFixture(t, tc.cfg)

			_, err := f.connect(t)
			require.Error(t, err)

			// Internal, not Unknown: a store fault is a SERVER fault and must say so
			// with a status the handler chose, rather than by grpc-go stringifying
			// whatever bubbled up.
			assert.Equal(t, codes.Internal, status.Code(err))

			msg := status.Convert(err).Message()
			for _, leak := range []string{"workers_hostname_key", "SQLSTATE", "btree", "index row size", "INSERT", "workers"} {
				assert.NotContains(t, msg, leak,
					"a peer that presented no credential must not be told the schema; got %q", msg)
			}
		})
	}
}

// TestDefaultAutoEnrollWorkerCeiling_IsTheValueREADMEDocuments. One layer ABOVE
// the resolver test: that one pins "nil resolves to the default", this pins what
// the default IS. Changing the constant from 1024 to 7 left all three packages
// green while README documented 1024 in two places and the startup line printed
// 7 - the classic wrong-prose shape, with the prose right and the code moved.
func TestDefaultAutoEnrollWorkerCeiling_IsTheValueREADMEDocuments(t *testing.T) {
	assert.Equal(t, 1024, DefaultAutoEnrollWorkerCeiling,
		"README documents 1024 in two places (the env table row and the auto-enrollment cost section); "+
			"change both, and the RELAY_GRPC_MAX_CONNS anchor this is derived from, before changing this")
}
