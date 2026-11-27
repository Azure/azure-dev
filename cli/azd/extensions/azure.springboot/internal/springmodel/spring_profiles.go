// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package springmodel

import (
	"errors"
	"strings"
	"unicode"
)

type profileExpression struct {
	tokens []string
	next   int
	active map[string]bool
}

func matchesSpringProfileList(expressions []string, profiles []string) (bool, error) {
	if len(expressions) == 0 {
		return false, errors.New("empty Spring profile activation expression")
	}
	matched := false
	for _, expression := range expressions {
		ok, err := matchesSpringProfiles(expression, profiles)
		if err != nil {
			return false, err
		}
		matched = matched || ok
	}
	return matched, nil
}

func matchesSpringProfiles(expression string, profiles []string) (bool, error) {
	parts := strings.Split(expression, ",")
	matched := false
	for _, part := range parts {
		ok, err := matchesSingleSpringProfileExpression(part, profiles)
		if err != nil {
			return false, err
		}
		matched = matched || ok
	}
	return matched, nil
}

func matchesSingleSpringProfileExpression(expression string, profiles []string) (bool, error) {
	tokens := strings.Fields(strings.NewReplacer(
		"(", " ( ", ")", " ) ", "!", " ! ", "&", " & ", "|", " | ",
	).Replace(expression))
	if len(tokens) == 0 {
		return false, errors.New("empty Spring profile activation expression")
	}
	p := profileExpression{tokens: tokens, active: map[string]bool{}}
	for _, profile := range profiles {
		p.active[profile] = true
	}
	result, err := p.parseExpression()
	if err != nil || p.next != len(tokens) {
		return false, errors.New("invalid Spring profile activation expression")
	}
	return result, nil
}

func (p *profileExpression) parseExpression() (bool, error) {
	result, err := p.parseTerm()
	operator := ""
	for err == nil && p.next < len(p.tokens) && (p.tokens[p.next] == "&" || p.tokens[p.next] == "|") {
		current := p.tokens[p.next]
		p.next++
		if operator != "" && operator != current {
			return false, errors.New("Spring profile operators require explicit grouping")
		}
		operator = current
		var next bool
		next, err = p.parseTerm()
		if operator == "&" {
			result = result && next
		} else {
			result = result || next
		}
	}
	return result, err
}

func (p *profileExpression) parseTerm() (bool, error) {
	if p.consume("!") {
		result, err := p.parseTerm()
		return !result, err
	}
	if p.consume("(") {
		result, err := p.parseExpression()
		if !p.consume(")") {
			return false, errors.New("unclosed Spring profile expression")
		}
		return result, err
	}
	if p.next >= len(p.tokens) {
		return false, errors.New("missing Spring profile expression")
	}
	token := p.tokens[p.next]
	p.next++
	if strings.ContainsAny(token, "()!&|") || strings.ContainsFunc(token, unicode.IsSpace) {
		return false, errors.New("invalid Spring profile name")
	}
	return p.active[token], nil
}

func (p *profileExpression) consume(token string) bool {
	if p.next < len(p.tokens) && p.tokens[p.next] == token {
		p.next++
		return true
	}
	return false
}
