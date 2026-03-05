package cmd

import (
	"bytes"
	"dave/addr"
	"dave/buffer"
	"dave/file"
	dregs "dave/regexp"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"unicode/utf8"
)

// Exec executes a single parsed command in the session.
func (s *Session) Exec(cmd *Command) error {
	ctx := newExecCtx(s)
	err := s.execCmd(cmd, s.Current, ctx)
	if err != nil {
		ctx.rollback()
		return err
	}
	return ctx.commit()
}

// ExecString parses and executes one or more commands from a string.
func (s *Session) ExecString(input string) error {
	cmds, err := ParseCommands(input)
	if err != nil {
		return err
	}
	for _, cmd := range cmds {
		if err := s.Exec(cmd); err != nil {
			return err
		}
	}
	return nil
}

// execCmd executes cmd on file f within the given transaction context.
func (s *Session) execCmd(cmd *Command, f *file.File, ctx *execCtx) error {
	if f != nil {
		if err := f.EnsureLoaded(); err != nil {
			return err
		}
	}

	switch cmd.Op {
	case 'u':
		if err := ctx.commit(); err != nil {
			return err
		}
		return s.doUndo(cmd.N)
	case 'q':
		if err := ctx.commit(); err != nil {
			return err
		}
		return s.tryQuit(false)
	case 'b':
		return s.execB(cmd.Args[0])
	case 'B':
		return s.execBig(cmd.Args[0])
	case 'D':
		return s.execD(cmd.Args[0])
	case 'n':
		return s.execN()
	case 'X':
		return s.execXFile(cmd.Pat, cmd.Body, ctx)
	case 'Y':
		return s.execYFile(cmd.Pat, cmd.Body, ctx)
	}

	// Commands below require a current file.
	if f == nil {
		return fmt.Errorf("no current file")
	}

	// Evaluate address. Most commands default to dot; 'w' defaults to the
	// whole file (0,$) when no address is given — writing just dot would
	// silently truncate the file to a null selection.
	var dot buffer.Range
	if cmd.Op == 'w' && cmd.Addr == nil {
		dot = buffer.Range{Start: 0, End: f.Len()}
	} else {
		var err error
		dot, err = s.evalAddr(cmd.Addr, f)
		if err != nil {
			return err
		}
	}

	switch cmd.Op {
	case 'd':
		return s.execDelete(f, dot, ctx)
	case 'p':
		return s.execPrint(f, dot)
	case '=':
		return s.execEqual(f, dot, cmd.Flag)
	case 'k':
		f.SetMark()
		return nil
	case 'a':
		return s.execAppend(f, dot, cmd.Args[0], ctx)
	case 'i':
		return s.execInsert(f, dot, cmd.Args[0], ctx)
	case 'c':
		return s.execChange(f, dot, cmd.Args[0], ctx)
	case 's':
		return s.execSubst(f, dot, cmd.Pat, cmd.Pat2, cmd.Flag, ctx)
	case 'g':
		return s.execG(f, dot, cmd.Pat, cmd.Body, ctx)
	case 'v':
		return s.execV(f, dot, cmd.Pat, cmd.Body, ctx)
	case 'x':
		return s.execX(f, dot, cmd.Pat, cmd.Body, ctx)
	case 'y':
		return s.execY(f, dot, cmd.Pat, cmd.Body, ctx)
	case '{':
		return s.execGroup(f, dot, cmd.Body, ctx)
	case 'e':
		return s.execE(f, cmd.Args[0], ctx)
	case 'r':
		return s.execR(f, dot, cmd.Args[0], ctx)
	case 'w':
		return s.execW(f, dot, cmd.Args[0])
	case 'f':
		return s.execF(f, cmd.Args[0])
	case 'm':
		return s.execMove(f, dot, cmd.Dest, ctx)
	case 't':
		return s.execCopy(f, dot, cmd.Dest, ctx)
	case '<':
		return s.execShellIn(f, dot, cmd.Args[0], ctx)
	case '>':
		return s.execShellOut(f, dot, cmd.Args[0])
	case '|':
		return s.execShellPipe(f, dot, cmd.Args[0], ctx)
	}

	return fmt.Errorf("unknown command %q", string(cmd.Op))
}

// evalAddr evaluates an address against f, defaulting to dot if addr is nil.
func (s *Session) evalAddr(a *addr.Addr, f *file.File) (buffer.Range, error) {
	if a == nil {
		return f.Dot(), nil
	}
	return addr.Eval(a, f, s.Files)
}

// ---- Text commands ----------------------------------------------------------

func (s *Session) execDelete(f *file.File, dot buffer.Range, ctx *execCtx) error {
	if err := ctx.addChange(f, dot.Start, dot.Len(), ""); err != nil {
		return err
	}
	f.SetDot(buffer.Range{Start: dot.Start, End: dot.Start})
	return nil
}

func (s *Session) execPrint(f *file.File, dot buffer.Range) error {
	text := f.Buffer().ReadAt(dot.Start, dot.Len())
	s.printf("%s", text)
	if !strings.HasSuffix(text, "\n") {
		s.printf("\n")
	}
	f.SetDot(dot)
	return nil
}

func (s *Session) execEqual(f *file.File, dot buffer.Range, flag string) error {
	if flag == "#" {
		s.printf("#%d,#%d\n", dot.Start, dot.End)
		return nil
	}
	str := f.String()
	line1 := strings.Count(str[:runeToByteOff(str, dot.Start)], "\n") + 1
	// For the end, use dot.End-1 so that a selection ending at a line boundary
	// reports the last included line, not the next one.
	endPos := dot.End
	if endPos > dot.Start {
		endPos--
	}
	line2 := strings.Count(str[:runeToByteOff(str, endPos)], "\n") + 1
	if dot.IsNull() {
		s.printf("%d\n", line1)
	} else {
		s.printf("%d,%d\n", line1, line2)
	}
	return nil
}

// unescapeText converts escape sequences in command text (a/i/c/s replacement).
// \n → newline, \\ → backslash. Other backslash sequences are passed through.
func unescapeText(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				b.WriteByte('\n')
				i++
				continue
			case '\\':
				b.WriteByte('\\')
				i++
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func (s *Session) execAppend(f *file.File, dot buffer.Range, text string, ctx *execCtx) error {
	text = unescapeText(text)
	if err := ctx.addChange(f, dot.End, 0, text); err != nil {
		return err
	}
	n := utf8.RuneCountInString(text)
	f.SetDot(buffer.Range{Start: dot.End, End: dot.End + n})
	return nil
}

func (s *Session) execInsert(f *file.File, dot buffer.Range, text string, ctx *execCtx) error {
	text = unescapeText(text)
	if err := ctx.addChange(f, dot.Start, 0, text); err != nil {
		return err
	}
	n := utf8.RuneCountInString(text)
	f.SetDot(buffer.Range{Start: dot.Start, End: dot.Start + n})
	return nil
}

func (s *Session) execChange(f *file.File, dot buffer.Range, text string, ctx *execCtx) error {
	text = unescapeText(text)
	if err := ctx.addChange(f, dot.Start, dot.Len(), text); err != nil {
		return err
	}
	n := utf8.RuneCountInString(text)
	f.SetDot(buffer.Range{Start: dot.Start, End: dot.Start + n})
	return nil
}

// ---- Substitute -------------------------------------------------------------

func (s *Session) execSubst(f *file.File, dot buffer.Range, pat, repl, flag string, ctx *execCtx) error {
	re, err := dregs.Compile(pat)
	if err != nil {
		return err
	}
	// Get the text within dot.
	text := f.Buffer().ReadAt(dot.Start, dot.Len())
	// Unescape \n etc. in replacement text, then normalize & → ${0}.
	repl = unescapeText(repl)
	repl = strings.ReplaceAll(repl, "&", "${0}")

	if flag == "g" {
		return s.substAll(f, dot, re, repl, text, ctx)
	}
	// Default: first match (or nth if flag is a digit).
	nth := 1
	if len(flag) == 1 && flag[0] >= '2' && flag[0] <= '9' {
		nth = int(flag[0] - '0')
	}
	return s.substNth(f, dot, re, repl, text, nth, ctx)
}

func (s *Session) substNth(f *file.File, dot buffer.Range, re *dregs.Regexp, repl, text string, nth int, ctx *execCtx) error {
	var idx []int
	if nth <= 1 {
		idx = re.FindSubmatchIndex(text, 0, len(text))
	} else {
		// Find nth occurrence.
		all := re.FindForwardAll(text, 0, len(text))
		if nth-1 >= len(all) {
			return nil // No nth match.
		}
		m := all[nth-1]
		idx = re.FindSubmatchIndex(text, m[0], len(text))
	}
	if idx == nil {
		return fmt.Errorf("s: no match")
	}
	expanded := re.ReplaceExpand(text, repl, idx)
	mLo := utf8.RuneCountInString(text[:idx[0]])
	mHi := utf8.RuneCountInString(text[:idx[1]])
	pos := dot.Start + mLo
	del := mHi - mLo
	if err := ctx.addChange(f, pos, del, expanded); err != nil {
		return err
	}
	n := utf8.RuneCountInString(expanded)
	f.SetDot(buffer.Range{Start: pos, End: pos + n})
	return nil
}

func (s *Session) substAll(f *file.File, dot buffer.Range, re *dregs.Regexp, repl, text string, ctx *execCtx) error {
	all := re.FindForwardAll(text, 0, len(text))
	if all == nil {
		return fmt.Errorf("s: no match")
	}
	var lastDot buffer.Range
	for _, m := range all {
		idx := re.FindSubmatchIndex(text, m[0], len(text))
		if idx == nil || idx[0] != m[0] {
			continue
		}
		expanded := re.ReplaceExpand(text, repl, idx)
		mLo := utf8.RuneCountInString(text[:m[0]])
		mHi := utf8.RuneCountInString(text[:m[1]])
		pos := dot.Start + mLo
		del := mHi - mLo
		if err := ctx.addChange(f, pos, del, expanded); err != nil {
			return err
		}
		n := utf8.RuneCountInString(expanded)
		lastDot = buffer.Range{Start: pos, End: pos + n}
	}
	if lastDot.Start > 0 || lastDot.End > 0 {
		f.SetDot(lastDot)
	}
	return nil
}

// ---- Structural commands ----------------------------------------------------

func (s *Session) execG(f *file.File, dot buffer.Range, pat string, body []*Command, ctx *execCtx) error {
	re, err := dregs.Compile(pat)
	if err != nil {
		return err
	}
	text := f.Buffer().ReadAt(dot.Start, dot.Len())
	lo, _ := re.FindForward(text, 0, len(text))
	if lo != -1 {
		f.SetDot(dot)
		for _, cmd := range body {
			if err := s.execCmd(cmd, f, ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Session) execV(f *file.File, dot buffer.Range, pat string, body []*Command, ctx *execCtx) error {
	re, err := dregs.Compile(pat)
	if err != nil {
		return err
	}
	text := f.Buffer().ReadAt(dot.Start, dot.Len())
	lo, _ := re.FindForward(text, 0, len(text))
	if lo == -1 {
		f.SetDot(dot)
		for _, cmd := range body {
			if err := s.execCmd(cmd, f, ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

// execX implements the x command.
//
// All match positions are computed against the ORIGINAL buffer content.
// Null matches adjacent to non-null matches are skipped (Sam paper §4.1).
func (s *Session) execX(f *file.File, dot buffer.Range, pat string, body []*Command, ctx *execCtx) error {
	re, err := dregs.Compile(pat)
	if err != nil {
		return err
	}

	// Snapshot the text at the region of interest (original coords).
	fullStr := f.String()
	dotByteStart := runeToByteOff(fullStr, dot.Start)
	dotByteEnd := runeToByteOff(fullStr, dot.End)
	dotText := fullStr[dotByteStart:dotByteEnd]

	allMatches := re.FindForwardAll(dotText, 0, len(dotText))
	if allMatches == nil {
		return nil
	}

	// Convert to rune-Range values in file space, applying null-match filter.
	type matchRng struct{ r buffer.Range }
	var matches []matchRng
	prevEnd := -1
	for _, m := range allMatches {
		lo := dot.Start + utf8.RuneCountInString(dotText[:m[0]])
		hi := dot.Start + utf8.RuneCountInString(dotText[:m[1]])
		isNull := lo == hi
		// Skip null match if it abuts the end of the previous match.
		if isNull && lo == prevEnd {
			continue
		}
		matches = append(matches, matchRng{buffer.Range{Start: lo, End: hi}})
		prevEnd = hi
	}

	for _, m := range matches {
		f.SetDot(m.r)
		for _, cmd := range body {
			if err := s.execCmd(cmd, f, ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

// execY implements the y command: run body for text BETWEEN matches.
func (s *Session) execY(f *file.File, dot buffer.Range, pat string, body []*Command, ctx *execCtx) error {
	re, err := dregs.Compile(pat)
	if err != nil {
		return err
	}

	fullStr := f.String()
	dotByteStart := runeToByteOff(fullStr, dot.Start)
	dotByteEnd := runeToByteOff(fullStr, dot.End)
	dotText := fullStr[dotByteStart:dotByteEnd]

	allMatches := re.FindForwardAll(dotText, 0, len(dotText))

	var betweens []buffer.Range
	prev := 0
	for _, m := range allMatches {
		if m[0] > prev {
			lo := dot.Start + utf8.RuneCountInString(dotText[:prev])
			hi := dot.Start + utf8.RuneCountInString(dotText[:m[0]])
			betweens = append(betweens, buffer.Range{Start: lo, End: hi})
		}
		prev = m[1]
	}
	if prev < len(dotText) {
		lo := dot.Start + utf8.RuneCountInString(dotText[:prev])
		betweens = append(betweens, buffer.Range{Start: lo, End: dot.End})
	}

	for _, r := range betweens {
		f.SetDot(r)
		for _, cmd := range body {
			if err := s.execCmd(cmd, f, ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Session) execXFile(pat string, body []*Command, ctx *execCtx) error {
	re, err := dregs.Compile(pat)
	if err != nil {
		return err
	}
	for _, f := range s.Files {
		ml := f.MenuLine(f == s.Current)
		if re.MatchString(ml) {
			s.Current = f
			for _, cmd := range body {
				if err := s.execCmd(cmd, f, ctx); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *Session) execYFile(pat string, body []*Command, ctx *execCtx) error {
	re, err := dregs.Compile(pat)
	if err != nil {
		return err
	}
	for _, f := range s.Files {
		ml := f.MenuLine(f == s.Current)
		if !re.MatchString(ml) {
			s.Current = f
			for _, cmd := range body {
				if err := s.execCmd(cmd, f, ctx); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// execGroup runs body commands sequentially, each starting with dot=groupDot.
// The buffer isn't updated between commands (two-pass), but dot is updated
// by each command for the next one to see.
func (s *Session) execGroup(f *file.File, dot buffer.Range, body []*Command, ctx *execCtx) error {
	f.SetDot(dot)
	for _, cmd := range body {
		if err := s.execCmd(cmd, f, ctx); err != nil {
			return err
		}
	}
	return nil
}

// ---- File commands ----------------------------------------------------------

func (s *Session) execN() error {
	for _, f := range s.Files {
		ml := f.MenuLine(f == s.Current)
		s.printf("%s\n", ml)
	}
	return nil
}

func (s *Session) execB(arg string) error {
	names := strings.Fields(arg)
	for _, name := range names {
		f := s.FindFile(name)
		if f != nil {
			s.Current = f
			return nil
		}
	}
	if len(names) > 0 {
		return fmt.Errorf("b: %q not in buffer list", names[0])
	}
	return nil
}

func (s *Session) execBig(arg string) error {
	names := strings.Fields(arg)
	for _, name := range names {
		f := s.FindFile(name)
		if f == nil {
			f = file.NewNamed(name)
			s.AddFile(f)
		}
		s.Current = f
		s.printf("%s\n", f.MenuLine(true))
	}
	return nil
}

func (s *Session) execD(arg string) error {
	names := strings.Fields(arg)
	if len(names) == 0 || (len(names) == 1 && names[0] == "") {
		if s.Current == nil {
			return fmt.Errorf("D: no current file")
		}
		return s.RemoveFile(s.Current, false)
	}
	for _, name := range names {
		f := s.FindFile(name)
		if f == nil {
			return fmt.Errorf("D: %q not in buffer list", name)
		}
		if err := s.RemoveFile(f, false); err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) execE(f *file.File, name string, ctx *execCtx) error {
	name = strings.TrimSpace(name)
	if name == "" {
		name = f.Name()
	}
	if name == "" {
		return fmt.Errorf("e: no filename")
	}
	// Load new content.
	data, err := os.ReadFile(name)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("e: %w", err)
	}
	newContent := f.DecodeContent(string(data))
	oldLen := f.Len()
	// Replace all old content with new content via transcript.
	if oldLen > 0 || newContent != "" {
		del := oldLen
		if err := ctx.addChange(f, 0, del, newContent); err != nil {
			return err
		}
	}
	f.SetName(name)
	f.SetDot(buffer.Range{})
	return nil
}

func (s *Session) execR(f *file.File, dot buffer.Range, name string, ctx *execCtx) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("r: no filename")
	}
	content, err := file.ReadFromPath(name)
	if err != nil {
		return err
	}
	if err := ctx.addChange(f, dot.Start, dot.Len(), content); err != nil {
		return err
	}
	n := utf8.RuneCountInString(content)
	f.SetDot(buffer.Range{Start: dot.Start, End: dot.Start + n})
	return nil
}

func (s *Session) execW(f *file.File, dot buffer.Range, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		name = f.Name()
	}
	if name == "" {
		return fmt.Errorf("w: no filename")
	}
	// Write addressed range, restoring original line endings if needed.
	content := f.EncodeContent(f.Buffer().ReadAt(dot.Start, dot.Len()))
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		return fmt.Errorf("w: %w", err)
	}
	if name == f.Name() {
		f.SetModified(false)
	}
	s.printf("%s\n", name)
	return nil
}

func (s *Session) execF(f *file.File, name string) error {
	name = strings.TrimSpace(name)
	if name != "" {
		f.SetName(name)
	}
	s.printf("%s\n", f.MenuLine(f == s.Current))
	return nil
}

// ---- Move and Copy ----------------------------------------------------------

func (s *Session) execMove(f *file.File, dot buffer.Range, dest *addr.Addr, ctx *execCtx) error {
	destRange, err := addr.Eval(dest, f, s.Files)
	if err != nil {
		return err
	}
	if destRange.Start > dot.Start && destRange.Start < dot.End {
		return fmt.Errorf("m: destination inside source range")
	}
	text := f.Buffer().ReadAt(dot.Start, dot.Len())
	n := utf8.RuneCountInString(text)

	if destRange.End <= dot.Start {
		// Move backward.
		if err := ctx.addChange(f, destRange.End, 0, text); err != nil {
			return err
		}
		if err := ctx.addChange(f, dot.Start, dot.Len(), ""); err != nil {
			return err
		}
		f.SetDot(buffer.Range{Start: destRange.End, End: destRange.End + n})
	} else {
		// Move forward.
		if err := ctx.addChange(f, dot.Start, dot.Len(), ""); err != nil {
			return err
		}
		// After deletion, destination shifts left by dot.Len().
		adj := destRange.End - dot.Len()
		if err := ctx.addChange(f, adj, 0, text); err != nil {
			return err
		}
		f.SetDot(buffer.Range{Start: adj, End: adj + n})
	}
	return nil
}

func (s *Session) execCopy(f *file.File, dot buffer.Range, dest *addr.Addr, ctx *execCtx) error {
	destRange, err := addr.Eval(dest, f, s.Files)
	if err != nil {
		return err
	}
	text := f.Buffer().ReadAt(dot.Start, dot.Len())
	if err := ctx.addChange(f, destRange.End, 0, text); err != nil {
		return err
	}
	n := utf8.RuneCountInString(text)
	f.SetDot(buffer.Range{Start: destRange.End, End: destRange.End + n})
	return nil
}

// ---- Shell commands ---------------------------------------------------------

// shellCommand returns the shell and args for running a shell command.
// Resolved once here; in a real implementation this would be read from config.
func shellCommand() (string, []string) {
	if runtime.GOOS == "windows" {
		return "pwsh", []string{"-Command"}
	}
	return "sh", []string{"-c"}
}

func (s *Session) execShellIn(f *file.File, dot buffer.Range, shellCmd string, ctx *execCtx) error {
	prog, args := shellCommand()
	out, err := exec.Command(prog, append(args, shellCmd)...).Output()
	if err != nil {
		return fmt.Errorf("< %s: %w", shellCmd, err)
	}
	text := string(out)
	if err := ctx.addChange(f, dot.Start, dot.Len(), text); err != nil {
		return err
	}
	n := utf8.RuneCountInString(text)
	f.SetDot(buffer.Range{Start: dot.Start, End: dot.Start + n})
	return nil
}

func (s *Session) execShellOut(f *file.File, dot buffer.Range, shellCmd string) error {
	text := f.Buffer().ReadAt(dot.Start, dot.Len())
	prog, args := shellCommand()
	cmd := exec.Command(prog, append(args, shellCmd)...)
	cmd.Stdin = strings.NewReader(text)
	cmd.Stdout = s.Output
	cmd.Stderr = s.Output
	return cmd.Run()
}

func (s *Session) execShellPipe(f *file.File, dot buffer.Range, shellCmd string, ctx *execCtx) error {
	text := f.Buffer().ReadAt(dot.Start, dot.Len())
	prog, args := shellCommand()
	cmd := exec.Command(prog, append(args, shellCmd)...)
	cmd.Stdin = strings.NewReader(text)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = s.Output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("| %s: %w", shellCmd, err)
	}
	result := out.String()
	if err := ctx.addChange(f, dot.Start, dot.Len(), result); err != nil {
		return err
	}
	n := utf8.RuneCountInString(result)
	f.SetDot(buffer.Range{Start: dot.Start, End: dot.Start + n})
	return nil
}

// ---- Helpers ----------------------------------------------------------------

func runeToByteOff(s string, runeN int) int {
	off := 0
	for i := 0; i < runeN && off < len(s); i++ {
		_, sz := utf8.DecodeRuneInString(s[off:])
		off += sz
	}
	return off
}
