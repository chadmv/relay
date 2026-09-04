package perforce

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// optionsLinesOf returns every line of a written spec that BEGINS with
// "Options:". The anchoring is the point: a client spec also carries a
// SubmitOptions: field, and a Description: line may contain the word.
func optionsLinesOf(spec string) []string {
	var out []string
	for _, line := range strings.Split(spec, "\n") {
		if strings.HasPrefix(line, "Options:") {
			out = append(out, line)
		}
	}
	return out
}

// optionsTokensOf returns the written Options: line's token list. Assertions in
// this file compare that slice rather than searching the spec for a substring,
// because "noclobber" contains "clobber" and so require.Contains on the word is
// satisfied by an untransformed spec.
func optionsTokensOf(t *testing.T, spec string) []string {
	t.Helper()
	lines := optionsLinesOf(spec)
	require.Len(t, lines, 1, "expected exactly one Options: line in %q", spec)
	return strings.Fields(strings.TrimPrefix(lines[0], "Options:"))
}

func TestClient_CreateStreamClient_Default(t *testing.T) {
	fr := newFakeP4Fixture(t)
	fr.set("client -o -S //streams/X/main relay_h_abc", `Client: relay_h_abc
Owner: relay
Root: D:\rw\abcdef
Stream: //streams/X/main
View: //streams/X/main/... //relay_h_abc/...
`)
	fr.set("client -i", "Client relay_h_abc saved.\n")
	c := &Client{r: fr}
	err := c.CreateStreamClient(context.Background(), "relay_h_abc", `D:\rw\abcdef`, "//streams/X/main", "", false)
	require.NoError(t, err)
	// Two calls: -o (read template) then -i (commit)
	require.Len(t, fr.calls, 2)
	require.Equal(t, []string{"client", "-o", "-S", "//streams/X/main", "relay_h_abc"}, fr.calls[0].args)
	require.Equal(t, []string{"client", "-i"}, fr.calls[1].args)
	require.Contains(t, fr.calls[1].stdin, "Root:")
}

func TestClient_CreateStreamClient_WithTemplate(t *testing.T) {
	fr := newFakeP4Fixture(t)
	fr.set("client -o -S //streams/X/main -t base relay_h_abc", `Client: relay_h_abc
Stream: //streams/X/main
Options: clobber
View: //streams/X/main/... //relay_h_abc/...
`)
	fr.set("client -i", "Client saved.\n")
	c := &Client{r: fr}
	err := c.CreateStreamClient(context.Background(), "relay_h_abc", `D:\rw\abcdef`, "//streams/X/main", "base", false)
	require.NoError(t, err)
	require.Equal(t, []string{"client", "-o", "-S", "//streams/X/main", "-t", "base", "relay_h_abc"}, fr.calls[0].args)
}

// p4 prefers an AltRoot over Root when the current working directory matches
// one, and relay runs its workspace-scoped calls from the workspace root. An
// AltRoots block inherited from a caller-supplied template would therefore be
// able to move the whole workspace out from under Root, which
// CreateStreamClient sets precisely to control where files land.
//
// The fixture carries a two-line block plus a following field, so a rule that
// dropped only the "AltRoots:" line, or that swallowed the rest of the spec,
// fails differently from one that removes the block.
func TestClient_CreateStreamClient_DropsAltRoots(t *testing.T) {
	fr := newFakeP4Fixture(t)
	fr.set("client -o -S //streams/X/main relay_h_abc", `Client: relay_h_abc
Root: D:\somewhere\else
AltRoots:
	/mnt/elsewhere
	C:\other
Options: clobber
`)
	fr.set("client -i", "Client saved.\n")
	c := &Client{r: fr}
	require.NoError(t, c.CreateStreamClient(context.Background(), "relay_h_abc", `D:\rw\abcdef`, "//streams/X/main", "", false))

	spec := fr.calls[1].stdin
	require.NotContains(t, spec, "AltRoots")
	require.NotContains(t, spec, "/mnt/elsewhere")
	require.NotContains(t, spec, `C:\other`)
	require.Contains(t, spec, "Root:\t"+`D:\rw\abcdef`, "Root is still the one we set")
	require.Contains(t, spec, "Options: clobber", "and the field after the block survives")
}

// The property: with clobber requested, only the Options: line's noclobber
// TOKEN changes, and no other field does.
//
// The fixture's Description: block carries the word noclobber and is placed
// BEFORE the Options: line, which is what discriminates. An implementation that
// replaces the first "noclobber" byte sequence in the spec edits the
// Description and leaves Options: untouched; with the poison after the subject
// that same implementation edits the right field and the test passes on it.
//
// noallwrite sits in position 0 for the second discriminator: an implementation
// that strips a "no" prefix rather than comparing whole tokens flips it to
// allwrite, a different p4 option, and only a whole-slice comparison sees that.
func TestClient_CreateStreamClient_ClobberEditsOnlyTheOptionsToken(t *testing.T) {
	fr := newFakeP4Fixture(t)
	fr.set("client -o -S //streams/X/main relay_h_abc", `Client: relay_h_abc
Description:
`+"\tbuild farm template - do not set noclobber here\n"+
		`Root: D:\somewhere\else
`+"Options:\tnoallwrite noclobber nocompress unlocked nomodtime normdir\n"+
		`View: //streams/X/main/... //relay_h_abc/...
`)
	fr.set("client -i", "Client saved.\n")
	c := &Client{r: fr}
	require.NoError(t, c.CreateStreamClient(context.Background(), "relay_h_abc", `D:\rw\abcdef`, "//streams/X/main", "", true))

	spec := fr.calls[1].stdin
	require.Contains(t, spec, "\tbuild farm template - do not set noclobber here\n",
		"the Description: block, including its own noclobber, must be byte-identical")
	require.Equal(t,
		[]string{"noallwrite", "clobber", "nocompress", "unlocked", "nomodtime", "normdir"},
		optionsTokensOf(t, spec))
}

// The property: with clobber off, the Options: line reaching `client -i` is the
// fetched one, byte for byte.
//
// Byte identity is stronger than a token comparison here because it also pins
// that setSpecField was never called for Options: that writer normalises the
// separator to a tab, and the two rows' separators differ from each other, so
// any row surviving a re-render would have to survive it unchanged.
//
// The second row is the discriminating one. An operator template that
// deliberately sets clobber must not be overwritten with noclobber when the
// knob is off - forcing noclobber would destroy a setting the operator chose,
// and would insert an Options: line into a spec that has none.
func TestClient_CreateStreamClient_ClobberOffLeavesTheOptionsLineByteIdentical(t *testing.T) {
	rows := []struct {
		name        string
		optionsLine string
	}{
		{"p4 default", "Options:\tnoallwrite noclobber nocompress unlocked nomodtime normdir"},
		{"operator template already sets clobber", "Options: clobber"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			fr := newFakeP4Fixture(t)
			fr.set("client -o -S //streams/X/main relay_h_abc", `Client: relay_h_abc
Root: D:\somewhere\else
`+row.optionsLine+`
View: //streams/X/main/... //relay_h_abc/...
`)
			fr.set("client -i", "Client saved.\n")
			c := &Client{r: fr}
			require.NoError(t, c.CreateStreamClient(context.Background(), "relay_h_abc", `D:\rw\abcdef`, "//streams/X/main", "", false))

			require.Equal(t, []string{row.optionsLine}, optionsLinesOf(fr.calls[1].stdin))
		})
	}
}

// The property: a fetched spec with no Options: line is left alone and the
// inertness is announced. Synthesising "Options:\tclobber" would hand p4 a
// one-token option set whose effect on the options nobody named is unverified -
// the same hazard as the setSpecField insert-at-top branch this asserts was
// never reached.
//
// The fixture is the shape TestClient_CreateStreamClient_Default already uses,
// so the no-op half of this is what keeps that test green. The warning is the
// only observable difference between "the knob is off" and "the knob is on and
// found nothing to edit", which is why it is asserted here.
func TestClient_CreateStreamClient_ClobberWithNoOptionsLineChangesNothingAndWarns(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	fr := newFakeP4Fixture(t)
	fr.set("client -o -S //streams/X/main relay_h_abc", `Client: relay_h_abc
Owner: relay
Root: D:\somewhere\else
Stream: //streams/X/main
View: //streams/X/main/... //relay_h_abc/...
`)
	fr.set("client -i", "Client saved.\n")
	c := &Client{r: fr}
	require.NoError(t, c.CreateStreamClient(context.Background(), "relay_h_abc", `D:\rw\abcdef`, "//streams/X/main", "", true))

	spec := fr.calls[1].stdin
	require.Empty(t, optionsLinesOf(spec), "no Options: line may be synthesised")
	require.Contains(t, spec, "Client: relay_h_abc\n")
	require.Contains(t, spec, "Stream: //streams/X/main\n")
	require.Contains(t, spec, "View: //streams/X/main/... //relay_h_abc/...\n")
	require.Contains(t, buf.String(), "relay_h_abc", "the warning must name the client")
}

// The property: when the fetched Options: line carries neither token, clobber is
// appended and nothing else moves; when it already carries clobber, nothing
// changes at all.
//
// The SubmitOptions: line is what discriminates the anchoring. It is a real p4
// client-spec field ending in the same word, so a reader matching anywhere in a
// line reads its value and, writing through an anchored writer, lands that
// value on the Options: line.
//
// It is placed BEFORE Options: for the reason the Description: poison is: an
// unanchored reader takes the FIRST match, so with SubmitOptions: after the
// subject it reads the right line anyway and the mutant survives. p4 emits the
// two fields the other way round; the ordering here is chosen to discriminate,
// not to model p4's output.
func TestClient_CreateStreamClient_ClobberAppendsWhenNeitherTokenIsPresent(t *testing.T) {
	t.Run("appends and leaves SubmitOptions alone", func(t *testing.T) {
		var buf bytes.Buffer
		log.SetOutput(&buf)
		defer log.SetOutput(os.Stderr)

		fr := newFakeP4Fixture(t)
		fr.set("client -o -S //streams/X/main relay_h_abc", `Client: relay_h_abc
Root: D:\somewhere\else
`+"SubmitOptions:\tsubmitunchanged\n"+
			"Options:\tnocompress unlocked\n")
		fr.set("client -i", "Client saved.\n")
		c := &Client{r: fr}
		require.NoError(t, c.CreateStreamClient(context.Background(), "relay_h_abc", `D:\rw\abcdef`, "//streams/X/main", "", true))

		spec := fr.calls[1].stdin
		require.Equal(t, []string{"nocompress", "unlocked", "clobber"}, optionsTokensOf(t, spec))
		require.Contains(t, spec, "SubmitOptions:\tsubmitunchanged\n")
		require.Empty(t, buf.String(), "a well-formed line must not warn")
	})

	t.Run("idempotent when clobber is already set", func(t *testing.T) {
		fr := newFakeP4Fixture(t)
		fr.set("client -o -S //streams/X/main relay_h_abc", `Client: relay_h_abc
Root: D:\somewhere\else
`+"Options:\tclobber nocompress\n")
		fr.set("client -i", "Client saved.\n")
		c := &Client{r: fr}
		require.NoError(t, c.CreateStreamClient(context.Background(), "relay_h_abc", `D:\rw\abcdef`, "//streams/X/main", "", true))

		require.Equal(t, []string{"clobber", "nocompress"}, optionsTokensOf(t, fr.calls[1].stdin))
	})
}

// The property: an Options: line this transform cannot read is left alone and
// the refusal is announced.
//
// Both rows discriminate against writing the line back through setSpecField.
// The non-alphabetic row does so because setSpecField passes its value into
// regexp.ReplaceAll, where a $-bearing token is expanded rather than written
// literally, so the token itself changes; the empty row does so because zero
// tokens would otherwise reach the append branch and produce a one-token option
// set - the outcome the missing-line case exists to forbid.
func TestClient_CreateStreamClient_ClobberLeavesAMalformedOptionsLineAlone(t *testing.T) {
	rows := []struct {
		name        string
		optionsLine string
	}{
		{"non-alphabetic token", "Options:\tnoallwrite no$1clobber unlocked"},
		{"empty value", "Options:"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			var buf bytes.Buffer
			log.SetOutput(&buf)
			defer log.SetOutput(os.Stderr)

			fr := newFakeP4Fixture(t)
			fr.set("client -o -S //streams/X/main relay_h_abc", `Client: relay_h_abc
Root: D:\somewhere\else
`+row.optionsLine+`
View: //streams/X/main/... //relay_h_abc/...
`)
			fr.set("client -i", "Client saved.\n")
			c := &Client{r: fr}
			require.NoError(t, c.CreateStreamClient(context.Background(), "relay_h_abc", `D:\rw\abcdef`, "//streams/X/main", "", true))

			require.Equal(t, []string{row.optionsLine}, optionsLinesOf(fr.calls[1].stdin))
			require.Contains(t, buf.String(), "relay_h_abc", "the warning must name the client")
		})
	}
}

func TestClient_ResolveHead(t *testing.T) {
	fr := newFakeP4Fixture(t)
	fr.set("-c relay_h_abc changes -m1 //relay_h_abc/...#head", "Change 12345 on 2026-04-24 by relay@h '...'\n")
	c := &Client{r: fr}
	cl, err := c.ResolveHead(context.Background(), "relay_h_abc", "//relay_h_abc/...")
	require.NoError(t, err)
	require.Equal(t, int64(12345), cl)
	// The global -c pins the client server-side, so no cwd is needed - and this
	// call runs before the workspace is acquired, where a cwd is a hazard.
	// TestProvider_HeadResolutionRunsWithNoWorkspaceCwd states why.
	require.Len(t, fr.calls, 1)
	require.Equal(t, "", fr.calls[0].cwd)
}

func TestClient_RunFailureBubbles(t *testing.T) {
	fr := newFakeP4Fixture(t)
	fr.setErr("-c relay_h_abc changes -m1 //relay_h_abc/...#head",
		errors.New("Perforce password (P4PASSWD) invalid or unset."))
	c := &Client{r: fr}
	_, err := c.ResolveHead(context.Background(), "relay_h_abc", "//relay_h_abc/...")
	require.ErrorContains(t, err, "P4PASSWD")
}
