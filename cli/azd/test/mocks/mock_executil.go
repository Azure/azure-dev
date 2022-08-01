package mocks

import (
	"context"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/executil"
)

type ExecUtilWhenPredicate func(args executil.RunArgs) bool

type MockExecUtil struct {
	expressions []*CommandExpression
}

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

	return match.Response, match.Error
}

func (m *MockExecUtil) When(predicate ExecUtilWhenPredicate) *CommandExpression {
	expr := CommandExpression{
		executil:    m,
		predicateFn: predicate,
	}

	m.expressions = append(m.expressions, &expr)
	return &expr
}

type CommandExpression struct {
	Command     string
	Response    executil.RunResult
	Error       error
	executil    *MockExecUtil
	predicateFn ExecUtilWhenPredicate
}

func (e *CommandExpression) Respond(response executil.RunResult) *MockExecUtil {
	e.Response = response
	return e.executil
}

func (e *CommandExpression) SetError(err error) *MockExecUtil {
	e.Error = err
	return e.executil
}
