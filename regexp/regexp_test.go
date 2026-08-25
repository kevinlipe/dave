package regexp

import (
	"testing"
)

func TestAtMatchesNewline(t *testing.T) {
	r := MustCompile("@+")
	lo, hi := r.FindForward("a\nb", 0, 3)
	if lo != 0 || hi != 3 {
		t.Errorf("@ should match newline: got [%d,%d), want [0,3)", lo, hi)
	}
}

func TestAtMatchesAll(t *testing.T) {
	r := MustCompile("a@b")
	lo, hi := r.FindForward("a\nb", 0, 3)
	if lo != 0 || hi != 3 {
		t.Errorf("a@b on a\\nb: got [%d,%d), want [0,3)", lo, hi)
	}
}

func TestDotDoesNotMatchNewline(t *testing.T) {
	r := MustCompile("a.b")
	lo, hi := r.FindForward("a\nb", 0, 3)
	if lo != -1 {
		t.Errorf("a.b should not match a\\nb, got [%d,%d)", lo, hi)
	}
}

func TestBackslashNMatchesNewline(t *testing.T) {
	r := MustCompile(`a\nb`)
	lo, hi := r.FindForward("a\nb", 0, 3)
	if lo != 0 || hi != 3 {
		t.Errorf(`a\nb should match a\nb: got [%d,%d), want [0,3)`, lo, hi)
	}
}

func TestEmptyPatternReuse(t *testing.T) {
	ResetLastPattern()
	MustCompile("hello")
	r2, err := Compile("")
	if err != nil {
		t.Fatal(err)
	}
	if r2.Pattern() != "hello" {
		t.Errorf("empty pattern should reuse last: got %q", r2.Pattern())
	}
}

func TestEmptyPatternNoLast(t *testing.T) {
	ResetLastPattern()
	_, err := Compile("")
	if err == nil {
		t.Error("expected error with no previous pattern")
	}
}

func TestForwardSearch(t *testing.T) {
	r := MustCompile("cat")
	s := "the cat sat"
	lo, hi := r.FindForward(s, 0, len(s))
	if lo != 4 || hi != 7 {
		t.Errorf("forward search: got [%d,%d), want [4,7)", lo, hi)
	}
}

func TestForwardSearchNotFound(t *testing.T) {
	r := MustCompile("dog")
	s := "the cat sat"
	lo, hi := r.FindForward(s, 0, len(s))
	if lo != -1 {
		t.Errorf("forward search not found: got [%d,%d), want -1", lo, hi)
	}
}

func TestForwardSearchInRange(t *testing.T) {
	r := MustCompile("o")
	s := "foobar"
	lo, hi := r.FindForward(s, 2, len(s)) // search from 'o' at index 2
	if lo != 2 || hi != 3 {
		t.Errorf("got [%d,%d), want [2,3)", lo, hi)
	}
}

func TestBackwardSearch(t *testing.T) {
	r := MustCompile("cat")
	s := "cat and cat"
	// Backward search should find the LAST match.
	lo, hi := r.FindBackward(s, 0, len(s))
	if lo != 8 || hi != 11 {
		t.Errorf("backward search: got [%d,%d), want [8,11)", lo, hi)
	}
}

func TestBackwardSearchSingleMatch(t *testing.T) {
	r := MustCompile("cat")
	s := "the cat sat"
	lo, hi := r.FindBackward(s, 0, len(s))
	if lo != 4 || hi != 7 {
		t.Errorf("backward single match: got [%d,%d), want [4,7)", lo, hi)
	}
}

func TestBackwardSearchNotFound(t *testing.T) {
	r := MustCompile("dog")
	s := "the cat sat"
	lo, _ := r.FindBackward(s, 0, len(s))
	if lo != -1 {
		t.Errorf("backward not found: got %d, want -1", lo)
	}
}

func TestFindForwardAll(t *testing.T) {
	r := MustCompile("cat")
	s := "cat and cat"
	matches := r.FindForwardAll(s, 0, len(s))
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matches))
	}
	if matches[0] != [2]int{0, 3} {
		t.Errorf("match[0] = %v, want [0,3]", matches[0])
	}
	if matches[1] != [2]int{8, 11} {
		t.Errorf("match[1] = %v, want [8,11]", matches[1])
	}
}

func TestFindForwardAllEmpty(t *testing.T) {
	r := MustCompile("dog")
	s := "the cat sat"
	matches := r.FindForwardAll(s, 0, len(s))
	if matches != nil {
		t.Errorf("expected nil, got %v", matches)
	}
}

func TestMatchString(t *testing.T) {
	r := MustCompile("hel+o")
	if !r.MatchString("hello world") {
		t.Error("MatchString should return true")
	}
	if r.MatchString("world") {
		t.Error("MatchString should return false")
	}
}

func TestReplaceExpand(t *testing.T) {
	r := MustCompile(`(hello) (world)`)
	s := "hello world"
	idx := r.FindSubmatchIndex(s, 0, len(s))
	if idx == nil {
		t.Fatal("no match")
	}
	result := r.ReplaceExpand(s, `$2, $1`, idx)
	if result != "world, hello" {
		t.Errorf("ReplaceExpand = %q, want %q", result, "world, hello")
	}
}

func TestAmpersandExpand(t *testing.T) {
	r := MustCompile(`[a-z]+`)
	s := "hello world"
	idx := r.FindSubmatchIndex(s, 0, len(s))
	if idx == nil {
		t.Fatal("no match")
	}
	// & is not natively supported by Go's ExpandString but Sam uses it.
	// Test that our callers handle & → ${0} transformation.
	// (The cmd package does this substitution before calling ReplaceExpand.)
	result := r.ReplaceExpand(s, `${0}!`, idx)
	if result != "hello!" {
		t.Errorf("ReplaceExpand with $0 = %q, want %q", result, "hello!")
	}
}

func TestMultilinePattern(t *testing.T) {
	r := MustCompile(`(?m)^hello`)
	s := "world\nhello\nfoo"
	lo, hi := r.FindForward(s, 0, len(s))
	if lo != 6 || hi != 11 {
		t.Errorf("multiline ^hello: got [%d,%d), want [6,11)", lo, hi)
	}
}

func TestAtAcrossLines(t *testing.T) {
	// @ should span multiple lines
	r := MustCompile(`begin@+end`)
	s := "begin\n\nend"
	lo, hi := r.FindForward(s, 0, len(s))
	if lo != 0 || hi != len(s) {
		t.Errorf("@+ across lines: got [%d,%d), want [0,%d)", lo, hi, len(s))
	}
}

// Sam prefers the earliest match and then the longest one (newmatch,
// sam/regexp.c). Go's default is Perl's leftmost-first, under which an
// alternation returns whichever branch is written first rather than the longest
// — so /foo|foobar/ would match only "foo".
func TestLeftmostLongest(t *testing.T) {
	tests := []struct {
		pat, text      string
		wantLo, wantHi int
	}{
		{"foo|foobar", "foobar", 0, 6},
		{"foobar|foo", "foobar", 0, 6},
		{"a|ab|abc", "abc", 0, 3},
		{"x*", "xxx", 0, 3},
		{"(a|ab)(c|bcd)", "abcd", 0, 4},
		// Leftmost still wins over longest.
		{"bb|a", "abb", 0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.pat, func(t *testing.T) {
			re, err := Compile(tt.pat)
			if err != nil {
				t.Fatal(err)
			}
			lo, hi := re.FindForward(tt.text, 0, len(tt.text))
			if lo != tt.wantLo || hi != tt.wantHi {
				t.Errorf("%q on %q = [%d,%d), want [%d,%d)",
					tt.pat, tt.text, lo, hi, tt.wantLo, tt.wantHi)
			}
		})
	}
}

// Longest changes the whole-match preference; submatch boundaries must still be
// reported, since s//\1/ depends on them.
func TestSubmatchesSurviveLongest(t *testing.T) {
	re, err := Compile("(t)(w)")
	if err != nil {
		t.Fatal(err)
	}
	idx := re.FindSubmatchIndex("two", 0, 3)
	want := []int{0, 2, 0, 1, 1, 2}
	if len(idx) != len(want) {
		t.Fatalf("FindSubmatchIndex = %v, want %v", idx, want)
	}
	for i := range want {
		if idx[i] != want[i] {
			t.Fatalf("FindSubmatchIndex = %v, want %v", idx, want)
		}
	}
}

// Sam's lex knows exactly one escape, \n; every other \X is the literal X
// (sam/regexp.c). Go would read most of these as classes, anchors or control
// characters, so a pattern written for sam selected different text here without
// reporting anything. Backslash-escaped punctuation already agreed.
func TestEscapesAreLiteralAsInSam(t *testing.T) {
	tests := []struct {
		pat, text      string
		wantLo, wantHi int
	}{
		{`\d`, "a d 7", 2, 3},   // the letter d, not the digit
		{`\w`, "a w 7", 2, 3},   // the letter w
		{`\t`, "a t\t", 2, 3},   // the letter t, not a tab
		{`\b`, "a b c", 2, 3},   // the letter b, not a word boundary
		{`\s`, "a s b", 2, 3},   // the letter s
		{`\.`, "a.b", 1, 2},     // punctuation is unchanged: a literal dot
		{`\*`, "a*b", 1, 2},     // likewise
		{`\\`, `a\b`, 1, 2},     // a literal backslash
		{`a\`, `xa\y`, 1, 3},    // a trailing backslash is literal, not an error
		{"a\\nb", "a\nb", 0, 3}, // \n is the one escape sam has
	}
	for _, tt := range tests {
		t.Run(tt.pat, func(t *testing.T) {
			re, err := Compile(tt.pat)
			if err != nil {
				t.Fatalf("Compile(%q): %v", tt.pat, err)
			}
			lo, hi := re.FindForward(tt.text, 0, len(tt.text))
			if lo != tt.wantLo || hi != tt.wantHi {
				t.Errorf("%q on %q = [%d,%d), want [%d,%d)",
					tt.pat, tt.text, lo, hi, tt.wantLo, tt.wantHi)
			}
		})
	}
}

// Braces are ordinary characters, as in sam. Go would read counted repetition,
// a second meaning for braces alongside the 0,$ address model.
func TestBracesAreLiteral(t *testing.T) {
	for _, tt := range []struct {
		pat, text      string
		wantLo, wantHi int
	}{
		{`a{2,3}`, "xa{2,3}y", 1, 7},
		{`{`, "a{b", 1, 2},
		{`}`, "a}b", 1, 2},
		// aa is not a match for a{2}: the braces are text.
		{`a{2}`, "aa", -1, -1},
	} {
		re, err := Compile(tt.pat)
		if err != nil {
			t.Fatalf("Compile(%q): %v", tt.pat, err)
		}
		lo, hi := re.FindForward(tt.text, 0, len(tt.text))
		if lo != tt.wantLo || hi != tt.wantHi {
			t.Errorf("%q on %q = [%d,%d), want [%d,%d)", tt.pat, tt.text, lo, hi, tt.wantLo, tt.wantHi)
		}
	}
}

// POSIX classes stay: sam has no syntax for them either, but they read as
// classes rather than as a second meaning for a punctuation pair already spoken
// for elsewhere in the language.
func TestPosixClassesAreKept(t *testing.T) {
	for _, tt := range []struct {
		pat, text      string
		wantLo, wantHi int
	}{
		{`[[:digit:]]+`, "ab123", 2, 5},
		{`[[:alpha:]]+`, "12abc", 2, 5},
	} {
		re, err := Compile(tt.pat)
		if err != nil {
			t.Fatalf("Compile(%q): %v", tt.pat, err)
		}
		lo, hi := re.FindForward(tt.text, 0, len(tt.text))
		if lo != tt.wantLo || hi != tt.wantHi {
			t.Errorf("%q on %q = [%d,%d), want [%d,%d)", tt.pat, tt.text, lo, hi, tt.wantLo, tt.wantHi)
		}
	}
}

// Sam seeds every negated class with a newline as it builds it (`classp[n++] =
// '\n'; /* don't match newline in negate case */`, bldcclass), so `[^ ]` cannot
// cross a line boundary there. Go's can, which made `,x/[^ ]+/` iterate
// line-spanning runs here and line-bounded ones in sam — the same kind of quiet
// difference in selected text as the Perl escapes.
func TestNegatedClassExcludesNewline(t *testing.T) {
	tests := []struct {
		pat, text      string
		wantLo, wantHi int
	}{
		{`[^o]+`, "ab\ncd", 0, 2},
		{`[^ ]+`, "ab\ncd", 0, 2},
		{`[^o]`, "\n", -1, -1},
		{`[^[:digit:]]+`, "ab\n1", 0, 2},
		// An explicit \n in a negated class is not a contradiction, just a
		// duplicate.
		{`[^\n]+`, "ab\ncd", 0, 2},
		// A plain class is untouched: it matches a newline only if it says so.
		{`[\n]`, "\n", 0, 1},
		{`[ab\n]+`, "ab\ncd", 0, 3},
	}
	for _, tt := range tests {
		t.Run(tt.pat, func(t *testing.T) {
			re, err := Compile(tt.pat)
			if err != nil {
				t.Fatalf("Compile(%q): %v", tt.pat, err)
			}
			lo, hi := re.FindForward(tt.text, 0, len(tt.text))
			if lo != tt.wantLo || hi != tt.wantHi {
				t.Errorf("%q on %q = [%d,%d), want [%d,%d)",
					tt.pat, tt.text, lo, hi, tt.wantLo, tt.wantHi)
			}
		})
	}
}

// Scanning the class to place that newline has to get the rest of the class
// right: a POSIX name carries a ']' that does not end the class, an escaped ']'
// does not either, and '@' is an ordinary character inside brackets rather than
// the any-character operator it is outside them.
func TestClassContents(t *testing.T) {
	tests := []struct {
		pat, text      string
		wantLo, wantHi int
	}{
		{`[[:alpha:]]+`, "ab1", 0, 2},
		{`[[:alpha:]0-9]+`, "a1!", 0, 2},
		{`[\]]`, "]", 0, 1},
		{`[a\]b]+`, "a]b", 0, 3},
		// @ inside a class is the character, not "any character": the
		// substitution used to run here too and corrupt the class.
		{`[a@]`, "@", 0, 1},
		{`[a@]`, "z", -1, -1},
		// Escapes inside a class follow the same rule as outside.
		{`[\d]`, "d", 0, 1},
		{`[\d]`, "7", -1, -1},
		// A backslash-escaped '-' is a literal, not a range.
		{`[a\-z]`, "-", 0, 1},
		{`[a\-z]`, "m", -1, -1},
		// Braces inside a class are ordinary, as they are outside.
		{`[{}]+`, "x{}y", 1, 3},
	}
	for _, tt := range tests {
		t.Run(tt.pat+"_"+tt.text, func(t *testing.T) {
			re, err := Compile(tt.pat)
			if err != nil {
				t.Fatalf("Compile(%q): %v", tt.pat, err)
			}
			lo, hi := re.FindForward(tt.text, 0, len(tt.text))
			if lo != tt.wantLo || hi != tt.wantHi {
				t.Errorf("%q on %q = [%d,%d), want [%d,%d)",
					tt.pat, tt.text, lo, hi, tt.wantLo, tt.wantHi)
			}
		})
	}
}
