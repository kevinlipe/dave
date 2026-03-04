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
	Args    []string // text args (a/i/c/s replacement, shell command)
	Pat     string   // regexp pattern (x/y/g/v/s/X/Y)
	Pat2    string   // second regexp (s command: replacement)
	Flag    string   // modifier flag (s command: 'g' or digit)
	N       int      // numeric arg (u command)
	Body    []*Command // x/y/g/v/X/Y body; { } group
	Body2   []*Command // unused for now
	Dest    *addr.Addr // m/t command destination
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
		// Skip comments (# to end of line).
		if s[pos] == '#' {
			for pos < len(s) && s[pos] != '\n' {
				pos++
			}
			continue
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
	cmd := &Command{Addr: addrNode, Op: op}

	switch op {
	case 'a', 'i', 'c':
		text, err := p.parseTextArg()
		if err != nil {
			return nil, err
		}
		cmd.Args = []string{text}

	case 'd', 'p', 'k', 'n':
		// No arguments.

	case '=':
		// Optional '#' suffix.
		if p.peek() == '#' {
			p.consume()
			cmd.Flag = "#"
		}

	case 's':
		// s[n]/re/text/[g]
		// An optional digit n before the delimiter selects the nth match.
		if p.pos >= len(p.input) {
			return nil, fmt.Errorf("s: missing pattern")
		}
		if ch := p.peek(); ch >= '2' && ch <= '9' {
			cmd.Flag = string(p.consume()) // nth-match flag
		}
		if p.pos >= len(p.input) {
			return nil, fmt.Errorf("s: missing delimiter")
		}
		delim := p.consume()
		pat, err := p.scanDelimited(delim)
		if err != nil {
			return nil, fmt.Errorf("s: %w", err)
		}
		repl, err := p.scanDelimited(delim)
		if err != nil {
			return nil, fmt.Errorf("s: %w", err)
		}
		cmd.Pat = pat
		cmd.Pat2 = repl
		// Optional trailing 'g' flag (global substitution).
		if p.pos < len(p.input) && p.peek() == 'g' {
			cmd.Flag = string(p.consume())
		}

	case 'x', 'y', 'g', 'v':
		// x/re/ cmd or x (space) cmd (using .*\n as pattern)
		if p.pos < len(p.input) && p.input[p.pos] == ' ' {
			// x (space) — use .*\n pattern (match each line)
			cmd.Pat = `.*\n`
			p.skipWS()
		} else if p.pos < len(p.input) {
			delim := p.consume()
			pat, err := p.scanDelimited(delim)
			if err != nil {
				return nil, fmt.Errorf("%c: %w", op, err)
			}
			cmd.Pat = pat
		}
		body, err := p.parseBody()
		if err != nil {
			return nil, err
		}
		cmd.Body = body

	case 'X', 'Y':
		if p.pos < len(p.input) {
			delim := p.consume()
			pat, err := p.scanDelimited(delim)
			if err != nil {
				return nil, fmt.Errorf("%c: %w", op, err)
			}
			cmd.Pat = pat
		}
		body, err := p.parseBody()
		if err != nil {
			return nil, err
		}
		cmd.Body = body

	case '{':
		// Group of commands terminated by '}'.
		cmds, err := p.parseGroup()
		if err != nil {
			return nil, err
		}
		cmd.Body = cmds

	case 'u':
		// u or un (undo n commands)
		cmd.N = 1
		if p.pos < len(p.input) && p.input[p.pos] >= '1' && p.input[p.pos] <= '9' {
			cmd.N = int(p.consume() - '0')
		}

	case 'b', 'B':
		// b/B filename (rest of line)
		p.skipWS()
		cmd.Args = []string{p.restOfLine()}

	case 'D':
		// D filename (rest of line, optional)
		p.skipWS()
		cmd.Args = []string{p.restOfLine()}

	case 'e', 'r', 'f':
		// e/r/f filename
		p.skipWS()
		cmd.Args = []string{p.restOfLine()}

	case 'w':
		// w filename (optional)
		p.skipWS()
		cmd.Args = []string{p.restOfLine()}

	case 'm', 't':
		// m/t address
		p.skipWS()
		a, consumed, err := addr.Parse(p.input[p.pos:])
		if err != nil {
			return nil, fmt.Errorf("%c: %w", op, err)
		}
		if a == nil {
			return nil, fmt.Errorf("%c: missing destination address", op)
		}
		p.pos += consumed
		cmd.Dest = a

	case '<', '>', '|':
		// Shell command — rest of line.
		p.skipWS()
		cmd.Args = []string{p.restOfLine()}

	case 'q':
		// No arguments.

	default:
		return nil, fmt.Errorf("unknown command %q", string(op))
	}

	return cmd, nil
}

// parseTextArg parses a text argument in the forms:
//   a/text/  →  returns "text"
//   a\nline1\nline2\n.  →  ed-style multi-line
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
func (p *cmdParser) parseBody() ([]*Command, error) {
	p.skipNL()
	cmd, err := p.parseOne()
	if err != nil {
		return nil, err
	}
	if cmd == nil {
		return nil, nil
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
		// Skip comments.
		if p.peek() == '#' {
			for p.pos < len(p.input) && p.input[p.pos] != '\n' {
				p.pos++
			}
			continue
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
