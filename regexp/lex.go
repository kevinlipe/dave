package regexp

import (
	"errors"
	"fmt"
)

// The lexer, ported from sam's lex, nextrec and bldcclass.
//
// sam's pattern language is eleven tokens wide: \ * ? + | . ( ) ^ $ and [.
// Everything else is a literal, `\X` is the literal X with `\n` the sole
// escape that means something, and braces are ordinary characters. dave adds
// one operator, @, which sam reads as an ordinary character — so a pattern
// written for sam means here exactly what it means there.

// sam's regexp errors, by its wording. A pattern that sam rejects should be
// rejected here, saying the same thing.
var (
	errBadClass  = errors.New("malformed `[]'")
	errBadRegexp = errors.New("malformed regexp")
	errLeftPar   = errors.New("unmatched `('")
	errRightPar  = errors.New("unmatched `)'")
)

func errMissingOperand(c rune) error { return fmt.Errorf("no operand for `%c'", c) }

// Operator priorities. The numbers are the grammar: evaluntil unwinds the
// operator stack by comparing them, so their order is sam's precedence, and
// prPrime sits below everything to prime the stack.
const (
	prPrime = iota
	prStart
	prRbra
	prLbra
	prOr
	prCat
	prStar
	prPlus
	prQuest
)

type lexKind uint8

const (
	lexEnd lexKind = iota
	lexOperand
	lexOperator
)

type lexTok struct {
	kind lexKind
	op   opcode // lexOperand
	r    rune   // opRune
	cls  int    // opClass, opNClass
	pri  int    // lexOperator
	ch   rune   // the source character, for error messages
}

type lexer struct {
	pat []rune
	pos int
	p   *prog
}

func (l *lexer) peek() rune {
	if l.pos >= len(l.pat) {
		return 0
	}
	return l.pat[l.pos]
}

func (l *lexer) next() (lexTok, error) {
	if l.pos >= len(l.pat) {
		return lexTok{kind: lexEnd}, nil
	}
	c := l.pat[l.pos]
	l.pos++
	switch c {
	case '\\':
		// `\n` is a newline; every other `\X` is the literal X, and a
		// trailing backslash is a literal backslash.
		if l.pos < len(l.pat) {
			c = l.pat[l.pos]
			l.pos++
			if c == 'n' {
				c = '\n'
			}
		}
		return lexTok{kind: lexOperand, op: opRune, r: c}, nil
	case '*':
		return lexTok{kind: lexOperator, pri: prStar, ch: c}, nil
	case '?':
		return lexTok{kind: lexOperator, pri: prQuest, ch: c}, nil
	case '+':
		return lexTok{kind: lexOperator, pri: prPlus, ch: c}, nil
	case '|':
		return lexTok{kind: lexOperator, pri: prOr, ch: c}, nil
	case '(':
		return lexTok{kind: lexOperator, pri: prLbra, ch: c}, nil
	case ')':
		return lexTok{kind: lexOperator, pri: prRbra, ch: c}, nil
	case '.':
		return lexTok{kind: lexOperand, op: opAny}, nil
	case '@':
		// dave's one addition. sam has no such operator and reads @ as an
		// ordinary character, so no sam pattern changes meaning here.
		return lexTok{kind: lexOperand, op: opAnyNL}, nil
	case '^':
		return lexTok{kind: lexOperand, op: opBOL}, nil
	case '$':
		return lexTok{kind: lexOperand, op: opEOL}, nil
	case '[':
		cls, op, err := l.class()
		if err != nil {
			return lexTok{}, err
		}
		return lexTok{kind: lexOperand, op: op, cls: cls}, nil
	}
	return lexTok{kind: lexOperand, op: opRune, r: c}, nil
}

// nextrec reads one character of a class, reporting whether it was escaped.
//
// sam carries that flag in a high bit of the rune (QUOTED) so that an escaped
// `]` or `-` is not read as punctuation. A bool says the same thing without
// putting a value in a rune that is not one.
func (l *lexer) nextrec() (rune, bool, error) {
	if l.pos >= len(l.pat) {
		return 0, false, errBadClass
	}
	if l.pat[l.pos] == '\\' {
		if l.pos+1 >= len(l.pat) {
			return 0, false, errBadClass
		}
		l.pos++
		c := l.pat[l.pos]
		l.pos++
		if c == 'n' {
			return '\n', true, nil
		}
		return c, true, nil
	}
	c := l.pat[l.pos]
	l.pos++
	return c, false, nil
}

// class parses the body of [...] after the '['.
func (l *lexer) class() (int, opcode, error) {
	var c class
	op := opClass
	if l.peek() == '^' {
		l.pos++
		op = opNClass
		// sam seeds a negated class with newline, so [^a] never matches one.
		c = append(c, runeRange{'\n', '\n'})
	}
	for {
		c1, quoted, err := l.nextrec()
		if err != nil {
			return 0, 0, err
		}
		if !quoted && c1 == ']' {
			break
		}
		if !quoted && c1 == '-' {
			return 0, 0, errBadClass
		}
		// The lookahead is at the raw pattern, so `a\-z` is three characters
		// and not a range — which is how sam reads it too.
		if l.peek() == '-' {
			l.pos++
			c2, q2, err := l.nextrec()
			if err != nil {
				return 0, 0, err
			}
			if !q2 && c2 == ']' {
				return 0, 0, errBadClass
			}
			c = append(c, runeRange{c1, c2})
			continue
		}
		c = append(c, runeRange{c1, c1})
	}
	l.p.classes = append(l.p.classes, c)
	return len(l.p.classes) - 1, op, nil
}
