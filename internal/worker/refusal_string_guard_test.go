package worker

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// unauthenticatedRefusalSites is how many
// status.Errorf(codes.Unauthenticated, ...) returns handler.go carries.
//
// IT IS A TRIPWIRE, NOT THE PROPERTY. The property - every refusal on the gRPC
// registration surface returns the identical string - is enforced by the second
// half of the guard below, which requires the message argument to be the
// msgAuthFailed CONSTANT rather than a literal. That makes a twelfth site
// indistinguishable automatically, which a count never could.
//
// What the count is for: a new site is not necessarily a new STRING, but it may
// well be a new OUTCOME, and
// TestConnect_EveryCredentialRefusalIsIndistinguishable's table has to gain an
// arm for it or that test silently stops covering the surface it names. Bumping
// this number is the moment to ask that question.
const unauthenticatedRefusalSites = 11

// TestRegistrationRefusals_AllUseTheSharedConstant is what makes the
// indistinguishability claim CHECKABLE rather than merely true today.
//
// AN EXHAUSTIVENESS CLAIM IS A CLAIM ABOUT THE COMPLEMENT and cannot be checked
// by reading the test that makes it. The refusal table used to say it drove
// "EVERY refusal on the gRPC registration surface" and was "exhaustive BY
// CONSTRUCTION"; it drove 5 of 11 sites, and its own next clause admitted a new
// arm is "only caught if it is added here too". That is exactly how
// "auto-enroll disabled" survived the earlier two-arm version of it - the
// property held by coincidence of five separately-typed string literals
// agreeing.
func TestRegistrationRefusals_AllUseTheSharedConstant(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "handler.go", nil, 0)
	require.NoError(t, err)

	var sites int
	var literals []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) < 2 {
			return true
		}
		fn, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || fn.Sel.Name != "Errorf" {
			return true
		}
		if pkg, ok := fn.X.(*ast.Ident); !ok || pkg.Name != "status" {
			return true
		}
		code, ok := call.Args[0].(*ast.SelectorExpr)
		if !ok || code.Sel.Name != "Unauthenticated" {
			return true
		}
		sites++
		// The message argument must be the shared constant. A BasicLit here is a
		// site that can drift; an Ident that is not msgAuthFailed is a second
		// constant, which is the same defect wearing a better costume.
		id, ok := call.Args[1].(*ast.Ident)
		if !ok || id.Name != "msgAuthFailed" {
			literals = append(literals, fset.Position(call.Args[1].Pos()).String())
		}
		return true
	})

	assert.Empty(t, literals,
		"every codes.Unauthenticated refusal in handler.go must pass msgAuthFailed, not its own string: "+
			"an unauthenticated peer that can tell two refusals apart can fingerprint server configuration "+
			"and hostname state. These sites do not: %v", literals)

	assert.Equal(t, unauthenticatedRefusalSites, sites,
		"handler.go's count of codes.Unauthenticated refusals changed. The shared constant keeps a new "+
			"site indistinguishable automatically, so this is not a correctness failure - it is the prompt "+
			"to ask whether the new site is a new OUTCOME, and if so to add an arm to "+
			"TestConnect_EveryCredentialRefusalIsIndistinguishable's table before bumping this number")
}
