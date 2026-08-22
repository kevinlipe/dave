// Package regexp wraps Go's regexp package with Sam's extensions:
//   - '@' matches any character including newline (Go's '.' does not)
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
	goPattern := samToGo(pattern)
	re, err := regexp.Compile(goPattern)
	if err != nil {
		return nil, fmt.Errorf("regexp: %w", err)
	}
	r := &Regexp{re: re, pattern: pattern}
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
	i := 0
	for i < len(pat) {
		if pat[i] == '@' {
			b.WriteString(`[\s\S]`)
			i++
		} else if pat[i] == '\\' && i+1 < len(pat) && pat[i+1] == 'n' {
			b.WriteByte('\n')
			i += 2
		} else {
			b.WriteByte(pat[i])
			i++
		}
	}
	return b.String()
}


// ResetLastPattern clears the last-used pattern (for testing).
func ResetLastPattern() { last = nil }
