package buffer

// Edit tracking.
//
// A consumer that keeps something derived from the buffer -- a wrapped
// layout, say -- needs to know not just that the buffer changed but *where*,
// so it can rebuild only the affected part. Gen answers the first question;
// this answers the second.
//
// Several panes can show one file, each having last looked at a different
// generation, so this cannot be a single dirty flag that someone clears. It
// is a bounded log of recent edits that any number of consumers can query
// independently, each naming the generation it last saw.

// editLogSize is how many recent edits are retained. An edit is one Insert
// or Delete; a keystroke is one or two of them (a replacement over a
// selection is a Delete then an Insert). A few dozen covers any plausible
// gap between one consumer's relayout and the next, and a consumer that
// falls further behind simply rebuilds from scratch.
const editLogSize = 64

// edit records one Insert or Delete, in the coordinates in force before it
// was applied.
type edit struct {
	gen int // buffer generation this edit produced
	pos int // codepoint offset where the change began
	del int // codepoints removed
	ins int // codepoints inserted
}

// recordEdit appends to the ring. Called from Insert and Delete, which are
// the only operations that change content and move gen.
func (b *Buffer) recordEdit(pos, del, ins int) {
	if len(b.edits) < editLogSize {
		b.edits = append(b.edits, edit{gen: b.gen, pos: pos, del: del, ins: ins})
		return
	}
	b.edits[b.editHead] = edit{gen: b.gen, pos: pos, del: del, ins: ins}
	b.editHead = (b.editHead + 1) % editLogSize
}

// editsInOrder returns the retained edits from oldest to newest.
func (b *Buffer) editsInOrder() []edit {
	if len(b.edits) < editLogSize {
		return b.edits
	}
	out := make([]edit, 0, editLogSize)
	out = append(out, b.edits[b.editHead:]...)
	out = append(out, b.edits[:b.editHead]...)
	return out
}

// EditsSince returns a codepoint range in the buffer's current coordinates
// that contains everything changed since generation gen. Text outside
// [lo, hi) is exactly what it was at gen, though it may have moved.
//
// ok is false when the answer cannot be given: gen is newer than the buffer,
// or so old that the edits bridging the gap are no longer retained. Callers
// must then rebuild whatever they derived from the buffer in full.
//
// When gen is the current generation the range is empty (lo == hi == 0) and
// ok is true: nothing has changed.
//
// The range is allowed to be wider than strictly necessary. Folding a run of
// edits into one interval is conservative by construction, and over-reporting
// costs a caller some extra recomputation, never correctness.
func (b *Buffer) EditsSince(gen int) (lo, hi int, ok bool) {
	if gen > b.gen {
		return 0, 0, false
	}
	if gen == b.gen {
		return 0, 0, true
	}
	all := b.editsInOrder()
	// The log must reach back far enough to cover gen. Every edit bumps gen
	// by one, so the oldest retained edit produced generation
	// b.gen-len(all)+1, and it can only explain changes from the generation
	// before that onwards.
	if len(all) == 0 || all[0].gen-1 > gen {
		return 0, 0, false
	}

	first := true
	for _, e := range all {
		if e.gen <= gen {
			continue // already reflected in what the caller has
		}
		delta := e.ins - e.del
		if first {
			lo, hi = e.pos, e.pos+e.ins
			first = false
			continue
		}
		// Move the range we have so far into the coordinates after e.
		if e.pos < lo {
			lo += delta
		}
		if e.pos < hi {
			hi += delta
		}
		if hi < lo {
			hi = lo
		}
		// Then widen it to include e itself.
		if e.pos < lo {
			lo = e.pos
		}
		if end := e.pos + e.ins; end > hi {
			hi = end
		}
	}
	if first {
		return 0, 0, true // nothing newer than gen
	}
	if lo < 0 {
		lo = 0
	}
	if hi > b.totalRunes {
		hi = b.totalRunes
	}
	if hi < lo {
		hi = lo
	}
	return lo, hi, true
}
