package cmd

import (
	"dave/buffer"
	"dave/file"
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
	// Exactly the addressed text, with nothing added. Sam's display writes the
	// range and stops (sam/xec.c), so printing a range that does not end in a
	// newline leaves the cursor mid-line — which is the honest report, and what
	// makes `,x/.../ p` output concatenate the way a script expects.
	s.printf("%s", f.Buffer().ReadAt(dot.Start, dot.Len()))
	f.SetDot(dot)
	return nil
}

// execNewline is sam's nl_cmd with no address: expand dot to whole lines and
// print that; if dot already covers whole lines exactly, print the line after
// it instead. Pressing Enter on an empty command line therefore walks down the
// file, which is how sam pages through one.
//
// The two expansions follow lineaddr's l==0 cases (sam/address.c): backwards
// from dot's start to the beginning of its line, and forwards from one before
// dot's end to just past the next newline — so a dot that already ends right
// after a newline is not extended over the line that follows.
func (s *Session) execNewline(f *file.File, dot buffer.Range) error {
	whole := wholeLines(f, dot)
	if whole == dot {
		whole = lineAfter(f, dot)
	}
	return s.execPrint(f, whole)
}

func wholeLines(f *file.File, dot buffer.Range) buffer.Range {
	buf := f.Buffer()
	start := dot.Start
	for start > 0 && buf.RuneAt(start-1) != '\n' {
		start--
	}
	if dot.End == 0 {
		return buffer.Range{Start: 0, End: 0}
	}
	end := dot.End - 1
	for end < buf.Len() && buf.RuneAt(end) != '\n' {
		end++
	}
	if end < buf.Len() {
		end++ // include the newline the line ends with
	}
	return buffer.Range{Start: start, End: end}
}

// lineAfter is the whole line following dot, empty at end of file.
func lineAfter(f *file.File, dot buffer.Range) buffer.Range {
	buf := f.Buffer()
	start := dot.End
	if start >= buf.Len() {
		return buffer.Range{Start: buf.Len(), End: buf.Len()}
	}
	end := start
	for end < buf.Len() && buf.RuneAt(end) != '\n' {
		end++
	}
	if end < buf.Len() {
		end++
	}
	return buffer.Range{Start: start, End: end}
}

func (s *Session) execEqual(f *file.File, dot buffer.Range, flag string) error {
	// The name as given. 9front's printposn prefixes the working directory when
	// the name is relative; dave does not — see doc/sam-differences.md.
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
	// The line-start table answers both of these without copying the file or
	// scanning it, which matters because = is often the body of an x loop and
	// so runs once per match.
	buf := f.Buffer()
	line1 := buf.LineNumber(dot.Start)
	// For the end, use dot.End-1 so that a selection ending at a line boundary
	// reports the last included line, not the next one.
	endPos := dot.End
	if endPos > dot.Start {
		endPos--
	}
	line2 := buf.LineNumber(endPos)
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
