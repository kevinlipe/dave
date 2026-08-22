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
	Args []string   // text args (a/i/c/s replacement, shell command)
	Pat  string     // regexp pattern (x/y/g/v/s/X/Y)
	Pat2 string     // second regexp (s command: replacement)
	Flag string     // modifier flag (s command: 'g' or digit)
	N    int        // numeric arg (u command)
	Body []*Command // x/y/g/v/X/Y body; { } group
	Dest *addr.Addr // m/t command destination
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
	for pos < len(s) {
		// Skip whitespace and newlines between commands.
		for pos < len(s) && (s[pos] == ' ' || s[pos] == '\t' || s[pos] == '\n' || s[pos] == '\r') {
			pos++
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
	spec := lookupCmd(op)
	if spec == nil {
		return nil, fmt.Errorf("unknown command %q", string(op))
	}
	cmd := &Command{Addr: addrNode, Op: op}
	if err := p.parseArgs(spec, cmd); err != nil {
		return nil, err
	}
	return cmd, nil
}

// cmdSpec says how to read the arguments of each command in ops. Between
// them these fields describe every command's syntax except the two shapes
// that are genuinely one-off, which supply a parse function instead.
type cmdSpec struct {
	ops   string // the command letters this row describes
	text  bool   // a text argument follows: /text/ or an ed-style block
	pat   bool   // a delimited regexp follows
	spPat string // if a space stands in for the delimiter, use this pattern
	body  bool   // a body command follows
	group bool   // a { } group follows
	count bool   // an optional single digit follows
	rest  bool   // the rest of the line is the argument
	dest  bool   // a destination address follows

	parse func(p *cmdParser, c *Command) error
}

// cmdtab is the command language. Adding a command means adding its letter
// to a row here plus its implementation in exec.go; the parser itself does
// not change.
var cmdtab = []cmdSpec{
	{ops: "aic", text: true},
	{ops: "dpknq"},
	{ops: "=", parse: parseEqualsFlag},
	{ops: "s", parse: parseSubst},
	// x and friends accept a space in place of the delimiter, standing for
	// "every line". X and Y do not.
	{ops: "xygv", pat: true, spPat: `.*\n`, body: true},
	{ops: "XY", pat: true, body: true},
	{ops: "{", group: true},
	{ops: "u", count: true},
	{ops: "mt", dest: true},
	{ops: "bBDerfw<>|", rest: true},
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
	if spec.pat && p.pos < len(p.input) {
		if spec.spPat != "" && p.input[p.pos] == ' ' {
			cmd.Pat = spec.spPat
			p.skipWS()
		} else {
			delim := p.consume()
			pat, err := p.scanDelimited(delim)
			if err != nil {
				return fmt.Errorf("%c: %w", cmd.Op, err)
			}
			cmd.Pat = pat
		}
	}
	if spec.body {
		body, err := p.parseBody()
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
		cmd.N = 1
		if p.pos < len(p.input) && p.input[p.pos] >= '1' && p.input[p.pos] <= '9' {
			cmd.N = int(p.consume() - '0')
		}
	}
	if spec.rest {
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
	if ch := p.peek(); ch >= '2' && ch <= '9' {
		c.Flag = string(p.consume())
	}
	if p.pos >= len(p.input) {
		return fmt.Errorf("s: missing delimiter")
	}
	delim := p.consume()
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

// parseBody parses the body command after an x/y/g/v/X/Y pattern.
// Returns an error if no body command is present — structural commands
// require one (the parser already accepts `x/re/{}` for an explicit empty
// group; a truly-missing body is a user error, not a silent no-op).
func (p *cmdParser) parseBody() ([]*Command, error) {
	p.skipNL()
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
