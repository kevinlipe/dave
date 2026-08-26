package main

import (
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
