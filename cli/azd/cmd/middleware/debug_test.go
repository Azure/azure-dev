// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/require"
)

func TestNewDebugMiddleware(t *testing.T) {
	mc := mockinput.NewMockConsole()
	m := NewDebugMiddleware(&Options{}, mc)
	require.NotNil(t, m)

	dm, ok := m.(*DebugMiddleware)
	require.True(t, ok)
	require.NotNil(t, dm.options)
	require.NotNil(t, dm.console)
}

func TestDebugMiddleware_Run_ChildAction(t *testing.T) {
	mc := mockinput.NewMockConsole()
	m := &DebugMiddleware{
		options: &Options{CommandPath: "test"},
		console: mc,
	}

	nextCalled := false
	nextFn := func(
		ctx context.Context,
	) (*actions.ActionResult, error) {
		nextCalled = true
		return &actions.ActionResult{}, nil
	}

	ctx := WithChildAction(context.Background())
	result, err := m.Run(ctx, nextFn)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, nextCalled,
		"child action should skip debug and call next")
}

func TestDebugMiddleware_Run_NoEnvVar(t *testing.T) {
	mc := mockinput.NewMockConsole()
	m := &DebugMiddleware{
		options: &Options{CommandPath: "test"},
		console: mc,
	}

	// Ensure AZD_DEBUG is not set
	t.Setenv("AZD_DEBUG", "")

	nextCalled := false
	nextFn := func(
		ctx context.Context,
	) (*actions.ActionResult, error) {
		nextCalled = true
		return &actions.ActionResult{}, nil
	}

	result, err := m.Run(context.Background(), nextFn)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, nextCalled,
		"no AZD_DEBUG should skip debug and call next")
}

func TestDebugMiddleware_Run_EnvVarFalse(t *testing.T) {
	mc := mockinput.NewMockConsole()
	m := &DebugMiddleware{
		options: &Options{CommandPath: "test"},
		console: mc,
	}

	t.Setenv("AZD_DEBUG", "false")

	nextCalled := false
	nextFn := func(
		ctx context.Context,
	) (*actions.ActionResult, error) {
		nextCalled = true
		return &actions.ActionResult{}, nil
	}

	result, err := m.Run(context.Background(), nextFn)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, nextCalled)
}

func TestDebugMiddleware_Run_EnvVarInvalid(t *testing.T) {
	mc := mockinput.NewMockConsole()
	m := &DebugMiddleware{
		options: &Options{CommandPath: "test"},
		console: mc,
	}

	t.Setenv("AZD_DEBUG", "not-a-bool")

	nextCalled := false
	nextFn := func(
		ctx context.Context,
	) (*actions.ActionResult, error) {
		nextCalled = true
		return &actions.ActionResult{}, nil
	}

	result, err := m.Run(context.Background(), nextFn)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, nextCalled,
		"invalid bool should parse as false and call next")
}

func TestDebugMiddleware_Run_TelemetryCommand(t *testing.T) {
	mc := mockinput.NewMockConsole()
	m := &DebugMiddleware{
		options: &Options{
			CommandPath: "azd telemetry upload",
		},
		console: mc,
	}

	// AZD_DEBUG is set but we're running a telemetry command.
	// It checks AZD_DEBUG_TELEMETRY instead.
	t.Setenv("AZD_DEBUG", "true")
	t.Setenv("AZD_DEBUG_TELEMETRY", "")

	nextCalled := false
	nextFn := func(
		ctx context.Context,
	) (*actions.ActionResult, error) {
		nextCalled = true
		return &actions.ActionResult{}, nil
	}

	result, err := m.Run(context.Background(), nextFn)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, nextCalled,
		"telemetry checks AZD_DEBUG_TELEMETRY, not AZD_DEBUG")
}

func TestDebugMiddleware_Run_ConfirmDeclined(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockContext.Console.
		WhenConfirm(func(options input.ConsoleOptions) bool {
			return true
		}).
		Respond(false)

	m := &DebugMiddleware{
		options: &Options{CommandPath: "test"},
		console: mockContext.Console,
	}

	t.Setenv("AZD_DEBUG", "true")

	nextFn := func(
		ctx context.Context,
	) (*actions.ActionResult, error) {
		t.Fatal("next should not be called when declined")
		return nil, nil
	}

	_, err := m.Run(*mockContext.Context, nextFn)

	require.Error(t, err)
	require.True(t, errors.Is(err, ErrDebuggerAborted))
}

func TestDebugMiddleware_Run_ConfirmAccepted(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockContext.Console.
		WhenConfirm(func(options input.ConsoleOptions) bool {
			return true
		}).
		Respond(true)

	m := &DebugMiddleware{
		options: &Options{CommandPath: "test"},
		console: mockContext.Console,
	}

	t.Setenv("AZD_DEBUG", "true")

	nextCalled := false
	nextFn := func(
		ctx context.Context,
	) (*actions.ActionResult, error) {
		nextCalled = true
		return &actions.ActionResult{}, nil
	}

	result, err := m.Run(*mockContext.Context, nextFn)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, nextCalled)
}
