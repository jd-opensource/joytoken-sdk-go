package tooldef

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// EvalExpression evaluates a self-contained arithmetic expression and returns
// the numeric result. It is the single source of truth for expression
// evaluation, reused by both the Calculator tool here and higher-level packages
// (e.g. agent/toolkit) so the parsing logic is never duplicated.
func EvalExpression(input string) (float64, error) {
	return evalExpression(input)
}

// evalExpression evaluates an arithmetic expression using a recursive-descent
// parser. It supports + - * / % operators, unary minus, parentheses and
// floating-point literals, with no external dependencies.
//
// Grammar:
//
//	expr   = term { ("+" | "-") term }
//	term   = factor { ("*" | "/" | "%") factor }
//	factor = number | "(" expr ")" | ("+" | "-") factor
func evalExpression(input string) (float64, error) {
	p := &exprParser{input: input}
	p.skipSpaces()
	if p.pos >= len(p.input) {
		return 0, fmt.Errorf("empty expression")
	}
	value, err := p.parseExpr()
	if err != nil {
		return 0, err
	}
	p.skipSpaces()
	if p.pos != len(p.input) {
		return 0, fmt.Errorf("unexpected character %q at position %d", p.input[p.pos], p.pos)
	}
	return value, nil
}

type exprParser struct {
	input string
	pos   int
}

func (p *exprParser) skipSpaces() {
	for p.pos < len(p.input) && unicode.IsSpace(rune(p.input[p.pos])) {
		p.pos++
	}
}

func (p *exprParser) peek() byte {
	if p.pos < len(p.input) {
		return p.input[p.pos]
	}
	return 0
}

func (p *exprParser) parseExpr() (float64, error) {
	value, err := p.parseTerm()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpaces()
		op := p.peek()
		if op != '+' && op != '-' {
			return value, nil
		}
		p.pos++
		right, err := p.parseTerm()
		if err != nil {
			return 0, err
		}
		if op == '+' {
			value += right
		} else {
			value -= right
		}
	}
}

func (p *exprParser) parseTerm() (float64, error) {
	value, err := p.parseFactor()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpaces()
		op := p.peek()
		if op != '*' && op != '/' && op != '%' {
			return value, nil
		}
		p.pos++
		right, err := p.parseFactor()
		if err != nil {
			return 0, err
		}
		switch op {
		case '*':
			value *= right
		case '/':
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			value /= right
		case '%':
			if right == 0 {
				return 0, fmt.Errorf("modulo by zero")
			}
			value = float64(int64(value) % int64(right))
		}
	}
}

func (p *exprParser) parseFactor() (float64, error) {
	p.skipSpaces()
	switch p.peek() {
	case '+':
		p.pos++
		return p.parseFactor()
	case '-':
		p.pos++
		value, err := p.parseFactor()
		if err != nil {
			return 0, err
		}
		return -value, nil
	case '(':
		p.pos++
		value, err := p.parseExpr()
		if err != nil {
			return 0, err
		}
		p.skipSpaces()
		if p.peek() != ')' {
			return 0, fmt.Errorf("expected ')' at position %d", p.pos)
		}
		p.pos++
		return value, nil
	default:
		return p.parseNumber()
	}
}

func (p *exprParser) parseNumber() (float64, error) {
	p.skipSpaces()
	start := p.pos
	for p.pos < len(p.input) {
		c := p.input[p.pos]
		if (c >= '0' && c <= '9') || c == '.' {
			p.pos++
			continue
		}
		break
	}
	literal := strings.TrimSpace(p.input[start:p.pos])
	if literal == "" {
		return 0, fmt.Errorf("expected number at position %d", start)
	}
	value, err := strconv.ParseFloat(literal, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number %q", literal)
	}
	return value, nil
}