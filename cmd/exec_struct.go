package cmd

import (
	"dave/buffer"
	"dave/file"
	dregs "dave/regexp"
)

// execGuard implements g (wantMatch=true) and v (wantMatch=false):
// run body iff whether pat matches inside dot equals wantMatch.
func (s *Session) execGuard(f *file.File, dot buffer.Range, pat string, body []*Command, ctx *execCtx, wantMatch bool) error {
	re, err := dregs.Compile(pat)
	if err != nil {
		return err
	}
	text := f.Buffer().ReadAt(dot.Start, dot.Len())
	lo, _ := re.FindForward(text, 0, len(text))
	if (lo != -1) != wantMatch {
		return nil
	}
	f.SetDot(dot)
	ctx.nest++
	defer func() { ctx.nest-- }()
	for _, cmd := range body {
		if err := s.execCmd(cmd, f, ctx); err != nil {
			return err
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
	rc := runeCounter{s: dotText}
	for _, m := range allMatches {
		lo := dot.Start + rc.at(m[0])
		hi := dot.Start + rc.at(m[1])
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

	// Every gap, including the empty ones: before a match at the very start,
	// between two adjacent matches, and after a match at the very end. Sam's
	// looper emits one range per iteration whether or not it is null, so
	// `,y/a/` over "aabb" gives #0, #1 and #2,#4 — three gaps for two matches.
	// Skipping the empty ones loses the position between adjacent matches,
	// which is exactly where a y loop is usually asked to insert something.
	betweens := make([]buffer.Range, 0, len(allMatches)+1)
	prev := 0
	rc := runeCounter{s: dotText}
	for _, m := range allMatches {
		betweens = append(betweens, buffer.Range{
			Start: dot.Start + rc.at(prev),
			End:   dot.Start + rc.at(m[0]),
		})
		prev = m[1]
	}
	betweens = append(betweens, buffer.Range{Start: dot.Start + rc.at(prev), End: dot.End})

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

// execXFile runs the body in every file whose menu line matches. With no
// pattern at all — `X p` — it runs in every file, as sam does.
func (s *Session) execXFile(pat string, all bool, body []*Command, ctx *execCtx) error {
	var re *dregs.Regexp
	if !all {
		var err error
		if re, err = dregs.Compile(pat); err != nil {
			return err
		}
	}
	ctx.nest++
	for _, f := range s.Files {
		ml := f.MenuLine(f == s.Current, false)
		if all || re.MatchString(ml) {
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

// execYFile is execXFile with the match inverted — except with no pattern,
// where sam runs the body in every file for Y as well.
func (s *Session) execYFile(pat string, all bool, body []*Command, ctx *execCtx) error {
	var re *dregs.Regexp
	if !all {
		var err error
		if re, err = dregs.Compile(pat); err != nil {
			return err
		}
	}
	ctx.nest++
	for _, f := range s.Files {
		ml := f.MenuLine(f == s.Current, false)
		if all || !re.MatchString(ml) {
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
// execGroup runs each command in a { } group against the group's address. Dot
// is restored before *every* command, not just the first: sam assigns it inside
// the loop (`a.f->dot = a; cmdexec(a.f, cp)`, sam/xec.c:57), which is what makes
// a group mean "run all of these against this address" rather than "run these in
// sequence, each starting where the last one left off".
func (s *Session) execGroup(f *file.File, dot buffer.Range, body []*Command, ctx *execCtx) error {
	ctx.nest++
	for _, cmd := range body {
		f.SetDot(dot)
		if err := s.execCmd(cmd, f, ctx); err != nil {
			ctx.nest--
			return err
		}
	}
	ctx.nest--
	return nil
}
