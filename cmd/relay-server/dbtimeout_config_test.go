package main

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The sub-millisecond row goes FIRST because it is the one failure mode that
// DISABLES the control while looking like a tightening: Postgres reads
// statement_timeout = 0 as "no timeout".
//
// 999999ns is the boundary, not 100us. Duration is nanoseconds and
// Milliseconds() truncates toward zero, so every positive duration strictly
// below 1ms renders as "0" and 999999ns is the largest of them. The 1ms row is
// the control: without it, a parser that refused every small value would pass on
// the refusal rows alone.
func TestParseDBStatementTimeout_SubMillisecondIsRefused(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
		want    string
	}{
		{"largest value that truncates to zero", "999999ns", true, ""},
		{"the spec's example", "100us", true, ""},
		{"smallest expressible timeout", "1ms", false, "1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseDBStatementTimeout("RELAY_DB_STATEMENT_TIMEOUT", tc.raw)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "RELAY_DB_STATEMENT_TIMEOUT",
					"a refusal must name the variable the operator has to fix")
				assert.Contains(t, strings.ToUpper(err.Error()), "DISABLE",
					"and must say what the accepted-but-rounded value would have DONE, "+
						"or the operator reads it as an arbitrary minimum")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestParseDBStatementTimeout_Outcomes(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{"unset uses the default", "", "30000", false},
		{"explicit zero means do not set the key", "0", "", false},
		{"explicit zero seconds means the same", "0s", "", false},
		{"a plain duration renders as milliseconds", "5s", "5000", false},
		{"minutes render as milliseconds", "2m", "120000", false},
		{"negative is refused", "-5s", "", true},
		{"unparseable is refused", "thirty", "", true},
		{"a bare integer is refused, because Go durations need a unit", "30", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseDBStatementTimeout("RELAY_DB_STATEMENT_TIMEOUT", tc.raw)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// The default is DERIVED from the constant, so moving the constant without
// moving README's table row cannot pass here silently.
func TestParseDBStatementTimeout_DefaultIsTheConstant(t *testing.T) {
	got, err := parseDBStatementTimeout("RELAY_DB_STATEMENT_TIMEOUT", "")
	require.NoError(t, err)
	assert.Equal(t, "30000", got)
	assert.Equal(t, int64(30000), defaultDBStatementTimeout.Milliseconds())
}

// EXECUTED, and it needs no Postgres: pgxpool.ParseConfig parses a DSN string
// offline and never connects.
//
// The two halves are a pair. An armed control must OVERWRITE what the DSN
// supplied - relay's setting wins, and that is a documented decision - while the
// disabled value must leave the DSN's own value standing, which is the whole
// point of the escape.
func TestApplyStatementTimeout_WritesTheRuntimeParam(t *testing.T) {
	const dsn = "postgres://relay:relay@127.0.0.1:5432/relay?sslmode=disable&statement_timeout=7s"

	armed, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	require.Equal(t, "7s", armed.ConnConfig.Config.RuntimeParams["statement_timeout"],
		"fixture: pgx must carry a DSN-supplied runtime parameter through ParseConfig, or the "+
			"overwrite assertion below proves nothing")
	applyStatementTimeout(armed, "30000")
	assert.Equal(t, "30000", armed.ConnConfig.Config.RuntimeParams["statement_timeout"],
		"relay's setting must win over the DSN's; that precedence is documented in README")

	disabled, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	applyStatementTimeout(disabled, "")
	assert.Equal(t, "7s", disabled.ConnConfig.Config.RuntimeParams["statement_timeout"],
		"the empty value means relay does not touch the key at all, leaving whatever the DSN, the "+
			"role or the server default provides")
}

// Asserting the key is ABSENT, not that it is empty: an empty string would reach
// the startup packet as `statement_timeout=`, which is not the same thing as not
// sending it.
func TestApplyStatementTimeout_DisabledAddsNoKey(t *testing.T) {
	cfg, err := pgxpool.ParseConfig("postgres://relay:relay@127.0.0.1:5432/relay?sslmode=disable")
	require.NoError(t, err)
	applyStatementTimeout(cfg, "")
	_, present := cfg.ConnConfig.Config.RuntimeParams["statement_timeout"]
	assert.False(t, present, "a disabled control must send no key at all")
}

// The disabled line must name the control as unarmed. A silent disable is the
// failure this parser exists to make impossible, and a log line that reads like
// the armed one is a silent disable with extra steps.
func TestDBStatementTimeoutLine(t *testing.T) {
	armed := dbStatementTimeoutLine("30000")
	assert.Contains(t, armed, "30000")
	assert.NotContains(t, strings.ToLower(armed), "not set")

	off := dbStatementTimeoutLine("")
	assert.Contains(t, off, "RELAY_DB_STATEMENT_TIMEOUT")
	assert.Contains(t, strings.ToLower(off), "not set",
		"an operator scanning the boot log must be able to see that this control is off")
}
