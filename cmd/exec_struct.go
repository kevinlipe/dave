package cmd

import (
	"dave/buffer"
	"dave/file"
	dregs "dave/regexp"
	"unicode/utf8"
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

// execX and execY are sam's one loop, with the x/y flag.
func (s *Session) execX(f *file.File, dot buffer.Range, pat string, body []*Command, ctx *execCtx) error {
	return s.looper(f, dot, pat, body, true, ctx)
}

func (s *Session) execY(f *file.File, dot buffer.Range, pat string, body []*Command, ctx *execCtx) error {
	return s.looper(f, dot, pat, body, false, ctx)
}

// looper runs body over the matches of pat in r (x) or over the text between
// them (y). It is a port of sam's looper (sam/xec.c), not a reimplementation.
//
// The obvious formulation — find every match, then derive the ranges from them
// — is what dave had, and it was wrong at both ends in opposite directions: it
// dropped the empty gap between two adjacent matches, and invented one past the
// last match when that match ended at the end of the range. The null-match skip
// and the gap bookkeeping are one interlocking rule here, not two, and reading
// them off the C is the only way to keep them in step.
//
// Positions resolve against the original text throughout. Dave applies a
// command's changes when it commits, and sam's buffer is likewise untouched
// while the loop runs, so a search never sees a half-edited buffer.
func (s *Session) looper(f *file.File, r buffer.Range, pat string, body []*Command, isX bool, ctx *execCtx) error {
	re, err := dregs.Compile(pat)
	if err != nil {
		return err
	}
	full := f.String()
	text := full[runeToByteOff(full, r.Start):runeToByteOff(full, r.End)]
	rc := runeCounter{s: text}

	// Byte offsets within text; r.Start converts to file coordinates. op is the
	// end of the previous match, and -1 for x so that a null match at the very
	// start is not mistaken for one abutting a previous match.
	op := 0
	if isX {
		op = -1
	}
	setDot := func(lo, hi int) {
		f.SetDot(buffer.Range{Start: r.Start + rc.at(lo), End: r.Start + rc.at(hi)})
	}

	ctx.nest++
	defer func() { ctx.nest-- }()
	for p := 0; p <= len(text); {
		mlo, mhi := re.FindForward(text, p, len(text))
		if mlo < 0 {
			// y still runs once on the text after the last match; x is done.
			if isX || op > len(text) {
				break
			}
			setDot(op, len(text))
			p = len(text) + 1
		} else {
			if mlo == mhi {
				// A null match abutting the previous match is skipped, so a
				// pattern that can match nothing does not stall or double up.
				if mlo == op {
					p = nextRune(text, p)
					continue
				}
				p = nextRune(text, mhi)
			} else {
				p = mhi
			}
			if isX {
				setDot(mlo, mhi)
			} else {
				setDot(op, mlo)
			}
			op = mhi
		}
		for _, cmd := range body {
			if err := s.execCmd(cmd, f, ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

// nextRune returns the byte offset one codepoint past i, and one past the end
// of s when there is nothing left — sam advances by a character here, and a
// byte step would split a multi-byte one.
func nextRune(s string, i int) int {
	if i >= len(s) {
		return i + 1
	}
	_, w := utf8.DecodeRuneInString(s[i:])
	return i + w
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
