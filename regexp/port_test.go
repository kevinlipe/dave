package regexp

import (
	"testing"
	"unicode/utf8"
)

// The ported machine against the Go-backed one it replaces, over patterns
// both read the same way. Where these two disagree on such a pattern, one of
// them has a bug.
//
// Anchors are deliberately absent from this corpus, because the old engine is
// wrong about both of them and cannot serve as an oracle: it searches by
// slicing s[start:end] and handing the slice to Go, so `(?m)^` matches at the
// start of any search range rather than at a line start, and its `$` matches
// at the end of the text where sam's forward EOL is only `c == '\n'`. Both
// were confirmed against sam by hand and are pinned in
// TestPortedAnchorsMatchSam below, with sam's answers rather than the old
// engine's.

var portPatterns = []string{
	"a", "abc", "z",
	"a*", "a+", "a?", "ab*c", "a*b*",
	"a|b", "abc|abd", "(a|b)c", "(ab)+", "(a|b)*c",
	"[a-z]", "[a-z]+", "[^a-z]", "[^a-z]+", "[abc]", "[a-cx-z]+",
	".", ".*", "a.c", ".+",
	"\\n", "a\\nb", "\\*", "a\\|b",
	"(a)(b)(c)", "((a)b)c",
	"héllo", "[é☃]+",
}

var portInputs = []string{
	"", "a", "abc", "aaa", "abcabc",
	"hello world", "ababab",
	"a\nb\n", "line one\nline two\n",
	"héllo wörld", "☃é☃",
	"xyz", "abd",
}

// runeOff converts a byte offset in s to a codepoint offset, for comparing
// the old byte-based API against the new rune-based one.
func runeOff(s string, b int) int {
	if b < 0 {
		return -1
	}
	return utf8.RuneCountInString(s[:b])
}

// Anchors, against sam's answers rather than the old engine's — every value
// below was read off plan9port's sam by hand. `^` is a line start and nothing
// else, so it does not match merely because a search began there; forward `$`
// is only ever "before a newline", so it does not match at the end of a file
// with no final newline. The old engine gets both wrong.
func TestPortedAnchorsMatchSam(t *testing.T) {
	for _, c := range []struct {
		in, pat        string
		start          int
		wantLo, wantHi int // -1, -1 for no match
	}{
		{"aaa\nbbb\n", "a$", 0, 2, 3},
		{"aaa\nbbb\n", "b$", 0, 6, 7},
		{"aaa\nbbb\n", "^bbb$", 0, 4, 7},
		{"aaa\nbbb\n", "^aaa$", 0, 0, 3},
		{"aaa\nbbb\n", "^b", 0, 4, 5},
		{"aaa\nbbb\n", "^", 1, 4, 4},
		{"aaa\nbbb\n", "^a", 1, -1, -1}, // sam wraps to find this; nothing at or after 1
		{"abc", "c$", 0, -1, -1},        // no final newline, so no end of line
		{"abc", "abc$", 0, -1, -1},
		{"aa\n\nbb\n", "^$", 0, 3, 3},
		{"aa\n\nbb\n", "^bb$", 0, 4, 6},
	} {
		p, err := compileProg(c.pat, false)
		if err != nil {
			t.Fatalf("compileProg(%q): %v", c.pat, err)
		}
		runes := NewRunes(c.in)
		lo, hi := -1, -1
		if se, ok := p.execute(runes, c.start, runes.Len()); ok {
			lo, hi = se[0].p1, se[0].p2
		}
		if lo != c.wantLo || hi != c.wantHi {
			t.Errorf("/%s/ on %q from %d: got (%d,%d), sam (%d,%d)",
				c.pat, c.in, c.start, lo, hi, c.wantLo, c.wantHi)
		}
	}
}

// The backward machine, against sam. These are the cases the borrowed engine
// could not express: sam takes the match ending last and the longest among
// those, where "find every forward match and keep the last" takes the last of
// a non-overlapping partition. `a+` agrees, which is what hid this.
func TestPortedBackwardMatchesSam(t *testing.T) {
	const in = "aaaa\nababa\nxaaay\n" // a:0-3 nl:4 ababa:5-9 nl:10 xaaay:11-15
	runes := NewRunes(in)
	for _, c := range []struct {
		pat            string
		end            int
		wantLo, wantHi int
	}{
		{"aa", 17, 13, 15},
		{"aba", 17, 7, 10},
		{"a+", 17, 12, 15},
		{"aa", 4, 2, 4},
		{"aa", 3, 1, 3},
		{"aba", 10, 7, 10},
		{"aba", 9, 5, 8},
		// Asymmetric patterns, which are the ones that can tell a backward
		// program from a forward one run over reversed input. Without them a
		// palindrome corpus passes with the concatenation swap removed, which
		// is exactly what the first version of this test did.
		{"ab", 17, 7, 9},
		{"ba", 17, 8, 10},
		{"xa+", 17, 11, 15},
		{"a+y", 17, 12, 16},
		{"ab*a", 17, 13, 15},
		{"ab", 10, 7, 9},
		{"[a-b]y", 17, 14, 16},
		{"(ab)+", 17, 5, 9},
	} {
		p, err := compileProg(c.pat, true)
		if err != nil {
			t.Fatalf("compileProg(%q, backwards): %v", c.pat, err)
		}
		lo, hi := -1, -1
		if se, ok := p.bexecute(runes, 0, c.end); ok {
			lo, hi = se[0].p1, se[0].p2
		}
		if lo != c.wantLo || hi != c.wantHi {
			t.Errorf("?%s? on %q before %d: got (%d,%d), sam (%d,%d)",
				c.pat, in, c.end, lo, hi, c.wantLo, c.wantHi)
		}
	}
}
