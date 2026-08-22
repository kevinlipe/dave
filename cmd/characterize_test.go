package cmd

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dave/addr"
	"dave/buffer"
	"dave/file"
)

// Characterization test for the command parser and address evaluator.
//
// This exists to make behaviour-preserving refactors verifiable. It records
// what a corpus of command strings currently does -- how each parses, what
// its address evaluates to, and what executing it produces -- into a golden
// file. A refactor that changes any of that shows up as a diff.
//
// Everything recorded is deliberately independent of how commands and
// addresses are represented internally: addresses are recorded by the range
// they evaluate to, never by their node types. Otherwise a change to the
// representation could force an update to the golden file, and the golden
// file would stop being evidence of anything.
//
// Regenerate with: go test ./cmd/ -run Characterize -update

var update = flag.Bool("update", false, "rewrite the characterization golden file")

const characterizeFile = "one two three\nfour five six\nséven eight\nnine\n"

// corpus covers every command letter the parser accepts, every address form,
// and the error paths for both.
var corpus = []string{
	// --- addresses, with an implied print -------------------------------
	"", " ", "\n",
	"0", "1", "2", "4", "5", "$", ".", "'",
	"#0", "#4", "#99",
	"/two/", "?five?", `/s.ven/`,
	",", ";", "1,2", "1;2", "0,$", ".,$", "1,", ",3",
	".+1", ".-1", "1+1", "$-1", ".+", ".-", "+", "-", "+-", "-+",
	"/two/+1", "2-/two/",
	"-1", "1,2,3",
	// regexp as the right operand of + and -, both search directions, since
	// evalPlus and evalMinus override the direction the parser recorded
	"+/two/", "-/two/", "+?two?", "-?two?",
	// all four operator/delimiter combinations against a pattern that occurs
	// on both sides of dot, so each lands somewhere different
	".+/o/", ".+?o?", ".-/o/", ".-?o?", "1+/o/", "$-?o?",
	"/two/+/five/", "?five?-?two?",
	// nested and repeated composition
	"1+1+1", "$-1-1", ".+1-1", "1,2+1", "1;.+1",
	"#0+1", "#4-#2", "0;/two/",
	// file patterns
	`"x"`, `"x"1`,

	// --- text commands ---------------------------------------------------
	"a/added/", "i/ins/", "c/chg/",
	"1a/added/", "$a/end/", "0i/front/",
	"a/multi\\nline/", "c/with\\/slash/",
	"a/unterminated",
	"a\nline one\nline two\n.\n", // ed-style
	"a\n.\n",

	// --- simple commands --------------------------------------------------
	"d", "1d", "1,2d", "p", "1p", ",p", "k", "n", "=", "=#", "1,2=", "1,2=#",

	// --- substitution -----------------------------------------------------
	"s/two/2/", ",s/e/E/", ",s/e/E/g", "s2/e/E/", "s9/e/E/",
	"s/e/E", "s/e", "s", "s/", `s/\/x/Y/`, "s/(t)(w)/\\2\\1/",
	"s/two/&&/", ",s/[aeiou]/./g",

	// --- structural -------------------------------------------------------
	"x/two/ p", "x/two/p", ",x/o/ d", ",y/o/ p", ",g/two/ p", ",g/zzz/ p",
	",v/two/ p", "x p", "x/o/", ",x/o/ c/0/",
	",x/o/ {p\nd\n}", "{p}", "{}", "{p\np}", "{p",
	"X/./ p", "Y/./ p", "X p",
	",x/o/ x/o/ c/Q/",

	// --- misc -------------------------------------------------------------
	"u", "u2", "u9", "q",
	"m$", "1t$", "1m2", "m", "t",
	"b file.txt", "B file.txt", "D", "D file.txt",
	"e file.txt", "r file.txt", "f newname", "w", "w out.txt",
	"< echo hi", "> cat", "| tr a-z A-Z",

	// --- errors and oddities ----------------------------------------------
	"Z", "1Z", "!", "}",
	"p p", "1p\n2p", "1p;2p",
	"   1p   ", "\n\n1p\n\n",
}

func dumpCommand(c *Command, depth int) string {
	if c == nil {
		return strings.Repeat("  ", depth) + "<nil>\n"
	}
	ind := strings.Repeat("  ", depth)
	var sb strings.Builder
	fmt.Fprintf(&sb, "%sop=%q addr=%v dest=%v", ind, string(c.Op), c.Addr != nil, c.Dest != nil)
	if len(c.Args) > 0 {
		fmt.Fprintf(&sb, " args=%q", c.Args)
	}
	if c.Pat != "" {
		fmt.Fprintf(&sb, " pat=%q", c.Pat)
	}
	if c.Pat2 != "" {
		fmt.Fprintf(&sb, " pat2=%q", c.Pat2)
	}
	if c.Flag != "" {
		fmt.Fprintf(&sb, " flag=%q", c.Flag)
	}
	if c.N != 0 {
		fmt.Fprintf(&sb, " n=%d", c.N)
	}
	sb.WriteByte('\n')
	for _, b := range c.Body {
		sb.WriteString(dumpCommand(b, depth+1))
	}
	return sb.String()
}

// dumpAddrEval records an address by what it selects, not by how it is built.
func dumpAddrEval(a *addr.Addr) string {
	if a == nil {
		return "none"
	}
	f := file.New()
	f.Buffer().Insert(0, characterizeFile)
	f.SetDot(buffer.Range{Start: 4, End: 7})
	r, err := addr.Eval(a, f, []*file.File{f})
	if err != nil {
		return "error: " + err.Error()
	}
	return fmt.Sprintf("[%d,%d)", r.Start, r.End)
}

// execSafe reports whether running this command in-process is side-effect
// free. File and shell commands are still parsed and their addresses still
// evaluated; only their execution is skipped.
func execSafe(s string) bool {
	for _, c := range []string{"e ", "r ", "f ", "w", "b ", "B ", "D", "<", ">", "|", "q"} {
		if strings.HasPrefix(strings.TrimSpace(s), c) {
			return false
		}
	}
	return true
}

func characterize() string {
	var sb strings.Builder
	for _, input := range corpus {
		fmt.Fprintf(&sb, "=== %q\n", input)

		cmds, err := ParseCommands(input)
		if err != nil {
			fmt.Fprintf(&sb, "parse error: %v\n\n", err)
			continue
		}
		if len(cmds) == 0 {
			fmt.Fprintf(&sb, "parse: (no commands)\n\n")
			continue
		}
		for _, c := range cmds {
			sb.WriteString(dumpCommand(c, 1))
			fmt.Fprintf(&sb, "  addr-> %s\n", dumpAddrEval(c.Addr))
			if c.Dest != nil {
				fmt.Fprintf(&sb, "  dest-> %s\n", dumpAddrEval(c.Dest))
			}
		}

		if execSafe(input) {
			var out strings.Builder
			s := NewSession(&out)
			f := file.New()
			f.Buffer().Insert(0, characterizeFile)
			f.SetDot(buffer.Range{Start: 4, End: 7})
			s.AddFile(f)
			execErr := s.ExecString(input)
			fmt.Fprintf(&sb, "exec: contents=%q dot=[%d,%d) out=%q err=%v\n",
				f.String(), f.Dot().Start, f.Dot().End, out.String(), execErr)
		} else {
			fmt.Fprintf(&sb, "exec: (skipped, has side effects)\n")
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

func TestCharacterize(t *testing.T) {
	got := characterize()
	golden := filepath.Join("testdata", "characterize.golden")

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
		t.Fatalf("%v (run: go test ./cmd/ -run Characterize -update)", err)
	}
	if got == string(want) {
		return
	}

	// Report the first differing stanza rather than the whole file.
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
			lo := max(0, i-8)
			t.Fatalf("behaviour changed at line %d\n"+
				"context:\n%s\n\n  got: %s\n want: %s",
				i+1, strings.Join(wantLines[lo:i], "\n"), g, w)
		}
	}
}
