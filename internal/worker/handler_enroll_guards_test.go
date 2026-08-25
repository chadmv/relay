package worker

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"relay/internal/events"
	"relay/internal/metrics"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	// GetWorkerByHostname / GetWorkerByHostnameForUpdate: hostname is $1.
	case strings.Contains(sql, "FROM workers") && strings.Contains(sql, "hostname = $1"):
		if s.existingHostname != "" && strArg(args, 0) == s.existingHostname {
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
	allowAutoEnroll  bool
	execTag          string // "" keeps fakeTx's historical "DELETE 0"
}

func newEnrollFixture(t *testing.T, cfg enrollConfig) *enrollFixture {
	t.Helper()

	script := &rowScript{existingHostname: cfg.existingHostname}
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
