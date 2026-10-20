// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"fmt"
	"regexp"
	"strings"
)

// This deliberately bounded ARM condition grammar supports documented role
// delegation request attributes. Unknown syntax/attributes never imply approval.
var conditionToken = regexp.MustCompile(
	`^\s*(@(?:Request|Resource)\[[^\]]+\]|'[^']*'|[(){},!]|[A-Za-z0-9_.*:/-]+)`,
)

type conditionParser struct {
	tokens []string
	index  int
	op     Operation
}

func evaluateDelegation(expression string, op Operation) (Status, error) {
	var tokens []string
	for strings.TrimSpace(expression) != "" {
		match := conditionToken.FindStringSubmatch(expression)
		if len(match) != 2 {
			return Unverified, fmt.Errorf("unsupported condition token")
		}
		tokens = append(tokens, match[1])
		expression = expression[len(match[0]):]
	}
	parser := conditionParser{tokens: tokens, op: op}
	status, err := parser.or()
	if err != nil || parser.index != len(tokens) {
		return Unverified, fmt.Errorf("unsupported or incomplete delegation expression")
	}
	return status, nil
}

func (p *conditionParser) take(token string) bool {
	if p.index < len(p.tokens) && strings.EqualFold(p.tokens[p.index], token) {
		p.index++
		return true
	}
	return false
}

func (p *conditionParser) next() string {
	if p.index == len(p.tokens) {
		return ""
	}
	token := p.tokens[p.index]
	p.index++
	return token
}

func (p *conditionParser) or() (Status, error) {
	left, err := p.and()
	for err == nil && p.take("OR") {
		var right Status
		right, err = p.and()
		if left == Pass || right == Pass {
			left = Pass
		} else if left == Unverified || right == Unverified {
			left = Unverified
		} else {
			left = Fail
		}
	}
	return left, err
}

func (p *conditionParser) and() (Status, error) {
	left, err := p.atom()
	for err == nil && p.take("AND") {
		var right Status
		right, err = p.atom()
		if left == Fail || right == Fail {
			left = Fail
		} else if left == Unverified || right == Unverified {
			left = Unverified
		} else {
			left = Pass
		}
	}
	return left, err
}

func (p *conditionParser) atom() (Status, error) {
	if p.take("!") || p.take("NOT") {
		status, err := p.atom()
		if status == Pass {
			return Fail, err
		}
		if status == Fail {
			return Pass, err
		}
		return Unverified, err
	}
	if p.take("(") {
		status, err := p.or()
		if !p.take(")") {
			return Unverified, fmt.Errorf("missing closing parenthesis")
		}
		return status, err
	}
	if p.take("ActionMatches") {
		values, err := p.values()
		if err != nil {
			return Unverified, err
		}
		for _, value := range values {
			if actionMatches(value, p.op.Action) {
				return Pass, nil
			}
		}
		return Fail, nil
	}
	attribute := p.next()
	operator := p.next()
	values, err := p.values()
	if err != nil {
		return Unverified, err
	}
	value := ""
	const prefix = "@request[microsoft.authorization/roleassignments:"
	switch strings.ToLower(attribute) {
	case prefix + "roledefinitionid]":
		value = p.op.RoleID
		if i := strings.LastIndex(value, "/"); i >= 0 {
			value = value[i+1:]
		}
	case prefix + "principalid]":
		value = p.op.PrincipalID
	case prefix + "principaltype]":
		value = p.op.PrincipalType
		if value == "" {
			value = "ServicePrincipal"
		}
	default:
		return Unverified, nil
	}
	operator = strings.TrimPrefix(strings.ToLower(operator), "foranyofanyvalues:")
	if operator != "guidequals" && operator != "stringequalsignorecase" && operator != "stringequals" {
		return Unverified, nil
	}
	if value == "" {
		return Unverified, nil
	}
	for _, expected := range values {
		if operator == "stringequals" && value == expected ||
			operator != "stringequals" && strings.EqualFold(value, expected) {
			return Pass, nil
		}
	}
	return Fail, nil
}

func (p *conditionParser) values() ([]string, error) {
	braced := p.take("{")
	var values []string
	for {
		value := p.next()
		if value == "" || strings.ContainsAny(value, "(){},!") || strings.HasPrefix(value, "@") {
			return nil, fmt.Errorf("missing or unsupported comparison value")
		}
		values = append(values, strings.Trim(value, "'"))
		if !braced {
			return values, nil
		}
		if p.take("}") {
			return values, nil
		}
		if !p.take(",") {
			return nil, fmt.Errorf("missing comma or closing value set")
		}
	}
}
