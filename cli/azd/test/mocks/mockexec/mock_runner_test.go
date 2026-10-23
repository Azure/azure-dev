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

func TestRun_PreCanceledContextSkipsPredicatesAndResponses(t *testing.T) {
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

	_, err := mock.Run(ctx, exec.NewRunArgs("tool"))

	require.ErrorIs(t, err, context.Canceled)
	require.False(t, predicateCalled)
	require.False(t, responseCalled)
}

func TestRunList_PreCanceledContextSkipsPredicatesAndResponses(t *testing.T) {
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

	_, err := mock.RunList(ctx, []string{"tool"}, exec.RunArgs{})

	require.ErrorIs(t, err, context.Canceled)
	require.False(t, predicateCalled)
	require.False(t, responseCalled)
}

func TestRun_PreCanceledContextPreservesCause(t *testing.T) {
	mock := NewMockCommandRunner()
	cause := errors.New("custom cancellation cause")
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(cause)

	_, err := mock.Run(ctx, exec.NewRunArgs("tool"))

	require.ErrorIs(t, err, cause)
	require.NotErrorIs(t, err, context.Canceled)
}

func TestRunList_PreCanceledContextPreservesCause(t *testing.T) {
	mock := NewMockCommandRunner()
	cause := errors.New("custom cancellation cause")
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(cause)

	_, err := mock.RunList(ctx, []string{"tool"}, exec.RunArgs{})

	require.ErrorIs(t, err, cause)
	require.NotErrorIs(t, err, context.Canceled)
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
