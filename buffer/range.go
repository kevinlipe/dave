package buffer

// Range is a half-open interval [Start, End) of codepoint offsets.
// When Start == End, it is a null (empty) selection at that position.
type Range struct {
	Start, End int
}

// Len returns the number of codepoints in the range.
func (r Range) Len() int { return r.End - r.Start }

// IsNull reports whether the range is empty (Start == End).
func (r Range) IsNull() bool { return r.Start == r.End }

// Clamp returns r clamped to [0, n].
func (r Range) Clamp(n int) Range {
	if r.Start < 0 {
		r.Start = 0
	}
	if r.End > n {
		r.End = n
	}
	if r.Start > r.End {
		r.Start = r.End
	}
	return r
}
