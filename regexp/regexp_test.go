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
	r := MustCompile(`\w+`)
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
