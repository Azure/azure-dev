// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package mockexec

import (
	"context"
	"errors"
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
