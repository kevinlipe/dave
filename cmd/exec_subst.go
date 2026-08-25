package cmd

import (
	"dave/buffer"
	"dave/file"
	dregs "dave/regexp"
	"fmt"
	"strings"
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
	text := f.Buffer().ReadAt(dot.Start, dot.Len())
	repl = samReplacement(repl)

	var lastDot buffer.Range
	matched := false
	rc := runeCounter{s: text}
	for _, m := range re.FindForwardAll(text, 0, len(text)) {
		if nth > 1 {
			nth--
			continue
		}
		idx := re.FindSubmatchIndex(text, m[0], len(text))
		if idx == nil || idx[0] != m[0] {
			continue
		}
		expanded := re.ReplaceExpand(text, repl, idx)
		mLo, mHi := rc.at(m[0]), rc.at(m[1])
		pos := dot.Start + mLo
		if err := ctx.addChange(f, pos, mHi-mLo, expanded); err != nil {
			return err
		}
		lastDot = buffer.Range{Start: pos, End: pos + utf8.RuneCountInString(expanded)}
		matched = true
		if !global {
			break
		}
	}
	if !matched {
		// Sam reports this only at the top level: inside an x or g loop most
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

// samReplacement turns an s command's replacement into a template for Go's
// Expand. Sam's rhs takes \1-\9 for the subexpressions and & for the whole
// match (sam/xec.c:252); every other \X is the literal X, and '$' means
// nothing, so it has to be escaped on the way into a template that reads it.
func samReplacement(repl string) string {
	var b strings.Builder
	for i := 0; i < len(repl); i++ {
		c := repl[i]
		switch {
		case c == '$':
			b.WriteString("$$")
		case c == '&':
			b.WriteString("${0}")
		case c == '\\' && i+1 < len(repl):
			switch n := repl[i+1]; {
			case n >= '1' && n <= '9':
				b.WriteString("${")
				b.WriteByte(n)
				b.WriteString("}")
			case n == 'n':
				b.WriteByte('\n')
			case n == '$':
				b.WriteString("$$")
			default:
				b.WriteByte(n)
			}
			i++
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
