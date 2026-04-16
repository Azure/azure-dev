// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package workflow

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

type mockCommandRunner struct {
	execFn func(ctx context.Context, args []string) error
}

func (m *mockCommandRunner) ExecuteContext(ctx context.Context, args []string) error {
	return m.execFn(ctx, args)
}

func TestRunner_Run_StopsOnErrAbortedByUser(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	stepsCalled := []string{}

	runner := NewRunner(&mockCommandRunner{
		execFn: func(ctx context.Context, args []string) error {
			stepsCalled = append(stepsCalled, args[0])
			if args[0] == "provision" {
				return internal.ErrAbortedByUser
			}
			return nil
		},
	}, mockContext.Console)

	workflow := &Workflow{
		Name: "up",
		Steps: []*Step{
			{AzdCommand: Command{Args: []string{"package", "--all"}}},
			{AzdCommand: Command{Args: []string{"provision"}}},
			{AzdCommand: Command{Args: []string{"deploy", "--all"}}},
		},
	}

	err := runner.Run(*mockContext.Context, workflow)

	// ErrAbortedByUser should propagate without wrapping
	require.ErrorIs(t, err, internal.ErrAbortedByUser)
	// "deploy" should NOT have been called
	require.Equal(t, []string{"package", "provision"}, stepsCalled)
}

func TestRunner_Run_ErrAbortedByUser_NotWrapped(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	runner := NewRunner(&mockCommandRunner{
		execFn: func(ctx context.Context, args []string) error {
			return internal.ErrAbortedByUser
		},
	}, mockContext.Console)

	workflow := &Workflow{
		Name: "test",
		Steps: []*Step{
			{AzdCommand: Command{Args: []string{"provision"}}},
		},
	}

	err := runner.Run(*mockContext.Context, workflow)

	// The error should be exactly ErrAbortedByUser, not wrapped
	require.ErrorIs(t, err, internal.ErrAbortedByUser)
	require.Equal(t, internal.ErrAbortedByUser.Error(), err.Error())
}

func TestRunner_Run_OtherErrors_AreWrapped(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	originalErr := errors.New("some deployment error")

	runner := NewRunner(&mockCommandRunner{
		execFn: func(ctx context.Context, args []string) error {
			return originalErr
		},
	}, mockContext.Console)

	workflow := &Workflow{
		Name: "test",
		Steps: []*Step{
			{AzdCommand: Command{Args: []string{"provision"}}},
		},
	}

	err := runner.Run(*mockContext.Context, workflow)

	// Other errors should be wrapped with step context
	require.ErrorIs(t, err, originalErr)
	require.Contains(t, err.Error(), "error executing step command 'provision'")
}

func TestRunner_Run_AllStepsSucceed(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	stepsCalled := []string{}

	runner := NewRunner(&mockCommandRunner{
		execFn: func(ctx context.Context, args []string) error {
			stepsCalled = append(stepsCalled, args[0])
			return nil
		},
	}, mockContext.Console)

	workflow := &Workflow{
		Name: "up",
		Steps: []*Step{
			{AzdCommand: Command{Args: []string{"package"}}},
			{AzdCommand: Command{Args: []string{"provision"}}},
			{AzdCommand: Command{Args: []string{"deploy"}}},
		},
	}

	err := runner.Run(*mockContext.Context, workflow)

	require.NoError(t, err)
	require.Equal(t, []string{"package", "provision", "deploy"}, stepsCalled)
}

func TestRunner_Run_WrappedErrAbortedByUser(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	runner := NewRunner(&mockCommandRunner{
		execFn: func(ctx context.Context, args []string) error {
			return fmt.Errorf("inner context: %w", internal.ErrAbortedByUser)
		},
	}, mockContext.Console)

	workflow := &Workflow{
		Name: "test",
		Steps: []*Step{
			{AzdCommand: Command{Args: []string{"provision"}}},
		},
	}

	err := runner.Run(*mockContext.Context, workflow)

	// Even when wrapped, errors.Is should detect it and the runner should not add more wrapping
	require.ErrorIs(t, err, internal.ErrAbortedByUser)
	require.NotContains(t, err.Error(), "error executing step command")
}
