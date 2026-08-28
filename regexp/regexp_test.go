package regexp

import (
	"testing"
)

func TestAtMatchesNewline(t *testing.T) {
	r := MustCompile("@+")
	lo, hi := r.FindForward(NewRunes("a\nb"), 0, 3)
	if lo != 0 || hi != 3 {
		t.Errorf("@ should match newline: got [%d,%d), want [0,3)", lo, hi)
	}
}

func TestAtMatchesAll(t *testing.T) {
	r := MustCompile("a@b")
	lo, hi := r.FindForward(NewRunes("a\nb"), 0, 3)
	if lo != 0 || hi != 3 {
		t.Errorf("a@b on a\\nb: got [%d,%d), want [0,3)", lo, hi)
	}
}

func TestDotDoesNotMatchNewline(t *testing.T) {
	r := MustCompile("a.b")
	lo, hi := r.FindForward(NewRunes("a\nb"), 0, 3)
	if lo != -1 {
		t.Errorf("a.b should not match a\\nb, got [%d,%d)", lo, hi)
	}
}

func TestBackslashNMatchesNewline(t *testing.T) {
	r := MustCompile(`a\nb`)
	lo, hi := r.FindForward(NewRunes("a\nb"), 0, 3)
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
	lo, hi := r.FindForward(NewRunes(s), 0, len(s))
	if lo != 4 || hi != 7 {
		t.Errorf("forward search: got [%d,%d), want [4,7)", lo, hi)
	}
}

func TestForwardSearchNotFound(t *testing.T) {
	r := MustCompile("dog")
	s := "the cat sat"
	lo, hi := r.FindForward(NewRunes(s), 0, len(s))
	if lo != -1 {
		t.Errorf("forward search not found: got [%d,%d), want -1", lo, hi)
	}
}

func TestForwardSearchInRange(t *testing.T) {
	r := MustCompile("o")
	s := "foobar"
	lo, hi := r.FindForward(NewRunes(s), 2, len(s)) // search from 'o' at index 2
	if lo != 2 || hi != 3 {
		t.Errorf("got [%d,%d), want [2,3)", lo, hi)
	}
}

func TestBackwardSearch(t *testing.T) {
	r := MustCompile("cat")
	s := "cat and cat"
	// Backward search should find the LAST match.
	lo, hi := r.FindBackward(NewRunes(s), 0, len(s))
	if lo != 8 || hi != 11 {
		t.Errorf("backward search: got [%d,%d), want [8,11)", lo, hi)
	}
}

func TestBackwardSearchSingleMatch(t *testing.T) {
	r := MustCompile("cat")
	s := "the cat sat"
	lo, hi := r.FindBackward(NewRunes(s), 0, len(s))
	if lo != 4 || hi != 7 {
		t.Errorf("backward single match: got [%d,%d), want [4,7)", lo, hi)
	}
}

func TestBackwardSearchNotFound(t *testing.T) {
	r := MustCompile("dog")
	s := "the cat sat"
	lo, _ := r.FindBackward(NewRunes(s), 0, len(s))
	if lo != -1 {
		t.Errorf("backward not found: got %d, want -1", lo)
	}
}

// Successive matches, the way sam's s and x loops walk them: search from
// where the last match ended. This replaced FindForwardAll, which collected
// every match up front.
func TestSuccessiveForwardMatches(t *testing.T) {
	r := MustCompile("cat")
	in := NewRunes("cat and cat")
	var got [][2]int
	for p := 0; p <= in.Len(); {
		lo, hi := r.FindForward(in, p, in.Len())
		if lo < 0 {
			break
		}
		got = append(got, [2]int{lo, hi})
		if hi == lo {
			p = hi + 1
		} else {
			p = hi
		}
	}
	want := [][2]int{{0, 3}, {8, 11}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("matches = %v, want %v", got, want)
	}
}

func TestNoForwardMatches(t *testing.T) {
	r := MustCompile("dog")
	in := NewRunes("the cat sat")
	if lo, _ := r.FindForward(in, 0, in.Len()); lo != -1 {
		t.Errorf("expected no match, got %d", lo)
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

// Sam's replacement text: \1..\9 are the groups, & is the whole match, and
// any other \X is the literal X. Go's ${1} template syntax is gone with the
// engine that needed it.
func TestExpandGroups(t *testing.T) {
	r := MustCompile(`(hello) (world)`)
	in := NewRunes("hello world")
	idx := r.FindSubmatchIndex(in, 0, in.Len())
	if idx == nil {
		t.Fatal("no match")
	}
	if got := Expand(in, `\2, \1`, idx); got != "world, hello" {
		t.Errorf("Expand = %q, want %q", got, "world, hello")
	}
}

func TestExpandAmpersand(t *testing.T) {
	r := MustCompile(`[a-z]+`)
	in := NewRunes("hello world")
	idx := r.FindSubmatchIndex(in, 0, in.Len())
	if idx == nil {
		t.Fatal("no match")
	}
	if got := Expand(in, `&!`, idx); got != "hello!" {
		t.Errorf("Expand = %q, want %q", got, "hello!")
	}
	// A backslash before anything else is that character, so \& is a literal.
	if got := Expand(in, `\&`, idx); got != "&" {
		t.Errorf("Expand of an escaped & = %q, want %q", got, "&")
	}
}

// ^ and $ are line anchors in sam, with no flag to turn that on and none to
// turn it off; (?m) is a Go-ism and is gone with Go's engine.
func TestLineAnchors(t *testing.T) {
	r := MustCompile(`^hello`)
	s := "world\nhello\nfoo"
	lo, hi := r.FindForward(NewRunes(s), 0, len(s))
	if lo != 6 || hi != 11 {
		t.Errorf("^hello: got [%d,%d), want [6,11)", lo, hi)
	}
}

func TestAtAcrossLines(t *testing.T) {
	// @ should span multiple lines
	r := MustCompile(`begin@+end`)
	s := "begin\n\nend"
	lo, hi := r.FindForward(NewRunes(s), 0, len(s))
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
			lo, hi := re.FindForward(NewRunes(tt.text), 0, len(tt.text))
			if lo != tt.wantLo || hi != tt.wantHi {
				t.Errorf("%q on %q = [%d,%d), want [%d,%d)",
					tt.pat, tt.text, lo, hi, tt.wantLo, tt.wantHi)
			}
		})
	}
}

// Submatch boundaries must be reported, since s//\1/ depends on them. Sam
// keeps a fixed nine groups plus the whole match, so a match always reports
// ten pairs and the ones that did not participate are (-1,-1).
func TestSubmatchesSurviveLongest(t *testing.T) {
	re, err := Compile("(t)(w)")
	if err != nil {
		t.Fatal(err)
	}
	idx := re.FindSubmatchIndex(NewRunes("two"), 0, 3)
	want := []int{0, 2, 0, 1, 1, 2, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1}
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
			lo, hi := re.FindForward(NewRunes(tt.text), 0, len(tt.text))
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
		lo, hi := re.FindForward(NewRunes(tt.text), 0, len(tt.text))
		if lo != tt.wantLo || hi != tt.wantHi {
			t.Errorf("%q on %q = [%d,%d), want [%d,%d)", tt.pat, tt.text, lo, hi, tt.wantLo, tt.wantHi)
		}
	}
}

// POSIX classes are gone, along with every other construct sam has no syntax
// for. What is left is what sam reads: `[[:digit:]]` is a class of the six
// distinct characters between the brackets -- [ : d i g t -- followed by a
// literal `]`, so it does not match a digit. Verified against sam, which
// reports no search for /[[:alpha:]]/ on a line of prose.
func TestPosixClassesReadAsSamReadsThem(t *testing.T) {
	for _, tt := range []struct {
		pat, text      string
		wantLo, wantHi int
	}{
		{`[[:digit:]]+`, "ab123", -1, -1},
		{`[[:alpha:]]+`, "12abc", -1, -1},
		{`[[:digit:]]+`, "d]]", 0, 3}, // a class character, then the literal ]s
		{`[0-9]+`, "ab123", 2, 5},     // what sam writes instead
	} {
		re, err := Compile(tt.pat)
		if err != nil {
			t.Fatalf("Compile(%q): %v", tt.pat, err)
		}
		in := NewRunes(tt.text)
		lo, hi := re.FindForward(in, 0, in.Len())
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
			lo, hi := re.FindForward(NewRunes(tt.text), 0, len(tt.text))
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
			lo, hi := re.FindForward(NewRunes(tt.text), 0, len(tt.text))
			if lo != tt.wantLo || hi != tt.wantHi {
				t.Errorf("%q on %q = [%d,%d), want [%d,%d)",
					tt.pat, tt.text, lo, hi, tt.wantLo, tt.wantHi)
			}
		})
	}
}
