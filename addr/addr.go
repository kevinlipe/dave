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
	"unicode/utf8"
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
	KindFilePat   Kind = '"'  // "re"
	KindComma     Kind = ','  // a1,a2
	KindSemicolon Kind = ';'  // a1;a2
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
func Eval(a *Addr, f *file.File, files []*file.File) (buffer.Range, error) {
	return eval(a, f, files)
}

func eval(a *Addr, f *file.File, files []*file.File) (buffer.Range, error) {
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
		// Returns whole range of matched file; cmd layer switches to that file.
		ff, err := FindFileByPat(a.Pat, files, f)
		if err != nil {
			return buffer.Range{}, err
		}
		if err := ff.EnsureLoaded(); err != nil {
			return buffer.Range{}, err
		}
		n := ff.Len()
		return buffer.Range{Start: 0, End: n}, nil
	case KindComma:
		return evalComma(a, f, files)
	case KindSemicolon:
		return evalSemicolon(a, f, files)
	case KindPlus:
		return evalPlus(a, f, files)
	case KindMinus:
		return evalMinus(a, f, files)
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
func evalRegexp(pat string, backward bool, f *file.File) (buffer.Range, error) {
	re, err := dregs.Compile(pat)
	if err != nil {
		return buffer.Range{}, err
	}
	s := f.String()
	dot := f.Dot()
	if !backward {
		// Search forward from dot.End, wrapping.
		lo, hi := re.FindForward(s, byteOff(s, dot.End), len(s))
		if lo == -1 {
			lo, hi = re.FindForward(s, 0, byteOff(s, dot.End))
			if lo == -1 {
				return buffer.Range{}, fmt.Errorf("addr: /%s/ no match", pat)
			}
		}
		return byteRangeToRune(s, lo, hi), nil
	}
	// Search backward from dot.Start, wrapping.
	lo, hi := re.FindBackward(s, 0, byteOff(s, dot.Start))
	if lo == -1 {
		lo, hi = re.FindBackward(s, byteOff(s, dot.Start), len(s))
		if lo == -1 {
			return buffer.Range{}, fmt.Errorf("addr: ?%s? no match", pat)
		}
	}
	return byteRangeToRune(s, lo, hi), nil
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
func evalComma(a *Addr, f *file.File, files []*file.File) (buffer.Range, error) {
	var r1 buffer.Range
	var err error
	if a.Left != nil {
		r1, err = eval(a.Left, f, files)
		if err != nil {
			return buffer.Range{}, err
		}
	}
	var r2 buffer.Range
	if a.Right != nil {
		r2, err = eval(a.Right, f, files)
		if err != nil {
			return buffer.Range{}, err
		}
	} else {
		n := f.Len()
		r2 = buffer.Range{Start: n, End: n}
	}
	return buffer.Range{Start: r1.Start, End: r2.End}, nil
}

// evalSemicolon evaluates a1;a2 (a2 evaluated with dot := a1).
func evalSemicolon(a *Addr, f *file.File, files []*file.File) (buffer.Range, error) {
	var r1 buffer.Range
	var err error
	if a.Left != nil {
		r1, err = eval(a.Left, f, files)
		if err != nil {
			return buffer.Range{}, err
		}
	}
	saved := f.Dot()
	f.SetDot(r1)
	defer f.SetDot(saved)

	var r2 buffer.Range
	if a.Right != nil {
		r2, err = eval(a.Right, f, files)
		if err != nil {
			return buffer.Range{}, err
		}
	} else {
		n := f.Len()
		r2 = buffer.Range{Start: n, End: n}
	}
	return buffer.Range{Start: r1.Start, End: r2.End}, nil
}

// evalPlus evaluates a1+a2.
func evalPlus(a *Addr, f *file.File, files []*file.File) (buffer.Range, error) {
	base, err := evalBase(a.Left, f, files, false)
	if err != nil {
		return buffer.Range{}, err
	}
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
	return eval(right, f, files)
}

// evalMinus evaluates a1-a2.
func evalMinus(a *Addr, f *file.File, files []*file.File) (buffer.Range, error) {
	base, err := evalBase(a.Left, f, files, true)
	if err != nil {
		return buffer.Range{}, err
	}
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
	return eval(right, f, files)
}

// evalBase evaluates the left side of + or -, defaulting to dot.
func evalBase(left *Addr, f *file.File, files []*file.File, useStart bool) (buffer.Range, error) {
	if left == nil {
		return f.Dot(), nil
	}
	return eval(left, f, files)
}

// ---- Byte/rune conversion ---------------------------------------------------

func byteOff(s string, runeN int) int {
	off := 0
	for i := 0; i < runeN && off < len(s); i++ {
		_, sz := utf8.DecodeRuneInString(s[off:])
		off += sz
	}
	return off
}

func byteRangeToRune(s string, lo, hi int) buffer.Range {
	return buffer.Range{
		Start: utf8.RuneCountInString(s[:lo]),
		End:   utf8.RuneCountInString(s[:hi]),
	}
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

// parseCompound parses a full compound address.
func (p *parser) parseCompound() (*Addr, error) {
	left, err := p.parseSimple()
	if err != nil {
		return nil, err
	}

	// Leading bare comma: ,a2 means 0,a2
	if left == nil && p.peek() == ',' {
		p.consume()
		right, err := p.parseSimple()
		if err != nil {
			return nil, err
		}
		if right == nil {
			right = &Addr{Kind: KindEnd}
		}
		return &Addr{Kind: KindComma,
			Left:  &Addr{Kind: KindLine, Num: 0},
			Right: right,
		}, nil
	}
	if left == nil {
		return nil, nil
	}

	for {
		switch p.peek() {
		case ',':
			p.consume()
			right, err := p.parseSimple()
			if err != nil {
				return nil, err
			}
			if right == nil {
				right = &Addr{Kind: KindEnd}
			}
			left = &Addr{Kind: KindComma, Left: left, Right: right}
		case ';':
			p.consume()
			right, err := p.parseSimple()
			if err != nil {
				return nil, err
			}
			if right == nil {
				right = &Addr{Kind: KindEnd}
			}
			left = &Addr{Kind: KindSemicolon, Left: left, Right: right}
		case '+':
			p.consume()
			right, err := p.parseSimple()
			if err != nil {
				return nil, err
			}
			left = &Addr{Kind: KindPlus, Left: left, Right: right}
		case '-':
			p.consume()
			right, err := p.parseSimple()
			if err != nil {
				return nil, err
			}
			left = &Addr{Kind: KindMinus, Left: left, Right: right}
		default:
			return left, nil
		}
	}
}

// parseSimple parses a single simple address token.
func (p *parser) parseSimple() (*Addr, error) {
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
