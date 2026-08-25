package buffer

import "io"

// Writing a buffer never needs to flatten it. The content is already two
// contiguous runs of bytes — one on each side of the gap — so it can go
// straight to an io.Writer without the file-sized copy that String() makes.

// WriteTo writes the entire buffer contents to w. Buffer implements
// io.WriterTo.
func (b *Buffer) WriteTo(w io.Writer) (int64, error) {
	n, err := writeSeg(w, b.data[:b.gapStart])
	if err != nil {
		return n, err
	}
	m, err := writeSeg(w, b.data[b.gapEnd:])
	return n + m, err
}

// WriteRangeTo writes n codepoints starting at codepoint offset pos. Like
// ReadAt, the range is clamped to the buffer, so a short range near the end
// writes however many codepoints are actually there.
func (b *Buffer) WriteRangeTo(w io.Writer, pos, n int) (int64, error) {
	if n <= 0 {
		return 0, nil
	}
	lo, hi := pos, pos+n
	if lo < 0 {
		lo = 0
	}
	if hi <= lo {
		return 0, nil
	}
	start := b.runeToBytePos(lo)
	end := b.runeToBytePos(hi)
	if end <= start {
		return 0, nil
	}
	// start and end are logical offsets; shift by the gap size to index
	// data for anything at or after the gap.
	gap := b.gapSize()
	switch {
	case end <= b.gapStart:
		return writeSeg(w, b.data[start:end]) // wholly pre-gap
	case start >= b.gapStart:
		return writeSeg(w, b.data[start+gap:end+gap]) // wholly post-gap
	default:
		pre, err := writeSeg(w, b.data[start:b.gapStart])
		if err != nil {
			return pre, err
		}
		post, err := writeSeg(w, b.data[b.gapEnd:end+gap])
		return pre + post, err
	}
}

// writeSeg writes one gap segment, skipping the call when it is empty.
func writeSeg(w io.Writer, seg []byte) (int64, error) {
	if len(seg) == 0 {
		return 0, nil
	}
	n, err := w.Write(seg)
	return int64(n), err
}
