package mocks

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
)

type WhenPredicate func(options input.ConsoleOptions) bool

var truePredicateFn WhenPredicate = func(_ input.ConsoleOptions) bool { return true }

type MockConsole struct {
	expressions []*MockConsoleExpression
}

func NewMockConsole() *MockConsole {
	return &MockConsole{
		expressions: []*MockConsoleExpression{},
	}
}

func (c *MockConsole) Message(ctx context.Context, message string) error {
	return nil
}

func (c *MockConsole) Confirm(ctx context.Context, options input.ConsoleOptions) (bool, error) {
	value, err := c.respond("Confirm", options)
	return value.(bool), err
}

func (c *MockConsole) Prompt(ctx context.Context, options input.ConsoleOptions) (string, error) {
	value, err := c.respond("Prompt", options)
	return value.(string), err
}

func (c *MockConsole) Select(ctx context.Context, options input.ConsoleOptions) (string, error) {
	value, err := c.respond("Select", options)
	return value.(string), err
}

func (c *MockConsole) PromptLocation(ctx context.Context, message string) (string, error) {
	value, err := c.respond("PromptLocation", input.ConsoleOptions{})
	return value.(string), err
}

func (c *MockConsole) PromptTemplate(ctx context.Context, message string) (string, error) {
	value, err := c.respond("PromptTemplate", input.ConsoleOptions{})
	return value.(string), err
}

func (c *MockConsole) findMatchingExpression(command string, options input.ConsoleOptions) *MockConsoleExpression {
	for _, expr := range c.expressions {
		if expr.command == command && expr.predicateFn(options) {
			return expr
		}
	}

	return nil
}

func (c *MockConsole) respond(command string, options input.ConsoleOptions) (any, error) {
	var match *MockConsoleExpression

	for _, expr := range c.expressions {
		if expr.predicateFn(options) {
			match = expr
			break
		}
	}

	if match == nil {
		panic(fmt.Sprintf("No mock found for command: '%s'", command))
	}

	return match.response, match.error
}

func (c *MockConsole) WhenPrompt(predicate WhenPredicate) *MockConsoleExpression {
	expr := MockConsoleExpression{
		command:     "Prompt",
		console:     c,
		predicateFn: predicate,
	}

	c.expressions = append(c.expressions, &expr)
	return &expr
}

func (c *MockConsole) WhenConfirm(predicate WhenPredicate) *MockConsoleExpression {
	expr := MockConsoleExpression{
		command:     "Confirm",
		console:     c,
		predicateFn: predicate,
	}

	c.expressions = append(c.expressions, &expr)
	return &expr
}

func (c *MockConsole) WhenSelect(predicate WhenPredicate) *MockConsoleExpression {
	expr := MockConsoleExpression{
		command:     "Select",
		console:     c,
		predicateFn: predicate,
	}

	c.expressions = append(c.expressions, &expr)
	return &expr
}

func (c *MockConsole) WhenPromptLocation() *MockConsoleExpression {
	expr := MockConsoleExpression{
		command:     "PromptLocation",
		console:     c,
		predicateFn: truePredicateFn,
	}

	c.expressions = append(c.expressions, &expr)
	return &expr
}

func (c *MockConsole) WhenPromptTemplate() *MockConsoleExpression {
	expression := &MockConsoleExpression{
		command:     "PromptTemplate",
		console:     c,
		predicateFn: truePredicateFn,
	}

	return expression
}

type MockConsoleExpression struct {
	command     string
	response    any
	error       error
	console     *MockConsole
	predicateFn WhenPredicate
}

func (e *MockConsoleExpression) Respond(value any) *MockConsole {
	e.response = value
	return e.console
}

func (e *MockConsoleExpression) SetError(err error) *MockConsole {
	e.error = err
	return e.console
}
