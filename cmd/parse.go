package cmd

import (
	"dave/addr"
	"dave/buffer"
	"fmt"
	"strings"
	"unicode"
)

// Command is a parsed command ready for execution.
type Command struct {
	// Address (may be nil → use dot).
	Addr *addr.Addr

	// The command letter.
	Op byte

	// Arguments depending on Op:
	Args []string // text args (a/i/c/s replacement, shell command)
	Pat  string   // regexp pattern (x/y/g/v/s/X/Y)
	Pat2 string   // second regexp (s command: replacement)
	Flag string   // modifier flag (s command: 'g' or digit)
	N    int      // numeric arg (u command)
	// AllFiles is set when X is given no pattern, which sam reads as "every
	// file" (sam/xec.c filelooper: cp->re==0 runs the body regardless of XY).
	// Only X can be: `Y ` takes the next character as a delimiter.
	AllFiles bool
	Body     []*Command // x/y/g/v/X/Y body; { } group
	Dest     *addr.Addr // m/t command destination
}

// ParseCommand parses one command (with optional address prefix) from s
// starting at position pos. Returns the command and the position after it.
func ParseCommand(s string, pos int) (*Command, int, error) {
	p := &cmdParser{input: s, pos: pos}
	cmd, err := p.parseOne()
	return cmd, p.pos, err
}

// ParseCommands parses all commands from s (a complete command-line input).
func ParseCommands(s string) ([]*Command, error) {
	var cmds []*Command
	pos := 0
	first := true
	for pos < len(s) {
		// Skip blanks, and the newline that ends the command just parsed. Any
		// newline beyond that one is a command in its own right: sam reads a
		// line holding nothing but a newline as nl_cmd, which prints the line
		// after dot and moves there.
		for pos < len(s) && (s[pos] == ' ' || s[pos] == '\t' || s[pos] == '\r') {
			pos++
		}
		for pos < len(s) && s[pos] == '\n' {
			pos++
			if !first {
				first = true // that newline ended the previous command
			} else {
				cmds = append(cmds, &Command{Op: '\n'})
			}
			for pos < len(s) && (s[pos] == ' ' || s[pos] == '\t' || s[pos] == '\r') {
				pos++
			}
		}
		if pos >= len(s) {
			break
		}
		cmd, newPos, err := ParseCommand(s, pos)
		if err != nil {
			return nil, err
		}
		if cmd == nil {
			break
		}
		cmds = append(cmds, cmd)
		first = false
		pos = newPos
	}
	return cmds, nil
}

type cmdParser struct {
	input string
	pos   int
}

func (p *cmdParser) peek() byte {
	if p.pos >= len(p.input) {
		return 0
	}
	return p.input[p.pos]
}

func (p *cmdParser) consume() byte {
	ch := p.input[p.pos]
	p.pos++
	return ch
}

// maxCount saturates a command count so a long digit string cannot overflow
// int. Any value this large exhausts the undo log anyway.
const maxCount = 1 << 30

// parseCount reads a command's numeric argument the way sam's getnum does
// (sam/cmd.c:166): an optional '-' followed by digits, with no blanks skipped
// on either side. A missing number is 1, so a bare '-' is -1; an explicit 0
// stays 0, which sam treats as a no-op. Only `u` takes a sign — sam passes
// signok=2 there and 1 everywhere else.
func (p *cmdParser) parseCount(signed bool) int {
	sign := 1
	if signed && p.peek() == '-' {
		sign = -1
		p.consume()
	}
	if !isDigit(p.peek()) {
		return sign
	}
	n := 0
	for isDigit(p.peek()) {
		d := int(p.consume() - '0')
		if n < maxCount {
			n = n*10 + d
		}
	}
	return sign * n
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func (p *cmdParser) skipWS() {
	for p.pos < len(p.input) && (p.input[p.pos] == ' ' || p.input[p.pos] == '\t') {
		p.pos++
	}
}

func (p *cmdParser) skipNL() {
	for p.pos < len(p.input) && (p.input[p.pos] == ' ' || p.input[p.pos] == '\t' ||
		p.input[p.pos] == '\n' || p.input[p.pos] == '\r') {
		p.pos++
	}
}

// parseOne parses one command with its optional address prefix.
func (p *cmdParser) parseOne() (*Command, error) {
	p.skipWS()
	if p.pos >= len(p.input) {
		return nil, nil
	}

	// Try to parse an address.
	var addrNode *addr.Addr
	a, consumed, err := addr.Parse(p.input[p.pos:])
	if err != nil {
		return nil, err
	}
	if a != nil {
		addrNode = a
		p.pos += consumed
	}

	p.skipWS()
	if p.pos >= len(p.input) || p.peek() == '\n' || p.peek() == '\r' {
		if addrNode != nil {
			// Bare address with no command — print (like Sam).
			return &Command{Addr: addrNode, Op: 'p'}, nil
		}
		return nil, nil
	}

	op := p.consume()
	if op == 'c' && p.peek() == 'd' {
		p.consume()
		op = OpCd
	}
	spec := lookupCmd(op)
	if spec == nil {
		return nil, fmt.Errorf("unknown command %q", string(op))
	}
	if addrNode != nil && strings.IndexByte(noAddrCmds, op) >= 0 {
		return nil, fmt.Errorf("%c: no address allowed", op)
	}
	cmd := &Command{Addr: addrNode, Op: op}
	if err := p.parseArgs(spec, cmd); err != nil {
		return nil, err
	}
	return cmd, nil
}

// OpCd is the cd command. Sam spells it as two characters and reads it with a
// peek after 'c' — "the sleazy two-character case", as cmd.c puts it — so it
// needs an opcode of its own that no letter collides with. It has to reach the
// tables as one byte: string(rune(OpCd)) would encode it as two in UTF-8 and
// the lookup, which scans bytes, would never find it.
const OpCd = 0x80 | 'c'

// noAddrCmds are the commands that take no address, so an address on one is an
// error rather than something silently ignored. Sam calls this defaddr aNo and
// raises Enoaddr (`if(ct->defaddr==aNo && cmd.addr) error(Enoaddr)`,
// sam/cmd.c:427). It is a property of the command rather than of how its
// arguments parse, so it lives here and not in cmdtab, whose rows deliberately
// group commands that share an argument shape but not an address rule — d and q
// parse alike and address differently.
//
// Sam's list is `b B D e f n q u X Y !` plus `cd`; dave has no `cd`. Everything
// absent here takes an address: `r` and the shell redirections default to dot,
// `w` to the whole file.
var noAddrCmds = "bBDefnquXY!" + string([]byte{OpCd})

// needBlankCmds must have a blank between the command and its argument. These
// are sam's getname callers (sam/sam.c: `if(c!=' ' && c!='\t') error(Eblank)`).
var needBlankCmds = "erfw" + string([]byte{OpCd})

// cmdSpec says how to read the arguments of each command in ops. Between
// them these fields describe every command's syntax except the two shapes
// that are genuinely one-off, which supply a parse function instead.
type cmdSpec struct {
	ops    string // the command letters this row describes
	text   bool   // a text argument follows: /text/ or an ed-style block
	pat    bool   // a delimited regexp follows
	spPat  string // if a space stands in for the delimiter, use this pattern
	body   bool   // a body command follows
	defCmd byte   // the body command to use when the line ends without one
	group  bool   // a { } group follows
	count  bool   // an optional signed count follows
	allPat bool   // a blank stands in for the pattern, meaning every file
	rest   bool   // the rest of the line is the argument
	dest   bool   // a destination address follows

	parse func(p *cmdParser, c *Command) error
}

// cmdtab is the command language. Adding a command means adding its letter
// to a row here plus its implementation in exec.go; the parser itself does
// not change.
var cmdtab = []cmdSpec{
	{ops: "aic", text: true},
	{ops: "dpknq"},
	{ops: "\n"},
	{ops: "=", parse: parseEqualsFlag},
	{ops: "s", parse: parseSubst},
	// Only x and X accept a blank in place of the delimiter — for x it stands
	// for "every line", for X for "every file". y, Y, g and v do not: sam's
	// test is `ct->cmdc!='x' && ct->cmdc!='X'` (sam/cmd.c parsecmd), and both
	// trees have it, so `Y ,p` reads the comma as a delimiter and means
	// Y/p/ with the default body. x's second branch picks up a final line with
	// no trailing newline, which sam reaches because it loops with lineaddr
	// rather than with a pattern (linelooper, sam/xec.c).
	{ops: "x", pat: true, spPat: `.*\n|.+`, body: true, defCmd: 'p'},
	{ops: "ygv", pat: true, body: true, defCmd: 'p'},
	{ops: "X", pat: true, allPat: true, body: true, defCmd: 'f'},
	{ops: "Y", pat: true, body: true, defCmd: 'f'},
	{ops: "{", group: true},
	{ops: "u", count: true},
	{ops: "mt", dest: true},
	{ops: "bBDerfw<>|!", rest: true},
	{ops: string([]byte{OpCd}), rest: true},
}

// okDelim rejects delimiters sam refuses (sam/cmd.c okdelim). A letter, digit
// or backslash after the command is far more likely to be a mistyped pattern
// than a deliberate delimiter, and taking it as one silently scans the rest of
// the line looking for its twin.
func okDelim(c byte) error {
	if c == '\\' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
		return fmt.Errorf("bad delimiter %q", string(c))
	}
	return nil
}

func lookupCmd(op byte) *cmdSpec {
	for i := range cmdtab {
		if strings.IndexByte(cmdtab[i].ops, op) >= 0 {
			return &cmdtab[i]
		}
	}
	return nil
}

// parseArgs reads a command's arguments as its table row describes them.
func (p *cmdParser) parseArgs(spec *cmdSpec, cmd *Command) error {
	if spec.parse != nil {
		return spec.parse(p, cmd)
	}
	if spec.text {
		text, err := p.parseTextArg()
		if err != nil {
			return err
		}
		cmd.Args = []string{text}
	}
	if spec.pat {
		// A blank straight after the command stands in for the pattern, but
		// only where sam allows it: `x` (and `X`), not `y`, `g` or `v`
		// (sam/cmd.c, the ct->cmdc!='x' && ct->cmdc!='X' test). End of input
		// counts as a blank, since sam reads commands from a line that always
		// ends in a newline.
		blank := spec.spPat != "" || spec.allPat
		if c := p.peek(); blank && (c == 0 || c == ' ' || c == '\t' || c == '\n') {
			cmd.AllFiles = spec.allPat
			cmd.Pat = spec.spPat
			p.skipWS()
		} else {
			p.skipWS()
			delim := p.peek()
			if delim == 0 || delim == '\n' || delim == '\r' {
				return fmt.Errorf("%c: pattern expected", cmd.Op)
			}
			if err := okDelim(delim); err != nil {
				return fmt.Errorf("%c: %w", cmd.Op, err)
			}
			p.consume()
			pat, err := p.scanDelimited(delim)
			if err != nil {
				return fmt.Errorf("%c: %w", cmd.Op, err)
			}
			cmd.Pat = pat
		}
	}
	if spec.body {
		body, err := p.parseBody(spec.defCmd)
		if err != nil {
			return err
		}
		cmd.Body = body
	}
	if spec.group {
		cmds, err := p.parseGroup()
		if err != nil {
			return err
		}
		cmd.Body = cmds
	}
	if spec.count {
		cmd.N = p.parseCount(true)
	}
	if spec.rest {
		// Sam's getname wants a blank between the command and its argument:
		// `efoo` is Eblank, not a request to read "foo". It applies to the
		// commands that go through getname, not to the shell redirections,
		// where the rest of the line is the command text.
		if c := p.peek(); c != 0 && c != ' ' && c != '\t' && c != '\n' && c != '\r' &&
			strings.IndexByte(needBlankCmds, cmd.Op) >= 0 {
			return fmt.Errorf("%c: blank expected", cmd.Op)
		}
		p.skipWS()
		cmd.Args = []string{p.restOfLine()}
	}
	if spec.dest {
		p.skipWS()
		a, consumed, err := addr.Parse(p.input[p.pos:])
		if err != nil {
			return fmt.Errorf("%c: %w", cmd.Op, err)
		}
		if a == nil {
			return fmt.Errorf("%c: missing destination address", cmd.Op)
		}
		p.pos += consumed
		cmd.Dest = a
	}
	return nil
}

// parseEqualsFlag reads the optional '#' suffix on =.
func parseEqualsFlag(p *cmdParser, c *Command) error {
	if p.peek() == '#' {
		p.consume()
		c.Flag = "#"
	}
	return nil
}

// parseSubst reads s[n]/re/repl/[g]. The optional digit before the
// delimiter selects the nth match; a trailing g substitutes every match.
func parseSubst(p *cmdParser, c *Command) error {
	if p.pos >= len(p.input) {
		return fmt.Errorf("s: missing pattern")
	}
	// The count is a full number, as sam's getnum reads it; a single digit
	// left `s10/a/b/` substituting the first match and handing `0/a/b/` to
	// the address parser.
	c.N = p.parseCount(false)
	p.skipWS()
	if p.pos >= len(p.input) || p.peek() == '\n' || p.peek() == '\r' {
		return fmt.Errorf("s: pattern expected")
	}
	delim := p.peek()
	if err := okDelim(delim); err != nil {
		return fmt.Errorf("s: %w", err)
	}
	p.consume()
	pat, err := p.scanDelimited(delim)
	if err != nil {
		return fmt.Errorf("s: %w", err)
	}
	repl, err := p.scanDelimited(delim)
	if err != nil {
		return fmt.Errorf("s: %w", err)
	}
	c.Pat = pat
	c.Pat2 = repl
	if p.pos < len(p.input) && p.peek() == 'g' {
		c.Flag = string(p.consume())
	}
	return nil
}

// parseTextArg parses a text argument in the forms:
//
//	a/text/  →  returns "text"
//	a\nline1\nline2\n.  →  ed-style multi-line
func (p *cmdParser) parseTextArg() (string, error) {
	if p.pos >= len(p.input) {
		return "", nil
	}
	ch := p.peek()
	if ch == '\n' {
		// ed-style: lines terminated by a lone dot.
		p.consume() // consume \n
		var sb strings.Builder
		for p.pos < len(p.input) {
			line := p.readLine()
			if line == "." {
				break
			}
			sb.WriteString(line)
			sb.WriteByte('\n')
		}
		return sb.String(), nil
	}
	// Delimiter style: a/text/
	delim := p.consume()
	return p.scanDelimited(delim)
}

// readLine reads up to (but not including) the next \n.
func (p *cmdParser) readLine() string {
	start := p.pos
	for p.pos < len(p.input) && p.input[p.pos] != '\n' {
		p.pos++
	}
	line := p.input[start:p.pos]
	if p.pos < len(p.input) {
		p.pos++ // consume \n
	}
	return line
}

// parseBody parses the body command after an x/y/g/v/X/Y pattern. A line that
// ends there gets the command's default body instead — p for x, y, g and v, f
// for X and Y (sam/cmd.c parsecmd, the ct->defcmd branch). Sam decides that
// with skipbl, which does not cross a newline, so a command on the next line is
// the next command and not this one's body.
func (p *cmdParser) parseBody(defCmd byte) ([]*Command, error) {
	p.skipWS()
	if c := p.peek(); c == 0 || c == '\n' || c == '\r' {
		body := &Command{Op: defCmd}
		if spec := lookupCmd(defCmd); spec != nil && spec.rest {
			// f takes the rest of the line, and reaches this with none of it:
			// a rename to an empty name, which is how sam's f_cmd with a null
			// ctext reports the file rather than renaming it.
			body.Args = []string{""}
		}
		return []*Command{body}, nil
	}
	cmd, err := p.parseOne()
	if err != nil {
		return nil, err
	}
	if cmd == nil {
		return nil, fmt.Errorf("missing body command")
	}
	return []*Command{cmd}, nil
}

// parseGroup parses commands inside { }.
func (p *cmdParser) parseGroup() ([]*Command, error) {
	var cmds []*Command
	for {
		p.skipNL()
		if p.pos >= len(p.input) {
			return nil, fmt.Errorf("unclosed {")
		}
		if p.peek() == '}' {
			p.consume()
			break
		}
		cmd, err := p.parseOne()
		if err != nil {
			return nil, err
		}
		if cmd == nil {
			continue
		}
		cmds = append(cmds, cmd)
	}
	return cmds, nil
}

// restOfLine returns the rest of the current line (trimmed), consuming it.
func (p *cmdParser) restOfLine() string {
	start := p.pos
	for p.pos < len(p.input) && p.input[p.pos] != '\n' {
		p.pos++
	}
	return strings.TrimRightFunc(p.input[start:p.pos], unicode.IsSpace)
}

// scanDelimited scans content up to the closing delimiter.
// The opening delimiter has already been consumed by the caller.
func (p *cmdParser) scanDelimited(delim byte) (string, error) {
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
			// Keep the backslash sequence as-is (regexp or \n handling).
			sb.WriteByte('\\')
			sb.WriteByte(next)
			p.pos += 2
			continue
		}
		if ch == '\n' {
			// Literal newline inside delimited expression is not allowed.
			// (The trailing delimiter may be omitted, so this ends the scan.)
			return sb.String(), nil
		}
		sb.WriteByte(ch)
		p.pos++
	}
	// Trailing delimiter omitted — OK.
	return sb.String(), nil
}

// bufferRangeText returns the text of a Range from a buffer (helper).
func bufferRangeText(buf *buffer.Buffer, r buffer.Range) string {
	return buf.ReadAt(r.Start, r.End-r.Start)
}
