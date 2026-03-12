package cmd

import (
	"dave/addr"
	"dave/buffer"
	"dave/file"
	"fmt"
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
		return s.execShellOut(f, dot, cmd.Args[0], ctx)
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
