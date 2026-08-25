package cmd

import (
	"bytes"
	"dave/buffer"
	"dave/file"
	"errors"
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

// runShell runs a shell command, returning its standard output when captured.
//
// A non-zero exit is a warning, not a failure. Sam applies whatever the command
// produced and prints `?warning: exit status` (`warn_s(Wbadstatus, retmsg)`,
// sam/shell.c), so a filter that fails partway still hands back what it managed
// — dave used to discard the output and roll the command back. Standard error
// goes to the session's output either way, as it reaches the terminal in sam;
// capturing and dropping it left a failing command with nothing to say.
//
// Only a command that could not be started at all is an error.
func (s *Session) runShell(shellCmd, stdin string, capture, warn bool) (string, error) {
	prog, args := shellCommand()
	c := exec.Command(prog, append(args, shellCmd)...)
	c.Stdin = strings.NewReader(stdin)
	var out bytes.Buffer
	if capture {
		c.Stdout = &out
	} else {
		c.Stdout = s.Output
	}
	c.Stderr = s.Output
	if err := c.Run(); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			return "", err
		}
		// Sam warns for < and | but not for >, which it does not check.
		if warn {
			s.printf("?warning: exit status %d\n", ee.ExitCode())
		}
	}
	return out.String(), nil
}

// replaceDot records a change replacing the addressed range with text and puts
// dot on the result.
func (s *Session) replaceDot(f *file.File, dot buffer.Range, text string, ctx *execCtx) error {
	if err := ctx.addChange(f, dot.Start, dot.Len(), text); err != nil {
		return err
	}
	f.SetDot(buffer.Range{Start: dot.Start, End: dot.Start + utf8.RuneCountInString(text)})
	return nil
}

// bang prints the "!" that marks a finished shell command, at top level only.
func (s *Session) bang(ctx *execCtx) {
	if ctx.nest == 0 {
		s.printf("!\n")
	}
}

func (s *Session) execShellIn(f *file.File, dot buffer.Range, shellCmd string, ctx *execCtx) error {
	out, err := s.runShell(shellCmd, "", true, true)
	if err != nil {
		return fmt.Errorf("< %s: %w", shellCmd, err)
	}
	if err := s.replaceDot(f, dot, out, ctx); err != nil {
		return err
	}
	s.bang(ctx)
	return nil
}

func (s *Session) execShellOut(f *file.File, dot buffer.Range, shellCmd string, ctx *execCtx) error {
	if _, err := s.runShell(shellCmd, f.Buffer().ReadAt(dot.Start, dot.Len()), false, false); err != nil {
		return fmt.Errorf("> %s: %w", shellCmd, err)
	}
	s.bang(ctx)
	return nil
}

func (s *Session) execShellPipe(f *file.File, dot buffer.Range, shellCmd string, ctx *execCtx) error {
	out, err := s.runShell(shellCmd, f.Buffer().ReadAt(dot.Start, dot.Len()), true, true)
	if err != nil {
		return fmt.Errorf("| %s: %w", shellCmd, err)
	}
	if err := s.replaceDot(f, dot, out, ctx); err != nil {
		return err
	}
	s.bang(ctx)
	return nil
}
