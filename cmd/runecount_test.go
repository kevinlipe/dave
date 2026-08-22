package cmd

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"
)

// runeCounter replaced repeated utf8.RuneCountInString(text[:off]) calls in
// the structural commands. It is only faster when callers move forward, but
// it must be correct for any sequence of offsets, so both are tested.

func TestRuneCounterMatchesFullCount(t *testing.T) {
	texts := []string{
		"",
		"plain ascii text",
		"café naïve",
		"héllo wörld — ☃ 日本語",
		"\n\n\n",
		"a\nb\nc",
		strings.Repeat("αβγ\n", 40),
	}
	for _, s := range texts {
		// Every byte offset that begins a rune, in order.
		var offs []int
		for i := range s {
			offs = append(offs, i)
		}
		offs = append(offs, len(s))

		rc := runeCounter{s: s}
		for _, off := range offs {
			if got, want := rc.at(off), utf8.RuneCountInString(s[:off]); got != want {
				t.Fatalf("%q: forward at(%d) = %d, want %d", s, off, got, want)
			}
		}

		// Backwards: correct, just not incremental.
		rc = runeCounter{s: s}
		for i := len(offs) - 1; i >= 0; i-- {
			off := offs[i]
			if got, want := rc.at(off), utf8.RuneCountInString(s[:off]); got != want {
				t.Fatalf("%q: backward at(%d) = %d, want %d", s, off, got, want)
			}
		}

		// Random access, including repeats of the same offset.
		rng := rand.New(rand.NewSource(11))
		rc = runeCounter{s: s}
		for i := 0; i < 200; i++ {
			off := offs[rng.Intn(len(offs))]
			if got, want := rc.at(off), utf8.RuneCountInString(s[:off]); got != want {
				t.Fatalf("%q: random at(%d) = %d, want %d", s, off, got, want)
			}
		}
	}
}

// ---- structural commands over multibyte text --------------------------------
//
// The converted call sites all turn a match's byte offsets into codepoint
// offsets. On pure ASCII the two are equal, so a broken conversion would
// still pass; these exercise text where they differ.

func TestXOverMultibyteText(t *testing.T) {
	// Each ö is two bytes, so a match's byte offset runs ahead of its
	// codepoint offset, and further ahead with every ö passed.
	r := runCmd(t, "öne twö thrée föur", dot(0, 18), `x/ö/ c/O/`)
	if r.err != nil {
		t.Fatalf("x over multibyte: %v", r.err)
	}
	if want := "One twO thrée fOur"; r.contents != want {
		t.Errorf("contents = %q, want %q", r.contents, want)
	}
}

func TestSubstGlobalOverMultibyteText(t *testing.T) {
	r := runCmd(t, "café café café", dot(0, 14), `s/café/tea/g`)
	if r.err != nil {
		t.Fatalf("s///g over multibyte: %v", r.err)
	}
	if r.contents != "tea tea tea" {
		t.Errorf("contents = %q, want %q", r.contents, "tea tea tea")
	}
}

func TestSubstNthOverMultibyteText(t *testing.T) {
	r := runCmd(t, "☃ a ☃ a ☃ a", dot(0, 11), `s2/a/Z/`)
	if r.err != nil {
		t.Fatalf("s2 over multibyte: %v", r.err)
	}
	if r.contents != "☃ a ☃ Z ☃ a" {
		t.Errorf("contents = %q, want %q", r.contents, "☃ a ☃ Z ☃ a")
	}
}

func TestYOverMultibyteText(t *testing.T) {
	// y runs the body on the text BETWEEN matches.
	r := runCmd(t, "é1é22é333é", dot(0, 10), `y/é/ c/-/`)
	if r.err != nil {
		t.Fatalf("y over multibyte: %v", r.err)
	}
	if r.contents != "é-é-é-é" {
		t.Errorf("contents = %q, want %q", r.contents, "é-é-é-é")
	}
}

// A structural command whose body is another structural command: the inner
// conversions run against a dot that is itself a match of the outer one.
func TestNestedStructuralOverMultibyteText(t *testing.T) {
	r := runCmd(t, "ötto\nötto\n", dot(0, 10), `x/ö[a-z]+/ x/t/ c/T/`)
	if r.err != nil {
		t.Fatalf("nested x over multibyte: %v", r.err)
	}
	if r.contents != "öTTo\nöTTo\n" {
		t.Errorf("contents = %q, want %q", r.contents, "öTTo\nöTTo\n")
	}
}

// = reports line numbers; it now reads them from the buffer's line table
// instead of copying and scanning the file.
func TestEqualsLineNumbersOverMultibyteText(t *testing.T) {
	const content = "héllo\nwörld\n☃ third\nfourth\n"
	for _, c := range []struct {
		dotStr string
		want   string
	}{
		// A line address selects the whole line, which is not a null dot,
		// so = reports it as a range.
		{"1", "1,1\n"},
		{"2", "2,2\n"},
		{"3", "3,3\n"},
		{"1,3", "1,3\n"},
		// $ is null at end of file; the trailing newline starts a 5th line.
		{"$", "5\n"},
	} {
		r := runCmd(t, content, dot(0, 0), c.dotStr+"=")
		if r.err != nil {
			t.Fatalf("%s=: %v", c.dotStr, r.err)
		}
		if r.output != c.want {
			t.Errorf("%s= printed %q, want %q", c.dotStr, r.output, c.want)
		}
	}
}

// = as the body of an x loop, which is what made it hot in the first place.
func TestEqualsAsLoopBody(t *testing.T) {
	r := runCmd(t, "a\nb\nc\n", dot(0, 6), `x/[abc]/ =`)
	if r.err != nil {
		t.Fatalf("x with = body: %v", r.err)
	}
	if want := "1,1\n2,2\n3,3\n"; r.output != want {
		t.Errorf("output = %q, want %q", r.output, want)
	}
}

func TestEqualsCharFlagUnaffected(t *testing.T) {
	r := runCmd(t, "héllo wörld", dot(2, 7), "=#")
	if r.err != nil {
		t.Fatalf("=#: %v", r.err)
	}
	if want := "#2,#7\n"; r.output != want {
		t.Errorf("=# printed %q, want %q", r.output, want)
	}
}

// A large-ish input with many matches: the point of the change is that this
// stays linear, but it also has to stay correct.
func TestManyMatchesStayCorrect(t *testing.T) {
	var sb strings.Builder
	const n = 500
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "ünit %d\n", i)
	}
	content := sb.String()
	r := runCmd(t, content, dot(0, len([]rune(content))), `x/ünit/ c/ITEM/`)
	if r.err != nil {
		t.Fatalf("many matches: %v", r.err)
	}
	if got := strings.Count(r.contents, "ITEM"); got != n {
		t.Errorf("replaced %d occurrences, want %d", got, n)
	}
	if strings.Contains(r.contents, "ünit") {
		t.Error("some occurrences were left unreplaced")
	}
}
