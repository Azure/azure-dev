package helm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_Cli_AddRepo(t *testing.T) {
	repo := &Repository{
		Name: "test",
		Url:  "https://test.com",
	}

	t.Run("Success", func(t *testing.T) {
		ran := false
		var runArgs exec.RunArgs

		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, "helm repo add")
			}).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				ran = true
				runArgs = args
				return exec.NewRunResult(0, "", ""), nil
			})

		cli := NewCli(mockContext.CommandRunner)
		err := cli.AddRepo(*mockContext.Context, repo)
		require.True(t, ran)
		require.NoError(t, err)

		require.Equal(t, "helm", runArgs.Cmd)
		require.Equal(t, []string{
			"repo",
			"add",
			"test",
			"https://test.com",
		}, runArgs.Args)
	})

	t.Run("Failure", func(t *testing.T) {
		ran := false

		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, "helm repo add")
			}).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				ran = true
				return exec.NewRunResult(1, "", ""), errors.New("failed to add repo")
			})

		cli := NewCli(mockContext.CommandRunner)
		err := cli.AddRepo(*mockContext.Context, repo)

		require.True(t, ran)
		require.Error(t, err)
		require.ErrorContains(t, err, "failed to add repo")
	})
}

func Test_Cli_UpdateRepo(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		ran := false
		var runArgs exec.RunArgs

		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, "helm repo update")
			}).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				ran = true
				runArgs = args
				return exec.NewRunResult(0, "", ""), nil
			})

		cli := NewCli(mockContext.CommandRunner)
		err := cli.UpdateRepo(*mockContext.Context, "test")
		require.True(t, ran)
		require.NoError(t, err)

		require.Equal(t, "helm", runArgs.Cmd)
		require.Equal(t, []string{
			"repo",
			"update",
			"test",
		}, runArgs.Args)
	})

	t.Run("Failure", func(t *testing.T) {
		ran := false

		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, "helm repo update")
			}).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				ran = true
				return exec.NewRunResult(1, "", ""), errors.New("failed to update repo")
			})

		cli := NewCli(mockContext.CommandRunner)
		err := cli.UpdateRepo(*mockContext.Context, "test")

		require.True(t, ran)
		require.Error(t, err)
		require.ErrorContains(t, err, "failed to update repo")
	})
}

func Test_Cli_Install(t *testing.T) {
	release := &Release{
		Name:    "test",
		Chart:   "test/chart",
		Version: "1.0.0",
	}

	t.Run("Success", func(t *testing.T) {
		ran := false
		var runArgs exec.RunArgs

		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, "helm install")
			}).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				ran = true
				runArgs = args
				return exec.NewRunResult(0, "", ""), nil
			})

		cli := NewCli(mockContext.CommandRunner)
		err := cli.Install(*mockContext.Context, release)
		require.True(t, ran)
		require.NoError(t, err)

		require.Equal(t, "helm", runArgs.Cmd)
		require.Equal(t, []string{
			"install",
			"test",
			"test/chart",
		}, runArgs.Args)
	})

	t.Run("WithValues", func(t *testing.T) {
		ran := false
		var runArgs exec.RunArgs

		releaseWithValues := *release
		releaseWithValues.Values = "values.yaml"

		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, "helm install")
			}).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				ran = true
				runArgs = args
				return exec.NewRunResult(0, "", ""), nil
			})

		cli := NewCli(mockContext.CommandRunner)
		err := cli.Install(*mockContext.Context, &releaseWithValues)
		require.True(t, ran)
		require.NoError(t, err)

		require.Equal(t, "helm", runArgs.Cmd)
		require.Equal(t, []string{
			"install",
			"test",
			"test/chart",
			"--values",
			"values.yaml",
		}, runArgs.Args)
	})

	t.Run("Failure", func(t *testing.T) {
		ran := false

		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, "helm install")
			}).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				ran = true
				return exec.NewRunResult(1, "", ""), errors.New("failed to install release")
			})

		cli := NewCli(mockContext.CommandRunner)
		err := cli.Install(*mockContext.Context, release)

		require.True(t, ran)
		require.Error(t, err)
		require.ErrorContains(t, err, "failed to install release")
	})
}

func Test_Cli_Upgrade(t *testing.T) {
	release := &Release{
		Name:  "test",
		Chart: "test/chart",
	}

	t.Run("Success", func(t *testing.T) {
		ran := false
		var runArgs exec.RunArgs

		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, "helm upgrade")
			}).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				ran = true
				runArgs = args
				return exec.NewRunResult(0, "", ""), nil
			})

		cli := NewCli(mockContext.CommandRunner)
		err := cli.Upgrade(*mockContext.Context, release)
		require.True(t, ran)
		require.NoError(t, err)

		require.Equal(t, "helm", runArgs.Cmd)
		require.Equal(t, []string{
			"upgrade",
			"test",
			"test/chart",
			"--install",
			"--wait",
		}, runArgs.Args)
	})

	t.Run("WithValues", func(t *testing.T) {
		ran := false
		var runArgs exec.RunArgs

		releaseWithValues := *release
		releaseWithValues.Values = "values.yaml"

		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, "helm upgrade")
			}).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				ran = true
				runArgs = args
				return exec.NewRunResult(0, "", ""), nil
			})

		cli := NewCli(mockContext.CommandRunner)
		err := cli.Upgrade(*mockContext.Context, &releaseWithValues)
		require.True(t, ran)
		require.NoError(t, err)

		require.Equal(t, "helm", runArgs.Cmd)
		require.Equal(t, []string{
			"upgrade",
			"test",
			"test/chart",
			"--install",
			"--wait",
			"--values",
			"values.yaml",
		}, runArgs.Args)
	})

	t.Run("WithVersion", func(t *testing.T) {
		ran := false
		var runArgs exec.RunArgs

		releaseWithVersion := *release
		releaseWithVersion.Version = "1.0.0"

		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, "helm upgrade")
			}).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				ran = true
				runArgs = args
				return exec.NewRunResult(0, "", ""), nil
			})

		cli := NewCli(mockContext.CommandRunner)
		err := cli.Upgrade(*mockContext.Context, &releaseWithVersion)
		require.True(t, ran)
		require.NoError(t, err)

		require.Equal(t, "helm", runArgs.Cmd)
		require.Equal(t, []string{
			"upgrade",
			"test",
			"test/chart",
			"--install",
			"--wait",
			"--version",
			"1.0.0",
		}, runArgs.Args)
	})

	t.Run("WithNamespace", func(t *testing.T) {
		ran := false
		var runArgs exec.RunArgs

		releaseWithNamespace := *release
		releaseWithNamespace.Namespace = "test-namespace"

		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, "helm upgrade")
			}).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				ran = true
				runArgs = args
				return exec.NewRunResult(0, "", ""), nil
			})

		cli := NewCli(mockContext.CommandRunner)
		err := cli.Upgrade(*mockContext.Context, &releaseWithNamespace)
		require.True(t, ran)
		require.NoError(t, err)

		require.Equal(t, "helm", runArgs.Cmd)
		require.Equal(t, []string{
			"upgrade",
			"test",
			"test/chart",
			"--install",
			"--wait",
			"--namespace",
			"test-namespace",
			"--create-namespace",
		}, runArgs.Args)
	})

	t.Run("Failure", func(t *testing.T) {
		ran := false

		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, "helm upgrade")
			}).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				ran = true
				return exec.NewRunResult(1, "", ""), errors.New("failed to upgrade release")
			})

		cli := NewCli(mockContext.CommandRunner)
		err := cli.Upgrade(*mockContext.Context, release)

		require.True(t, ran)
		require.Error(t, err)
		require.ErrorContains(t, err, "failed to upgrade release")
	})
}

func Test_Cli_Status(t *testing.T) {
	release := &Release{
		Name: "test",
	}

	t.Run("Success", func(t *testing.T) {
		ran := false
		var runArgs exec.RunArgs

		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, "helm status")
			}).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				ran = true
				runArgs = args
				return exec.NewRunResult(0, `{
					"info": {
						"status": "deployed"
					}
				}`, ""), nil
			})

		cli := NewCli(mockContext.CommandRunner)
		status, err := cli.Status(*mockContext.Context, release)
		require.True(t, ran)
		require.NoError(t, err)
		require.Equal(t, StatusKindDeployed, status.Info.Status)

		require.Equal(t, "helm", runArgs.Cmd)
		require.Equal(t, []string{
			"status",
			"test",
			"--output",
			"json",
		}, runArgs.Args)
	})

	t.Run("WithNamespace", func(t *testing.T) {
		ran := false
		var runArgs exec.RunArgs

		releaseWithNamespace := *release
		releaseWithNamespace.Namespace = "test-namespace"

		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, "helm status")
			}).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				ran = true
				runArgs = args
				return exec.NewRunResult(0, `{
					"info": {
						"status": "deployed"
					}
				}`, ""), nil
			})

		cli := NewCli(mockContext.CommandRunner)
		status, err := cli.Status(*mockContext.Context, &releaseWithNamespace)
		require.True(t, ran)
		require.NoError(t, err)
		require.Equal(t, StatusKindDeployed, status.Info.Status)

		require.Equal(t, "helm", runArgs.Cmd)
		require.Equal(t, []string{
			"status",
			"test",
			"--output",
			"json",
			"--namespace",
			"test-namespace",
		}, runArgs.Args)
	})

	t.Run("Failure", func(t *testing.T) {
		ran := false

		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.
			When(func(args exec.RunArgs, command string) bool {
				return strings.Contains(command, "helm status")
			}).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				ran = true
				return exec.NewRunResult(1, "", ""), errors.New("failed to get status")
			})

		cli := NewCli(mockContext.CommandRunner)
		_, err := cli.Status(*mockContext.Context, release)

		require.True(t, ran)
		require.Error(t, err)
		require.ErrorContains(t, err, "failed to get status")
	})
}
