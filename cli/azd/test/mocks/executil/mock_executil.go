package executil

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/executil"
)

type CommandWhenPredicate func(args executil.RunArgs, command string) bool

type ResponseFn func(args executil.RunArgs) (executil.RunResult, error)

// MockCommandRunner is used to register and implement mock calls and responses out to dependent CLI applications
type MockCommandRunner struct {
	expressions []*CommandExpression
}

// Creates a new instance of a mock executil
func NewMockCommandRunner() *MockCommandRunner {
	return &MockCommandRunner{
		expressions: []*CommandExpression{},
	}
}

// The executil RunWithResult definition that matches the real function definition
// This implementation will find the first matching mocked expression and return the configured response or error
func (m *MockCommandRunner) RunWithResult(ctx context.Context, args executil.RunArgs) (executil.RunResult, error) {
	var match *CommandExpression

	cmdArgs := []string{args.Cmd}
	cmdArgs = append(cmdArgs, args.Args...)
	command := strings.Join(cmdArgs, " ")

	for _, expr := range m.expressions {
		if expr.predicateFn(args, command) {
			match = expr
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

// Registers a mock expression against the mock executil
func (m *MockCommandRunner) When(predicate CommandWhenPredicate) *CommandExpression {
	expr := CommandExpression{
		executil:    m,
		predicateFn: predicate,
	}

	m.expressions = append(m.expressions, &expr)
	return &expr
}

// Represents an mocked expression against a dependent tool command
type CommandExpression struct {
	Command     string
	response    executil.RunResult
	responseFn  ResponseFn
	error       error
	executil    *MockCommandRunner
	predicateFn CommandWhenPredicate
}

// Sets the response that will be returned for the current expression
func (e *CommandExpression) Respond(response executil.RunResult) *MockCommandRunner {
	e.response = response
	return e.executil
}

// Sets the response that will be returned for the current expression
func (e *CommandExpression) RespondFn(responseFn ResponseFn) *MockCommandRunner {
	e.responseFn = responseFn
	return e.executil
}

// Sets the error that will be returned for the current expression
func (e *CommandExpression) SetError(err error) *MockCommandRunner {
	e.error = err
	return e.executil
}

func AddAzLoginMocks(execUtil *MockCommandRunner) {
	execUtil.When(func(args executil.RunArgs, command string) bool {
		return strings.Contains(command, "az account get-access-token")
	}).RespondFn(func(args executil.RunArgs) (executil.RunResult, error) {
		now := time.Now().UTC().Format(time.RFC3339)
		requestJson := fmt.Sprintf(`{"AccessToken": "abc123", "ExpiresOn": "%s"}`, now)
		return executil.NewRunResult(0, requestJson, ""), nil
	})
}
