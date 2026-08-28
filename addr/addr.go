// Package addr implements Sam's address language: parsing and evaluation.
//
// An Address identifies a substring (possibly empty) within a file.
// Evaluation takes a File and returns a Range of codepoint offsets.
//
// The parser inserts all defaults (implicit dot, implicit + before bare regexp)
// so the evaluator never guesses about defaults.
package addr

import (
	"dave/buffer"
	"dave/file"
	dregs "dave/regexp"
	"fmt"
	"strconv"
	"strings"
)

// Kind identifies the type of an address node. Following sam, the value is
// the character that introduces the address in the source text, so the
// parser records what it read rather than translating it into a parallel
// vocabulary. KindLine is the exception: a line number has no introducing
// character, so it borrows sam's 'l'.
type Kind byte

const (
	KindLine      Kind = 'l'  // n
	KindChar      Kind = '#'  // #n
	KindDot       Kind = '.'  // .
	KindMark      Kind = '\'' // '
	KindEnd       Kind = '$'  // $
	KindRegexp    Kind = '/'  // /re/
	KindBackRe    Kind = '?'  // ?re?
	KindFilePat   Kind = 'F'  // "re"
	KindComma     Kind = ','  // a1,a2
	KindSemicolon Kind = ';'  // a1;a2
	KindInFile    Kind = '"'  // "pat"a2: a2 evaluated in the file "pat" selects
	KindPlus      Kind = '+'  // a1+a2
	KindMinus     Kind = '-'  // a1-a2
)

// Addr is a node in an address parse tree.
type Addr struct {
	Kind  Kind
	Num   int    // KindLine, KindChar
	Pat   string // KindRegexp, KindBackRe, KindFilePat
	Left  *Addr
	Right *Addr
}

// searchBackward reports which way a regexp address searches, given the
// sign set by the operator it hangs off: +1 after '+', -1 after '-', and 0
// when the regexp stands alone.
//
// This follows sam (address.c), where '?' does not mean "search backwards"
// outright -- it negates whatever direction the preceding operator set, and
// only falls back to backwards when there was no operator:
//
//	case '?':
//		sign = -sign;
//		if(sign == 0)
//			sign = -1;
//		/* fall through */
//	case '/':
//
// So /re/ and +/re/ search forward, ?re? and -/re/ search backward, and the
// two that surprise people -- +?re? and -?re? -- search backward and forward
// respectively.
func searchBackward(sign int, kind Kind) bool {
	if kind == KindBackRe {
		sign = -sign
		if sign == 0 {
			sign = -1
		}
	}
	return sign < 0
}

// --- Evaluation --------------------------------------------------------------

// Eval evaluates addr against f using the provided file list.
// The file must be loaded (EnsureLoaded called) before calling Eval.
// evalCtx carries the file an address is being evaluated against. A `"pat"`
// address rebinds it, and everything after that in the same address — and the
// command itself — runs in the file it selected.
type evalCtx struct {
	f     *file.File
	files []*file.File
}

// Eval evaluates a against f and returns the range together with the file it
// landed in, which differs from f when the address contains a `"pat"`.
func Eval(a *Addr, f *file.File, files []*file.File) (buffer.Range, *file.File, error) {
	ec := &evalCtx{f: f, files: files}
	r, err := eval(a, ec)
	return r, ec.f, err
}

func eval(a *Addr, ec *evalCtx) (buffer.Range, error) {
	f := ec.f
	switch a.Kind {
	case KindLine:
		return lineRange(f, a.Num)
	case KindChar:
		return charRange(f.Len(), a.Num)
	case KindDot:
		return f.Dot(), nil
	case KindMark:
		return f.Mark(), nil
	case KindEnd:
		n := f.Len()
		return buffer.Range{Start: n, End: n}, nil
	case KindRegexp, KindBackRe:
		return evalRegexp(a.Pat, searchBackward(0, a.Kind), f)
	case KindFilePat:
		// Selects a file and evaluates to its dot, as sam does
		// (`a = matchfile(ap->are)->dot; f = a.f`, sam/address.c). The rebind
		// is what makes `"pat"p` print that file rather than this one.
		ff, err := FindFileByPat(a.Pat, ec.files, f)
		if err != nil {
			return buffer.Range{}, err
		}
		if err := ff.EnsureLoaded(); err != nil {
			return buffer.Range{}, err
		}
		ec.f = ff
		return ff.Dot(), nil
	case KindInFile:
		return evalInFile(a, ec)
	case KindComma:
		return evalComma(a, ec)
	case KindSemicolon:
		return evalSemicolon(a, ec)
	case KindPlus:
		return evalPlus(a, ec)
	case KindMinus:
		return evalMinus(a, ec)
	}
	return buffer.Range{}, fmt.Errorf("addr: unknown address kind %q", rune(a.Kind))
}

// lineRange returns the range for line n (1-indexed). Line 0 is the null
// string before the first line.
//
// The line lookup goes through the buffer's line-start table, which is built
// once per buffer generation, so a command resolving many line addresses
// scans the file once rather than once per address.
func lineRange(f *file.File, n int) (buffer.Range, error) {
	if n == 0 {
		return buffer.Range{}, nil
	}
	if n < 0 {
		return buffer.Range{}, fmt.Errorf("addr: negative line number %d", n)
	}
	r, ok := f.Buffer().LineRange(n)
	if !ok {
		return buffer.Range{}, fmt.Errorf("addr: line %d beyond end of file", n)
	}
	return r, nil
}

// charRange returns a null range at codepoint position n.
func charRange(total, n int) (buffer.Range, error) {
	if n < 0 || n > total {
		return buffer.Range{}, fmt.Errorf("addr: #%d out of range [0,%d]", n, total)
	}
	return buffer.Range{Start: n, End: n}, nil
}

// evalRegexp evaluates /pat/ or ?pat? starting from dot.
//
// The search runs over the buffer itself; there is no copy of the file and no
// byte offset anywhere in it. Sam wraps inside its machine, and dave wraps
// here instead, where it can be read.
func evalRegexp(pat string, backward bool, f *file.File) (buffer.Range, error) {
	re, err := dregs.Compile(pat)
	if err != nil {
		return buffer.Range{}, err
	}
	buf := f.Buffer()
	dot := f.Dot()
	n := buf.Len()

	if !backward {
		// Forward from dot.End, then round to what precedes it.
		lo, hi := re.FindForward(buf, dot.End, n)
		if lo < 0 {
			lo, hi = re.FindForward(buf, 0, dot.End)
			if lo < 0 {
				return buffer.Range{}, fmt.Errorf("addr: /%s/ no match", pat)
			}
		}
		return buffer.Range{Start: lo, End: hi}, nil
	}
	// Backward from dot.Start, then round to what follows it.
	lo, hi := re.FindBackward(buf, 0, dot.Start)
	if lo < 0 {
		lo, hi = re.FindBackward(buf, dot.Start, n)
		if lo < 0 {
			return buffer.Range{}, fmt.Errorf("addr: ?%s? no match", pat)
		}
	}
	return buffer.Range{Start: lo, End: hi}, nil
}

// FindFileByPat finds the unique file whose menu line matches pat.
func FindFileByPat(pat string, files []*file.File, current *file.File) (*file.File, error) {
	re, err := dregs.Compile(pat)
	if err != nil {
		return nil, err
	}
	var matched []*file.File
	for _, ff := range files {
		ml := ff.MenuLine(ff == current, false)
		if re.MatchString(ml) {
			matched = append(matched, ff)
		}
	}
	switch len(matched) {
	case 0:
		return nil, fmt.Errorf("addr: \"%.20s\": no matching file", pat)
	case 1:
		return matched[0], nil
	default:
		return nil, fmt.Errorf("addr: \"%.20s\": ambiguous (matches %d files)", pat, len(matched))
	}
}

// evalComma evaluates a1,a2.
func evalComma(a *Addr, ec *evalCtx) (buffer.Range, error) {
	// Both sides are evaluated against the incoming file: sam recurses with the
	// same `a` for each, so a `"pat"` on the left does not carry over to the
	// right the way it does after a ';'.
	start := ec.f
	var r1 buffer.Range
	var err error
	if a.Left != nil {
		r1, err = eval(a.Left, ec)
		if err != nil {
			return buffer.Range{}, err
		}
	}
	left := ec.f
	ec.f = start
	var r2 buffer.Range
	if a.Right != nil {
		r2, err = eval(a.Right, ec)
		if err != nil {
			return buffer.Range{}, err
		}
	} else {
		n := ec.f.Len()
		r2 = buffer.Range{Start: n, End: n}
	}
	if ec.f != left {
		return buffer.Range{}, fmt.Errorf("addr: addresses in different files")
	}
	return joinRange(r1, r2)
}

// evalInFile evaluates `"pat"a2`: the left side selects a file, and a2 is
// evaluated there, against that file's dot. Sam gets this by continuing its
// address chain with the newly bound file (sam/address.c).
func evalInFile(a *Addr, ec *evalCtx) (buffer.Range, error) {
	// The left side is always a file pattern, which evaluates to the selected
	// file's own dot, so the right side is already relative to the right place
	// once ec.f has been rebound.
	if _, err := eval(a.Left, ec); err != nil {
		return buffer.Range{}, err
	}
	return eval(a.Right, ec)
}

// joinRange combines the two sides of a ',' or ';'. Sam refuses a range that
// runs backwards — Eorder, "addresses out of order" (sam/address.c) — rather
// than handing an inverted range to whatever command follows.
func joinRange(r1, r2 buffer.Range) (buffer.Range, error) {
	if r2.End < r1.Start {
		return buffer.Range{}, fmt.Errorf("addr: addresses out of order")
	}
	return buffer.Range{Start: r1.Start, End: r2.End}, nil
}

// evalSemicolon evaluates a1;a2 (a2 evaluated with dot := a1).
func evalSemicolon(a *Addr, ec *evalCtx) (buffer.Range, error) {
	var r1 buffer.Range
	var err error
	if a.Left != nil {
		r1, err = eval(a.Left, ec)
		if err != nil {
			return buffer.Range{}, err
		}
	}
	f := ec.f
	saved := f.Dot()
	f.SetDot(r1)
	defer f.SetDot(saved)

	var r2 buffer.Range
	if a.Right != nil {
		r2, err = eval(a.Right, ec)
		if err != nil {
			return buffer.Range{}, err
		}
	} else {
		n := f.Len()
		r2 = buffer.Range{Start: n, End: n}
	}
	return joinRange(r1, r2)
}

// evalPlus evaluates a1+a2.
func evalPlus(a *Addr, ec *evalCtx) (buffer.Range, error) {
	base, err := evalBase(a.Left, ec, false)
	if err != nil {
		return buffer.Range{}, err
	}
	f := ec.f
	right := a.Right
	if right == nil {
		right = &Addr{Kind: KindLine, Num: 1}
	}
	// Evaluate right relative to base.End.
	switch right.Kind {
	case KindChar:
		pos := base.End + right.Num
		n := f.Len()
		if pos > n {
			return buffer.Range{}, fmt.Errorf("addr: +#%d out of range", right.Num)
		}
		return buffer.Range{Start: pos, End: pos}, nil
	case KindLine:
		buf := f.Buffer()
		// Determine which line "+1" refers to:
		// - If base.End is at a line start (offset 0 or just after \n), then
		//   "+1" means the line AT that position (don't advance past another \n).
		// - If base.End is mid-line, "+1" means the NEXT line (advance past \n).
		firstLine := buf.LineNumber(base.End)
		if base.End > 0 && buf.RuneAt(base.End-1) != '\n' {
			firstLine++
		}
		return lineRange(f, firstLine+right.Num-1)
	case KindRegexp, KindBackRe:
		// + sets the direction forward; ?re? negates it. See searchSign.
		saved := f.Dot()
		f.SetDot(buffer.Range{Start: base.End, End: base.End})
		defer f.SetDot(saved)
		return evalRegexp(right.Pat, searchBackward(+1, right.Kind), f)
	}
	saved := f.Dot()
	f.SetDot(buffer.Range{Start: base.End, End: base.End})
	defer f.SetDot(saved)
	return eval(right, ec)
}

// evalMinus evaluates a1-a2.
func evalMinus(a *Addr, ec *evalCtx) (buffer.Range, error) {
	base, err := evalBase(a.Left, ec, true)
	if err != nil {
		return buffer.Range{}, err
	}
	f := ec.f
	right := a.Right
	if right == nil {
		right = &Addr{Kind: KindLine, Num: 1}
	}
	switch right.Kind {
	case KindChar:
		pos := base.Start - right.Num
		if pos < 0 {
			return buffer.Range{}, fmt.Errorf("addr: -#%d out of range", right.Num)
		}
		return buffer.Range{Start: pos, End: pos}, nil
	case KindLine:
		target := f.Buffer().LineNumber(base.Start) - right.Num
		if target <= 0 {
			return buffer.Range{}, nil
		}
		return lineRange(f, target)
	case KindRegexp, KindBackRe:
		// - sets the direction backward; ?re? negates it. See searchSign.
		saved := f.Dot()
		f.SetDot(buffer.Range{Start: base.Start, End: base.Start})
		defer f.SetDot(saved)
		return evalRegexp(right.Pat, searchBackward(-1, right.Kind), f)
	}
	saved := f.Dot()
	f.SetDot(buffer.Range{Start: base.Start, End: base.Start})
	defer f.SetDot(saved)
	return eval(right, ec)
}

// evalBase evaluates the left side of + or -, defaulting to dot.
func evalBase(left *Addr, ec *evalCtx, useStart bool) (buffer.Range, error) {
	if left == nil {
		return ec.f.Dot(), nil
	}
	return eval(left, ec)
}

// --- Parser ------------------------------------------------------------------

// Parse parses an address expression from the start of s.
// Returns the parsed Addr and the number of bytes consumed.
// If no address is found, returns (nil, 0, nil).
func Parse(s string) (*Addr, int, error) {
	p := &parser{input: s}
	a, err := p.parseCompound()
	if err != nil {
		return nil, 0, err
	}
	if a == nil {
		return nil, 0, nil
	}
	return a, p.pos, nil
}

type parser struct {
	input string
	pos   int
}

func (p *parser) peek() byte {
	if p.pos >= len(p.input) {
		return 0
	}
	return p.input[p.pos]
}

func (p *parser) consume() byte {
	ch := p.input[p.pos]
	p.pos++
	return ch
}

// skipbl consumes spaces and tabs, as sam's skipbl does. A newline is not
// blank here: it ends a command.
func (p *parser) skipbl() {
	for p.pos < len(p.input) && (p.input[p.pos] == ' ' || p.input[p.pos] == '\t') {
		p.pos++
	}
}

// peekbl is the next character that is not a blank, without consuming
// anything. Sam reaches the same place by consuming the blanks and ungetting
// the character; looking past them instead means a failed match leaves the
// position where it found it, which is what Parse's contract of consuming
// nothing when there is no address depends on.
func (p *parser) peekbl() byte {
	i := p.pos
	for i < len(p.input) && (p.input[i] == ' ' || p.input[i] == '\t') {
		i++
	}
	if i >= len(p.input) {
		return 0
	}
	return p.input[i]
}

// parseCompound parses a full compound address.
// parseCompound parses a full address. This is sam's compoundaddr: ',' and ';'
// are the lowest-precedence operators, and their right side is another compound
// address, so `2,+1` is `2,(+1)` and not `(2,$)+1`. Right recursion is not just
// shape — for a ';' chain it decides what the third address is relative to.
func (p *parser) parseCompound() (*Addr, error) {
	left, err := p.parseChain()
	if err != nil {
		return nil, err
	}
	k := p.peekbl()
	if k != ',' && k != ';' {
		return left, nil
	}
	p.skipbl()
	p.consume()
	if left == nil {
		left = &Addr{Kind: KindLine, Num: 0} // ,a2 means 0,a2
	}
	right, err := p.parseCompound()
	if err != nil {
		return nil, err
	}
	if right == nil {
		right = &Addr{Kind: KindEnd} // a1, means a1,$
	} else if (right.Kind == KindComma || right.Kind == KindSemicolon) && right.Left == nil {
		return nil, fmt.Errorf("addr: bad address")
	}
	return &Addr{Kind: Kind(k), Left: left, Right: right}, nil
}

// parseChain parses one address and any '+'/'-' chain hanging off it. This is
// sam's simpleaddr, where '+' and '-' are address types in their own right, so
// a chain may begin with one and take an implicit dot as its left operand. A
// nil Left is how the evaluator already spells that.
func (p *parser) parseChain() (*Addr, error) {
	left, err := p.parseSimple()
	if err != nil {
		return nil, err
	}
	if left == nil && p.peekbl() != '+' && p.peekbl() != '-' {
		return nil, nil
	}
	for {
		if k := p.peekbl(); k == '+' || k == '-' {
			p.skipbl()
			p.consume()
			right, err := p.parseSimple()
			if err != nil {
				return nil, err
			}
			left = &Addr{Kind: Kind(k), Left: left, Right: right}
			continue
		}
		save := p.pos
		right, err := p.parseSimple()
		if err != nil {
			return nil, err
		}
		if right == nil {
			p.pos = save
			return left, nil
		}
		switch right.Kind {
		case KindDot, KindEnd, KindMark, KindFilePat:
			// sam raises Eaddress rather than inserting a '+' before an
			// address that is not relative to anything.
			return nil, fmt.Errorf("addr: bad address")
		}
		if left != nil && left.Kind == KindFilePat {
			// A file address chains by switching files, not by offsetting:
			// `"pat"5` is line 5 of the file "pat" selects.
			left = &Addr{Kind: KindInFile, Left: left, Right: right}
			continue
		}
		left = &Addr{Kind: KindPlus, Left: left, Right: right}
	}
}

func (p *parser) parseSimple() (*Addr, error) {
	save := p.pos
	p.skipbl()
	a, err := p.parseSimpleAt()
	if err == nil && a == nil {
		p.pos = save
	}
	return a, err
}

func (p *parser) parseSimpleAt() (*Addr, error) {
	if p.pos >= len(p.input) {
		return nil, nil
	}
	ch := p.peek()
	switch {
	case ch == '.':
		p.consume()
		return &Addr{Kind: KindDot}, nil
	case ch == '$':
		p.consume()
		return &Addr{Kind: KindEnd}, nil
	case ch == '\'':
		p.consume()
		return &Addr{Kind: KindMark}, nil
	case ch == '#':
		p.consume()
		n, err := p.parseNum(0)
		if err != nil {
			return nil, err
		}
		return &Addr{Kind: KindChar, Num: n}, nil
	case ch >= '0' && ch <= '9':
		n, err := p.parseNum(0)
		if err != nil {
			return nil, err
		}
		return &Addr{Kind: KindLine, Num: n}, nil
	case ch == '/':
		p.consume() // consume opening /
		pat, err := p.scanDelimited('/')
		if err != nil {
			return nil, err
		}
		return &Addr{Kind: KindRegexp, Pat: pat}, nil
	case ch == '?':
		p.consume()
		pat, err := p.scanDelimited('?')
		if err != nil {
			return nil, err
		}
		return &Addr{Kind: KindBackRe, Pat: pat}, nil
	case ch == '"':
		p.consume()
		pat, err := p.scanDelimited('"')
		if err != nil {
			return nil, err
		}
		return &Addr{Kind: KindFilePat, Pat: pat}, nil
	}
	return nil, nil
}

// parseNum parses a decimal integer starting at p.pos.
// defaultVal is returned if no digits are present.
func (p *parser) parseNum(defaultVal int) (int, error) {
	start := p.pos
	for p.pos < len(p.input) && p.input[p.pos] >= '0' && p.input[p.pos] <= '9' {
		p.pos++
	}
	if p.pos == start {
		return defaultVal, nil
	}
	return strconv.Atoi(p.input[start:p.pos])
}

// scanDelimited scans content up to the closing delimiter (which was already
// opened by the caller). Handles \" escaping.
func (p *parser) scanDelimited(delim byte) (string, error) {
	var sb strings.Builder
	for p.pos < len(p.input) {
		ch := p.input[p.pos]
		if ch == delim {
			p.pos++
			return sb.String(), nil
		}
		if ch == '\\' && p.pos+1 < len(p.input) {
			next := p.input[p.pos+1]
			if next == delim {
				sb.WriteByte(delim)
				p.pos += 2
				continue
			}
			sb.WriteByte('\\')
			sb.WriteByte(next)
			p.pos += 2
			continue
		}
		sb.WriteByte(ch)
		p.pos++
	}
	// Trailing delimiter omitted — that's OK.
	return sb.String(), nil
}
