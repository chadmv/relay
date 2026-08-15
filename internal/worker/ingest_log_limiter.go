package worker

import "time"

// ingestLogLimiter bounds caller-driven log volume for ONE agent connection.
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
// map now holds five kinds of key, NOT because it closes the flood. Moving the
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
type ingestLogLimiter struct {
	// seen maps a key to the instant it was last LOGGED. An entry older than
	// ingestLogDedupeWindow is treated as absent, which is what re-arms the key.
	seen   map[logKey]time.Time
	tokens int
	last   time.Time
	now    func() time.Time // injectable for the deterministic refill tests only
}

// logKind partitions the budget's dedupe keys. Values are never persisted or
// sent anywhere, so they may be renumbered freely.
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
	kindInventory                          // handleInventoryUpdate's persist failure
)

// logKey is the dedupe key. Only kindTaskLogPersist populates id and epoch; the
// other four kinds deliberately carry NO wire value, so the caller cannot vary
// them (see the per-site table in the spec's section 6.4).
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
	// BE TIME-BOUNDED, and that is not a nicety - four of the five kinds carry no
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

func newIngestLogLimiter() *ingestLogLimiter { return newIngestLogLimiterAt(time.Now) }

// newIngestLogLimiterAt is newIngestLogLimiter with the clock injected. Every
// time this type reads comes through `now`, INCLUDING the initial `last` stamp:
// a constructor that stamped last from time.Now directly while a test overrode
// only the field would hand that test a real-vs-fake skew of however far apart
// the two clocks happen to be, and the first allow would silently refill by
// hours. There is no way to half-use the seam from here.
func newIngestLogLimiterAt(now func() time.Time) *ingestLogLimiter {
	return &ingestLogLimiter{
		seen:   make(map[logKey]time.Time),
		tokens: ingestLogBurst,
		last:   now(),
		now:    now,
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
		return false
	}

	// 3. SPEND. A key suppressed for lack of a token is deliberately NOT
	// recorded, so the diagnostic reappears when tokens refill rather than being
	// swallowed for the connection's whole lifetime.
	if l.tokens == 0 {
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
	// all for most keys: the four kinds carrying no wire value can never reach 128
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
