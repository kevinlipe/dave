package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"dave/file"
)

// A differential harness: run the same script through the real sam and through
// dave, and compare what each one leaves behind.
//
// This exists because reading the C and reproducing a case by hand only finds
// what someone thought to look for. Every divergence this project has fixed
// showed up at a composition point — an address to the right of a comma, two
// commands in a group, an alternation in a pattern — and survived because the
// tests exercised the cases a person would think to write. An oracle does not
// have that blind spot.
//
// Two signals, in order of trust:
//
//   - The file each side writes with `w`. Byte-for-byte, no formatting in the
//     way. This is the one that matters.
//   - Their output, after normalising the formatting differences listed in
//     normalise. Weaker, but it is the only way to see what an address
//     resolved to.
//
// # Limits worth knowing before trusting a green run
//
// The oracle is plan9port's sam, because that is what builds and runs on a Unix
// box. It is not the last word: 9front's sam differs from it in small ways —
// `printposn` alone differs in both whether it prints the file name and what it
// prints for a line address — and dave has sided with each tree at different
// times. A diff here is a *candidate*, to be settled against both sources and
// recorded in doc/sam-differences.md, not a verdict.
//
// It reaches the command language and nothing else. Rendering, panes,
// interactive typing and the mouse are all invisible to it.
//
// It is skipped when sam is not installed, so a green `make check` on a machine
// without it means less than it looks like. `make difftest` says so out loud.

// samBin is the oracle. plan9port installs it as `sam`.
const samBin = "sam"

// The names given to the files inside each run's directory: _f1_, _f2_, … for
// the inputs, _out_ for what the script writes. They are ugly on purpose: dave
// prints the file name before a position while plan9port does not, so the
// harness has to strip it, and a name that could occur in the text being edited
// would be stripped out of a `p` that ran into the next line. That is not
// hypothetical — it swallowed a line the first time this ran.
const outName = "_out_"

func inputName(i int) string { return fmt.Sprintf("_f%d_", i+1) }

func TestDifferential(t *testing.T) {
	if _, err := exec.LookPath(samBin); err != nil {
		t.Skipf("no %s on this machine; see make difftest", samBin)
	}
	cases, err := filepath.Glob("testdata/diff/*.sam")
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("no cases in testdata/diff")
	}
	for _, script := range cases {
		name := strings.TrimSuffix(filepath.Base(script), ".sam")
		t.Run(name, func(t *testing.T) {
			runDifferential(t, script)
		})
	}
}

func runDifferential(t *testing.T, scriptPath string) {
	t.Helper()
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	// A case opens one file by default and more when it supplies them: .in,
	// then .in2, .in3 and so on, in that order on the command line. Sam sorts
	// the list by name and takes the first, so _f1_ is the current file
	// whatever order they were given in.
	var inputs [][]byte
	base := strings.TrimSuffix(scriptPath, ".sam")
	for i := 0; ; i++ {
		name := base + ".in"
		if i > 0 {
			name = fmt.Sprintf("%s.in%d", base, i+1)
		}
		b, err := os.ReadFile(name)
		if err != nil {
			if i == 0 {
				t.Fatal(err)
			}
			break
		}
		inputs = append(inputs, b)
	}

	// Each side gets its own directory so their writes cannot collide, and so
	// the paths in their output can be reduced to the same names.
	samDir, daveDir := t.TempDir(), t.TempDir()
	write := func(dir string) []string {
		var paths []string
		for i, in := range inputs {
			p := filepath.Join(dir, inputName(i))
			if err := os.WriteFile(p, in, 0o644); err != nil {
				t.Fatal(err)
			}
			paths = append(paths, p)
		}
		return paths
	}
	samFiles, daveFiles := write(samDir), write(daveDir)

	// Both scripts end by writing the buffer and quitting. The first q reports
	// the modified file and the second one goes through, in both editors.
	//
	// The trailing newline is trimmed first: a blank line is the \n command in
	// both editors now, so leaving one in would have the harness diff a command
	// of its own making rather than one the case asked for.
	//
	// $F1, $F2 … stand for the input paths, which are per-run and absolute, so
	// a case can name a file for b, D, e or r without knowing where it landed.
	build := func(dir string, files []string) string {
		text := strings.TrimRight(string(script), "\n")
		for i := len(files) - 1; i >= 0; i-- {
			text = strings.ReplaceAll(text, fmt.Sprintf("$F%d", i+1), files[i])
		}
		return text + "\nw " + filepath.Join(dir, outName) + "\nq\nq\n"
	}

	samText := runSam(t, samFiles, build(samDir, samFiles))
	daveText := runDave(t, daveFiles, build(daveDir, daveFiles))

	samSaved := readOrEmpty(filepath.Join(samDir, outName))
	daveSaved := readOrEmpty(filepath.Join(daveDir, outName))
	if samSaved != daveSaved {
		t.Errorf("the buffers differ.\nsam  wrote %q\ndave wrote %q", samSaved, daveSaved)
	}

	gotSam := normalise(samText, samDir)
	gotDave := normalise(daveText, daveDir)
	if gotSam != gotDave {
		t.Errorf("the output differs.\nsam:\n%s\ndave:\n%s", indent(gotSam), indent(gotDave))
	}
}

// runSam runs the oracle over a copy of the input, returning everything it
// printed on either stream.
func runSam(t *testing.T, paths []string, script string) string {
	t.Helper()
	c := exec.Command(samBin, append([]string{"-d"}, paths...)...)
	c.Stdin = strings.NewReader(script)
	out, err := c.CombinedOutput()
	var ee *exec.ExitError
	if err != nil && !errors.As(err, &ee) {
		t.Fatalf("running %s: %v", samBin, err)
	}
	return string(out)
}

// runDave runs the same script through dave's engine. This is `dave -d`
// (main.go runREPL) reproduced in process, so the harness does not need the
// binary — and therefore does not need SDL, keeping the engine's test suite
// buildable without it.
func runDave(t *testing.T, paths []string, script string) string {
	t.Helper()
	var out strings.Builder
	s := NewSession(&out)
	for _, p := range paths {
		s.AddFile(file.NewNamed(p))
	}
	// main.go's -d startup: the first file in the list — which AddFile keeps in
	// name order — is current, and it is loaded and reported before the first
	// command runs.
	s.Current = s.Files[0]
	if err := s.Current.EnsureLoaded(); err != nil {
		t.Fatal(err)
	}
	s.AnnounceLoad(s.Current)

	scanner := bufio.NewScanner(strings.NewReader(script))
	var accum string
	var state InputState
	for scanner.Scan() {
		line := scanner.Text()

		accum += line + "\n"
		if !state.Feed(line) {
			continue
		}
		if err := s.ExecString(accum); err != nil {
			if errors.Is(err, ErrQuit) {
				break
			}
			// -d writes these to stderr; the harness reads one stream.
			out.WriteString("? " + err.Error() + "\n")
		}
		accum = ""
		state.Reset()
	}
	return out.String()
}

var (
	// The file name dave prints before a position. plan9port's printposn prints
	// none; 9front's does, and dave follows 9front, so the harness takes it off
	// both sides rather than picking a winner. Anchored to the exact name, not
	// to "anything before a colon": p prints without a trailing newline, so the
	// text ahead of a position report is arbitrary.
	posnName = regexp.MustCompile(`_f\d+_:(#|\d)`)
)

// normalise strips the formatting the two editors are allowed to differ in, so
// what is left is what they are meant to agree about. Every rule here is a
// difference this project has looked at and decided not to arbitrate; a rule
// added carelessly is a divergence hidden rather than found.
func normalise(text, dir string) string {
	// Absolute paths are per-run, so reduce them to the bare names both sides
	// see: "f" for the file, "out" for what w wrote.
	text = strings.ReplaceAll(text, dir+string(filepath.Separator), "")
	text = strings.ReplaceAll(text, dir, "")

	var keep []string
	for _, line := range strings.Split(text, "\n") {
		switch {
		case line == "":
			continue
		// The complaint about unwritten changes on the first q, worded
		// differently by each.
		case strings.Contains(line, "changed files"):
			continue
		// Errors and warnings: that one happened is the signal, not its text.
		// Neither editor promises the other's wording.
		case strings.HasPrefix(line, "?"):
			keep = append(keep, "?")
			continue
		}
		line = posnName.ReplaceAllString(line, "$1")
		// Dave's w notes a file it had to create; sam does not.
		line = strings.Replace(line, ": (new file) ", ": ", 1)
		keep = append(keep, line)
	}
	return strings.Join(keep, "\n")
}

func readOrEmpty(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

func indent(s string) string {
	if s == "" {
		return "    <nothing>"
	}
	return "    " + strings.ReplaceAll(s, "\n", "\n    ")
}
