package mocks

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
)

// A predicate function definition for registering expressions
type WhenPredicate func(options input.ConsoleOptions) bool

var truePredicateFn WhenPredicate = func(_ input.ConsoleOptions) bool { return true }

// A mock implementation of the input.Console interface
type MockConsole struct {
	expressions []*MockConsoleExpression
	log         []string
}

func NewMockConsole() *MockConsole {
	return &MockConsole{
		expressions: []*MockConsoleExpression{},
	}
}

func (c *MockConsole) Output() []string {
	return c.log
}

// Prints a message to the console
func (c *MockConsole) Message(ctx context.Context, message string) error {
	c.log = append(c.log, message)
	return nil
}

// Prints a confirmation message to the console for the user to confirm
func (c *MockConsole) Confirm(ctx context.Context, options input.ConsoleOptions) (bool, error) {
	c.log = append(c.log, options.Message)
	value, err := c.respond("Confirm", options)
	return value.(bool), err
}

// Writes a single answer prompt to the console for the user to complete
func (c *MockConsole) Prompt(ctx context.Context, options input.ConsoleOptions) (string, error) {
	c.log = append(c.log, options.Message)
	value, err := c.respond("Prompt", options)
	return value.(string), err
}

// Writes a multiple choice selection to the console for the user to choose
func (c *MockConsole) Select(ctx context.Context, options input.ConsoleOptions) (int, error) {
	c.log = append(c.log, options.Message)
	value, err := c.respond("Select", options)
	return value.(int), err
}

// Writes a location selection choice to the console for the user to choose
func (c *MockConsole) PromptLocation(ctx context.Context, message string) (string, error) {
	c.log = append(c.log, message)
	value, err := c.respond("PromptLocation", input.ConsoleOptions{})
	return value.(string), err
}

// Writes a template selection choice to the console for the user to choose
func (c *MockConsole) PromptTemplate(ctx context.Context, message string) (templates.Template, error) {
	c.log = append(c.log, message)
	value, err := c.respond("PromptTemplate", input.ConsoleOptions{})
	return value.(templates.Template), err
}

// Finds a matching mock expression and returns the configured value
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

// Registers a prompt expression for mocking in unit tests
func (c *MockConsole) WhenPrompt(predicate WhenPredicate) *MockConsoleExpression {
	expr := MockConsoleExpression{
		command:     "Prompt",
		console:     c,
		predicateFn: predicate,
	}

	c.expressions = append(c.expressions, &expr)
	return &expr
}

// Registers a confirmation expression for mocking in unit tests
func (c *MockConsole) WhenConfirm(predicate WhenPredicate) *MockConsoleExpression {
	expr := MockConsoleExpression{
		command:     "Confirm",
		console:     c,
		predicateFn: predicate,
	}

	c.expressions = append(c.expressions, &expr)
	return &expr
}

// Registers a multiple choice selection express for mocking in unit tests
func (c *MockConsole) WhenSelect(predicate WhenPredicate) *MockConsoleExpression {
	expr := MockConsoleExpression{
		command:     "Select",
		console:     c,
		predicateFn: predicate,
	}

	c.expressions = append(c.expressions, &expr)
	return &expr
}

// Registers a location prompt selection for mocking in unit tests
func (c *MockConsole) WhenPromptLocation() *MockConsoleExpression {
	expr := MockConsoleExpression{
		command:     "PromptLocation",
		console:     c,
		predicateFn: truePredicateFn,
	}

	c.expressions = append(c.expressions, &expr)
	return &expr
}

// Registers a template prompt selection for mocking in unit tests
func (c *MockConsole) WhenPromptTemplate() *MockConsoleExpression {
	expression := &MockConsoleExpression{
		command:     "PromptTemplate",
		console:     c,
		predicateFn: truePredicateFn,
	}

	return expression
}

// MockConsoleExpression is an expression with options response or error
type MockConsoleExpression struct {
	command     string
	response    any
	error       error
	console     *MockConsole
	predicateFn WhenPredicate
}

// Sets the response that will be returned for the current expression
func (e *MockConsoleExpression) Respond(value any) *MockConsole {
	e.response = value
	return e.console
}

// Sets the error that will be returned for the current expression
func (e *MockConsoleExpression) SetError(err error) *MockConsole {
	e.error = err
	return e.console
}
