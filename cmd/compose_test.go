package cmd

import (
	"strings"
	"testing"
)

// Composition regression tests. These cover cases where a structural
// command (x, y, g, v) contains another command as its body, including
// nesting and spacing variants. Historically Dave's `^` was not
// line-anchored, which caused `,x/^.*\n/ g/thing/ p` to silently print
// nothing; these tests guard against that regression and its cousins.

const composeContent = "alpha\nthis is a thing\nbeta\nanother thing here\ngamma\n"

// ---- x with g (the original repro shape) ------------------------------------

func TestCompose_XLines_GPrint_spaced(t *testing.T) {
	r := runCmd(t, composeContent, dot(0, 0), `,x/^.*\n/ g/thing/ p`)
	assertNoError(t, r.err)
	assertOutput(t, r.output, "this is a thing\nanother thing here\n")
}

func TestCompose_XLines_GPrint_unspaced(t *testing.T) {
	r := runCmd(t, composeContent, dot(0, 0), `,x/^.*\n/g/thing/p`)
	assertNoError(t, r.err)
	assertOutput(t, r.output, "this is a thing\nanother thing here\n")
}

// ---- x with v (inverse guard) -----------------------------------------------

func TestCompose_XLines_VPrint(t *testing.T) {
	// Print each line that does NOT contain "thing".
	r := runCmd(t, composeContent, dot(0, 0), `,x/^.*\n/ v/thing/ p`)
	assertNoError(t, r.err)
	assertOutput(t, r.output, "alpha\nbeta\ngamma\n")
}

// ---- x with y ---------------------------------------------------------------

func TestCompose_XLines_YPrint(t *testing.T) {
	// x over lines, then y over comma-splits within each line.
	src := "a,b,c\nd,e\n"
	r := runCmd(t, src, dot(0, 0), `,x/^.*\n/ y/,/ p`)
	assertNoError(t, r.err)
	// Fields per line: {a, b, c\n} then {d, e\n}. The last field of each
	// line includes the trailing newline because y splits between matches.
	assertOutput(t, r.output, "a\nb\nc\nd\ne\n")
}

// ---- g and v nested inside x inside a range address -------------------------

func TestCompose_RangedX_NestedG(t *testing.T) {
	// Restrict outer x to lines 2..4 by range, then keep lines matching /thing/.
	r := runCmd(t, composeContent, dot(0, 0), `2,4 x/^.*\n/ g/thing/ p`)
	assertNoError(t, r.err)
	// Lines 2..4 are: "this is a thing\n", "beta\n", "another thing here\n".
	// Matching /thing/: lines 2 and 4.
	assertOutput(t, r.output, "this is a thing\nanother thing here\n")
}

func TestCompose_RangedX_NestedV(t *testing.T) {
	r := runCmd(t, composeContent, dot(0, 0), `2,4 x/^.*\n/ v/thing/ p`)
	assertNoError(t, r.err)
	assertOutput(t, r.output, "beta\n")
}

// ---- sub-command variants: p / d / c ----------------------------------------

func TestCompose_XG_Delete(t *testing.T) {
	// Delete every line containing "thing".
	r := runCmd(t, composeContent, dot(0, 0), `,x/^.*\n/ g/thing/ d`)
	assertNoError(t, r.err)
	assertContents(t, r.contents, "alpha\nbeta\ngamma\n")
}

func TestCompose_XG_Change(t *testing.T) {
	// Replace every line containing "thing" with a fixed line.
	r := runCmd(t, composeContent, dot(0, 0), `,x/^.*\n/ g/thing/ c/REDACTED\n/`)
	assertNoError(t, r.err)
	assertContents(t, r.contents, "alpha\nREDACTED\nbeta\nREDACTED\ngamma\n")
}

// ---- guard matches nothing → silent no-op -----------------------------------

func TestCompose_XG_GuardMatchesNothing(t *testing.T) {
	r := runCmd(t, composeContent, dot(0, 0), `,x/^.*\n/ g/zzz/ p`)
	assertNoError(t, r.err)
	assertOutput(t, r.output, "")
	assertContents(t, r.contents, composeContent)
}

// ---- outer x matches nothing → silent no-op ---------------------------------

func TestCompose_XG_OuterMatchesNothing(t *testing.T) {
	// Outer x pattern won't find anything.
	r := runCmd(t, composeContent, dot(0, 0), `,x/qqqqq/ g/thing/ p`)
	assertNoError(t, r.err)
	assertOutput(t, r.output, "")
	assertContents(t, r.contents, composeContent)
}

// ---- spacing invariance -----------------------------------------------------

// Whitespace between the pattern-closing delimiter and the sub-command must
// not change the result. This exercises the parser's skipWS before the body.
func TestCompose_Spacing_Invariance(t *testing.T) {
	variants := []string{
		`,x/^.*\n/ g/thing/ p`,
		`,x/^.*\n/g/thing/p`,
		`,x/^.*\n/  g/thing/  p`,
		`,x/^.*\n/	g/thing/	p`, // tabs
	}
	const want = "this is a thing\nanother thing here\n"
	for _, v := range variants {
		r := runCmd(t, composeContent, dot(0, 0), v)
		if r.err != nil {
			t.Errorf("%q: unexpected error %v", v, r.err)
			continue
		}
		if r.output != want {
			t.Errorf("%q: output = %q, want %q", v, r.output, want)
		}
	}
}

// ---- multiline anchor: direct exercise --------------------------------------

// Guards the underlying fix: `^` and `$` must be line-anchored.
func TestCompose_CaretIsLineAnchored(t *testing.T) {
	r := runCmd(t, composeContent, dot(0, 0), `,x/^.*\n/ p`)
	assertNoError(t, r.err)
	// Every line should print, not just the first.
	if !strings.Contains(r.output, "beta\n") || !strings.Contains(r.output, "gamma\n") {
		t.Errorf("^ is not line-anchored: output = %q", r.output)
	}
}

func TestCompose_DollarIsLineAnchored(t *testing.T) {
	// Match "thing" only when it's at end of line — should match both
	// "this is a thing" and neither should match "thing here" (mid-line).
	r := runCmd(t, composeContent, dot(0, 0), `,x/thing$/ p`)
	assertNoError(t, r.err)
	// "this is a thing" ends in "thing"; "another thing here" doesn't.
	// x iterates matches of "thing$"; only one such match exists.
	assertOutput(t, r.output, "thing\n")
}
