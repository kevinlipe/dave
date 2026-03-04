package buffer

import (
	"fmt"
	"unicode/utf8"
)

// Change records a single edit in ORIGINAL-buffer codepoint coordinates.
// It means: at position Pos, delete Del codepoints, then insert Text.
type Change struct {
	Pos  int    // codepoint offset in the original (pre-transaction) buffer
	Del  int    // number of codepoints to delete
	Text string // text to insert after deletion
}

// Transcript collects an ordered sequence of Changes for a single command.
// Changes must be added in monotonically non-decreasing order of Pos.
// All Pos values are in the coordinate space of the buffer before this
// transaction began — they do not account for earlier changes in the same
// transaction.
type Transcript struct {
	changes []Change
	// lastPos is the "high-water mark" — the end position of the last
	// change in original coordinates. New changes must have Pos >= lastPos.
	lastPos int
	sealed  bool // true after Apply; no more changes allowed
}

// NewTranscript returns an empty Transcript.
func NewTranscript() *Transcript { return &Transcript{} }

// Add appends a change to the transcript. Returns an error if the change
// position violates monotonic ordering or if the transcript has been applied.
func (t *Transcript) Add(c Change) error {
	if t.sealed {
		return fmt.Errorf("transcript already applied")
	}
	if c.Pos < t.lastPos {
		return fmt.Errorf("transcript: change at %d is before last position %d (changes must be in order)", c.Pos, t.lastPos)
	}
	// Validate that Del doesn't overlap with the previous change's end.
	// lastPos tracks the end of the previous change in original coordinates.
	if len(t.changes) > 0 {
		prev := t.changes[len(t.changes)-1]
		prevEnd := prev.Pos + prev.Del
		if c.Pos < prevEnd {
			return fmt.Errorf("transcript: change at %d overlaps previous change ending at %d", c.Pos, prevEnd)
		}
	}
	t.changes = append(t.changes, c)
	end := c.Pos + c.Del
	if end > t.lastPos {
		t.lastPos = end
	}
	if c.Pos > t.lastPos {
		t.lastPos = c.Pos
	}
	return nil
}

// IsEmpty reports whether the transcript has no changes.
func (t *Transcript) IsEmpty() bool { return len(t.changes) == 0 }

// Apply applies all changes in the transcript to buf.
// It returns the inverse transcript that can be used to undo this operation.
// After Apply, no more changes may be added to t.
func (t *Transcript) Apply(buf *Buffer) (*Transcript, error) {
	t.sealed = true
	inverse := &Transcript{}

	delta := 0 // cumulative codepoint offset due to prior changes in this transaction
	for _, c := range t.changes {
		actualPos := c.Pos + delta

		// Capture the text being deleted (for the inverse).
		var deleted string
		if c.Del > 0 {
			deleted = buf.ReadAt(actualPos, c.Del)
			buf.Delete(actualPos, c.Del)
		}

		// Insert new text.
		if len(c.Text) > 0 {
			buf.Insert(actualPos, c.Text)
		}

		// Build the inverse change: at actualPos, delete inserted text, re-insert deleted text.
		insertedRunes := utf8.RuneCountInString(c.Text)
		inv := Change{
			Pos:  actualPos,
			Del:  insertedRunes,
			Text: deleted,
		}
		// Inverse changes must also be in monotonic order; since original
		// changes were, the actual positions are too.
		if err := inverse.Add(inv); err != nil {
			return nil, fmt.Errorf("building inverse: %w", err)
		}

		delta += insertedRunes - c.Del
	}

	// Seal the inverse too — it's ready to be applied directly.
	// (We don't seal here; the caller will apply it via Apply again if undoing.)

	return inverse, nil
}

// Len returns the number of changes in the transcript.
func (t *Transcript) Len() int { return len(t.changes) }
