// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package copilot

import (
	"context"
	"runtime"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestCopilotCLIPath(t *testing.T) {
	path, err := copilotCLIPath()
	require.NoError(t, err)
	require.NotEmpty(t, path)
	require.Contains(t, path, "copilot-cli-"+cliVersion)
	if runtime.GOOS == "windows" {
		require.True(t, len(path) > 4 && path[len(path)-4:] == ".exe")
	}
}

func TestDownloadURL(t *testing.T) {
	tests := []struct {
		goos   string
		goarch string
		pkg    string
	}{
		{"windows", "amd64", "copilot-win32-x64"},
		{"darwin", "amd64", "copilot-darwin-x64"},
		{"darwin", "arm64", "copilot-darwin-arm64"},
		{"linux", "amd64", "copilot-linux-x64"},
		{"linux", "arm64", "copilot-linux-arm64"},
	}

	for _, tt := range tests {
		t.Run(tt.goos+"/"+tt.goarch, func(t *testing.T) {
			if runtime.GOOS == tt.goos && runtime.GOARCH == tt.goarch {
				expectedURL := "https://registry.npmjs.org/@github/" + tt.pkg +
					"/-/" + tt.pkg + "-" + cliVersion + ".tgz"
				require.Contains(t, expectedURL, tt.pkg)
				require.Contains(t, expectedURL, cliVersion)
			}
		})
	}
}

func TestCLIVersionPinned(t *testing.T) {
	require.NotEmpty(t, cliVersion)
	require.Regexp(t, `^\d+\.\d+\.\d+$`, cliVersion)
}

func TestCopilotCLI_ExternalToolInterface(t *testing.T) {
	cli := &CopilotCLI{}
	require.Equal(t, "GitHub Copilot CLI", cli.Name())
	require.NotEmpty(t, cli.InstallUrl())
	require.Contains(t, cli.InstallUrl(), "github.com")
}

func TestIsFeatureEnabled(t *testing.T) {
	t.Run("Nil panics", func(t *testing.T) {
		require.Panics(t, func() {
			IsFeatureEnabled(nil)
		})
	})
}

func TestConfigKeyComposition(t *testing.T) {
	require.Equal(t, "copilot", ConfigRoot)
	require.Equal(t, "copilot.model", ConfigKeyModel)
	require.Equal(t, "copilot.model.type", ConfigKeyModelType)
	require.Equal(t, "copilot.tools.available", ConfigKeyToolsAvailable)
	require.Equal(t, "copilot.skills.directories", ConfigKeySkillsDirectories)
	require.Equal(t, "copilot.mcp.servers", ConfigKeyMCPServers)
	require.Equal(t, "copilot.consent", ConfigKeyConsent)
	require.Equal(t, "copilot.errorHandling.fix", ConfigKeyErrorHandlingFix)
	require.Equal(t, "copilot.errorHandling.category", ConfigKeyErrorHandlingCategory)

	for _, key := range []string{
		ConfigKeyModelType, ConfigKeyModel, ConfigKeyReasoningEffort,
		ConfigKeySystemMessage, ConfigKeyToolsAvailable, ConfigKeyToolsExcluded,
		ConfigKeySkillsDirectories, ConfigKeySkillsDisabled, ConfigKeyMCPServers,
		ConfigKeyConsent, ConfigKeyLogLevel, ConfigKeyMode,
		ConfigKeyErrorHandlingFix, ConfigKeyErrorHandlingCategory,
	} {
		require.True(t, len(key) > len(ConfigRoot), "key %q should be longer than root", key)
		require.Equal(t, ConfigRoot+".", key[:len(ConfigRoot)+1], "key %q should start with root", key)
	}
}

func TestCopilotCLI_ListPlugins(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "copilot" && len(args.Args) >= 2 &&
			args.Args[0] == "plugin" && args.Args[1] == "list"
	}).Respond(exec.NewRunResult(
		0,
		"Installed plugins:\n  • azure (v1.0.1)\n  • wbreza-skills (v1.0.0)\n",
		"",
	))

	cli := &CopilotCLI{
		runner: mockContext.CommandRunner,
		path:   "copilot", // simulate already resolved
	}

	installed, err := cli.ListPlugins(*mockContext.Context)
	require.NoError(t, err)
	require.True(t, installed["azure"])
	require.True(t, installed["wbreza-skills"])
	require.False(t, installed["nonexistent"])
}

func TestCopilotCLI_ListPlugins_Empty(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "copilot" && len(args.Args) >= 2 &&
			args.Args[0] == "plugin" && args.Args[1] == "list"
	}).Respond(exec.NewRunResult(
		0,
		"Installed plugins:\n",
		"",
	))

	cli := &CopilotCLI{
		runner: mockContext.CommandRunner,
		path:   "copilot",
	}

	installed, err := cli.ListPlugins(*mockContext.Context)
	require.NoError(t, err)
	require.Empty(t, installed)
}

func TestCopilotCLI_ListPlugins_Error(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "copilot"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(1, "", "command not found"), &exec.ExitError{ExitCode: 1}
	})

	cli := &CopilotCLI{
		runner: mockContext.CommandRunner,
		path:   "copilot",
	}

	installed, err := cli.ListPlugins(*mockContext.Context)
	require.Error(t, err)
	require.Contains(t, err.Error(), "listing plugins")
	require.Nil(t, installed)
}

func TestCopilotCLI_InstallPlugin(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	var capturedArgs []string
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "copilot" && len(args.Args) >= 3 &&
			args.Args[0] == "plugin" && args.Args[1] == "install"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		capturedArgs = args.Args
		return exec.NewRunResult(0, "Plugin \"azure\" installed successfully.", ""), nil
	})

	cli := &CopilotCLI{
		runner: mockContext.CommandRunner,
		path:   "copilot",
	}

	err := cli.InstallPlugin(*mockContext.Context, "microsoft/GitHub-Copilot-for-Azure:plugin")
	require.NoError(t, err)
	require.Equal(t, []string{"plugin", "install", "microsoft/GitHub-Copilot-for-Azure:plugin"}, capturedArgs)
}

func TestCopilotCLI_InstallPlugin_Error(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "copilot"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(1, "", "network error"), &exec.ExitError{ExitCode: 1}
	})

	cli := &CopilotCLI{
		runner: mockContext.CommandRunner,
		path:   "copilot",
	}

	err := cli.InstallPlugin(*mockContext.Context, "some/plugin")
	require.Error(t, err)
	require.Contains(t, err.Error(), "installing plugin")
}

func TestCopilotCLI_PathOverride(t *testing.T) {
	t.Setenv("AZD_COPILOT_CLI_PATH", "/custom/copilot")

	cli := &CopilotCLI{}
	path, err := cli.Path(context.Background())
	require.NoError(t, err)
	require.Equal(t, "/custom/copilot", path)
}

func TestCopilotCLI_Login(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	var capturedArgs []string
	var capturedInteractive bool
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "copilot" && len(args.Args) >= 1 && args.Args[0] == "login"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		capturedArgs = args.Args
		capturedInteractive = args.Interactive
		return exec.NewRunResult(0, "Authentication complete.", ""), nil
	})

	cli := &CopilotCLI{
		runner: mockContext.CommandRunner,
		path:   "copilot",
	}

	err := cli.Login(*mockContext.Context)
	require.NoError(t, err)
	require.Equal(t, []string{"login"}, capturedArgs)
	require.True(t, capturedInteractive, "login should run in interactive mode")
}

func TestCopilotCLI_Login_Error(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "copilot"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(1, "", "auth failed"), &exec.ExitError{ExitCode: 1}
	})

	cli := &CopilotCLI{
		runner: mockContext.CommandRunner,
		path:   "copilot",
	}

	err := cli.Login(*mockContext.Context)
	require.Error(t, err)
	require.Contains(t, err.Error(), "copilot login failed")
}
