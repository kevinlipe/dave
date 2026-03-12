package cmd

import (
	"dave/buffer"
	"dave/file"
	"strings"
	"unicode/utf8"
)

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
	if name := f.Name(); name != "" {
		s.printf("%s:", name)
	}
	if flag == "#" {
		if dot.IsNull() {
			s.printf("#%d\n", dot.Start)
		} else {
			s.printf("#%d,#%d\n", dot.Start, dot.End)
		}
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

