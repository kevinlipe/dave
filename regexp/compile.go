package regexp

// The compiler, ported from sam's realcompile, operand, operator, evaluntil
// and the four stack operations.
//
// It is an operator-precedence parser over two stacks: operands, each a
// fragment of the instruction graph with a first and a last instruction, and
// operators waiting for their arguments. Concatenation is implicit — an
// operand arriving while the last thing seen was also an operand inserts one.
//
// The whole of backward matching lives in one line of the CAT case below.

// node is an operand: a fragment of the graph, entered at first and left at
// last.
type node struct{ first, last int }

type ator struct {
	pri   int
	ch    rune
	subid int
}

type compiler struct {
	p          *prog
	lex        *lexer
	ands       []node
	ators      []ator
	cursubid   int
	nbra       int
	lastWasAnd bool
	backwards  bool
}

func (c *compiler) pushand(f, l int) { c.ands = append(c.ands, node{f, l}) }

func (c *compiler) popand(op rune) (node, error) {
	if len(c.ands) == 0 {
		if op != 0 {
			return node{}, errMissingOperand(op)
		}
		return node{}, errBadRegexp
	}
	n := c.ands[len(c.ands)-1]
	c.ands = c.ands[:len(c.ands)-1]
	return n, nil
}

// pushator records the group number alongside the operator, as sam's parallel
// subid stack does. A pattern may have more groups than a match can record;
// the ones past nsubexp get -1 and are matched but not reported.
func (c *compiler) pushator(pri int, ch rune) {
	id := c.cursubid
	if id >= nsubexp {
		id = -1
	}
	c.ators = append(c.ators, ator{pri, ch, id})
}

func (c *compiler) popator() ator {
	a := c.ators[len(c.ators)-1]
	c.ators = c.ators[:len(c.ators)-1]
	return a
}

func (c *compiler) topPri() int { return c.ators[len(c.ators)-1].pri }

func (c *compiler) operand(t lexTok) {
	if c.lastWasAnd {
		c.operator(lexTok{kind: lexOperator, pri: prCat})
	}
	i := c.p.add(t.op)
	switch t.op {
	case opRune:
		c.p.insts[i].r = t.r
	case opClass, opNClass:
		c.p.insts[i].cls = t.cls
	}
	c.pushand(i, i)
	c.lastWasAnd = true
}

func (c *compiler) operator(t lexTok) error {
	if t.pri == prRbra {
		c.nbra--
		if c.nbra < 0 {
			return errRightPar
		}
	}
	if t.pri == prLbra {
		c.cursubid++
		c.nbra++
		if c.lastWasAnd {
			if err := c.operator(lexTok{kind: lexOperator, pri: prCat}); err != nil {
				return err
			}
		}
	} else if err := c.evaluntil(t.pri); err != nil {
		return err
	}
	if t.pri != prRbra {
		c.pushator(t.pri, t.ch)
	}
	// A closed group and a repetition are operands as far as what follows is
	// concerned: `a*b` concatenates, it does not repeat twice.
	c.lastWasAnd = t.pri == prStar || t.pri == prQuest || t.pri == prPlus || t.pri == prRbra
	return nil
}

func (c *compiler) evaluntil(pri int) error {
	for pri == prRbra || c.topPri() >= pri {
		a := c.popator()
		switch a.pri {
		case prLbra:
			op1, err := c.popand('(')
			if err != nil {
				return err
			}
			i2 := c.p.add(opRbra)
			c.p.insts[i2].subid = a.subid
			c.p.insts[op1.last].next = i2
			i1 := c.p.add(opLbra)
			c.p.insts[i1].subid = a.subid
			c.p.insts[i1].next = op1.first
			c.pushand(i1, i2)
			return nil // this was the RBRA that sent us here

		case prOr:
			op2, err := c.popand('|')
			if err != nil {
				return err
			}
			op1, err := c.popand('|')
			if err != nil {
				return err
			}
			i2 := c.p.add(opNop)
			c.p.insts[op2.last].next = i2
			c.p.insts[op1.last].next = i2
			i1 := c.p.add(opOr)
			c.p.insts[i1].right = op1.first
			c.p.insts[i1].next = op2.first // next is sam's left
			c.pushand(i1, i2)

		case prCat:
			op2, err := c.popand(0)
			if err != nil {
				return err
			}
			op1, err := c.popand(0)
			if err != nil {
				return err
			}
			// This is backward matching, entire. Compiling the same source
			// with the operands of concatenation swapped gives a program that
			// runs right to left; sam does exactly this and nothing else.
			// The END that terminates the program is not swapped to the front.
			if c.backwards && c.p.insts[op2.first].op != opEnd {
				op1, op2 = op2, op1
			}
			c.p.insts[op1.last].next = op2.first
			c.pushand(op1.first, op2.last)

		case prStar:
			op2, err := c.popand('*')
			if err != nil {
				return err
			}
			i1 := c.p.add(opOr)
			c.p.insts[op2.last].next = i1
			c.p.insts[i1].right = op2.first
			c.pushand(i1, i1)

		case prPlus:
			op2, err := c.popand('+')
			if err != nil {
				return err
			}
			i1 := c.p.add(opOr)
			c.p.insts[op2.last].next = i1
			c.p.insts[i1].right = op2.first
			c.pushand(op2.first, i1)

		case prQuest:
			op2, err := c.popand('?')
			if err != nil {
				return err
			}
			i1 := c.p.add(opOr)
			i2 := c.p.add(opNop)
			c.p.insts[i1].next = i2 // sam's left
			c.p.insts[i1].right = op2.first
			c.p.insts[op2.last].next = i2
			c.pushand(i1, i2)

		default:
			return errBadRegexp
		}
	}
	return nil
}

// compileProg compiles pattern in one direction.
func compileProg(pattern string, backwards bool) (*prog, error) {
	p := &prog{}
	c := &compiler{
		p:         p,
		lex:       &lexer{pat: []rune(pattern), p: p},
		backwards: backwards,
	}
	// Prime the operator stack with something below every real operator, so
	// that unwinding to prStart always terminates.
	c.ators = append(c.ators, ator{pri: prPrime})

	for {
		t, err := c.lex.next()
		if err != nil {
			return nil, err
		}
		if t.kind == lexEnd {
			break
		}
		if t.kind == lexOperator {
			if err := c.operator(t); err != nil {
				return nil, err
			}
		} else {
			c.operand(t)
		}
	}
	if err := c.evaluntil(prStart); err != nil {
		return nil, err
	}
	c.operand(lexTok{kind: lexOperand, op: opEnd})
	if err := c.evaluntil(prStart); err != nil {
		return nil, err
	}
	if c.nbra != 0 {
		return nil, errLeftPar
	}
	if len(c.ands) == 0 {
		return nil, errBadRegexp
	}
	p.start = c.ands[len(c.ands)-1].first
	p.optimize()
	return p, nil
}
