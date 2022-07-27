package mocks

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
)

type WhenPredicate func(options input.ConsoleOptions) bool

var truePredicateFn WhenPredicate = func(_ input.ConsoleOptions) bool { return true }

type MockConsole struct {
	expressions []MockConsoleExpression
}

func NewMockConsole() *MockConsole {
	return &MockConsole{
		expressions: []MockConsoleExpression{},
	}
}

func (c *MockConsole) Message(ctx context.Context, message string) error {
	return nil
}

func (c *MockConsole) Confirm(ctx context.Context, options input.ConsoleOptions) (bool, error) {
	value := c.respond("Confirm", options)
	return value.(bool), nil
}

func (c *MockConsole) Prompt(ctx context.Context, options input.ConsoleOptions) (string, error) {
	value := c.respond("Prompt", options)
	return value.(string), nil
}

func (c *MockConsole) Select(ctx context.Context, options input.ConsoleOptions) (string, error) {
	value := c.respond("Select", options)
	return value.(string), nil
}

func (c *MockConsole) PromptLocation(ctx context.Context, message string) (string, error) {
	value := c.respond("PromptLocation", input.ConsoleOptions{})
	return value.(string), nil
}

func (c *MockConsole) PromptTemplate(ctx context.Context, message string) (string, error) {
	value := c.respond("PromptTemplate", input.ConsoleOptions{})
	return value.(string), nil
}

func (c *MockConsole) findMatchingExpression(command string, options input.ConsoleOptions) *MockConsoleExpression {
	for _, expr := range c.expressions {
		if expr.Command == command && expr.predicateFn(options) {
			return &expr
		}
	}

	return nil
}

func (c *MockConsole) respond(command string, options input.ConsoleOptions) any {
	expr := c.findMatchingExpression(command, options)
	if expr == nil {
		return nil
	}

	return expr.Response
}

func (c *MockConsole) WhenPrompt(predicate WhenPredicate) *MockConsoleExpression {
	expr := MockConsoleExpression{
		Command:     "Prompt",
		console:     c,
		predicateFn: predicate,
	}

	c.expressions = append(c.expressions, expr)
	return &expr
}

func (c *MockConsole) WhenConfirm(predicate WhenPredicate) *MockConsoleExpression {
	expr := MockConsoleExpression{
		Command:     "Confirm",
		console:     c,
		predicateFn: predicate,
	}

	c.expressions = append(c.expressions, expr)
	return &expr
}

func (c *MockConsole) WhenSelect(predicate WhenPredicate) *MockConsoleExpression {
	expr := MockConsoleExpression{
		Command:     "Select",
		console:     c,
		predicateFn: predicate,
	}

	c.expressions = append(c.expressions, expr)
	return &expr
}

func (c *MockConsole) WhenPromptLocation() *MockConsoleExpression {
	expr := MockConsoleExpression{
		Command:     "PromptLocation",
		console:     c,
		predicateFn: truePredicateFn,
	}

	c.expressions = append(c.expressions, expr)
	return &expr
}

func (c *MockConsole) WhenPromptTemplate() *MockConsoleExpression {
	expression := &MockConsoleExpression{
		Command:     "PromptTemplate",
		console:     c,
		predicateFn: truePredicateFn,
	}

	return expression
}

type MockConsoleExpression struct {
	Command     string
	Response    any
	console     *MockConsole
	predicateFn WhenPredicate
}

func (e *MockConsoleExpression) Respond(value any) *MockConsole {
	e.Response = value
	return e.console
}
