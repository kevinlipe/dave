package addr

import (
	"dave/buffer"
	"dave/file"
	"fmt"
	"strings"
	"testing"
)

// ---- test helpers -----------------------------------------------------------

func makeFile(content string) *file.File {
	f := file.New()
	f.Buffer().Insert(0, content)
	return f
}

func setDot(f *file.File, start, end int) {
	f.SetDot(buffer.Range{Start: start, End: end})
}

func evalStr(t *testing.T, addrStr string, f *file.File) buffer.Range {
	t.Helper()
	a, _, err := Parse(addrStr)
	if err != nil {
		t.Fatalf("Parse(%q): %v", addrStr, err)
	}
	if a == nil {
		t.Fatalf("Parse(%q): no address parsed", addrStr)
	}
	r, err := Eval(a, f, nil)
	if err != nil {
		t.Fatalf("Eval(%q): %v", addrStr, err)
	}
	return r
}

func evalErr(t *testing.T, addrStr string, f *file.File) error {
	t.Helper()
	a, _, err := Parse(addrStr)
	if err != nil {
		return err
	}
	if a == nil {
		t.Fatalf("Parse(%q): no address parsed", addrStr)
	}
	_, err = Eval(a, f, nil)
	return err
}

func wantRange(t *testing.T, r buffer.Range, start, end int) {
	t.Helper()
	if r.Start != start || r.End != end {
		t.Errorf("got [%d,%d), want [%d,%d)", r.Start, r.End, start, end)
	}
}

// ---- Line addresses ---------------------------------------------------------

func TestLine0(t *testing.T) {
	f := makeFile("hello\nworld\n")
	r := evalStr(t, "0", f)
	wantRange(t, r, 0, 0)
}

func TestLine1(t *testing.T) {
	f := makeFile("hello\nworld\n")
	r := evalStr(t, "1", f)
	// Line 1 = "hello\n" → [0,6)
	wantRange(t, r, 0, 6)
}

func TestLine2(t *testing.T) {
	f := makeFile("hello\nworld\n")
	r := evalStr(t, "2", f)
	// Line 2 = "world\n" → [6,12)
	wantRange(t, r, 6, 12)
}

func TestLineDollar(t *testing.T) {
	f := makeFile("hello\nworld\n")
	r := evalStr(t, "$", f)
	n := f.Len()
	wantRange(t, r, n, n)
}

func TestLineBeyondEnd(t *testing.T) {
	f := makeFile("hello\n")
	err := evalErr(t, "5", f)
	if err == nil {
		t.Error("expected error for line beyond end of file")
	}
}

func TestLineNoNewline(t *testing.T) {
	// File with no trailing newline.
	f := makeFile("hello")
	r := evalStr(t, "1", f)
	wantRange(t, r, 0, 5) // whole file
}

// ---- Character addresses ----------------------------------------------------

func TestChar0(t *testing.T) {
	f := makeFile("hello")
	r := evalStr(t, "#0", f)
	wantRange(t, r, 0, 0)
}

func TestCharN(t *testing.T) {
	f := makeFile("hello")
	r := evalStr(t, "#3", f)
	wantRange(t, r, 3, 3)
}

func TestCharEnd(t *testing.T) {
	f := makeFile("hello")
	r := evalStr(t, "#5", f)
	wantRange(t, r, 5, 5)
}

func TestCharBeyondEnd(t *testing.T) {
	f := makeFile("hello")
	err := evalErr(t, "#10", f)
	if err == nil {
		t.Error("expected error for char offset beyond end")
	}
}

// ---- Dot --------------------------------------------------------------------

func TestDotAddress(t *testing.T) {
	f := makeFile("hello world")
	setDot(f, 3, 7)
	r := evalStr(t, ".", f)
	wantRange(t, r, 3, 7)
}

// ---- Regexp addresses -------------------------------------------------------

func TestRegexpForward(t *testing.T) {
	f := makeFile("the cat sat")
	setDot(f, 0, 0)
	r := evalStr(t, "/cat/", f)
	// "cat" is at positions 4-6 (inclusive), range [4,7)
	wantRange(t, r, 4, 7)
}

func TestRegexpFromDot(t *testing.T) {
	f := makeFile("cat and cat")
	setDot(f, 4, 7) // dot = "and"
	r := evalStr(t, "/cat/", f)
	// Should find second "cat" starting from dot.End=7
	wantRange(t, r, 8, 11)
}

func TestRegexpForwardWrap(t *testing.T) {
	// Pattern is before dot — should wrap.
	f := makeFile("cat and dog")
	setDot(f, 8, 11) // dot = "dog"
	r := evalStr(t, "/cat/", f)
	wantRange(t, r, 0, 3)
}

func TestRegexpBackward(t *testing.T) {
	f := makeFile("cat and cat")
	setDot(f, 11, 11) // end of file
	r := evalStr(t, "?cat?", f)
	// Last "cat" before position 11 is at [8,11)
	wantRange(t, r, 8, 11)
}

func TestRegexpNoMatch(t *testing.T) {
	f := makeFile("hello world")
	err := evalErr(t, "/dog/", f)
	if err == nil {
		t.Error("expected error for no-match regexp")
	}
}

func TestRegexpAtExtension(t *testing.T) {
	f := makeFile("hello\nworld")
	setDot(f, 0, 0)
	r := evalStr(t, "/hello@world/", f)
	wantRange(t, r, 0, 11)
}

// ---- Compound addresses -----------------------------------------------------

func TestCommaWhole(t *testing.T) {
	f := makeFile("hello\nworld\n")
	r := evalStr(t, ",", f)
	n := f.Len()
	wantRange(t, r, 0, n)
}

func TestCommaRange(t *testing.T) {
	f := makeFile("hello\nworld\nfoo\n")
	r := evalStr(t, "1,2", f)
	// Line 1 starts at 0, line 2 ends at 12
	wantRange(t, r, 0, 12)
}

func TestCommaDotToDollar(t *testing.T) {
	f := makeFile("hello\nworld\n")
	setDot(f, 3, 5)
	r := evalStr(t, ".,$", f)
	n := f.Len()
	wantRange(t, r, 3, n)
}

func TestSemicolon(t *testing.T) {
	// 1;/world/ — find "world" with dot set to line 1
	f := makeFile("hello\nworld\n")
	r := evalStr(t, "1;/world/", f)
	// Line 1 is "hello\n" [0,6); then /world/ from end of line 1 = pos 6
	wantRange(t, r, 0, 11)
}

// ---- Relative addresses -----------------------------------------------------

func TestDotPlusLine(t *testing.T) {
	f := makeFile("hello\nworld\nfoo\n")
	setDot(f, 0, 6) // dot = line 1
	r := evalStr(t, ".+1", f)
	// One line forward from end of dot (pos 6) → line 2 = [6,12)
	wantRange(t, r, 6, 12)
}

func TestDotMinusLine(t *testing.T) {
	f := makeFile("hello\nworld\nfoo\n")
	setDot(f, 6, 12) // dot = line 2
	r := evalStr(t, ".-1", f)
	// One line back from start of dot (pos 6) → line 1 = [0,6)
	wantRange(t, r, 0, 6)
}

func TestPlusMinusIdiom(t *testing.T) {
	// +-  means: the complete line containing end of dot
	// Equivalently a1+- = a1+0-0 which finds the line containing a1's end.
	// In practice: . +1 -1 → start of next line, then back 1 line.
	f := makeFile("hello\nworld\nfoo\n")
	setDot(f, 2, 4) // middle of line 1
	// "+-" = go forward 1 line (to line 2), back 1 line (to line 1)
	// Actually in Sam: a1+- means forward 1 then back 1 = whole line containing dot end
	r := evalStr(t, ".+-", f)
	// dot.End = 4 is in line 1; +1 gets us to line 2 [6,12); -1 gets back to line 1 [0,6)
	wantRange(t, r, 0, 6)
}

// ---- Parse tests ------------------------------------------------------------

func TestParseLine(t *testing.T) {
	a, n, err := Parse("42rest")
	if err != nil || a == nil {
		t.Fatalf("Parse(42rest): %v", err)
	}
	if a.Kind != KindLine || a.Num != 42 {
		t.Errorf("expected Line 42, got %v %d", a.Kind, a.Num)
	}
	if n != 2 {
		t.Errorf("consumed %d bytes, want 2", n)
	}
}

func TestParseChar(t *testing.T) {
	a, _, err := Parse("#7")
	if err != nil || a == nil || a.Kind != KindChar || a.Num != 7 {
		t.Errorf("Parse(#7): %v %+v", err, a)
	}
}

func TestParseDot(t *testing.T) {
	a, _, err := Parse(".")
	if err != nil || a == nil || a.Kind != KindDot {
		t.Errorf("Parse(.): %v %+v", err, a)
	}
}

func TestParseDollar(t *testing.T) {
	a, _, err := Parse("$")
	if err != nil || a == nil || a.Kind != KindEnd {
		t.Errorf("Parse($): %v %+v", err, a)
	}
}

func TestParseRegexp(t *testing.T) {
	a, _, err := Parse("/hello/")
	if err != nil || a == nil || a.Kind != KindRegexp || a.Pat != "hello" {
		t.Errorf("Parse(/hello/): %v %+v", err, a)
	}
}

func TestParseRegexpBackward(t *testing.T) {
	a, _, err := Parse("?hello?")
	if err != nil || a == nil || a.Kind != KindRegexp || !a.Backward {
		t.Errorf("Parse(?hello?): %v %+v", err, a)
	}
}

func TestParseComma(t *testing.T) {
	a, _, err := Parse("1,3")
	if err != nil || a == nil || a.Kind != KindComma {
		t.Fatalf("Parse(1,3): %v", err)
	}
	if a.Left.Kind != KindLine || a.Left.Num != 1 {
		t.Errorf("left: %v", a.Left)
	}
	if a.Right.Kind != KindLine || a.Right.Num != 3 {
		t.Errorf("right: %v", a.Right)
	}
}

func TestParseBareComma(t *testing.T) {
	a, _, err := Parse(",")
	if err != nil || a == nil || a.Kind != KindComma {
		t.Fatalf("Parse(,): %v %+v", err, a)
	}
	if a.Left.Kind != KindLine || a.Left.Num != 0 {
		t.Errorf("left of bare comma: %v", a.Left)
	}
	if a.Right.Kind != KindEnd {
		t.Errorf("right of bare comma: %v", a.Right)
	}
}

func TestParseRegexpTrailingDelimOmitted(t *testing.T) {
	a, _, err := Parse("/hello")
	if err != nil || a == nil || a.Kind != KindRegexp || a.Pat != "hello" {
		t.Errorf("Parse(/hello): %v %+v", err, a)
	}
}

// ---- Multibyte correctness --------------------------------------------------

func TestLineAddressMultibyte(t *testing.T) {
	// "héllo\nwörld\n" — contains multibyte chars
	content := "héllo\nwörld\n"
	f := makeFile(content)
	r := evalStr(t, "1", f)
	// Line 1 = "héllo\n" — h=0, é=1, l=2, l=3, o=4, \n=5 → [0,6)
	wantRange(t, r, 0, 6)
	r2 := evalStr(t, "2", f)
	// Line 2 = "wörld\n" → [6,12)
	wantRange(t, r2, 6, 12)
}

// ---- File pattern -----------------------------------------------------------

func TestFilePattern(t *testing.T) {
	f1 := file.NewNamed("foo.txt")
	f2 := file.NewNamed("bar.txt")
	files := []*file.File{f1, f2}

	a, _, err := Parse(`"foo"`)
	if err != nil || a == nil {
		t.Fatalf("Parse: %v", err)
	}

	ff, err := FindFileByPat("foo", files, f1)
	if err != nil {
		t.Fatalf("FindFileByPat: %v", err)
	}
	if ff != f1 {
		t.Error("FindFileByPat returned wrong file")
	}
}

func TestFilePatternNoMatch(t *testing.T) {
	f1 := file.NewNamed("foo.txt")
	files := []*file.File{f1}
	_, err := FindFileByPat("bar", files, f1)
	if err == nil {
		t.Error("expected error for no matching file")
	}
}

func TestFilePatternAmbiguous(t *testing.T) {
	f1 := file.NewNamed("foo.txt")
	f2 := file.NewNamed("foo2.txt")
	files := []*file.File{f1, f2}
	_, err := FindFileByPat("foo", files, f1)
	if err == nil {
		t.Error("expected error for ambiguous file pattern")
	}
}

// ---- Edge cases -------------------------------------------------------------

func TestEmptyFile(t *testing.T) {
	f := makeFile("")
	r := evalStr(t, "0", f)
	wantRange(t, r, 0, 0)

	r2 := evalStr(t, "$", f)
	wantRange(t, r2, 0, 0)

	r3 := evalStr(t, ",", f)
	wantRange(t, r3, 0, 0)
}

func TestFirstMatchFromStart(t *testing.T) {
	// 0+/pat/ finds first match in file
	f := makeFile("cat and cat and cat")
	setDot(f, 10, 14) // somewhere in the middle
	// 0 = {0,0}, then + /cat/ from position 0
	r := evalStr(t, "0+/cat/", f)
	wantRange(t, r, 0, 3)
}

func TestLastMatchFromEnd(t *testing.T) {
	f := makeFile("cat and cat and cat")
	n := f.Len()
	setDot(f, 0, 0)
	// $-/cat/ finds last match
	r := evalStr(t, "$-/cat/", f)
	wantRange(t, r, n-3, n)
}

// ---- lineRange unit test ----------------------------------------------------

func TestLineRangeHelper(t *testing.T) {
	cases := []struct {
		content    string
		n          int
		start, end int
	}{
		{"hello\nworld\nfoo\n", 0, 0, 0},
		{"hello\nworld\nfoo\n", 1, 0, 6},
		{"hello\nworld\nfoo\n", 2, 6, 12},
		{"hello\nworld\nfoo\n", 3, 12, 16},
		// A trailing newline begins a further empty line at end of file.
		{"hello\nworld\nfoo\n", 4, 16, 16},
		// No trailing newline: the last line runs to end of file.
		{"a\nb", 2, 2, 3},
		// An empty file still has one (empty) line.
		{"", 1, 0, 0},
		// Offsets are codepoints, not bytes.
		{"héllo\nwörld\n", 2, 6, 12},
	}
	for _, c := range cases {
		f := makeFile(c.content)
		r, err := lineRange(f, c.n)
		if err != nil {
			t.Errorf("lineRange(%q, n=%d): %v", c.content, c.n, err)
			continue
		}
		if r.Start != c.start || r.End != c.end {
			t.Errorf("lineRange(%q, n=%d) = [%d,%d), want [%d,%d)",
				c.content, c.n, r.Start, r.End, c.start, c.end)
		}
	}

	// Lines past the end are an error, as is a negative line number.
	for _, c := range []struct {
		content string
		n       int
	}{
		{"hello\nworld\nfoo\n", 5},
		{"a\nb", 3},
		{"", 2},
		{"hello\n", -1},
	} {
		if _, err := lineRange(makeFile(c.content), c.n); err == nil {
			t.Errorf("lineRange(%q, n=%d): expected error, got none", c.content, c.n)
		}
	}
}

// makeFile helper also needs Buffer access - expose via method
func init() {
	// nothing
	_ = strings.Count
}

// ---- differential tests against the pre-index implementations ---------------

// The line-start table in buffer replaced three string-scanning code paths in
// this package. The originals are kept here so the replacements can be
// checked against them exhaustively rather than only at hand-picked points.

var lineDiffContents = []string{
	"",
	"\n",
	"\n\n\n",
	"a",
	"a\n",
	"a\nb",
	"a\nb\n",
	"hello\nworld\nfoo\n",
	"héllo\nwörld\n☃\n",
	"no newlines at all",
	"trailing blank\n\n",
	"\na\n\nb\n",
}

// oldLineRange is the implementation lineRange replaced, verbatim.
func oldLineRange(s string, total, n int) (buffer.Range, error) {
	if n == 0 {
		return buffer.Range{}, nil
	}
	if n < 0 {
		return buffer.Range{}, errNegative
	}
	lineStart := 0
	bytePos := 0
	for line := 1; line < n; line++ {
		idx := strings.IndexByte(s[bytePos:], '\n')
		if idx == -1 {
			return buffer.Range{}, errBeyondEnd
		}
		nl := bytePos + idx
		lineStart += len([]rune(s[bytePos : nl+1]))
		bytePos = nl + 1
	}
	rest := s[bytePos:]
	idx := strings.IndexByte(rest, '\n')
	var lineEnd int
	if idx == -1 {
		lineEnd = total
	} else {
		lineEnd = lineStart + len([]rune(rest[:idx+1]))
	}
	if lineStart > total {
		return buffer.Range{}, errBeyondEnd
	}
	return buffer.Range{Start: lineStart, End: lineEnd}, nil
}

var (
	errNegative  = fmt.Errorf("negative")
	errBeyondEnd = fmt.Errorf("beyond end")
)

func TestLineRangeMatchesOldImplementation(t *testing.T) {
	for _, s := range lineDiffContents {
		total := len([]rune(s))
		f := makeFile(s)
		for n := -2; n <= total+3; n++ {
			wantR, wantErr := oldLineRange(s, total, n)
			gotR, gotErr := lineRange(f, n)
			if (gotErr != nil) != (wantErr != nil) {
				t.Fatalf("%q n=%d: err mismatch: got %v, old %v", s, n, gotErr, wantErr)
			}
			if gotErr == nil && gotR != wantR {
				t.Fatalf("%q n=%d: got [%d,%d), old [%d,%d)",
					s, n, gotR.Start, gotR.End, wantR.Start, wantR.End)
			}
		}
	}
}

// evalPlus and evalMinus derived a line number from a codepoint offset by
// byte-scanning. Check the buffer-backed derivations against those formulas
// at every position.
func TestLineNumberDerivationsMatchOldImplementation(t *testing.T) {
	for _, s := range lineDiffContents {
		f := makeFile(s)
		buf := f.Buffer()
		for pos := 0; pos <= len([]rune(s)); pos++ {
			byteP := byteOff(s, pos)

			// evalMinus: strings.Count(s[:byteP], "\n") + 1
			wantMinus := strings.Count(s[:byteP], "\n") + 1
			if got := buf.LineNumber(pos); got != wantMinus {
				t.Fatalf("%q pos=%d: LineNumber = %d, old minus-form = %d",
					s, pos, got, wantMinus)
			}

			// evalPlus: same, but +1 more when pos is mid-line.
			atLineStart := byteP == 0 || s[byteP-1] == '\n'
			wantPlus := wantMinus
			if !atLineStart {
				wantPlus++
			}
			gotPlus := buf.LineNumber(pos)
			if pos > 0 && buf.RuneAt(pos-1) != '\n' {
				gotPlus++
			}
			if gotPlus != wantPlus {
				t.Fatalf("%q pos=%d: plus-form = %d, old plus-form = %d",
					s, pos, gotPlus, wantPlus)
			}
		}
	}
}

// ---- benchmarks -------------------------------------------------------------

func benchFile() *file.File {
	var sb strings.Builder
	for i := 0; i < 100000; i++ {
		sb.WriteString("the quick brown fox jumps over the lazy dog\n")
	}
	return makeFile(sb.String())
}

// One line address per buffer generation: the new code pays for the table
// build, the old code paid for a String copy plus a scan. This is the case
// least favourable to the index.
//
// The edit that bumps the generation is made at the gap so that it costs
// essentially nothing; an edit chosen to force a full-buffer gap move would
// swamp the number being measured here.
func BenchmarkLineAddressColdOld(b *testing.B) {
	f := benchFile()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = oldLineRange(f.String(), f.Len(), i%100000+1)
	}
}

func BenchmarkLineAddressColdNew(b *testing.B) {
	f := benchFile()
	buf := f.Buffer()
	buf.Insert(buf.Len(), "x") // park the gap at the end, once
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Insert(buf.Len(), "x") // bump the generation, forcing a rebuild
		_, _ = lineRange(f, i%100000+1)
	}
}

// Many line addresses against one generation, which is what a ranged command
// or a loop body actually does.
func BenchmarkLineAddressWarmOld(b *testing.B) {
	f := benchFile()
	s := f.String()
	total := f.Len()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = oldLineRange(s, total, i%100000+1)
	}
}

func BenchmarkLineAddressWarmNew(b *testing.B) {
	f := benchFile()
	_, _ = lineRange(f, 1) // build the table, as the first address would
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = lineRange(f, i%100000+1)
	}
}
