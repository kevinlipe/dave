package cmd

import (
	"dave/buffer"
	"dave/file"
	dregs "dave/regexp"
	"unicode/utf8"
)

func (s *Session) execG(f *file.File, dot buffer.Range, pat string, body []*Command, ctx *execCtx) error {
	re, err := dregs.Compile(pat)
	if err != nil {
		return err
	}
	text := f.Buffer().ReadAt(dot.Start, dot.Len())
	lo, _ := re.FindForward(text, 0, len(text))
	if lo != -1 {
		f.SetDot(dot)
		ctx.nest++
		for _, cmd := range body {
			if err := s.execCmd(cmd, f, ctx); err != nil {
				ctx.nest--
				return err
			}
		}
		ctx.nest--
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
		ctx.nest++
		for _, cmd := range body {
			if err := s.execCmd(cmd, f, ctx); err != nil {
				ctx.nest--
				return err
			}
		}
		ctx.nest--
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

	ctx.nest++
	for _, m := range matches {
		f.SetDot(m.r)
		for _, cmd := range body {
			if err := s.execCmd(cmd, f, ctx); err != nil {
				ctx.nest--
				return err
			}
		}
	}
	ctx.nest--
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

	ctx.nest++
	for _, r := range betweens {
		f.SetDot(r)
		for _, cmd := range body {
			if err := s.execCmd(cmd, f, ctx); err != nil {
				ctx.nest--
				return err
			}
		}
	}
	ctx.nest--
	return nil
}

func (s *Session) execXFile(pat string, body []*Command, ctx *execCtx) error {
	re, err := dregs.Compile(pat)
	if err != nil {
		return err
	}
	ctx.nest++
	for _, f := range s.Files {
		ml := f.MenuLine(f == s.Current, false)
		if re.MatchString(ml) {
			s.Current = f
			for _, cmd := range body {
				if err := s.execCmd(cmd, f, ctx); err != nil {
					ctx.nest--
					return err
				}
			}
		}
	}
	ctx.nest--
	return nil
}

func (s *Session) execYFile(pat string, body []*Command, ctx *execCtx) error {
	re, err := dregs.Compile(pat)
	if err != nil {
		return err
	}
	ctx.nest++
	for _, f := range s.Files {
		ml := f.MenuLine(f == s.Current, false)
		if !re.MatchString(ml) {
			s.Current = f
			for _, cmd := range body {
				if err := s.execCmd(cmd, f, ctx); err != nil {
					ctx.nest--
					return err
				}
			}
		}
	}
	ctx.nest--
	return nil
}

// execGroup runs body commands sequentially, each starting with dot=groupDot.
// The buffer isn't updated between commands (two-pass), but dot is updated
// by each command for the next one to see.
func (s *Session) execGroup(f *file.File, dot buffer.Range, body []*Command, ctx *execCtx) error {
	f.SetDot(dot)
	ctx.nest++
	for _, cmd := range body {
		if err := s.execCmd(cmd, f, ctx); err != nil {
			ctx.nest--
			return err
		}
	}
	ctx.nest--
	return nil
}
