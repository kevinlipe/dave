package cmd

import (
	"dave/buffer"
	"dave/file"
	dregs "dave/regexp"
	"fmt"
	"unicode/utf8"
)

// execSubst runs the s command. This follows sam's loop (sam/xec.c s_cmd):
// walk the matches, skip the first n-1, substitute, and stop there unless the
// g flag is set — so `s3/x/y/g` replaces the third match and every one after
// it, not all of them.
func (s *Session) execSubst(f *file.File, dot buffer.Range, pat, repl string, nth int, global bool, ctx *execCtx) error {
	re, err := dregs.Compile(pat)
	if err != nil {
		return err
	}
	buf := f.Buffer()

	// sam's loop (s_cmd): walk the matches, count off the first n-1, and stop
	// after the first substitution unless g was given. Positions are codepoint
	// offsets into the file, and resolve against the buffer as it was before
	// the command — the changes are accumulated and applied when it commits.
	var lastDot buffer.Range
	matched := false
	op := -1
	for p1 := dot.Start; p1 <= dot.End; {
		idx := re.FindSubmatchIndex(buf, p1, dot.End)
		if idx == nil {
			break
		}
		mlo, mhi := idx[0], idx[1]
		if mlo == mhi {
			// An empty match where the last one ended would substitute twice
			// in the same place; step over it instead.
			if mlo == op {
				p1++
				continue
			}
			p1 = mhi + 1
		} else {
			p1 = mhi
		}
		op = mhi
		if nth > 1 {
			nth--
			continue
		}
		expanded := dregs.Expand(buf, repl, idx)
		if err := ctx.addChange(f, mlo, mhi-mlo, expanded); err != nil {
			return err
		}
		lastDot = buffer.Range{Start: mlo, End: mlo + utf8.RuneCountInString(expanded)}
		matched = true
		if !global {
			break
		}
	}
	if !matched {
		// sam reports this only at the top level: inside an x or g loop most
		// iterations are expected not to match, and erroring would abort the
		// loop on the first one that does not (`if(!didsub && nest==0)`).
		if ctx.nest == 0 {
			return fmt.Errorf("s: no match")
		}
		return nil
	}
	f.SetDot(lastDot)
	return nil
}
