package mocks

import (
	"context"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/executil"
)

type ExecUtilWhenPredicate func(args executil.RunArgs) bool

// MockExecUtil is used to register and implement mock calls and responses out to dependent CLI applications
type MockExecUtil struct {
	expressions []*CommandExpression
}

// Creates a new instance of a mock executil
func NewMockExecUtil() *MockExecUtil {
	return &MockExecUtil{
		expressions: []*CommandExpression{},
	}
}

// The executil RunWithResult definition that matches the real function definition
// This implementation will find the first matching mocked expression and return the configured response or error
func (m *MockExecUtil) RunWithResult(ctx context.Context, args executil.RunArgs) (executil.RunResult, error) {
	var match *CommandExpression

	for _, expr := range m.expressions {
		if expr.predicateFn(args) {
			match = expr
			break
		}
	}

	if match == nil {
		panic(fmt.Sprintf("No mock found for command: '%s %s'", args.Cmd, strings.Join(args.Args, " ")))
	}

	return match.response, match.error
}

// Registers a mock expression against the mock executil
func (m *MockExecUtil) When(predicate ExecUtilWhenPredicate) *CommandExpression {
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
	error       error
	executil    *MockExecUtil
	predicateFn ExecUtilWhenPredicate
}

// Sets the response that will be returned for the current expression
func (e *CommandExpression) Respond(response executil.RunResult) *MockExecUtil {
	e.response = response
	return e.executil
}

// Sets the error that will be returned for the current expression
func (e *CommandExpression) SetError(err error) *MockExecUtil {
	e.error = err
	return e.executil
}
