// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package scaffold

import (
	"bytes"
	"fmt"
)

type Kind uint32

const (
	PropertyExpr Kind = 1 << iota
	SpecExpr
	VaultExpr
	VarExpr
)

const (
	DotToken   = ""
	SpecToken  = "spec"
	VaultToken = "vault"
)

type PropertyExprData struct {
	// The dotted property path
	PropertyPath string
}

type SpecExprData struct {
	// The dotted property path
	PropertyPath string
}

type VaultExprData struct {
	// The vault secret path to the value of the expression
	SecretPath string
}

type VarExprData struct {
	// The name of the variable
	Name string
}

type expression struct {
	// The kind of expression.
	Kind Kind

	// The data associated with the kind of expression.
	Data any

	// The template that this expression is a part of.
	t *tmpl
	// The start and end positions of the expression in the template.
	start int
	end   int
}

func (e *expression) Replace(val string) {
	e.t.Replace(e, val)
}

const (
	DotChar      byte = '.'
	DoubleQuotes byte = '"'
)

// parser for a dot-like expression.
type parser struct {
	// The string to parse.
	s string

	// The terminal byte that ends the expression.
	terminal byte

	// The current cursor position.
	cursor int

	// The seen buffer.
	seen bytes.Buffer
}

func (p *parser) peek() byte {
	if p.cursor >= len(p.s) {
		return 0 // end of string
	}

	return p.s[p.cursor]
}

func (p *parser) next() byte {
	p.cursor++
	return p.peek()
}

func (p *parser) until(b byte) byte {
	p.seen.Reset()

	c := p.peek()
	for {
		if c == 0 || c == b {
			break
		}

		p.seen.WriteByte(c)
		c = p.next()
	}

	return c
}

func (p *parser) parseExpression() (*expression, error) {
	seen := bytes.Buffer{}
	var expr *expression
	for c := p.peek(); c != 0; c = p.next() {
		switch c {
		case '.':
			p.next() // skip the '.' character
			switch seen.String() {
			case VaultToken:
				expr = &expression{Kind: VaultExpr}
				p.until(p.terminal)

				path := p.seen.String()
				expr.Data = VaultExprData{
					SecretPath: path,
				}
				return expr, nil
			case SpecToken:
				expr = &expression{Kind: SpecExpr}
				p.until(p.terminal)

				path := p.seen.String()
				expr.Data = SpecExprData{
					PropertyPath: path,
				}
				return expr, nil
			case "":
				expr = &expression{Kind: PropertyExpr}
				p.until(p.terminal)

				path := p.seen.String()
				expr.Data = PropertyExprData{
					PropertyPath: path,
				}
				return expr, nil
			default:
				return nil, fmt.Errorf("unknown expression: %s", seen.String())
			}
			// parse qualifier
		case 0, p.terminal: // we're done
			expr = &expression{Kind: VarExpr}
			expr.Data = VarExprData{
				Name: seen.String(),
			}
			return expr, nil
		}

		seen.WriteByte(byte(c))
	}

	return nil, nil
}

// tmpl represents a string with placeholders and their replacements
type tmpl struct {
	// raw is the pointer to the raw string with expressions
	raw *string
	// rawOffset is the offset of the raw string due to replacements from expressions
	rawOffset int
	// expressions are the parsed expressions in the template
	expressions []expression
}

func (t *tmpl) Replace(expr *expression, val string) {
	raw := *t.raw
	raw = raw[:expr.start+t.rawOffset] + val + raw[expr.end+t.rawOffset:]

	*t.raw = raw
	t.rawOffset += len(val) - (expr.end - expr.start)
}

func parseExpressions(s *string) ([]expression, error) {
	var t *tmpl
	prev := rune(0)
	val := *s
	for i, c := range val {
		switch c {
		case '$':
			prev = c
			if prev == '$' { // escape character, reset prev to avoid parsing
				prev = 0
			}
		case '{':
			if prev == '$' {
				p := parser{s: val[i+1:], terminal: '}'}
				expr, err := p.parseExpression()
				if err != nil || expr == nil {
					return nil, err
				}

				if t == nil {
					t = &tmpl{
						raw: s,
					}
				}

				expr.start = i - 1                // start of '${'
				expr.end = (i + 1) + p.cursor + 1 // end of '}'
				expr.t = t
				t.expressions = append(t.expressions, *expr)
			}
		}

		prev = c
	}

	if t == nil {
		return nil, nil
	}

	return t.expressions, nil
}
