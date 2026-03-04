// Package buffer implements a gap buffer with Unicode codepoint addressing,
// a transaction log, and multi-level undo.
//
// All external positions are codepoint offsets. Byte-level operations are
// internal only. This invariant is enforced by keeping all conversion in
// this package.
package buffer

import "unicode/utf8"

const defaultGapSize = 4096

// Buffer is a gap buffer holding UTF-8 text. All positions exposed via the
// public API are codepoint (rune) offsets, not byte offsets.
type Buffer struct {
	data     []byte // entire storage: [0..gapStart) ++ gap ++ [gapEnd..len)
	gapStart int    // first byte of the gap (byte index)
	gapEnd   int    // first byte after the gap (byte index)
}

// New returns an empty Buffer.
func New() *Buffer {
	b := &Buffer{}
	b.data = make([]byte, defaultGapSize)
	b.gapStart = 0
	b.gapEnd = defaultGapSize
	return b
}

// NewFromString creates a Buffer pre-loaded with s.
func NewFromString(s string) *Buffer {
	b := New()
	b.insert(0, []byte(s))
	return b
}

// Len returns the number of codepoints in the buffer.
func (b *Buffer) Len() int {
	return utf8.RuneCount(b.data[:b.gapStart]) +
		utf8.RuneCount(b.data[b.gapEnd:])
}

// byteLen returns the number of content bytes (excluding the gap).
func (b *Buffer) byteLen() int {
	return b.gapStart + len(b.data) - b.gapEnd
}

// runeToBytePos converts a codepoint offset to a byte offset in the
// logical (gap-collapsed) content. It panics if pos is out of range.
func (b *Buffer) runeToBytePos(runePos int) int {
	if runePos < 0 {
		panic("buffer: negative rune position")
	}
	// Count runes in the pre-gap segment
	preRunes := utf8.RuneCount(b.data[:b.gapStart])
	if runePos <= preRunes {
		// position is within pre-gap bytes
		return nthRuneByteOffset(b.data[:b.gapStart], runePos)
	}
	// position is within post-gap bytes
	postPos := runePos - preRunes
	postOff := nthRuneByteOffset(b.data[b.gapEnd:], postPos)
	// Return as a logical byte offset: pre-gap bytes + post-gap offset
	return b.gapStart + postOff
}

// nthRuneByteOffset returns the byte offset of the n-th rune in p.
// If n == len(runes in p), it returns len(p) (the end position).
func nthRuneByteOffset(p []byte, n int) int {
	off := 0
	for i := 0; i < n; i++ {
		_, sz := utf8.DecodeRune(p[off:])
		if sz == 0 {
			break
		}
		off += sz
	}
	return off
}

// moveGap moves the gap so that gapStart == bytePos (a logical byte offset).
func (b *Buffer) moveGap(bytePos int) {
	if bytePos == b.gapStart {
		return
	}
	if bytePos < b.gapStart {
		// Move gap left: copy bytes from just before gap to just after gap
		n := b.gapStart - bytePos
		copy(b.data[b.gapEnd-n:], b.data[bytePos:b.gapStart])
		b.gapEnd -= n
		b.gapStart = bytePos
	} else {
		// bytePos is a logical byte offset; actual position in data is
		// bytePos + gapSize (since the gap sits in the middle).
		// But bytePos here is already the logical position; we need the
		// physical position in data[gapEnd:].
		//
		// Logical byte offset > gapStart means we're in the post-gap segment.
		// The physical start of post-gap content is gapEnd.
		// The amount to move is bytePos - gapStart.
		n := bytePos - b.gapStart
		copy(b.data[b.gapStart:], b.data[b.gapEnd:b.gapEnd+n])
		b.gapStart += n
		b.gapEnd += n
	}
}

// grow ensures the gap has at least need bytes.
func (b *Buffer) grow(need int) {
	gapSize := b.gapEnd - b.gapStart
	if gapSize >= need {
		return
	}
	newGap := need + defaultGapSize
	newData := make([]byte, len(b.data)+newGap-gapSize)
	copy(newData, b.data[:b.gapStart])
	newGapEnd := b.gapStart + newGap
	copy(newData[newGapEnd:], b.data[b.gapEnd:])
	b.data = newData
	b.gapEnd = newGapEnd
}

// insert inserts text (bytes) at logical byte position bytePos.
func (b *Buffer) insert(bytePos int, text []byte) {
	b.grow(len(text))
	b.moveGap(bytePos)
	copy(b.data[b.gapStart:], text)
	b.gapStart += len(text)
}

// delete removes n bytes starting at logical byte position bytePos.
func (b *Buffer) delete(bytePos, n int) {
	b.moveGap(bytePos)
	b.gapEnd += n
}

// Insert inserts text at codepoint position pos.
func (b *Buffer) Insert(pos int, text string) {
	bytePos := b.runeToBytePos(pos)
	b.insert(bytePos, []byte(text))
}

// Delete removes n codepoints starting at codepoint position pos.
func (b *Buffer) Delete(pos, n int) {
	if n == 0 {
		return
	}
	byteStart := b.runeToBytePos(pos)
	byteEnd := b.runeToBytePos(pos + n)
	b.delete(byteStart, byteEnd-byteStart)
}

// ReadAt returns the string of n codepoints starting at codepoint offset pos.
func (b *Buffer) ReadAt(pos, n int) string {
	if n == 0 {
		return ""
	}
	// Collect bytes from both sides of the gap
	result := make([]byte, 0, n*3) // rough pre-alloc
	remaining := n
	cur := pos

	// pre-gap portion
	preRunes := utf8.RuneCount(b.data[:b.gapStart])
	if cur < preRunes {
		start := nthRuneByteOffset(b.data[:b.gapStart], cur)
		take := preRunes - cur
		if take > remaining {
			take = remaining
		}
		end := nthRuneByteOffset(b.data[:b.gapStart], cur+take)
		result = append(result, b.data[start:end]...)
		remaining -= take
		cur += take
	}

	// post-gap portion
	if remaining > 0 {
		postRunes := cur - preRunes
		postData := b.data[b.gapEnd:]
		start := nthRuneByteOffset(postData, postRunes)
		end := nthRuneByteOffset(postData, postRunes+remaining)
		result = append(result, postData[start:end]...)
	}

	return string(result)
}

// String returns the entire buffer contents as a string.
func (b *Buffer) String() string {
	return b.ReadAt(0, b.Len())
}

// RuneAt returns the codepoint at position pos.
func (b *Buffer) RuneAt(pos int) rune {
	s := b.ReadAt(pos, 1)
	if len(s) == 0 {
		return -1
	}
	r, _ := utf8.DecodeRuneInString(s)
	return r
}
