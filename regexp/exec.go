package regexp

// The machines, ported from sam's execute and bexecute.
//
// One list of live threads, stepped once per input rune, with no backtracking
// and no backing up: the cost of a search is the input it reads. The forward
// and backward machines are the same loop over two programs compiled from one
// source, and they differ only in which way p moves and where a thread's
// endpoints land — which is why sam wrote them twice rather than sharing, and
// why this port does too.
//
// sam wraps around the end of the file inside these loops. dave does the wrap
// where it can be seen, in addr's search, so the machines here take a range
// and stay inside it.

// Input is the text a match runs over: any rune at a codepoint position, and
// how many there are. RuneAt returns -1 outside the text, as sam's filereadc
// does.
//
// *buffer.Buffer satisfies this as it stands, which is the point — a search
// steps the gap buffer where it lives instead of a flattened copy of it, and
// its rune/byte memo makes stepping either direction cost O(1).
type Input interface {
	RuneAt(pos int) rune
	Len() int
}

// Runes adapts a plain string, for the callers that match against a line
// rather than a file.
type Runes []rune

func NewRunes(s string) Runes { return Runes([]rune(s)) }

func (r Runes) Len() int { return len(r) }

func (r Runes) RuneAt(pos int) rune {
	if pos < 0 || pos >= len(r) {
		return -1
	}
	return r[pos]
}

type subrange struct{ p1, p2 int }

// rangeset is a match: group 0 is the whole of it, the rest are captures.
type rangeset [nsubexp]subrange

type thread struct {
	pc int
	se rangeset
}

// addinst adds a thread unless its instruction is already live, in which case
// the one that started earlier wins.
//
// Going backwards a thread's start is stored negated, which makes this same
// comparison prefer the position that is later in the file — sam's trick, and
// the reason one addinst serves both machines.
func addinst(l *[]thread, pc int, se rangeset) bool {
	if pc < 0 {
		return false
	}
	for i := range *l {
		if (*l)[i].pc == pc {
			if se[0].p1 < (*l)[i].se[0].p1 {
				(*l)[i].se = se
			}
			return false
		}
	}
	*l = append(*l, thread{pc: pc, se: se})
	return true
}

// newmatch keeps the leftmost match, and the longest among those — sam's
// preference, and the reason Compile has always set Go's Longest.
func newmatch(sel *rangeset, sp rangeset) {
	if sel[0].p1 < 0 || sp[0].p1 < sel[0].p1 ||
		(sp[0].p1 == sel[0].p1 && sp[0].p2 > sel[0].p2) {
		*sel = sp
	}
}

// bnewmatch keeps the match ending last, and the longest among those, then
// puts the endpoints back in file order. This is the rule dave could not
// express while it was borrowing a forward-only engine.
func bnewmatch(sel *rangeset, sp rangeset) {
	if sel[0].p1 < 0 || sp[0].p1 > sel[0].p2 ||
		(sp[0].p1 == sel[0].p2 && sp[0].p2 < sel[0].p1) {
		for i := 0; i < nsubexp; i++ {
			sel[i].p1, sel[i].p2 = sp[i].p2, sp[i].p1
		}
	}
}

func emptyRangeset() rangeset {
	var rs rangeset
	for i := range rs {
		rs[i] = subrange{-1, -1}
	}
	return rs
}

// execute runs the forward machine over in[start:end].
func (p *prog) execute(in Input, start, end int) (rangeset, bool) {
	sel := emptyRangeset()
	tl, nl := p.tl[:0], p.nl[:0]
	startchar, hasStart := p.startRune()
	nnl := 0
	past := 0

	for pos := start; ; pos++ {
		// Read before testing the limit, as sam does: at the end of the range
		// the character just past it is still consulted, which is how a $ at a
		// range boundary sees the newline that makes it one.
		c := in.RuneAt(pos)
		if pos >= end || c < 0 {
			// sam lets the loop run one more click past the end so that a $
			// or an END still waiting on nothing can fire.
			past++
			if past > 1 {
				break
			}
		} else if sel[0].p1 > 0 && nnl == 0 {
			break
		}
		// sam's startchar: a position that cannot begin a match is skipped
		// outright while nothing is live.
		if hasStart && nnl == 0 && c != startchar {
			continue
		}

		tl, nl = nl, tl
		nl = nl[:0]
		nnl = 0
		if sel[0].p1 < 0 {
			e := emptyRangeset()
			e[0].p1 = pos
			addinst(&tl, p.start, e)
		}

		for i := 0; i < len(tl); i++ {
			pc := tl[i].pc
			for {
				ins := p.insts[pc]
				switch ins.op {
				case opRune:
					if c == ins.r {
						if addinst(&nl, ins.next, tl[i].se) {
							nnl++
						}
					}
				case opAny:
					// sam does not guard this against the end of the input;
					// the thread it adds there is discarded when the loop
					// stops, so guarding changes nothing but says more.
					if c >= 0 && c != '\n' {
						if addinst(&nl, ins.next, tl[i].se) {
							nnl++
						}
					}
				case opAnyNL:
					if c >= 0 {
						if addinst(&nl, ins.next, tl[i].se) {
							nnl++
						}
					}
				case opClass:
					if c >= 0 && p.classes[ins.cls].match(c) {
						if addinst(&nl, ins.next, tl[i].se) {
							nnl++
						}
					}
				case opNClass:
					if c >= 0 && !p.classes[ins.cls].match(c) {
						if addinst(&nl, ins.next, tl[i].se) {
							nnl++
						}
					}
				case opLbra:
					if ins.subid >= 0 {
						tl[i].se[ins.subid].p1 = pos
					}
					pc = ins.next
					continue
				case opRbra:
					if ins.subid >= 0 {
						tl[i].se[ins.subid].p2 = pos
					}
					pc = ins.next
					continue
				case opBOL:
					if pos == 0 || in.RuneAt(pos-1) == '\n' {
						pc = ins.next
						continue
					}
				case opEOL:
					// sam's forward $ matches only before a newline, not at
					// the end of the input. Its backward one differs; both
					// are ported as written.
					if c == '\n' {
						pc = ins.next
						continue
					}
				case opOr:
					addinst(&tl, ins.right, tl[i].se)
					pc = ins.next
					continue
				case opEnd:
					se := tl[i].se
					se[0].p2 = pos
					newmatch(&sel, se)
				}
				break
			}
		}
	}
	p.tl, p.nl = tl[:0], nl[:0]
	return sel, sel[0].p1 >= 0
}

// bexecute runs the backward machine over in[start:end], looking left from
// end. Positions move downward and a thread's start is carried negated.
func (p *prog) bexecute(in Input, start, end int) (rangeset, bool) {
	sel := emptyRangeset()
	tl, nl := p.tl[:0], p.nl[:0]
	startchar, hasStart := p.startRune()
	nnl := 0
	past := 0

	for pos := end; ; pos-- {
		c := in.RuneAt(pos - 1)
		if pos <= start || c < 0 {
			past++
			if past > 1 {
				break
			}
		} else if sel[0].p1 > 0 && nnl == 0 {
			break
		}
		if hasStart && nnl == 0 && c != startchar {
			continue
		}

		tl, nl = nl, tl
		nl = nl[:0]
		nnl = 0
		if sel[0].p1 < 0 {
			e := emptyRangeset()
			e[0].p1 = -pos
			addinst(&tl, p.start, e)
		}

		for i := 0; i < len(tl); i++ {
			pc := tl[i].pc
			for {
				ins := p.insts[pc]
				switch ins.op {
				case opRune:
					if c == ins.r {
						if addinst(&nl, ins.next, tl[i].se) {
							nnl++
						}
					}
				case opAny:
					if c >= 0 && c != '\n' {
						if addinst(&nl, ins.next, tl[i].se) {
							nnl++
						}
					}
				case opAnyNL:
					if c >= 0 {
						if addinst(&nl, ins.next, tl[i].se) {
							nnl++
						}
					}
				case opClass:
					if c >= 0 && p.classes[ins.cls].match(c) {
						if addinst(&nl, ins.next, tl[i].se) {
							nnl++
						}
					}
				case opNClass:
					if c >= 0 && !p.classes[ins.cls].match(c) {
						if addinst(&nl, ins.next, tl[i].se) {
							nnl++
						}
					}
				case opLbra:
					if ins.subid >= 0 {
						tl[i].se[ins.subid].p1 = pos
					}
					pc = ins.next
					continue
				case opRbra:
					if ins.subid >= 0 {
						tl[i].se[ins.subid].p2 = pos
					}
					pc = ins.next
					continue
				case opBOL:
					if c == '\n' || pos == 0 {
						pc = ins.next
						continue
					}
				case opEOL:
					if pos == in.Len() || in.RuneAt(pos) == '\n' {
						pc = ins.next
						continue
					}
				case opOr:
					addinst(&tl, ins.right, tl[i].se)
					pc = ins.next
					continue
				case opEnd:
					se := tl[i].se
					se[0].p1 = -se[0].p1
					se[0].p2 = pos
					bnewmatch(&sel, se)
				}
				break
			}
		}
	}
	p.tl, p.nl = tl[:0], nl[:0]
	return sel, sel[0].p1 >= 0
}
