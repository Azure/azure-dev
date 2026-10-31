// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"errors"
	"strings"
)

type profileExpression struct {
	tokens   []string
	position int
	selected map[string]bool
}

func matchesProfile(text string, selected map[string]bool) (bool, error) {
	if strings.Contains(text, ",") {
		matched := false
		for expression := range strings.SplitSeq(text, ",") {
			value, err := matchesProfile(expression, selected)
			if err != nil {
				return false, err
			}
			matched = matched || value
		}
		return matched, nil
	}
	spaced := strings.NewReplacer("(", " ( ", ")", " ) ", "!", " ! ", "&", " & ", "|", " | ").Replace(text)
	p := profileExpression{tokens: strings.Fields(spaced), selected: selected}
	if len(p.tokens) == 0 || len(p.tokens) > 128 {
		return false, errors.New("invalid profile expression")
	}
	value, err := p.expression(0)
	if err != nil || p.position != len(p.tokens) {
		return false, errors.New("invalid profile expression")
	}
	return value, nil
}

func (p *profileExpression) expression(depth int) (bool, error) {
	value, andUsed, err := p.term(depth)
	if err != nil {
		return false, err
	}
	for p.take("|") {
		if andUsed {
			return false, errors.New("mixed profile operators require parentheses")
		}
		right, andUsed, err := p.term(depth)
		if err != nil {
			return false, err
		}
		if andUsed {
			return false, errors.New("mixed profile operators require parentheses")
		}
		value = value || right
	}
	return value, nil
}

func (p *profileExpression) term(depth int) (bool, bool, error) {
	value, err := p.factor(depth)
	if err != nil {
		return false, false, err
	}
	andUsed := false
	for p.take("&") {
		andUsed = true
		right, err := p.factor(depth)
		if err != nil {
			return false, false, err
		}
		value = value && right
	}
	return value, andUsed, nil
}

func (p *profileExpression) factor(depth int) (bool, error) {
	if depth > 32 || p.position == len(p.tokens) {
		return false, errors.New("invalid profile expression")
	}
	if p.take("!") {
		value, err := p.factor(depth + 1)
		return !value, err
	}
	if p.take("(") {
		value, err := p.expression(depth + 1)
		if err != nil || !p.take(")") {
			return false, errors.New("invalid profile expression")
		}
		return value, nil
	}
	name := p.tokens[p.position]
	p.position++
	if !profileName.MatchString(name) {
		return false, errors.New("invalid profile name")
	}
	return p.selected[name], nil
}

func (p *profileExpression) take(token string) bool {
	if p.position < len(p.tokens) && p.tokens[p.position] == token {
		p.position++
		return true
	}
	return false
}
