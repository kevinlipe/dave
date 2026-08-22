package cmd

import (
	"dave/buffer"
	"dave/file"
	dregs "dave/regexp"
	"fmt"
	"strings"
	"unicode/utf8"
)

func (s *Session) execSubst(f *file.File, dot buffer.Range, pat, repl, flag string, ctx *execCtx) error {
	re, err := dregs.Compile(pat)
	if err != nil {
		return err
	}
	// Get the text within dot.
	text := f.Buffer().ReadAt(dot.Start, dot.Len())
	// Unescape \n etc. in replacement text, then normalize & → ${0}.
	repl = unescapeText(repl)
	repl = strings.ReplaceAll(repl, "&", "${0}")

	if flag == "g" {
		return s.substAll(f, dot, re, repl, text, ctx)
	}
	// Default: first match (or nth if flag is a digit).
	nth := 1
	if len(flag) == 1 && flag[0] >= '2' && flag[0] <= '9' {
		nth = int(flag[0] - '0')
	}
	return s.substNth(f, dot, re, repl, text, nth, ctx)
}

func (s *Session) substNth(f *file.File, dot buffer.Range, re *dregs.Regexp, repl, text string, nth int, ctx *execCtx) error {
	var idx []int
	if nth <= 1 {
		idx = re.FindSubmatchIndex(text, 0, len(text))
	} else {
		// Find nth occurrence.
		all := re.FindForwardAll(text, 0, len(text))
		if nth-1 >= len(all) {
			return nil // No nth match.
		}
		m := all[nth-1]
		idx = re.FindSubmatchIndex(text, m[0], len(text))
	}
	if idx == nil {
		return fmt.Errorf("s: no match")
	}
	expanded := re.ReplaceExpand(text, repl, idx)
	mLo := utf8.RuneCountInString(text[:idx[0]])
	mHi := utf8.RuneCountInString(text[:idx[1]])
	pos := dot.Start + mLo
	del := mHi - mLo
	if err := ctx.addChange(f, pos, del, expanded); err != nil {
		return err
	}
	n := utf8.RuneCountInString(expanded)
	f.SetDot(buffer.Range{Start: pos, End: pos + n})
	return nil
}

func (s *Session) substAll(f *file.File, dot buffer.Range, re *dregs.Regexp, repl, text string, ctx *execCtx) error {
	all := re.FindForwardAll(text, 0, len(text))
	if all == nil {
		return fmt.Errorf("s: no match")
	}
	var lastDot buffer.Range
	matched := false
	for _, m := range all {
		idx := re.FindSubmatchIndex(text, m[0], len(text))
		if idx == nil || idx[0] != m[0] {
			continue
		}
		expanded := re.ReplaceExpand(text, repl, idx)
		mLo := utf8.RuneCountInString(text[:m[0]])
		mHi := utf8.RuneCountInString(text[:m[1]])
		pos := dot.Start + mLo
		del := mHi - mLo
		if err := ctx.addChange(f, pos, del, expanded); err != nil {
			return err
		}
		n := utf8.RuneCountInString(expanded)
		lastDot = buffer.Range{Start: pos, End: pos + n}
		matched = true
	}
	if matched {
		f.SetDot(lastDot)
	}
	return nil
}
