package cmd

import (
	"dave/addr"
	"dave/buffer"
	"dave/file"
	"fmt"
	"unicode/utf8"
)

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
