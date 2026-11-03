// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package mockexec

import (
	"context"
	"fmt"
	osexec "os/exec"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
)

type CommandWhenPredicate func(args exec.RunArgs, command string) bool

type ResponseFn func(args exec.RunArgs) (exec.RunResult, error)

// ContextResponseFn produces a mock response with access to the command context.
type ContextResponseFn func(ctx context.Context, args exec.RunArgs) (exec.RunResult, error)

// MockCommandRunner is used to register and implement mock calls and responses out to dependent CLI applications
type MockCommandRunner struct {
	expressions     []*CommandExpression
	toolInPathMocks map[string]error
}

// Creates a new instance of a mock exec
func NewMockCommandRunner() *MockCommandRunner {
	return &MockCommandRunner{
		expressions:     []*CommandExpression{},
		toolInPathMocks: make(map[string]error),
	}
}

// The Run definition that matches the real function definition
// This implementation will find the first matching, most recent mocked expression and return the configured response or
// error
func (m *MockCommandRunner) Run(ctx context.Context, args exec.RunArgs) (exec.RunResult, error) {
	cmdArgs := []string{args.Cmd}
	cmdArgs = append(cmdArgs, args.Args...)
	command := strings.Join(cmdArgs, " ")

	return m.run(ctx, args, command)
}

func (m *MockCommandRunner) RunList(ctx context.Context, commands []string, args exec.RunArgs) (exec.RunResult, error) {
	args.Args = commands
	command := strings.Join(commands, " && ")

	return m.run(ctx, args, command)
}

func (m *MockCommandRunner) run(
	ctx context.Context,
	args exec.RunArgs,
	command string,
) (exec.RunResult, error) {
	if cause := context.Cause(ctx); cause != nil {
		return exec.RunResult{}, cause
	}

	var match *CommandExpression
	for _, expression := range slices.Backward(m.expressions) {
		if expression.predicateFn(args, command) {
			match = expression
			break
		}
	}

	if match == nil {
		panic(fmt.Sprintf("No mock found for command: '%s'", command))
	}

	if match.contextResponseFn != nil {
		return match.contextResponseFn(ctx, args)
	}
	if match.responseFn != nil {
		return match.responseFn(args)
	}

	return match.response, match.error
}

// ToolInPath implements the ToolInPath method of CommandRunner interface for mocking
func (m *MockCommandRunner) ToolInPath(name string) error {
	if err, exists := m.toolInPathMocks[name]; exists {
		return err
	}
	// Fallback to real implementation using osexec.LookPath
	_, err := osexec.LookPath(name)
	return err
}

// MockToolInPath allows setting up mock responses for ToolInPath calls
func (m *MockCommandRunner) MockToolInPath(name string, err error) {
	m.toolInPathMocks[name] = err
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

// Represents a mocked expression against a dependent tool command
type CommandExpression struct {
	response          exec.RunResult
	responseFn        ResponseFn
	contextResponseFn ContextResponseFn

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

// RespondContextFn sets a context-aware response for the current expression.
func (e *CommandExpression) RespondContextFn(responseFn ContextResponseFn) *MockCommandRunner {
	e.contextResponseFn = responseFn
	return e.exec
}

// Sets the error that will be returned for the current expression
func (e *CommandExpression) SetError(err error) *MockCommandRunner {
	e.error = err
	return e.exec
}
