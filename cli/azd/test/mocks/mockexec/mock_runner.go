package mockexec

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
)

type CommandWhenPredicate func(args exec.RunArgs, command string) bool

type ResponseFn func(args exec.RunArgs) (exec.RunResult, error)

// MockCommandRunner is used to register and implement mock calls and responses out to dependent CLI applications
type MockCommandRunner struct {
	expressions []*CommandExpression
}

// Creates a new instance of a mock exec
func NewMockCommandRunner() *MockCommandRunner {
	return &MockCommandRunner{
		expressions: []*CommandExpression{},
	}
}

// The Run definition that matches the real function definition
// This implementation will find the first matching, most recent mocked expression and return the configured response or
// error
func (m *MockCommandRunner) Run(ctx context.Context, args exec.RunArgs) (exec.RunResult, error) {
	var match *CommandExpression

	cmdArgs := []string{args.Cmd}
	cmdArgs = append(cmdArgs, args.Args...)
	command := strings.Join(cmdArgs, " ")

	for i := len(m.expressions) - 1; i >= 0; i-- {
		if m.expressions[i].predicateFn(args, command) {
			match = m.expressions[i]
			break
		}
	}

	if match == nil {
		panic(fmt.Sprintf("No mock found for command: '%s %s'", args.Cmd, strings.Join(args.Args, " ")))
	}

	// If the response function has been set, return the value
	if match.responseFn != nil {
		return match.responseFn(args)
	}

	return match.response, match.error
}

// Registers a mock expression against the mock exec
func (m *MockCommandRunner) When(predicate CommandWhenPredicate) *CommandExpression {
	expr := CommandExpression{
		exec:        m,
		predicateFn: predicate,
	}

	m.expressions = append(m.expressions, &expr)
	return &expr
}

// Represents an mocked expression against a dependent tool command
type CommandExpression struct {
	response   exec.RunResult
	responseFn ResponseFn

	error       error
	exec        *MockCommandRunner
	predicateFn CommandWhenPredicate
}

// Sets the response that will be returned for the current expression
func (e *CommandExpression) Respond(response exec.RunResult) *MockCommandRunner {
	e.response = response
	return e.exec
}

// Sets the response that will be returned for the current expression
func (e *CommandExpression) RespondFn(responseFn ResponseFn) *MockCommandRunner {
	e.responseFn = responseFn
	return e.exec
}

// Sets the error that will be returned for the current expression
func (e *CommandExpression) SetError(err error) *MockCommandRunner {
	e.error = err
	return e.exec
}

func AddAzLoginMocks(commandRunner *MockCommandRunner) {
	commandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "az account get-access-token")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		now := time.Now().UTC().Format(time.RFC3339)
		requestJson := fmt.Sprintf(`{"AccessToken": "abc123", "ExpiresOn": "%s"}`, now)
		return exec.NewRunResult(0, requestJson, ""), nil
	})
}
