package main

import (
	"go/ast"
	"go/parser"
	"go/token"
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

// TestStatementTimeoutIsAppliedBeforeThePoolIsBuilt is a PARSED guard, and it is
// parsed because nothing else can be: main ends in log.Fatalf, which no test can
// call, and the pool it builds needs a database.
//
// It covers the two shapes that silently unarm this control while compiling and
// vetting clean: the call being deleted, and the call being moved BELOW
// pgxpool.NewWithConfig, where it mutates a config the pool has already copied.
// It does NOT cover the VALUE - whether the second argument is the parsed
// timeout rather than some other string is checked by nothing. It also does not
// cover applyStatementTimeout's own body, which is EXECUTED in
// TestApplyStatementTimeout_WritesTheRuntimeParam; this guard exists only to
// prove main reaches it.
func TestStatementTimeoutIsAppliedBeforeThePoolIsBuilt(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	require.NoError(t, err)

	var body *ast.BlockStmt
	for _, d := range file.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == "main" && fd.Recv == nil && fd.Body != nil {
			body = fd.Body
		}
	}
	require.NotNil(t, body, "main.go no longer declares func main with a body")

	// Statement INDEX, not source line, and ast.Inspect descends: what is
	// recorded is the index of the top-level statement of main's body that
	// CONTAINS each call. So wrapping the call in an if still finds it, at the
	// index of the if - which is the behaviour wanted here, since
	// applyStatementTimeout already no-ops on the disabled value and an
	// `if statementTimeout != ""` around it is an equivalent program, not a
	// defect to redden on.
	applyAt, poolAt := -1, -1
	for i, st := range body.List {
		found := map[string]bool{}
		ast.Inspect(st, func(n ast.Node) bool {
			ce, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fn := ce.Fun.(type) {
			case *ast.Ident:
				found[fn.Name] = true
			case *ast.SelectorExpr:
				found[fn.Sel.Name] = true
			}
			return true
		})
		if found["applyStatementTimeout"] {
			require.Equal(t, -1, applyAt,
				"main calls applyStatementTimeout more than once; the last one decides and this guard "+
					"cannot say which config it mutated")
			applyAt = i
		}
		if found["NewWithConfig"] {
			require.Equal(t, -1, poolAt, "main builds more than one pool")
			poolAt = i
		}
	}

	require.NotEqual(t, -1, applyAt,
		"main never calls applyStatementTimeout as a direct statement of its own body, so "+
			"RELAY_DB_STATEMENT_TIMEOUT reaches no connection. The variable would still parse, the "+
			"startup line would still print, and nothing at all would be bounded.")
	require.NotEqual(t, -1, poolAt, "main no longer calls pgxpool.NewWithConfig")
	require.Less(t, applyAt, poolAt,
		"applyStatementTimeout runs at statement %d and the pool is built at statement %d. Mutating "+
			"the config after NewWithConfig has copied it is a no-op that compiles, vets clean and "+
			"leaves every package green.", applyAt, poolAt)
}
