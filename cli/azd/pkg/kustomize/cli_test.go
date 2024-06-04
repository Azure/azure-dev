package kustomize

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_Edit(t *testing.T) {
	args := []string{"set", "image", "nginx=nginx:1.7.9"}

	t.Run("Success", func(t *testing.T) {
		ran := false
		var runArgs exec.RunArgs

		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, "kustomize edit")
			}).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				ran = true
				runArgs = args
				return exec.NewRunResult(0, "", ""), nil
			})

		cli := NewCli(mockContext.CommandRunner)
		err := cli.Edit(*mockContext.Context, args...)
		require.True(t, ran)
		require.NoError(t, err)

		expected := []string{"edit"}
		expected = append(expected, args...)

		require.Equal(t, "kustomize", runArgs.Cmd)
		require.Equal(t, "", runArgs.Cwd)
		require.Equal(t, expected, runArgs.Args)
	})

	t.Run("WithCwd", func(t *testing.T) {
		ran := false
		var runArgs exec.RunArgs

		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, "kustomize edit")
			}).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				ran = true
				runArgs = args
				return exec.NewRunResult(0, "", ""), nil
			})

		cli := NewCli(mockContext.CommandRunner)
		err := cli.
			WithCwd("/tmp").
			Edit(*mockContext.Context, args...)

		require.True(t, ran)
		require.NoError(t, err)

		expected := []string{"edit"}
		expected = append(expected, args...)

		require.Equal(t, "kustomize", runArgs.Cmd)
		require.Equal(t, "/tmp", runArgs.Cwd)
		require.Equal(t, expected, runArgs.Args)
	})

	t.Run("Failure", func(t *testing.T) {
		ran := false

		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, "kustomize edit")
			}).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				ran = true
				return exec.NewRunResult(1, "", ""), errors.New("failed to edit kustomize config")
			})

		cli := NewCli(mockContext.CommandRunner)
		err := cli.Edit(*mockContext.Context, args...)

		require.True(t, ran)
		require.Error(t, err)
		require.ErrorContains(t, err, "failed to edit kustomize config")
	})
}
