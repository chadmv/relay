package main

import (
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// defaultDBStatementTimeout is what an unset RELAY_DB_STATEMENT_TIMEOUT
// resolves to. It is NOT the control that bounds a ?q= scan at today's table
// size and nobody should read it as one; its job is to turn "a statement can
// hold a pool connection indefinitely if the table grows, the plan flips, or the
// box is contended" into a bounded hold, for every statement rather than for two
// handlers.
const defaultDBStatementTimeout = 30 * time.Second

// parseDBStatementTimeout resolves RELAY_DB_STATEMENT_TIMEOUT into the value to
// write into the pool's statement_timeout runtime parameter, or "" meaning relay
// sets no key at all.
//
// FAIL-CLOSED ON A BAD VALUE, unlike RELAY_TASK_WATCHDOG_MARGIN next door, and
// the difference is the direction the mistake fails in. A watchdog bound that
// falls back to a default is still a bound. Here the value travels into a
// startup packet and pgxpool.NewWithConfig does not necessarily establish a
// connection eagerly, so a malformed runtime parameter surfaces as a connection
// error at the first query rather than at boot. The caller turns the error into
// a log.Fatalf, as parsePublicURL and ParseRateLimit already do.
//
// An explicit 0 returns "", meaning relay does not set the key and leaves
// whatever the DSN, the role or the server default provides. That is an escape
// for a deployment managing the setting elsewhere. It is NOT a tuning knob and
// must never appear in a remedy list beside "raise the value".
func parseDBStatementTimeout(name, raw string) (string, error) {
	if raw == "" {
		return strconv.FormatInt(defaultDBStatementTimeout.Milliseconds(), 10), nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return "", fmt.Errorf("%s is not a Go duration (try 30s, 500ms, 2m)", name)
	}
	if d < 0 {
		return "", fmt.Errorf("%s must not be negative", name)
	}
	if d == 0 {
		return "", nil
	}
	// Postgres reads statement_timeout = 0 as DISABLED. Duration is nanoseconds
	// and Milliseconds() truncates, so every positive value below 1ms renders as
	// "0" - 999999ns is the largest of them - and would unarm the control while
	// reading as the tightest setting on offer. Refuse rather than clamp: an
	// operator who wrote 100us meant something this parameter cannot express.
	//
	// %s of d renders the PARSED duration, not the raw environment string: this
	// message goes to a stderr usually shipped somewhere with broader read access
	// than the environment had, and a time.Duration can carry nothing but a
	// number and a unit.
	if d.Milliseconds() == 0 {
		return "", fmt.Errorf(
			"%s=%s is below 1ms, which Postgres would round to 0 and read as DISABLE. "+
				"Use 1ms or more, or exactly 0 if you meant to disable it.", name, d)
	}
	return strconv.FormatInt(d.Milliseconds(), 10), nil
}

// applyStatementTimeout writes param into the pool config's session runtime
// parameters, or leaves the key untouched when param is "".
//
// RuntimeParams rather than pgxpool.Config.AfterConnect: pgx sends these in the
// startup packet, so every pooled connection carries the setting from the moment
// it is established, with no extra round trip and no code path where a failed
// SET has to be handled. pgxpool.ParseConfig guarantees the map is non-nil.
//
// SETTING THE KEY OVERWRITES WHATEVER THE DSN SUPPLIED, deliberately: relay's
// setting wins, and the escape for a deployment that manages the timeout at the
// DSN or role level is the "" value, not a merge rule.
//
// THIS DOES NOT REACH MIGRATIONS AND MUST NOT. store.Migrate opens its own
// connection through golang-migrate before main ever calls pgxpool.ParseConfig,
// so a CREATE INDEX that runs for minutes on a large table is unaffected. That
// is also why this is applied in Go and never documented as "put
// statement_timeout in your DSN": migrateDSN is derived from dsn by prefix
// rewriting, so a DSN-carried timeout WOULD reach migrations.
func applyStatementTimeout(cfg *pgxpool.Config, param string) {
	if param == "" {
		return
	}
	cfg.ConnConfig.Config.RuntimeParams["statement_timeout"] = param
}

// dbStatementTimeoutLine renders the unconditional startup line. A fail-closed
// parser plus a silent success is half a control: the disabled state has to be
// visible in the boot log, because it is the state in which nothing else in the
// system will ever mention this bound again.
func dbStatementTimeoutLine(param string) string {
	if param == "" {
		return "database statement timeout: NOT SET by relay (RELAY_DB_STATEMENT_TIMEOUT=0), so a " +
			"statement's runtime is bounded only by whatever the DSN, the role or the server default " +
			"provides. A single query can hold a pool connection for as long as it runs."
	}
	return fmt.Sprintf("database statement timeout: %sms on every pooled connection "+
		"(RELAY_DB_STATEMENT_TIMEOUT). A statement exceeding it is cancelled by the server and the "+
		"request answers 500.", param)
}
