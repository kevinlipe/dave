// Package regexp wraps Go's regexp package with sam's pattern syntax plus one
// dave extension:
//   - '@' matches any character including newline (a dave extension; sam has
//     no such operator, and treats '@' as an ordinary character)
//   - '\n' in a pattern matches a literal newline
//   - '?' delimiters for backward search (e.g. ?pat?)
//   - Empty pattern reuses the last compiled pattern
//
// All search functions work on plain Go strings. Positions returned are
// byte offsets into those strings; callers are responsible for converting
// to codepoint offsets if needed.
package regexp

import (
	"fmt"
	"regexp"
	"strings"
)

// last is the package-level most-recently-compiled pattern.
var last *Regexp

// cache holds compiled patterns keyed by the original Sam pattern string.
//
// Structural commands recompile constantly: an x loop compiles its own
// pattern once but re-runs its body per match, so a body containing s/// or
// g// pays for a fresh compile on every iteration. The pattern text comes
// from the command itself, so the set of distinct patterns in play is
// bounded by the size of the script and is typically a handful.
//
// That access pattern -- a tiny working set, hit over and over -- is why
// this is a plain bounded map rather than an LRU: at these sizes the
// eviction bookkeeping would cost more than it saves. When the map fills it
// is dropped whole, which costs one recompile per pattern still in use and
// cannot happen partway through a command, whose patterns are fixed.
const maxCached = 256

var cache = map[string]*Regexp{}

// Regexp wraps a compiled Go regexp with the original Sam pattern.
type Regexp struct {
	re      *regexp.Regexp
	pattern string // original pattern as given by the user
}

// Compile compiles pattern with Sam extensions.
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
	goPattern := samToGo(pattern)
	re, err := regexp.Compile(goPattern)
	if err != nil {
		return nil, fmt.Errorf("regexp: %w", err)
	}
	// Sam prefers the earliest match and then the longest one (newmatch,
	// sam/regexp.c). Go's default is Perl's leftmost-first, which would make
	// /foo|foobar/ match only "foo". Longest switches the whole-match
	// preference and leaves (?m) and submatch assignment alone.
	re.Longest()
	r := &Regexp{re: re, pattern: pattern}
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

// Pattern returns the original Sam pattern string.
func (r *Regexp) Pattern() string { return r.pattern }

// FindForward finds the first match of r in s[start:end] (byte offsets).
// Returns the match byte range [lo, hi) or (-1, -1) if not found.
func (r *Regexp) FindForward(s string, start, end int) (lo, hi int) {
	sub := s[start:end]
	loc := r.re.FindStringIndex(sub)
	if loc == nil {
		return -1, -1
	}
	return start + loc[0], start + loc[1]
}

// FindForwardAll finds all non-overlapping matches of r in s[start:end].
// Returns a slice of [lo, hi) byte ranges.
func (r *Regexp) FindForwardAll(s string, start, end int) [][2]int {
	sub := s[start:end]
	locs := r.re.FindAllStringIndex(sub, -1)
	if locs == nil {
		return nil
	}
	result := make([][2]int, len(locs))
	for i, loc := range locs {
		result[i] = [2]int{start + loc[0], start + loc[1]}
	}
	return result
}

// FindBackward finds the rightmost match of r that starts strictly before
// end in s[start:end]. Returns the match byte range [lo, hi) or (-1,-1).
//
// This is implemented as "find all forward matches, return the rightmost."
// It is correct for all patterns and fast enough for the file sizes Dave
// will encounter.
func (r *Regexp) FindBackward(s string, start, end int) (lo, hi int) {
	sub := s[start:end]
	locs := r.re.FindAllStringIndex(sub, -1)
	if locs == nil {
		return -1, -1
	}
	last := locs[len(locs)-1]
	return start + last[0], start + last[1]
}

// FindSubmatch returns the match and captured groups in s[start:end].
// Returns nil if no match.
func (r *Regexp) FindSubmatch(s string, start, end int) []string {
	sub := s[start:end]
	m := r.re.FindStringSubmatch(sub)
	return m
}

// FindSubmatchIndex returns the byte index ranges for the match and groups.
// Returns nil if no match. All indices are relative to s (not sub).
func (r *Regexp) FindSubmatchIndex(s string, start, end int) []int {
	sub := s[start:end]
	idx := r.re.FindStringSubmatchIndex(sub)
	if idx == nil {
		return nil
	}
	// Adjust all indices by start.
	for i := range idx {
		if idx[i] >= 0 {
			idx[i] += start
		}
	}
	return idx
}

// MatchString reports whether r matches anywhere in s.
func (r *Regexp) MatchString(s string) bool {
	return r.re.MatchString(s)
}

// ReplaceExpand expands a replacement template using the submatch from src.
// idx is the submatch index from FindSubmatchIndex.
// template supports & (whole match) and \1..\9 (captured groups).
func (r *Regexp) ReplaceExpand(src string, template string, idx []int) string {
	return string(r.re.ExpandString(nil, template, src, idx))
}

// ---- Sam pattern → Go pattern conversion ------------------------------------

// samToGo translates a Sam/Dave regex pattern to a Go regexp pattern.
// Translations:
//   - Prepends '(?m)' so '^' and '$' match at line boundaries (Sam semantics;
//     Go's default anchors only match start/end of text).
//   - '@' → '[\s\S]' (any char including newline)
//   - '\n' (literal backslash-n) → actual newline in the pattern
func samToGo(pat string) string {
	var b strings.Builder
	b.WriteString("(?m)")
	for i := 0; i < len(pat); {
		switch {
		case pat[i] == '[':
			i = writeClass(&b, pat, i)
		case pat[i] == '@':
			b.WriteString(`[\s\S]`)
			i++
		case pat[i] == '\\':
			i = writeEscape(&b, pat, i)
		case pat[i] == '{' || pat[i] == '}':
			// Sam has no repetition braces, so these are ordinary characters
			// there. Go would read a{2,3} as counted repetition — a second
			// meaning for braces that reads against the 0,$ address model.
			b.WriteByte('\\')
			b.WriteByte(pat[i])
			i++
		default:
			b.WriteByte(pat[i])
			i++
		}
	}
	return b.String()
}

// writeEscape copies the escape sequence at pat[i] and returns the index past
// it. Sam's lex knows exactly one escape, \n; every other \X is the literal X
// (sam/regexp.c). Go reads \d, \w, \b, \t and friends as classes, anchors or
// control characters, which is a silent difference in what a pattern selects
// rather than an error anyone would notice. Escaped punctuation already means
// the literal in both, so it keeps its backslash.
func writeEscape(b *strings.Builder, pat string, i int) int {
	if i+1 >= len(pat) {
		// A trailing backslash is a literal one: sam's lex leaves c as '\\'
		// when nothing follows it. Go would reject the pattern.
		b.WriteString(`\\`)
		return i + 1
	}
	c := pat[i+1]
	switch {
	case c == 'n':
		b.WriteString(`\n`)
	case c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
		b.WriteByte(c)
	default:
		b.WriteByte('\\')
		b.WriteByte(c)
	}
	return i + 2
}

// writeClass copies the character class starting at pat[i] and returns the
// index past its closing ']'.
//
// A negated class gains a newline. Sam seeds every negated class with one as it
// builds it (`classp[n++] = '\n'; /* don't match newline in negate case */`,
// bldcclass), so `[^ ]` cannot cross a line boundary there; Go's can, which
// makes `,x/[^ ]+/` iterate line-spanning runs here and line-bounded ones in
// sam. Scanning the class is what the insertion costs: the newline has to go
// inside the brackets, and a POSIX name like [:alpha:] carries a ']' of its own
// that does not end the class.
func writeClass(b *strings.Builder, pat string, i int) int {
	b.WriteByte('[')
	i++
	negated := i < len(pat) && pat[i] == '^'
	if negated {
		b.WriteByte('^')
		i++
	}
	for i < len(pat) && pat[i] != ']' {
		switch {
		case pat[i] == '[' && i+1 < len(pat) && pat[i+1] == ':':
			if end := strings.Index(pat[i:], ":]"); end >= 0 {
				b.WriteString(pat[i : i+end+2])
				i += end + 2
				continue
			}
			b.WriteByte('[')
			i++
		case pat[i] == '\\':
			i = writeEscape(b, pat, i)
		default:
			b.WriteByte(pat[i])
			i++
		}
	}
	if negated {
		b.WriteString(`\n`)
	}
	if i < len(pat) {
		b.WriteByte(']')
		i++
	}
	return i
}

// ResetLastPattern clears the last-used pattern (for testing).
func ResetLastPattern() { last = nil }
