package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// relayInvocation finds `relay <command> [subcommand]` in prose. Case-SENSITIVE
// on "relay" so an English sentence opening with "Relay ..." is not read as an
// invocation; a command is always written lower-case.
var relayInvocation = regexp.MustCompile(`relay ([a-z][a-z-]*)(?: ([a-z][a-z-]*))?`)

// cliCommandSet parses internal/cli's source and returns command -> its
// subcommands. It reads the SOURCE rather than importing the package because
// there is no exported registry: Command and its fields are exported, but the
// list is assembled in cmd/relay/main.go, so nothing in internal/cli can be
// asked "what commands exist". Same technique as
// internal/worker/refusal_string_guard_test.go, and no new import edge.
//
// Subcommands are collected per FILE and attributed to every command that file
// declares. Today that is exact - each command has its own file, except jobs.go
// which declares four commands and no subcommands. If that ever stops being
// true the guard gets slightly permissive WITHIN one file and stays fail-closed
// across files, which is the direction that matters: a wholly invented
// subcommand still has nowhere to resolve.
func cliCommandSet(t *testing.T) map[string]map[string]bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "cli", "*.go"))
	require.NoError(t, err)
	require.NotEmpty(t, files, "internal/cli has no source files; this guard is not looking where it thinks")

	out := map[string]map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		require.NoError(t, err)
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, f, src, 0)
		require.NoError(t, err)

		var names, subs []string
		ast.Inspect(file, func(n ast.Node) bool {
			switch v := n.(type) {
			// Command{Name: "workers", ...}
			case *ast.CompositeLit:
				id, ok := v.Type.(*ast.Ident)
				if !ok || id.Name != "Command" {
					return true
				}
				for _, e := range v.Elts {
					kv, ok := e.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					if k, ok := kv.Key.(*ast.Ident); !ok || k.Name != "Name" {
						continue
					}
					if s, ok := stringLit(kv.Value); ok {
						names = append(names, s)
					}
				}
			// switch args[0] { case "revoke": ... }
			case *ast.CaseClause:
				for _, e := range v.List {
					if s, ok := stringLit(e); ok {
						subs = append(subs, s)
					}
				}
			// if args[0] != "enroll" { ... } - AgentCommand's shape, which is not
			// a switch and would otherwise leave `relay agent enroll` unresolvable.
			case *ast.BinaryExpr:
				if s, ok := stringLit(v.Y); ok && indexesArgs(v.X) {
					subs = append(subs, s)
				}
				if s, ok := stringLit(v.X); ok && indexesArgs(v.Y) {
					subs = append(subs, s)
				}
			}
			return true
		})

		for _, name := range names {
			if out[name] == nil {
				out[name] = map[string]bool{}
			}
			for _, s := range subs {
				out[name][s] = true
			}
		}
	}
	return out
}

func stringLit(e ast.Expr) (string, bool) {
	bl, ok := e.(*ast.BasicLit)
	if !ok || bl.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(bl.Value)
	if err != nil {
		return "", false
	}
	return s, true
}

// indexesArgs reports whether e is `args[N]`.
func indexesArgs(e ast.Expr) bool {
	ix, ok := e.(*ast.IndexExpr)
	if !ok {
		return false
	}
	id, ok := ix.X.(*ast.Ident)
	return ok && id.Name == "args"
}

// TestOperatorMessages_OnlyPrescribeCommandsThatExist is an ALLOW-LIST, and it
// replaces a deny-list that was evadable on the first try.
//
// The deny-list checked three known-bad spellings ("workers delete",
// "relay workers rm", "workers remove"). `relay worker delete` - singular -
// walked straight past it, and so would "destroy", "purge", or anything else a
// future author finds plausible. PLAUSIBILITY IS THE GENERATOR of this defect:
// `relay workers delete` was invented, shipped in the agent's terminal exit
// message, and pinned by a test, precisely because it sounded like it should
// exist. A deny-list can only ever name the ghosts somebody already caught.
//
// So assert the PROPERTY instead: every `relay ...` these messages prescribe
// must resolve against the CLI's real command set, parsed from
// internal/cli/*.go. A new ghost now fails whatever its spelling.
//
// ALL FOUR MESSAGES, not just the one that had the bug: the other arms name
// commands too, and covering only the site with the known defect is the same
// mistake one level up.
func TestOperatorMessages_OnlyPrescribeCommandsThatExist(t *testing.T) {
	cmds := cliCommandSet(t)
	require.Contains(t, cmds, "workers", "sanity: the parser must find the real command set")
	require.True(t, cmds["workers"]["revoke"], "sanity: workers must have its revoke subcommand")

	const tokenPath = "/var/lib/relay/token"
	messages := map[string]string{
		"authFailureMessage(stored agent token)": authFailureMessage(true, false, tokenPath),
		"authFailureMessage(enrollment token)":   authFailureMessage(false, true, tokenPath),
		"authFailureMessage(token-less)":         authFailureMessage(false, false, tokenPath),
		"EnrollmentIgnoredWarning":               EnrollmentIgnoredWarning(true, true, tokenPath),
	}

	for name, msg := range messages {
		for _, m := range relayInvocation.FindAllStringSubmatch(msg, -1) {
			cmd, sub := m[1], m[2]
			subs, ok := cmds[cmd]
			if !assert.True(t, ok,
				"%s prescribes `relay %s`, which is not a command. Add it to the CLI (a constructor "+
					"returning cli.Command, wired in cmd/relay/main.go) before naming it here. "+
					"If this is English prose rather than an invocation, rephrase it - the guard reads "+
					"lower-case `relay <word>` as a command by design.", name, cmd) {
				continue
			}
			if sub == "" || len(subs) == 0 {
				continue
			}
			assert.True(t, subs[sub],
				"%s prescribes `relay %s %s`, but %q has no %q subcommand. Its real subcommands are %v "+
					"(see internal/cli). Prescribing a command that does not exist is worse than "+
					"prescribing one that does not work: the operator has nothing to try and no way to "+
					"find that out.", name, cmd, sub, cmd, sub, sortedKeys(subs))
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// Deterministic failure messages.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
