// Package regexp implements sam's regular expressions, ported from
// sam/regexp.c, with one addition of dave's own:
//
//   - '@' matches any character including newline. sam has no such operator
//     and reads '@' as an ordinary character, so a pattern written for sam
//     means here exactly what it means there.
//   - '\n' in a pattern matches a newline; every other '\X' is the literal X,
//     and braces are ordinary characters. That is sam's whole escape rule.
//   - An empty pattern reuses the last one compiled.
//
// Matching is leftmost-longest going forward, and going backward it takes the
// match ending last and the longest among those — which is what running the
// machine right to left gives you, and what no forward-only engine can say.
//
// Searches run over an Input: any rune at a codepoint position. There are no
// byte offsets anywhere in this package's interface.
package regexp

import "fmt"

// last is the package-level most-recently-compiled pattern.
var last *Regexp

// cache holds compiled patterns keyed by the original sam pattern string.
//
// Structural commands recompile constantly: an x loop compiles its own
// pattern once but re-runs its body per match, so a body containing s/// or
// g// pays for a fresh compile on every iteration. The pattern text comes
// from the command itself, so the set of distinct patterns in play is
// bounded by the size of the script and is typically a handful.
//
// That access pattern — a tiny working set, hit over and over — is why
// this is a plain bounded map rather than an LRU: at these sizes the
// eviction bookkeeping would cost more than it saves. When the map fills it
// is dropped whole, which costs one recompile per pattern still in use and
// cannot happen partway through a command, whose patterns are fixed.
const maxCached = 256

var cache = map[string]*Regexp{}

// Regexp is a compiled pattern, in both directions. The backward program is
// the same source compiled with the operands of every concatenation swapped;
// building it eagerly costs a second small compile and means a backward search
// never pays for one.
type Regexp struct {
	pattern  string
	fwd, bwd *prog
}

// Compile compiles pattern with sam extensions.
// An empty pattern ("") reuses the last compiled pattern.
// Returns an error if the pattern is invalid.
func Compile(pattern string) (*Regexp, error) {
	if pattern == "" {
		if last == nil {
			return nil, fmt.Errorf("regexp: no previous pattern")
		}
		return last, nil
	}
	if r, ok := cache[pattern]; ok {
		last = r // a cache hit is still a use, so // reuse must see it
		return r, nil
	}
	fwd, err := compileProg(pattern, false)
	if err != nil {
		return nil, fmt.Errorf("regexp: %w", err)
	}
	bwd, err := compileProg(pattern, true)
	if err != nil {
		return nil, fmt.Errorf("regexp: %w", err)
	}
	r := &Regexp{pattern: pattern, fwd: fwd, bwd: bwd}
	if len(cache) >= maxCached {
		clear(cache)
	}
	cache[pattern] = r
	last = r
	return r, nil
}

// MustCompile is like Compile but panics on error.
func MustCompile(pattern string) *Regexp {
	r, err := Compile(pattern)
	if err != nil {
		panic(err)
	}
	return r
}

// Pattern returns the original sam pattern string.
func (r *Regexp) Pattern() string { return r.pattern }

// FindForward returns the leftmost-longest match in in[start:end], as
// codepoint offsets, or (-1, -1) if there is none. It does not wrap; the
// caller that wants sam's wrap-around search does it where it can be seen.
func (r *Regexp) FindForward(in Input, start, end int) (lo, hi int) {
	se, ok := r.fwd.execute(in, start, end)
	if !ok {
		return -1, -1
	}
	return se[0].p1, se[0].p2
}

// FindBackward returns the match in in[start:end] that ends last, and the
// longest of those, as codepoint offsets, or (-1, -1) if there is none.
func (r *Regexp) FindBackward(in Input, start, end int) (lo, hi int) {
	se, ok := r.bwd.bexecute(in, start, end)
	if !ok {
		return -1, -1
	}
	return se[0].p1, se[0].p2
}

// FindSubmatchIndex returns the match and its capture groups as codepoint
// offset pairs — [0] and [1] are the whole match — or nil if there is none.
// A group that did not participate is reported as (-1, -1).
//
// sam records nine groups and no more; a pattern may contain more, and they
// match, they are simply not reported.
func (r *Regexp) FindSubmatchIndex(in Input, start, end int) []int {
	se, ok := r.fwd.execute(in, start, end)
	if !ok {
		return nil
	}
	idx := make([]int, 0, 2*nsubexp)
	for _, g := range se {
		idx = append(idx, g.p1, g.p2)
	}
	return idx
}

// MatchString reports whether r matches anywhere in s. It is for the callers
// that match against one line — a file's menu entry — rather than a file.
func (r *Regexp) MatchString(s string) bool {
	in := NewRunes(s)
	lo, _ := r.FindForward(in, 0, in.Len())
	return lo >= 0
}

// Expand builds the replacement text for one match of an s command.
//
// sam\'s rules, from s_cmd: `&` is the whole match, `\1` to `\9` are the
// capture groups, and any other `\X` is the literal X. A `\n` reaches here as
// a newline already, resolved by the command parser the way sam\'s getrhs
// resolves it.
func Expand(in Input, repl string, idx []int) string {
	var b []rune
	rs := []rune(repl)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case c == '&':
			b = append(b, groupText(in, idx, 0)...)
		case c == '\\' && i+1 < len(rs):
			i++
			n := rs[i]
			if n >= '1' && n <= '9' {
				b = append(b, groupText(in, idx, int(n-'0'))...)
			} else if n == 'n' {
				b = append(b, '\n')
			} else {
				b = append(b, n)
			}
		default:
			b = append(b, c)
		}
	}
	return string(b)
}

func groupText(in Input, idx []int, n int) []rune {
	if 2*n+1 >= len(idx) {
		return nil
	}
	lo, hi := idx[2*n], idx[2*n+1]
	if lo < 0 || hi < lo {
		return nil
	}
	out := make([]rune, 0, hi-lo)
	for p := lo; p < hi; p++ {
		out = append(out, in.RuneAt(p))
	}
	return out
}

// Text returns in[lo:hi] as a string.
func Text(in Input, lo, hi int) string {
	if lo < 0 || hi < lo {
		return ""
	}
	out := make([]rune, 0, hi-lo)
	for p := lo; p < hi; p++ {
		out = append(out, in.RuneAt(p))
	}
	return string(out)
}

// ResetLastPattern clears the last-used pattern (for testing).
func ResetLastPattern() { last = nil }
