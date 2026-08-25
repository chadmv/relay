package worker

import "time"

// ingestLogLimiter bounds caller-driven log volume for ONE agent connection.
//
// WHAT BOUNDS THE UNIT THIS BUDGET IS STATED PER. A bound stated per connection
// is only a bound if connections are bounded, and until 2026-08-20 they were not.
// They are now, in three places:
//
//   - grpcMaxConcurrentStreams = 1 (cmd/relay-server/grpc_config.go) means one
//     connection carries at most ONE of these AT A TIME, not MaxUint32 of them -
//     this type is allocated per Connect call, i.e. per STREAM (handler.go:172).
//   - RELAY_GRPC_MAX_CONNS (default 1024) bounds live connections fleet-wide.
//   - RELAY_GRPC_MAX_CONNS_PER_IP (default 64) bounds them per source address.
//
// The arithmetic, out loud, because a bound nobody has multiplied is not yet a
// claim. What those three knobs bound exactly is the number of these limiters
// ALIVE AT ONCE, so the honest figure is the BURST: at the defaults, 1024 x 16 =
// 16384 lines fleet-wide and 64 x 16 = 1024 lines per source address. RAISING
// RELAY_GRPC_MAX_CONNS SCALES THAT LINEARLY, so those two knobs are part of this
// control's threat model and not merely capacity settings. Setting either to 0
// disables that cap and removes the corresponding ceiling entirely.
//
// WHAT IS NOT BOUNDED, STATED RATHER THAN GLOSSED. The steady-state rate - 6
// lines per minute per limiter - is a per-STREAM figure, and it holds only while
// a stream stays open. grpc.MaxConcurrentStreams caps CONCURRENT streams, not
// how many a connection may open over its life, so a caller that repeatedly
// opens a stream, spends its 16-token burst and closes it gets a fresh burst
// every cycle without ever needing a second connection. Multiplying 1024 by 6
// and calling the result a lines-per-minute ceiling would therefore be wrong.
// What actually prices that attack is not this file: reaching any of these log
// sites requires passing authenticateAndRegister first, which runs BEFORE the
// allocation of this limiter, so each cycle costs the caller a valid credential
// and a round trip to Postgres. That is a real cost, not a bound.
//
// THAT PRICE IS ZERO UNDER RELAY_ALLOW_AUTO_ENROLL. On that path the credential
// is not merely cheap, it is absent: any host able to reach the port registers
// by claiming a hostname. The Postgres round trip remains, and so do the
// connection caps and RELAY_GRPC_REGISTRATION_TIMEOUT, but the sentence above
// describes the credentialed paths only. Auto-enrollment is off by default and
// its documented trust model is that any host able to reach gRPC is trusted;
// this is one more thing that model is buying.
//
// See docs/superpowers/specs/2026-08-20-grpc-admission-bounds.md.
//
// It is two things stacked, and the split is the whole point.
//
//   - `seen` is a DEDUPLICATOR. It collapses a repeating failure to one line per
//     ingestLogDedupeWindow, and it is keyed on wire-supplied values on purpose,
//     because a caller that varies the key only makes its own diagnostics
//     noisier. It stores the LAST-LOGGED INSTANT, not a bare presence flag: the
//     suppression a dedupe map gives has to be time-bounded or it is permanent
//     for keys the caller cannot vary (see the window's own comment).
//   - `tokens` is the BOUND. It is keyed on nothing, so no wire value can
//     enlarge it.
//
// The predecessor (taskLogErrs, removed 2026-08-15) used the dedupe map AS the
// bound, so one wire field defeated both: it stored one entry per task id with
// the epoch as the VALUE, so a caller varying chunk.Epoch on a single task id
// overwrote that one entry forever, never reached the capacity cap, and got one
// log line per message with a map of exactly one entry. Do not merge these two
// back together, and in particular do not delete the bucket on the grounds that
// the map already limits things - the map deliberately does not.
//
// Note what the composite key does and does not buy. It is required because one
// map now holds eight kinds of key, NOT because it closes the flood. Moving the
// epoch back into the value would still be bounded, because the bucket is the
// bound. The key shape is a diagnostics decision; the bucket is the security
// control.
//
// Owned by ONE goroutine: allocated in Connect and used only from that
// connection's recv loop. No mutex, by design - adding one would be the second
// lock on a path whose whole complaint is lock contention. If a caller ever
// appears off that goroutine, that caller is the finding, not the missing lock.
// It is a stack local in Connect, so it dies with the frame: there is no
// teardown to get wrong and no way for one connection to reach another's.
//
// WHAT THE BUDGET COVERS, so the next reader does not have to enumerate call
// sites to find out. EIGHT log sites on the gRPC receive path go through it:
// handleTaskLog's bad-id and persist lines, handleTaskStatus's bad-id, GetTask,
// retry-write, status-write and dependency-cascade lines, and
// handleInventoryUpdate's persist line. WHAT IS STILL OUTSIDE IT, and why:
// registration-time lines (the budget is allocated after
// authenticateAndRegister returns - bug-2026-08-15-registration-log-sites-are-
// outside-the-connection-budget) and markWorkerOffline's teardown line, which
// runs once per connection teardown and so is bounded by the connection caps
// rather than by message volume. handleTaskLog's marshal line is deliberately
// unbudgeted; see its own comment for the argument. Counted at HEAD: thirteen
// log.Printf sites in handler.go, eight budgeted and five not.
//
// ONE FIELD IS THE EXCEPTION AND IT IS DELIBERATE: `drops` points at the
// Handler's process-lifetime counters, which every connection shares, because a
// count of what was dropped that died with the frame would read zero exactly
// when an operator went looking for it. That pointer is written only by atomic
// adds, so the no-mutex property above holds verbatim. The BUDGET is private;
// the COUNTERS are process-wide. Do not merge the two.
type ingestLogLimiter struct {
	// seen maps a key to the instant it was last LOGGED. An entry older than
	// ingestLogDedupeWindow is treated as absent, which is what re-arms the key.
	seen   map[logKey]time.Time
	tokens int
	last   time.Time
	now    func() time.Time // injectable for the deterministic refill tests only

	// drops is the process-lifetime counter home, shared with every other
	// connection's limiter. IT IS THE ONE THING IN THIS TYPE THAT IS SHARED, and
	// that is deliberate: a count of what was dropped has to outlive the
	// connection that caused it, or an operator reads zero after the attacker
	// disconnects. Every write to it is an atomic add - see ingestLogCounters -
	// so this type keeps its no-mutex property verbatim.
	drops *ingestLogCounters
}

// logKind partitions the budget's dedupe keys.
//
// TWO PROPERTIES OF THESE CONSTANTS ARE LOAD-BEARING, and the sentence this
// paragraph replaces - "Values are never persisted or sent anywhere, so they may
// be renumbered freely" - became false in BOTH directions on 2026-08-21, when
// the drops started being counted and published.
//
//   - THE VALUES ARE ARRAY INDICES. ingestLogCounters is a [kindCount][2] array
//     indexed by these constants, so they must stay a DENSE RUN starting at 1
//     with kindCount immediately after the last one. A gap, an explicit
//     out-of-run value, or a kind declared after the sentinel makes
//     ingestLogCounters.record drop that kind's counts SILENTLY - it fails
//     closed rather than panicking, because a panic on the recv goroutine kills
//     the process. Pinned by TestIngestLogKindsAreADenseRunFromOne and
//     TestEveryIngestLogKindUsedAtACallSiteIsCountedAndPublished, which are the
//     only things keeping that branch unreachable.
//   - THE NAMES ARE A RESPONSE CONTRACT. GET /v1/server/counters publishes one
//     JSON key per kind under ingest_log_budget.counts.deduped and
//     .counts.suppressed. RENAMING A KIND RENAMES AN OPERATOR-VISIBLE KEY and a
//     field of worker.IngestLogDropsByKind; ADDING one that nothing publishes is
//     a hole with no error. Pinned by
//     TestIngestLogCounters_EveryKindIsPublishedDistinctly here and by
//     counterPayloadLeaves in internal/api.
type logKind uint8

// The two bad-task-id kinds are deliberately SEPARATE, and the history is worth
// keeping because the original sharing looked like a saving and was not. One
// shared key let a single 1-byte forged TaskStatusUpdate{TaskId: "z"} consume the
// budget for the log path, whose line is the only signal anywhere for an agent
// losing 100% of a task's output to unparseable ids. Measured: 1 forged status
// message then 64 malformed log chunks gave 0 log-path lines and 1 status-path
// line. The sharing was defending ONE token out of sixteen; the thing it cost was
// the diagnostic for total, silent data loss.
const (
	kindTaskLogPersist  logKind = iota + 1 // handleTaskLog's non-ErrNoRows persist failure
	kindBadTaskIDLog                       // an unparseable task id on the LOG path
	kindBadTaskIDStatus                    // an unparseable task id on the STATUS path
	kindStatusGetTask                      // handleTaskStatus's non-ErrNoRows GetTask failure

	// kindInventory is handleInventoryUpdate's persist failure.
	kindInventory

	// THE THREE STATUS-WRITE KINDS ARE DELIBERATELY SEPARATE, AND ONE SHARED
	// KIND WOULD HAVE BEEN ONE JSON KEY INSTEAD OF THREE. Two things decided it:
	//
	//   - THEY ARE MUTUALLY EXCLUSIVE PER MESSAGE. The retry branch returns, the
	//     update branch returns, and FailDependentTasks runs only AFTER a
	//     successful UpdateTaskStatus. One message reaches at most one of them,
	//     so the split across three keys is a clean attribution rather than a
	//     mixture an operator has to decompose.
	//   - THE REMEDIES DIFFER. FailDependentTasks is a RECURSIVE CTE - the most
	//     expensive statement on this path and the first to hit a deadlock or a
	//     statement_timeout under contention - while UpdateTaskStatus is a
	//     single-row update. "Your recursive cascade is timing out" and "your
	//     simple updates are failing" are different incidents with different
	//     answers.
	//
	// The price is eight mechanical edits per kind, and it is paid down by the
	// guards that fire on an omission (see this type's comment).
	kindStatusRetryWrite     // handleTaskStatus's non-ErrNoRows IncrementTaskRetryCount failure
	kindStatusUpdateWrite    // handleTaskStatus's non-ErrNoRows UpdateTaskStatus failure
	kindStatusFailDependents // handleTaskStatus's FailDependentTasks failure (an :exec; no ErrNoRows arm)

	// kindCount MUST STAY LAST and is NOT a kind. It is the length of
	// ingestLogCounters' array. A kind added after it is not counted at all;
	// TestEveryIngestLogKindUsedAtACallSiteIsCountedAndPublished is what makes
	// that a RED test rather than a silent hole.
	kindCount
)

// logKey is the dedupe key. Only kindTaskLogPersist populates id and epoch; the
// other SEVEN kinds deliberately carry NO wire value, so the caller cannot vary
// them (see the per-site table in the spec's section 6.4). The three status-write
// kinds joined that set for the reason already written at kindStatusGetTask: an
// infrastructure episode is not per-task, so keying on the task id would multiply
// one event by the task count.
//
// The only string that ever reaches this struct is a CANONICALLY RE-ENCODED task
// id - uuidStr over the parsed pgtype.UUID, never the wire string. That is 36
// bytes by construction and, more importantly, it is 36 bytes the caller does not
// choose. Passing pgtype.UUID.Scan constrains the LENGTH of the wire string and
// not its bytes: for a 36-byte input parseUUID splices out indices 8, 13, 18 and
// 23 without ever checking they are hyphens, so those four bytes are fully
// attacker-chosen (Scan("aaaaaaaa\nbbbb\ncccc\ndddd\neeeeeeeeeeee") succeeds).
// Keying on the wire string would hand a caller 2^32 distinct keys for one
// (task, epoch) pair. The bad-id kinds, whose string has not parsed at all, store
// nothing here and clip + %q at the log site.
type logKey struct {
	kind  logKind
	id    string
	epoch int64
}

// All per connection, all untunable. THERE IS DELIBERATELY NO ENV KNOB: an
// operator raising the budget re-opens the vector this type exists to close, and
// no value here has an operational reason to move. Do not add one.
const (
	// Dedupe capacity. Far above any realistic count of distinct concurrently
	// failing tasks on one agent, so the honest case never clears.
	ingestLogSeenMax = 128

	// Tokens at connection start. Covers several tasks failing at once at
	// connection start without waiting on a refill.
	ingestLogBurst = 16

	// Steady state: the bucket regains 6 tokens per minute per connection, so a
	// flood of DISTINCT keys settles at 6 lines/min instead of one line per
	// message. It is the bucket alone; what a single repeating key reports is set
	// by ingestLogDedupeWindow below, not by this.
	ingestLogRefill = 10 * time.Second

	// How long one key stays deduplicated before it re-arms. THE SUPPRESSION MUST
	// BE TIME-BOUNDED, and that is not a nicety - seven of the eight kinds carry no
	// wire value, so each of them is exactly ONE key for the connection's whole
	// life and can never reach the capacity clear on its own. With a bare presence
	// flag they logged once per connection and then never again: a Postgres outage
	// at hour 1 reported one line and a SECOND, unrelated outage at hour 40 on the
	// same long-lived stream reported nothing at all. Measured with a frozen clock:
	// one failing inventory message per second for seven days gave allowed = 1 with
	// the bucket still full. That is a regression against the unbounded logging
	// this type replaced, and it is the project's own recorded lesson that a
	// recovery bound must be time-based - reset on duration, not on activity.
	//
	// 5m is chosen against the bucket, from both sides:
	//   - LONG ENOUGH that a repeating failure is still collapsed. One key
	//     re-arming costs 1 token per 5 minutes against a 6-tokens-per-minute
	//     refill, i.e. 0.6% of the steady-state budget, so no single repeating key
	//     can drain the bucket and starve the other kinds' diagnostics. The bucket
	//     therefore remains the bound, exactly as before.
	//   - SHORT ENOUGH that a new episode is actually reported. 5m is the worst
	//     case an operator waits for a fresh infra failure to surface on a
	//     connection that already logged that kind, and it is small next to any
	//     realistic incident.
	ingestLogDedupeWindow = 5 * time.Minute

	// Longest prefix of an UNPARSED, caller-supplied identifier that may reach
	// the log. Both bad-task-id sites run AFTER pgtype.UUID.Scan has failed, and
	// the auto-enroll site's reg.Hostname is never validated at all, so no length
	// constraint applies to any of them: each is a proto string bounded only by
	// gRPC's 4 MiB receive limit, and %q escapes without truncating. The budget
	// alone would still permit ingestLogBurst multi-megabyte lines, and the
	// auto-enroll line is outside the budget entirely.
	ingestLogIDClip = 64
)

func newIngestLogLimiter(drops *ingestLogCounters) *ingestLogLimiter {
	return newIngestLogLimiterAt(time.Now, drops)
}

// newIngestLogLimiterAt is newIngestLogLimiter with the clock injected. Every
// time this type reads comes through `now`, INCLUDING the initial `last` stamp:
// a constructor that stamped last from time.Now directly while a test overrode
// only the field would hand that test a real-vs-fake skew of however far apart
// the two clocks happen to be, and the first allow would silently refill by
// hours. There is no way to half-use the seam from here.
func newIngestLogLimiterAt(now func() time.Time, drops *ingestLogCounters) *ingestLogLimiter {
	return &ingestLogLimiter{
		seen:   make(map[logKey]time.Time),
		tokens: ingestLogBurst,
		last:   now(),
		now:    now,
		drops:  drops,
	}
}

// allow reports whether this log line may be emitted, recording the key if so.
//
// THREE of the four orderings below are pinned by tests, and the honest form of
// that is written here rather than implied. An earlier draft claimed "each one
// has a mutation that only this ordering survives", which was wrong; a review
// lens mutated all four and found only two reddened. Each mutation was re-run
// against the current unit suite (`go test ./internal/worker/`) and the count is
// now three:
//
//   - REFILL BEFORE SPEND: TestIngestLogLimiter_RefillsWholeIntervalsAndCapsAtBurst,
//     ...RefillDoesNotStallUnderCallsMoreFrequentThanTheInterval.
//   - DEDUPE BEFORE SPEND: TestIngestLogLimiter_DedupeHappensBeforeTheSpend. This
//     is what makes an honest repeating failure cost one token per dedupe window
//     rather than one per message.
//   - REFILL BEFORE DEDUPE: newly pinned by the window's own tests, which read
//     l.tokens straight after a re-arm. Moving the refill block below the dedupe
//     check gives `tokens went 15 -> 15, want a spend of exactly 1`, because a
//     re-arm's token then comes back in the same call that spent it.
//
// The one mutation that still survives is moving the capacity clear ABOVE the
// dedupe check. It is written after the dedupe for readability; do not claim a
// test defends that position.
func (l *ingestLogLimiter) allow(k logKey) bool {
	// Fail CLOSED rather than panic. Connect has no recover and grpc-go does not
	// recover handler panics, so a nil dereference here would kill the whole
	// server process. Losing a diagnostic is the cheaper failure. Production has
	// exactly one allocation site (Connect) so this is unreachable there.
	//
	// NOTHING IS COUNTED HERE, and it is not an oversight: no event was
	// suppressed on this path, because there was no limiter. It is also
	// unreachable to count - the counters live behind l - and adding a
	// package-level fallback to reach them would be a visible diff, not an
	// accident.
	if l == nil {
		return false
	}

	// ONE clock read per call, shared by the refill and the dedupe window, so the
	// two can never disagree about when "now" is within a single call.
	now := l.now()

	// 1. REFILL. Advance `last` by WHOLE CONSUMED INTERVALS, never to now:
	// setting last = now on a call that added zero tokens means a connection
	// calling more often than the refill interval never refills at all. That is
	// the most likely way to get this wrong.
	//
	// time.Now carries a monotonic reading and Sub uses it, so a wall-clock
	// adjustment cannot move this bucket.
	if elapsed := now.Sub(l.last); elapsed >= ingestLogRefill {
		n := int64(elapsed / ingestLogRefill)
		l.last = l.last.Add(time.Duration(n) * ingestLogRefill)
		if got := int64(l.tokens) + n; got >= int64(ingestLogBurst) {
			l.tokens = ingestLogBurst
		} else {
			l.tokens = int(got)
		}
	}

	// 2. DEDUPE, BEFORE the spend. An honest repeating failure - one task
	// streaming binary output - costs exactly ONE token per dedupe window no
	// matter how many chunks it produces.
	//
	// The window is what keeps this suppression from being permanent. An entry
	// older than it is treated as absent, so the key re-arms and pays for its
	// next line out of the bucket like any other. Re-arming on ELAPSED TIME and
	// not on a quiet period is deliberate: a key that keeps firing is exactly the
	// one an operator needs to keep hearing about, and an idle-based reset would
	// go permanently silent under a failure that never stops.
	if at, ok := l.seen[k]; ok && now.Sub(at) < ingestLogDedupeWindow {
		// COUNTED, NOT LOGGED. A log line here would be caller-driven volume on
		// the recv goroutine, which is the vector this whole type closes.
		l.drops.record(k.kind, ingestDropDeduped)
		return false
	}

	// 3. SPEND. A key suppressed for lack of a token is deliberately NOT
	// recorded, so the diagnostic reappears when tokens refill rather than being
	// swallowed for the connection's whole lifetime.
	if l.tokens == 0 {
		// The other arm, kept separate because they mean opposite things: this
		// one is a line dropped ENTIRELY, and a non-zero number here is either
		// an attack or a misconfiguration, while a deduped line is a healthy
		// collapse. One number for both would be uninterpretable.
		l.drops.record(k.kind, ingestDropSuppressed)
		return false
	}

	// 4. CAPACITY. Clearing re-arms every key, which is exactly the 2026-08-12
	// defect this type replaces - BUT ONLY WHEN THE MAP IS THE BOUND. With the
	// bucket as the bound, re-arming can at worst produce another burst and then
	// 6 lines/min. The alternative, permanent suppression, is also bounded but
	// has NO TIME-BASED RECOVERY: a connection that once tripped 128 distinct
	// failures would lose the diagnostic for its whole lifetime. Deleting the
	// bucket is therefore visibly the thing that re-opens the original bug.
	//
	// Note this branch is not the only recovery, and used not to be a recovery at
	// all for most keys: the seven kinds carrying no wire value can never reach 128
	// entries on their own, so they had exactly the permanent suppression this
	// comment argues against. ingestLogDedupeWindow is the general answer and this
	// branch is now only the map's size bound.
	if len(l.seen) >= ingestLogSeenMax {
		clear(l.seen)
	}

	l.tokens--
	l.seen[k] = now
	return true
}

// clipID bounds an UNVALIDATED caller-supplied string (a task id that failed to
// parse, a hostname that was never checked) before it reaches a log line. Callers must still use %q on the result: clipping is a volume control,
// %q is the injection control, and neither substitutes for the other. Slicing
// may split a UTF-8 rune; %q renders the partial bytes as \xNN escapes, which is
// safe.
func clipID(s string) string {
	if len(s) <= ingestLogIDClip {
		return s
	}
	return s[:ingestLogIDClip] + "...(truncated)"
}
