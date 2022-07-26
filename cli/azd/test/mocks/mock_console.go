package mocks

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
)

type MockConsole struct {
	expressions []MockConsoleExpression
}

func (c *MockConsole) Message(ctx context.Context, message string) error {
	return nil
}

func (c *MockConsole) Confirm(ctx context.Context, options input.ConsoleOptions) (bool, error) {
	return true, nil
}

func (c *MockConsole) Prompt(ctx context.Context, options input.ConsoleOptions) (string, error) {
	return "", nil
}

func (c *MockConsole) Select(ctx context.Context, options input.ConsoleOptions) (string, error) {
	return "", nil
}

func (c *MockConsole) PromptLocation(ctx context.Context, message string) (string, error) {
	return "", nil
}

func (c *MockConsole) PromptTemplate(ctx context.Context, message string) (string, error) {
	return "", nil
}

func (c *MockConsole) WhenPromptLocation() *MockConsoleExpression {
	expression := &MockConsoleExpression{
		Command: "PromptLocation",
		console: c,
	}

	return expression
}

func (c *MockConsole) WhenPromptTemplate() *MockConsoleExpression {
	expression := &MockConsoleExpression{
		Command: "PromptTemplate",
		console: c,
	}

	return expression
}

type MockConsoleExpression struct {
	Command  string
	Response string
	console  *MockConsole
}

func (e *MockConsoleExpression) Respond(value string) *MockConsole {
	e.Response = value
	return e.console
}
