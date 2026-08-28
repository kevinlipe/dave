package main

import (
	"dave/buffer"
	"slices"
	"testing"
)

// The command line is read the way sam's ARGBEGIN reads it. The rule that
// matters is that flags stop at the first argument that is not one, so a name
// after that point is a name whatever it looks like — and that an argument
// starting with `-` that is not a flag is an error rather than a file. Dave's
// old loop compared each argument against the literal "-d" and took everything
// else as a file, which turned `dave -x` into a buffer called `-x` that `w`
// would have written out.
//
// Checked against sam by hand: `sam -d f1 -d` opens two files, one of them
// named `-d`, and `sam -d --` opens the nameless buffer.
func TestParseArgs(t *testing.T) {
	for _, c := range []struct {
		args     []string
		headless bool
		files    []string
		what     string
	}{
		{nil, false, nil, "no arguments"},
		{[]string{"-d"}, true, nil, "the flag alone"},
		{[]string{"a", "b"}, false, []string{"a", "b"}, "files alone"},
		{[]string{"-d", "a"}, true, []string{"a"}, "flag then file"},
		{[]string{"-dd", "a"}, true, []string{"a"}, "the flag twice in one argument"},
		{[]string{"a", "-d"}, false, []string{"a", "-d"}, "a flag after a file is a file"},
		{[]string{"-d", "--", "-x"}, true, []string{"-x"}, "-- ends the flags"},
		{[]string{"--"}, false, nil, "-- alone leaves no files"},
		{[]string{"-"}, false, []string{"-"}, "a lone - is a file name"},
	} {
		headless, files, err := parseArgs(c.args)
		if err != nil {
			t.Errorf("%s: %v", c.what, err)
			continue
		}
		if headless != c.headless {
			t.Errorf("%s: headless = %v, want %v", c.what, headless, c.headless)
		}
		if !slices.Equal(files, c.files) {
			t.Errorf("%s: files = %q, want %q", c.what, files, c.files)
		}
	}
}

// An unrecognised flag is refused. Sam prints `sam: unknown flag x` and the
// usage line; the point here is only that it does not become a file name.
func TestParseArgsRejectsAnUnknownFlag(t *testing.T) {
	for _, args := range [][]string{{"-x"}, {"-dx"}, {"-x", "f"}, {"-d", "-x"}} {
		if _, _, err := parseArgs(args); err == nil {
			t.Errorf("%q: expected an error, got none", args)
		}
	}
}

// naiveLookForward is what lookForward means, stated the slow way: scan
// forward a codepoint at a time from start, then wrap and scan the part
// before it. Match ranges are in codepoints.
//
// It is not quite the implementation lookForward replaced. That one clamped
// the wrap pass with `if from == 0`, which is also true of the *first* pass
// whenever start is 0 -- so Look found nothing at all with the cursor at the
// top of the file. TestLookForwardFromTheStartOfTheFile pins the fix.
func naiveLookForward(buf *buffer.Buffer, needle string, start int) (int, int) {
	runes := []rune(buf.String())
	needleRunes := []rune(needle)
	n, nLen := len(runes), len(needleRunes)
	if nLen == 0 || nLen > n {
		return -1, -1
	}
	at := func(i int) bool { return slices.Equal(runes[i:i+nLen], needleRunes) }
	for i := start; i <= n-nLen; i++ {
		if at(i) {
			return i, i + nLen
		}
	}
	for i := 0; i < start && i <= n-nLen; i++ {
		if at(i) {
			return i, i + nLen
		}
	}
	return -1, -1
}

// Every needle against every starting position, on text where a codepoint is
// not a byte: the new version indexes bytes and converts back, so a multi-byte
// rune anywhere before the match is what would break it.
func TestLookForwardMatchesNaiveScan(t *testing.T) {
	contents := []string{
		"",
		"foo",
		"foo bar foo baz",
		"héllo wörld héllo",
		"☃☃☃",
		"aaaa",
		"one\ntwo\none\n",
	}
	needles := []string{"", "f", "foo", "o b", "héllo", "ö", "☃☃", "aa", "one", "zzz", "foo bar foo baz x"}
	for _, content := range contents {
		buf := buffer.NewFromString(content)
		for _, needle := range needles {
			for start := 0; start <= buf.Len(); start++ {
				gotLo, gotHi := lookForward(buf, needle, start)
				wantLo, wantHi := naiveLookForward(buf, needle, start)
				if gotLo != wantLo || gotHi != wantHi {
					t.Fatalf("lookForward(%q, %q, %d) = (%d,%d), want (%d,%d)",
						content, needle, start, gotLo, gotHi, wantLo, wantHi)
				}
			}
		}
	}
}

// Look with the cursor at the top of the file found nothing, whatever was
// under it: the wrap clamp fired on the first pass as well as the second.
// A search that starts at 0 has nothing to wrap around and must simply find
// the first occurrence.
func TestLookForwardFromTheStartOfTheFile(t *testing.T) {
	buf := buffer.NewFromString("foo bar foo")
	if lo, hi := lookForward(buf, "foo", 0); lo != 0 || hi != 3 {
		t.Fatalf("lookForward from 0 = (%d,%d), want (0,3)", lo, hi)
	}
	if lo, hi := lookForward(buf, "bar", 0); lo != 4 || hi != 7 {
		t.Fatalf("lookForward from 0 = (%d,%d), want (4,7)", lo, hi)
	}
}
