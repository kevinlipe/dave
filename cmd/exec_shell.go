package cmd

import (
	"bytes"
	"dave/buffer"
	"dave/file"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"unicode/utf8"
)

// shellCommand returns the shell and args for running a shell command.
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
	if ctx.nest == 0 {
		s.printf("!\n")
	}
	return nil
}

func (s *Session) execShellOut(f *file.File, dot buffer.Range, shellCmd string, ctx *execCtx) error {
	text := f.Buffer().ReadAt(dot.Start, dot.Len())
	prog, args := shellCommand()
	cmd := exec.Command(prog, append(args, shellCmd)...)
	cmd.Stdin = strings.NewReader(text)
	cmd.Stdout = s.Output
	cmd.Stderr = s.Output
	if err := cmd.Run(); err != nil {
		return err
	}
	if ctx.nest == 0 {
		s.printf("!\n")
	}
	return nil
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
	if ctx.nest == 0 {
		s.printf("!\n")
	}
	return nil
}
