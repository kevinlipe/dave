package pane

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dave/buffer"
)

// Characterization test for Wrap.
//
// Wrap had no tests at all before this: pane/layout_test.go builds Layout
// values by hand and never calls it. It is about to be restructured for
// incremental layout, so this records what it currently produces for a corpus
// of inputs, and any change to that shows up as a diff.
//
// Regenerate with: go test ./pane/ -run WrapCharacterize -update

var update = flag.Bool("update", false, "rewrite the wrap characterization golden file")

type wrapCase struct {
	name        string
	text        string
	widthPx     int32
	wordWrap    bool
	marginChars int
	tabWidth    int
}

// The fake face advances 10px for most runes, so a width of 100 fits ten
// ordinary characters and the wrap points can be reasoned about by hand.
var wrapCases = []wrapCase{
	{"empty", "", 100, true, 0, 4},
	{"empty-nowrap", "", 100, false, 0, 4},
	{"single-word", "hello", 100, true, 0, 4},
	{"exact-fit", "abcdefghij", 100, true, 0, 4},
	{"one-over", "abcdefghijk", 100, true, 0, 4},
	{"two-words", "hello world", 100, true, 0, 4},
	{"break-at-boundary", "abcde fghij klmno", 100, true, 0, 4},
	{"long-word-hard-wrap", "abcdefghijklmnopqrstuvwxyz", 100, true, 0, 4},
	{"long-word-after-short", "hi abcdefghijklmnopqrstuvwxyz", 100, true, 0, 4},

	{"trailing-newline", "one two\n", 100, true, 0, 4},
	{"no-trailing-newline", "one two", 100, true, 0, 4},
	{"blank-lines", "a\n\n\nb\n", 100, true, 0, 4},
	{"only-newlines", "\n\n\n", 100, true, 0, 4},
	{"multi-line-wrapping", "alpha beta gamma\ndelta epsilon\n", 100, true, 0, 4},

	{"tabs", "a\tb\tc\n", 200, true, 0, 4},
	{"tabs-narrow", "a\tb\tc\n", 100, true, 0, 4},
	{"tab-width-8", "a\tb\n", 200, true, 0, 8},
	{"leading-tab", "\tindented line here\n", 200, true, 0, 4},

	{"multibyte", "héllo wörld ☃ naïve\n", 100, true, 0, 4},
	{"wide-glyphs", "WWW mmm iii\n", 100, true, 0, 4},

	// Proportional widths matter here: Wrap averages MeasureText over the
	// lowercase alphabet to pick a character width for the margin.
	{"margin-fits", "alpha beta gamma delta\n", 400, true, 20, 4},
	{"margin-exceeds-pane", "alpha beta gamma delta\n", 100, true, 40, 4},
	{"margin-zero", "alpha beta gamma delta\n", 200, true, 0, 4},

	{"nowrap-long-line", "abcdefghijklmnopqrstuvwxyz\nshort\n", 100, false, 0, 4},
	{"nowrap-margin-ignored", "alpha beta gamma\n", 200, false, 20, 4},

	{"spaces-only", "     \n", 100, true, 0, 4},
	{"trailing-spaces", "hi        \n", 100, true, 0, 4},
	{"punctuation-breaks", "a,b.c;d:e f\n", 100, true, 0, 4},
	{"underscores-are-word", "foo_bar_baz_qux\n", 100, true, 0, 4},
}

func layoutFor(c wrapCase) Layout {
	buf := buffer.NewFromString(c.text)
	return Wrap(buf, newFakeFace(), c.widthPx, c.wordWrap, c.marginChars, c.tabWidth)
}

func characterizeWrap() string {
	var sb strings.Builder
	for _, c := range wrapCases {
		fmt.Fprintf(&sb, "=== %s text=%q width=%d wrap=%v margin=%d tab=%d\n",
			c.name, c.text, c.widthPx, c.wordWrap, c.marginChars, c.tabWidth)
		l := layoutFor(c)
		fmt.Fprintf(&sb, "  totalHeight=%d marginLeft=%d wrapWidth=%d lines=%d\n",
			l.TotalHeight, l.MarginLeft, l.WrapWidth, len(l.Lines))
		runes := []rune(c.text)
		for i, vl := range l.Lines {
			seg := ""
			if vl.Lo <= vl.Hi && vl.Hi <= len(runes) {
				seg = string(runes[vl.Lo:vl.Hi])
			}
			fmt.Fprintf(&sb, "  [%d] lo=%d hi=%d wrapped=%v %q\n",
				i, vl.Lo, vl.Hi, vl.Wrapped, seg)
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

func TestWrapCharacterize(t *testing.T) {
	got := characterizeWrap()
	golden := filepath.Join("testdata", "wrap.golden")

	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d bytes)", golden, len(got))
		return
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run: go test ./pane/ -run WrapCharacterize -update)", err)
	}
	if got == string(want) {
		return
	}
	gotLines, wantLines := strings.Split(got, "\n"), strings.Split(string(want), "\n")
	for i := 0; i < len(gotLines) || i < len(wantLines); i++ {
		g, w := "", ""
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if g != w {
			lo := max(0, i-10)
			t.Fatalf("wrap output changed at line %d\ncontext:\n%s\n\n  got: %s\n want: %s",
				i+1, strings.Join(wantLines[lo:i], "\n"), g, w)
		}
	}
}

// Y is always the line's index times the line height. Nothing but flushLine
// writes it, and incremental layout depends on that being true — it is what
// lets a splice renumber lines without touching Y at all.
func TestWrapYIsIndexTimesLineHeight(t *testing.T) {
	lineH := int32(newFakeFace().Height())
	for _, c := range wrapCases {
		l := layoutFor(c)
		if l.LineH != lineH {
			t.Fatalf("%s: LineH=%d, want %d", c.name, l.LineH, lineH)
		}
		for i := range l.Lines {
			if want := int32(i) * lineH; l.Y(i) != want {
				t.Fatalf("%s: line %d has Y=%d, want %d", c.name, i, l.Y(i), want)
			}
		}
		if want := int32(len(l.Lines)) * lineH; l.TotalHeight != want {
			t.Fatalf("%s: TotalHeight=%d, want %d", c.name, l.TotalHeight, want)
		}
	}
}

// Whatever the wrapping, the visual lines must tile the buffer: they start at
// 0, never go backwards, and cover every codepoint. Newlines are the one gap,
// since a line ending at '\n' excludes it.
func TestWrapLinesTileTheBuffer(t *testing.T) {
	for _, c := range wrapCases {
		l := layoutFor(c)
		runes := []rune(c.text)
		if len(l.Lines) == 0 {
			t.Fatalf("%s: no lines at all", c.name)
		}
		if l.Lines[0].Lo != 0 {
			t.Errorf("%s: first line starts at %d, want 0", c.name, l.Lines[0].Lo)
		}
		prevHi := 0
		for i, vl := range l.Lines {
			if vl.Lo > vl.Hi {
				t.Errorf("%s: line %d is inverted: [%d,%d)", c.name, i, vl.Lo, vl.Hi)
			}
			if vl.Lo < prevHi {
				t.Errorf("%s: line %d starts at %d, before previous end %d",
					c.name, i, vl.Lo, prevHi)
			}
			// The only codepoint a line may skip over is the newline it ended on.
			if gap := vl.Lo - prevHi; gap > 1 {
				t.Errorf("%s: line %d leaves a %d-codepoint gap after %d",
					c.name, i, gap, prevHi)
			} else if gap == 1 && prevHi < len(runes) && runes[prevHi] != '\n' {
				t.Errorf("%s: line %d skips %q at %d, which is not a newline",
					c.name, i, runes[prevHi], prevHi)
			}
			prevHi = vl.Hi
		}
		if prevHi != len(runes) {
			t.Errorf("%s: lines end at %d, buffer has %d codepoints",
				c.name, prevHi, len(runes))
		}
	}
}

// Wrapped marks a soft-wrap continuation. The first visual line of the buffer
// and any line starting just after a newline are not continuations.
func TestWrapWrappedFlagMarksContinuations(t *testing.T) {
	for _, c := range wrapCases {
		l := layoutFor(c)
		runes := []rune(c.text)
		for i, vl := range l.Lines {
			afterNewline := vl.Lo > 0 && vl.Lo <= len(runes) && runes[vl.Lo-1] == '\n'
			wantContinuation := i > 0 && !afterNewline
			if vl.Wrapped != wantContinuation {
				t.Errorf("%s: line %d (lo=%d) has Wrapped=%v, want %v",
					c.name, i, vl.Lo, vl.Wrapped, wantContinuation)
			}
		}
	}
}
