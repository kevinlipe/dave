package input

import (
	"fmt"
	"testing"

	"dave/buffer"
	"dave/config"
	"dave/file"
	"dave/pane"
	"dave/render"

	"github.com/veandco/go-sdl2/sdl"
)

// input/cursor.go had no tests, despite containing no SDL at all: it is the
// editor behaviour behind double-clicking, bracket matching and the arrow
// keys, expressed as plain functions over a buffer. The rest of the package
// is event plumbing that does need SDL; this file is not.

// fixedFace is a deterministic render.Face, so the layout-dependent movement
// can be tested without a font or a window.
type fixedFace struct{}

func (fixedFace) Height() int                                    { return 20 }
func (fixedFace) Ascent() int                                    { return 16 }
func (fixedFace) GlyphAdvance(rune) int                          { return 10 }
func (fixedFace) MeasureText(s string) int                       { return 10 * len([]rune(s)) }
func (fixedFace) DrawText(string, int32, int32, sdl.Color) error { return nil }
func (fixedFace) Close()                                         {}

var _ render.Face = fixedFace{}

func mkFile(text string) *file.File {
	f := file.New()
	f.Buffer().Insert(0, text)
	return f
}

func mkPane(t *testing.T, text string, width int32) (*file.File, *pane.Pane) {
	t.Helper()
	cfg := config.Default()
	cfg.Margin = 0
	cfg.TabWidth = 4
	f := mkFile(text)
	p := pane.New(f, cfg, render.Palette{}, fixedFace{})
	p.Relayout(width)
	return f, p
}

// ---- WordBounds: what a double-click selects --------------------------------

func TestWordBoundsSelectsWords(t *testing.T) {
	const text = "the quick_brown fox42 bar"
	cases := []struct {
		cp             int
		wantLo, wantHi int
		what           string
	}{
		{0, 0, 3, "start of the first word"},
		{1, 0, 3, "inside the first word"},
		{2, 0, 3, "last rune of the first word"},
		{4, 4, 15, "underscores join a word"},
		{9, 4, 15, "the underscore itself"},
		{16, 16, 21, "digits join a word"},
		{20, 16, 21, "a digit"},
		{22, 22, 25, "the final word, no trailing space"},
		{24, 22, 25, "last rune of the buffer"},
	}
	buf := mkFile(text).Buffer()
	for _, c := range cases {
		lo, hi := WordBounds(buf, c.cp)
		if lo != c.wantLo || hi != c.wantHi {
			t.Errorf("WordBounds(%d) = [%d,%d), want [%d,%d) — %s",
				c.cp, lo, hi, c.wantLo, c.wantHi, c.what)
		}
	}
}

// Clicking a non-word rune selects the run of non-word runes, which is what
// makes double-clicking a gap select the whole gap.
func TestWordBoundsSelectsRunsOfNonWordRunes(t *testing.T) {
	buf := mkFile("ab    cd..ef").Buffer()
	for _, c := range []struct{ cp, lo, hi int }{
		{2, 2, 6}, // the run of four spaces
		{3, 2, 6},
		{5, 2, 6},
		{8, 8, 10}, // the run of two dots
		{9, 8, 10},
	} {
		lo, hi := WordBounds(buf, c.cp)
		if lo != c.lo || hi != c.hi {
			t.Errorf("WordBounds(%d) = [%d,%d), want [%d,%d)", c.cp, lo, hi, c.lo, c.hi)
		}
	}
}

func TestWordBoundsEdges(t *testing.T) {
	if lo, hi := WordBounds(mkFile("").Buffer(), 0); lo != 0 || hi != 0 {
		t.Errorf("empty buffer: got [%d,%d), want [0,0)", lo, hi)
	}
	if lo, hi := WordBounds(mkFile("word").Buffer(), -1); lo != 0 || hi != 0 {
		t.Errorf("negative cp: got [%d,%d), want [0,0)", lo, hi)
	}
	// Past the end clamps onto the last rune.
	if lo, hi := WordBounds(mkFile("word").Buffer(), 99); lo != 0 || hi != 4 {
		t.Errorf("cp past end: got [%d,%d), want [0,4)", lo, hi)
	}
}

// Word selection is Unicode-aware: an accented letter is part of its word,
// the same way it is when deciding where a line wraps.
//
// These two definitions used to differ -- pane's was Unicode-aware and
// input's was ASCII-only -- so double-clicking "héllo" selected "h", "é" or
// "llo" depending where the click landed. Both now call buffer.IsWordRune.
func TestWordBoundsMultibyte(t *testing.T) {
	buf := mkFile("héllo ☃ wörld").Buffer()

	for _, cp := range []int{0, 1, 2, 3, 4} {
		if lo, hi := WordBounds(buf, cp); lo != 0 || hi != 5 {
			t.Errorf("cp %d in héllo: got [%d,%d), want [0,5)", cp, lo, hi)
		}
	}
	for _, cp := range []int{8, 9, 10, 11, 12} {
		if lo, hi := WordBounds(buf, cp); lo != 8 || hi != 13 {
			t.Errorf("cp %d in wörld: got [%d,%d), want [8,13)", cp, lo, hi)
		}
	}
	// A snowman is a symbol, not a letter, so it and the spaces round it are
	// still one run of non-word runes.
	if lo, hi := WordBounds(buf, 6); lo != 5 || hi != 8 {
		t.Errorf("the snowman run: got [%d,%d), want [5,8)", lo, hi)
	}
}

// Ctrl-arrow crosses an accented word in one step rather than stopping at
// the accent.
func TestSkipWordBoundaryCrossesAccentedWords(t *testing.T) {
	buf := mkFile("héllo wörld").Buffer()
	n := buf.Len()
	if got := skipWordBoundary(buf, 0, +1, n); got != 5 {
		t.Errorf("forward over héllo = %d, want 5", got)
	}
	if got := skipWordBoundary(buf, 11, -1, n); got != 6 {
		t.Errorf("backward over wörld = %d, want 6", got)
	}
}

// ---- bracket and quote matching ---------------------------------------------

func TestWordBoundsMatchesBrackets(t *testing.T) {
	cases := []struct {
		text           string
		cp             int
		wantLo, wantHi int
		what           string
	}{
		{"a(bc)d", 1, 1, 5, "open paren selects through its close"},
		{"a(bc)d", 4, 1, 5, "close paren selects back to its open"},
		{"a(b(c)d)e", 1, 1, 8, "nesting: outer open finds the outer close"},
		{"a(b(c)d)e", 3, 3, 6, "nesting: inner open finds the inner close"},
		{"a(b(c)d)e", 7, 1, 8, "nesting: outer close finds the outer open"},
		{"a(b(c)d)e", 5, 3, 6, "nesting: inner close finds the inner open"},
		{"x[y]z", 1, 1, 4, "square brackets"},
		{"x{y}z", 1, 1, 4, "braces"},
		{"x<y>z", 1, 1, 4, "angle brackets"},
		{`a"bc"d`, 1, 1, 5, "double quotes match the next one"},
		{"a'bc'd", 1, 1, 5, "single quotes"},
		{"a`bc`d", 1, 1, 5, "backticks"},
		// Unbalanced: the search runs off the end and stops there.
		{"a(bcd", 1, 1, 5, "unclosed open runs to end of buffer"},
		{"abc)d", 3, 0, 4, "unopened close runs back to the start"},
	}
	for _, c := range cases {
		lo, hi := WordBounds(mkFile(c.text).Buffer(), c.cp)
		if lo != c.wantLo || hi != c.wantHi {
			t.Errorf("%q WordBounds(%d) = [%d,%d), want [%d,%d) — %s",
				c.text, c.cp, lo, hi, c.wantLo, c.wantHi, c.what)
		}
	}
}

func TestMatchingPairClassification(t *testing.T) {
	for _, c := range []struct {
		r                  rune
		open, close        rune
		forward, isBracket bool
	}{
		{'(', '(', ')', true, true},
		{')', '(', ')', false, true},
		{'[', '[', ']', true, true},
		{']', '[', ']', false, true},
		{'{', '{', '}', true, true},
		{'}', '{', '}', false, true},
		{'<', '<', '>', true, true},
		{'>', '<', '>', false, true},
		{'"', '"', '"', true, true},
		{'\'', '\'', '\'', true, true},
		{'`', '`', '`', true, true},
		{'a', 0, 0, false, false},
		{' ', 0, 0, false, false},
	} {
		open, close, forward := matchingPair(c.r)
		if (open != 0) != c.isBracket {
			t.Errorf("matchingPair(%q): isBracket=%v, want %v", c.r, open != 0, c.isBracket)
			continue
		}
		if c.isBracket && (open != c.open || close != c.close || forward != c.forward) {
			t.Errorf("matchingPair(%q) = (%q,%q,%v), want (%q,%q,%v)",
				c.r, open, close, forward, c.open, c.close, c.forward)
		}
	}
}

// ---- word-wise movement ------------------------------------------------------

func TestSkipWordBoundary(t *testing.T) {
	const text = "one two  three"
	buf := mkFile(text).Buffer()
	n := buf.Len()

	// Forward: skip any non-word runes, then the word.
	for _, c := range []struct{ from, want int }{
		{0, 3}, {1, 3}, {3, 7}, {4, 7}, {7, 14}, {14, 14},
	} {
		if got := skipWordBoundary(buf, c.from, +1, n); got != c.want {
			t.Errorf("forward from %d = %d, want %d", c.from, got, c.want)
		}
	}
	// Backward: skip any non-word runes, then the word.
	for _, c := range []struct{ from, want int }{
		{14, 9}, {9, 4}, {4, 0}, {3, 0}, {0, 0},
	} {
		if got := skipWordBoundary(buf, c.from, -1, n); got != c.want {
			t.Errorf("backward from %d = %d, want %d", c.from, got, c.want)
		}
	}
}

// ---- dot movement -------------------------------------------------------------

func TestMoveDotCharCollapsesAndExtends(t *testing.T) {
	f := mkFile("hello")

	// With a selection and no extend, movement collapses to the near end.
	f.SetDot(buffer.Range{Start: 1, End: 4})
	moveDotChar(f, -1, false)
	if got := f.Dot(); got.Start != 1 || got.End != 1 {
		t.Errorf("collapse left = [%d,%d), want [1,1)", got.Start, got.End)
	}
	f.SetDot(buffer.Range{Start: 1, End: 4})
	moveDotChar(f, +1, false)
	if got := f.Dot(); got.Start != 4 || got.End != 4 {
		t.Errorf("collapse right = [%d,%d), want [4,4)", got.Start, got.End)
	}

	// Extending grows the selection from its anchor.
	f.SetDot(buffer.Range{Start: 2, End: 2})
	moveDotChar(f, +1, true)
	if got := f.Dot(); got.Start != 2 || got.End != 3 {
		t.Errorf("extend right = [%d,%d), want [2,3)", got.Start, got.End)
	}

	// Movement clamps at both ends of the buffer.
	f.SetDot(buffer.Range{Start: 0, End: 0})
	moveDotChar(f, -1, false)
	if got := f.Dot(); got.Start != 0 {
		t.Errorf("clamp at start = %d, want 0", got.Start)
	}
	f.SetDot(buffer.Range{Start: 5, End: 5})
	moveDotChar(f, +1, false)
	if got := f.Dot(); got.Start != 5 {
		t.Errorf("clamp at end = %d, want 5", got.Start)
	}
}

func TestMoveDotLineStartAndEnd(t *testing.T) {
	// Wide enough that no wrapping happens, so visual lines are logical ones.
	f, p := mkPane(t, "alpha\nbeta\ngamma\n", 1000)

	for _, c := range []struct{ from, wantStart, wantEnd int }{
		{0, 0, 5},    // first line
		{3, 0, 5},    // inside the first line
		{6, 6, 10},   // second line
		{12, 11, 16}, // third line
	} {
		f.SetDot(buffer.Range{Start: c.from, End: c.from})
		moveDotLineStart(f, p, false)
		if got := f.Dot().Start; got != c.wantStart {
			t.Errorf("lineStart from %d = %d, want %d", c.from, got, c.wantStart)
		}
		f.SetDot(buffer.Range{Start: c.from, End: c.from})
		moveDotLineEnd(f, p, false)
		if got := f.Dot().Start; got != c.wantEnd {
			t.Errorf("lineEnd from %d = %d, want %d", c.from, got, c.wantEnd)
		}
	}
}

// End-of-line stops before the newline, not on it, so typing at that point
// stays on the line.
func TestMoveDotLineEndStopsBeforeTheNewline(t *testing.T) {
	f, p := mkPane(t, "ab\ncd\n", 1000)
	f.SetDot(buffer.Range{Start: 0, End: 0})
	moveDotLineEnd(f, p, false)
	if got := f.Dot().Start; got != 2 {
		t.Errorf("line end = %d, want 2 (the newline is at 2)", got)
	}
	if r := f.Buffer().RuneAt(f.Dot().Start); r != '\n' {
		t.Errorf("expected to stop just before a newline, found %q", r)
	}
}

func TestMoveDotVisualLineAcrossWrappedLines(t *testing.T) {
	// 100px wide at 10px per rune wraps every ten runes.
	f, p := mkPane(t, "aaaaaaaaaabbbbbbbbbbcccccccccc\n", 100)
	layout := p.Layout()
	if len(layout.Lines) < 3 {
		t.Fatalf("expected the line to wrap into at least 3 visual lines, got %d",
			len(layout.Lines))
	}

	// From column 2 of the first visual line, down keeps the column.
	f.SetDot(buffer.Range{Start: 2, End: 2})
	moveDotVisualLine(f, p, +1, false, 400)
	if got := f.Dot().Start; got != 12 {
		t.Errorf("down from 2 = %d, want 12 (same column, next visual line)", got)
	}
	moveDotVisualLine(f, p, +1, false, 400)
	if got := f.Dot().Start; got != 22 {
		t.Errorf("down again = %d, want 22", got)
	}
	moveDotVisualLine(f, p, -1, false, 400)
	if got := f.Dot().Start; got != 12 {
		t.Errorf("back up = %d, want 12", got)
	}

	// Moving up from the top line stays on it.
	f.SetDot(buffer.Range{Start: 3, End: 3})
	moveDotVisualLine(f, p, -1, false, 400)
	if got := f.Dot().Start; got != 3 {
		t.Errorf("up from the first line = %d, want 3", got)
	}
}

// ---- editing helpers ----------------------------------------------------------

func TestRuneWidthAtTabStops(t *testing.T) {
	face := fixedFace{}
	const tabWidth = 4 // 4 * 10px = 40px stops
	for _, c := range []struct{ x, want int32 }{
		{0, 40}, {10, 30}, {30, 10}, {40, 40}, {45, 35},
	} {
		if got := runeWidthAt('\t', c.x, face, tabWidth); got != c.want {
			t.Errorf("tab at x=%d advances %d, want %d", c.x, got, c.want)
		}
	}
	if got := runeWidthAt('a', 0, face, tabWidth); got != 10 {
		t.Errorf("ordinary rune advances %d, want 10", got)
	}
}

func TestCpAtTargetXPicksNearestBoundary(t *testing.T) {
	f, p := mkPane(t, "abcdef\n", 1000)
	vl := p.Layout().Lines[0]
	buf := f.Buffer()
	for _, c := range []struct {
		x    int32
		want int
		what string
	}{
		{0, 0, "left edge"},
		{4, 0, "just left of the first midpoint"},
		{6, 1, "just right of the first midpoint"},
		// Exactly on a glyph's midpoint rounds to the right, since the
		// comparison is strict.
		{25, 3, "exactly the midpoint of c"},
		{24, 2, "just left of that midpoint"},
		{1000, 6, "past the end of the line"},
	} {
		if got := cpAtTargetX(buf, vl, c.x, fixedFace{}, 4); got != c.want {
			t.Errorf("cpAtTargetX(x=%d) = %d, want %d — %s", c.x, got, c.want, c.what)
		}
	}
}

func TestIsWordRune(t *testing.T) {
	// Letters and digits in any script, plus the underscore.
	for _, r := range []rune{'a', 'z', 'A', 'Z', '0', '9', '_', 'é', 'ö', 'ñ', 'α', '日', '٧'} {
		if !buffer.IsWordRune(r) {
			t.Errorf("%q should be a word rune", r)
		}
	}
	// Symbols, punctuation and whitespace are not, whatever their script.
	for _, r := range []rune{' ', '\t', '\n', '.', '-', '(', '☃', '—', '€'} {
		if buffer.IsWordRune(r) {
			t.Errorf("%q should not be a word rune", r)
		}
	}
}

func TestWordBoundsNeverReturnsAnInvertedOrOutOfRangeRange(t *testing.T) {
	texts := []string{
		"", "a", "()", "(((", ")))", `"unterminated`,
		"word (paren) 'quote' \"double\"\n\ttab",
		"héllo ☃ wörld — naïve",
	}
	for _, text := range texts {
		buf := mkFile(text).Buffer()
		n := buf.Len()
		for cp := -2; cp <= n+2; cp++ {
			lo, hi := WordBounds(buf, cp)
			what := fmt.Sprintf("%q cp=%d", text, cp)
			if lo > hi {
				t.Errorf("%s: inverted [%d,%d)", what, lo, hi)
			}
			if lo < 0 || hi > n {
				t.Errorf("%s: [%d,%d) outside [0,%d]", what, lo, hi, n)
			}
		}
	}
}
