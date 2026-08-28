package regexp

// The program a compiled pattern runs as, ported from sam's regexp.c.
//
// Sam's machine is Thompson's construction: a graph of instructions walked by
// a list of live threads, one step per input rune, with no backtracking. The
// port keeps sam's structure rather than improving on it, because the point is
// to be able to read the two side by side when they disagree.
//
// Two departures from the C, both mechanical. Instructions are indices into a
// slice rather than pointers into a fixed array, which lets a program be
// copied and compared and removes sam's NPROG ceiling. And sam packs an
// instruction's operands into unions keyed by type; here they are named
// fields, with the one overlap that carries meaning kept (see inst.next).

// nsubexp is how many capture groups a match records, group 0 being the whole
// match. Sam's NSUBEXP is 10, and a pattern may contain more groups than that
// -- the extra ones are compiled and matched, they simply are not recorded.
const nsubexp = 10

type opcode uint8

const (
	opRune   opcode = iota // a literal rune, in inst.r
	opAny                  // .  any rune except newline
	opAnyNL                // @  any rune at all; dave's addition, sam has no such operator
	opBOL                  // ^
	opEOL                  // $
	opClass                // [...]  inst.cls indexes prog.classes
	opNClass               // [^...]
	opNop                  // left behind by the compiler, removed by optimize
	opOr                   // alternation: take right later, fall through to next
	opLbra                 // ( opening subexpression inst.subid
	opRbra                 // ) closing it
	opEnd                  // a match
)

// inst is one instruction of the program.
//
// next doubles as sam's `left`. In the C those two are one union member
// (lnext/lleft), and that is load-bearing: optimize walks `next` for every
// instruction including an alternation, which is what makes the left branch of
// an alternation its fall-through. Splitting them into two fields would leave
// opOr's fall-through unoptimised and change which NOPs survive.
type inst struct {
	op    opcode
	r     rune // opRune
	cls   int  // opClass, opNClass: index into prog.classes
	next  int  // successor, and the left branch of opOr; -1 for none
	right int  // opOr: the branch taken later
	subid int  // opLbra, opRbra: which group, or -1 if beyond nsubexp
}

// runeRange is one span of a character class. A single character is a span of
// one, which is how sam stores it too.
type runeRange struct{ lo, hi rune }

type class []runeRange

func (c class) match(r rune) bool {
	for _, rr := range c {
		if rr.lo <= r && r <= rr.hi {
			return true
		}
	}
	return false
}

// prog is a compiled pattern in one direction. A pattern compiles to two of
// these: the forward machine, and the backward one built from the same source
// with the operands of every concatenation swapped.
type prog struct {
	insts   []inst
	start   int
	classes []class

	// Thread lists, kept here so a search does not allocate. Sam has them as
	// globals for the same reason; like sam's, they make a program unsafe to
	// run against itself concurrently, which single-threaded dave never does.
	tl, nl []thread
}

func (p *prog) add(op opcode) int {
	p.insts = append(p.insts, inst{op: op, next: -1, right: -1, subid: -1})
	return len(p.insts) - 1
}

// startRune returns the literal rune every match must begin with, and false
// when there is no such rune. Sam calls this startchar and uses it to skip
// input positions that cannot begin a match; it is the whole of sam's
// optimiser for the common case of searching for a word.
func (p *prog) startRune() (rune, bool) {
	if p.start < 0 || p.start >= len(p.insts) {
		return 0, false
	}
	if i := p.insts[p.start]; i.op == opRune {
		return i.r, true
	}
	return 0, false
}

// optimize removes the NOPs the compiler leaves behind, by pointing every
// successor past them. Ported verbatim; sam runs it once per direction.
func (p *prog) optimize() {
	for i := range p.insts {
		if p.insts[i].op == opEnd {
			continue
		}
		t := p.insts[i].next
		for t >= 0 && p.insts[t].op == opNop {
			t = p.insts[t].next
		}
		p.insts[i].next = t
	}
	for t := p.start; t >= 0 && p.insts[t].op == opNop; t = p.insts[t].next {
		p.start = p.insts[t].next
	}
}
