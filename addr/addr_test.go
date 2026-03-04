package addr

import (
	"dave/buffer"
	"dave/file"
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
	s := "hello\nworld\nfoo\n"
	total := len([]rune(s)) // 16

	cases := []struct {
		n          int
		start, end int
	}{
		{0, 0, 0},
		{1, 0, 6},
		{2, 6, 12},
		{3, 12, 16},
	}
	for _, c := range cases {
		r, err := lineRange(s, total, c.n)
		if err != nil {
			t.Errorf("lineRange(n=%d): %v", c.n, err)
			continue
		}
		if r.Start != c.start || r.End != c.end {
			t.Errorf("lineRange(n=%d) = [%d,%d), want [%d,%d)", c.n, r.Start, r.End, c.start, c.end)
		}
	}
}

// makeFile helper also needs Buffer access - expose via method
func init() {
	// nothing
	_ = strings.Count
}
