// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package mockexec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/stretchr/testify/require"
)

func TestPreCanceledContext(t *testing.T) {
	runners := []struct {
		name string
		run  func(*MockCommandRunner, context.Context) (exec.RunResult, error)
	}{
		{
			name: "Run",
			run: func(mock *MockCommandRunner, ctx context.Context) (exec.RunResult, error) {
				return mock.Run(ctx, exec.NewRunArgs("tool"))
			},
		},
		{
			name: "RunList",
			run: func(mock *MockCommandRunner, ctx context.Context) (exec.RunResult, error) {
				return mock.RunList(ctx, []string{"tool"}, exec.RunArgs{})
			},
		},
	}

	for _, runner := range runners {
		t.Run(runner.name, func(t *testing.T) {
			t.Run("SkipsPredicatesAndResponses", func(t *testing.T) {
				mock := NewMockCommandRunner()
				predicateCalled := false
				responseCalled := false
				mock.When(func(exec.RunArgs, string) bool {
					predicateCalled = true
					return true
				}).RespondFn(func(exec.RunArgs) (exec.RunResult, error) {
					responseCalled = true
					return exec.RunResult{}, nil
				})
				ctx, cancel := context.WithCancel(t.Context())
				cancel()

				_, err := runner.run(mock, ctx)

				require.ErrorIs(t, err, context.Canceled)
				require.False(t, predicateCalled)
				require.False(t, responseCalled)
			})

			t.Run("PreservesCause", func(t *testing.T) {
				mock := NewMockCommandRunner()
				cause := errors.New("custom cancellation cause")
				ctx, cancel := context.WithCancelCause(t.Context())
				cancel(cause)

				_, err := runner.run(mock, ctx)

				require.ErrorIs(t, err, cause)
				require.NotErrorIs(t, err, context.Canceled)
			})
		})
	}
}

func TestRespondContextFnReceivesCancellation(t *testing.T) {
	mock := NewMockCommandRunner()
	ctx, cancel := context.WithCancel(t.Context())
	mock.When(func(exec.RunArgs, string) bool {
		return true
	}).RespondContextFn(func(ctx context.Context, _ exec.RunArgs) (exec.RunResult, error) {
		cancel()
		<-ctx.Done()
		return exec.RunResult{}, ctx.Err()
	})

	_, err := mock.Run(ctx, exec.NewRunArgs("tool"))

	require.ErrorIs(t, err, context.Canceled)
}

func TestRespondFnRemainsCompatible(t *testing.T) {
	mock := NewMockCommandRunner()
	want := exec.NewRunResult(0, "out", "err")
	mock.When(func(exec.RunArgs, string) bool {
		return true
	}).RespondFn(func(exec.RunArgs) (exec.RunResult, error) {
		return want, nil
	})

	got, err := mock.Run(t.Context(), exec.NewRunArgs("tool"))

	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestRunUsesMostRecentMatchingExpression(t *testing.T) {
	mock := NewMockCommandRunner()
	mock.When(func(exec.RunArgs, string) bool {
		return true
	}).Respond(exec.NewRunResult(0, "first", ""))
	want := exec.NewRunResult(0, "second", "")
	mock.When(func(exec.RunArgs, string) bool {
		return true
	}).Respond(want)

	got, err := mock.Run(t.Context(), exec.NewRunArgs("tool"))

	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestRunPanicsWhenNoExpressionMatches(t *testing.T) {
	mock := NewMockCommandRunner()
	mock.When(func(exec.RunArgs, string) bool {
		return false
	})

	require.PanicsWithValue(t, "No mock found for command: 'tool arg'", func() {
		_, _ = mock.Run(t.Context(), exec.NewRunArgs("tool", "arg"))
	})
}

func TestRunListReturnsConfiguredResponseAndError(t *testing.T) {
	mock := NewMockCommandRunner()
	wantResult := exec.NewRunResult(1, "out", "err")
	wantErr := errors.New("command failed")
	expression := mock.When(func(args exec.RunArgs, command string) bool {
		require.Equal(t, []string{"first", "second"}, args.Args)
		require.Equal(t, "first && second", command)
		return true
	})
	expression.Respond(wantResult)
	expression.SetError(wantErr)

	got, err := mock.RunList(t.Context(), []string{"first", "second"}, exec.RunArgs{})

	require.ErrorIs(t, err, wantErr)
	require.Equal(t, wantResult, got)
}

func TestToolInPath(t *testing.T) {
	t.Run("Mocked", func(t *testing.T) {
		mock := NewMockCommandRunner()
		wantErr := errors.New("tool unavailable")
		mock.MockToolInPath("missing", wantErr)
		mock.MockToolInPath("available", nil)

		require.ErrorIs(t, mock.ToolInPath("missing"), wantErr)
		require.NoError(t, mock.ToolInPath("available"))
	})

	t.Run("Fallback", func(t *testing.T) {
		mock := NewMockCommandRunner()
		executable, err := os.Executable()
		require.NoError(t, err)

		require.NoError(t, mock.ToolInPath(executable))
		require.Error(t, mock.ToolInPath(filepath.Join(t.TempDir(), "missing")))
	})
}
