package console

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// A predicate function definition for registering expressions
type WhenPredicate func(options input.ConsoleOptions) bool

type SpinnerOpType string

const SpinnerOpShow SpinnerOpType = "show"
const SpinnerOpStop SpinnerOpType = "stop"

type SpinnerOp struct {
	Op      SpinnerOpType
	Message string
	Format  input.SpinnerUxType
}

// A mock implementation of the input.Console interface
type MockConsole struct {
	expressions []*MockConsoleExpression
	log         []string
	spinnerOps  []SpinnerOp
}

func NewMockConsole() *MockConsole {
	return &MockConsole{
		expressions: []*MockConsoleExpression{},
	}
}

func (c *MockConsole) IsUnformatted() bool {
	return true
}

func (c *MockConsole) GetFormatter() output.Formatter {
	return nil
}

func (c *MockConsole) GetWriter() io.Writer {
	return nil
}

func (c *MockConsole) SetWriter(writer io.Writer) {

}

func (c *MockConsole) Output() []string {
	return c.log
}

func (c *MockConsole) SpinnerOps() []SpinnerOp {
	return c.spinnerOps
}

func (c *MockConsole) Handles() input.ConsoleHandles {
	return input.ConsoleHandles{
		Stdout: io.Discard,
		Stderr: io.Discard,
		Stdin:  bytes.NewBufferString(""),
	}
}

// Prints a message to the console
func (c *MockConsole) Message(ctx context.Context, message string) {
	c.log = append(c.log, message)
}

func (c *MockConsole) MessageUx(ctx context.Context, message string, format input.MessageUxType) {
	c.Message(ctx, message)
}

func (c *MockConsole) ShowSpinner(ctx context.Context, title string, format input.SpinnerUxType) {
	c.spinnerOps = append(c.spinnerOps, SpinnerOp{
		Op:      SpinnerOpShow,
		Message: title,
		Format:  format,
	})
}

func (c *MockConsole) StopSpinner(ctx context.Context, lastMessage string, format input.SpinnerUxType) {
	c.spinnerOps = append(c.spinnerOps, SpinnerOp{
		Op:      SpinnerOpStop,
		Message: lastMessage,
		Format:  format,
	})
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

// Writes messages to the underlying writer
func (c *MockConsole) Flush() {
}

// Finds a matching mock expression and returns the configured value
func (c *MockConsole) respond(command string, options input.ConsoleOptions) (any, error) {
	var match *MockConsoleExpression

	for _, expr := range c.expressions {
		if command == expr.command && expr.predicateFn(options) {
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
