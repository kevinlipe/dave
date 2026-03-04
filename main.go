package main

import (
	"bufio"
	"dave/cmd"
	"dave/file"
	"errors"
	"fmt"
	"os"
)

func main() {
	args := os.Args[1:]

	headless := false
	files := []string{}

	for i := 0; i < len(args); i++ {
		if args[i] == "-d" {
			headless = true
		} else {
			files = append(files, args[i])
		}
	}

	s := cmd.NewSession(os.Stdout)

	// Load files into the session (lazy — not read from disk yet).
	for _, name := range files {
		f := file.NewNamed(name)
		s.AddFile(f)
	}

	if headless {
		runREPL(s)
		return
	}

	// Phase 2: SDL GUI. Not yet implemented.
	fmt.Fprintln(os.Stderr, "dave: GUI mode not yet implemented; use -d for headless mode")
	os.Exit(1)
}

// runREPL runs the interactive terminal session.
//
// Input is accumulated until the InputState reports a balanced command, then
// executed as a unit. This handles multi-line constructs:
//
//	{ cmd1        (brace opened — accumulate)
//	  cmd2        (still open)
//	}             (closed — execute all three lines)
//
//	a             (ed-style text — accumulate)
//	line 1        (text lines)
//	.             (terminator — execute)
func runREPL(s *cmd.Session) {
	scanner := bufio.NewScanner(os.Stdin)
	var accum string
	var state cmd.InputState

	for {
		if !scanner.Scan() {
			// EOF: execute whatever is pending.
			if accum != "" {
				execAccum(s, accum)
			}
			break
		}
		line := scanner.Text()
		if state.IsReady() && line == "" {
			continue // skip blank lines between top-level commands
		}
		accum += line + "\n"
		complete := state.Feed(line)
		if !complete {
			continue // need more input
		}
		if quit := execAccum(s, accum); quit {
			return
		}
		accum = ""
		state.Reset()
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "dave: %v\n", err)
	}
}

// execAccum executes the accumulated input and returns true if the session
// should quit.
func execAccum(s *cmd.Session, input string) (quit bool) {
	err := s.ExecString(input)
	if err != nil {
		if errors.Is(err, cmd.ErrQuit) {
			return true
		}
		fmt.Fprintf(os.Stderr, "? %v\n", err)
	}
	return false
}
