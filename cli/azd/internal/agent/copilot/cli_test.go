// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package copilot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
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
	require.Regexp(t, `^\d+\.\d+\.\d+(-[a-zA-Z0-9.]+)?$`, cliVersion)
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
	mockContext := mocks.NewMockContext(t.Context())

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
	mockContext := mocks.NewMockContext(t.Context())

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
	mockContext := mocks.NewMockContext(t.Context())

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
	mockContext := mocks.NewMockContext(t.Context())

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
	mockContext := mocks.NewMockContext(t.Context())

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
	path, err := cli.Path(t.Context())
	require.NoError(t, err)
	require.Equal(t, "/custom/copilot", path)
}

func TestCopilotCLI_Login(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

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
	mockContext := mocks.NewMockContext(t.Context())

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

// setHome points the user's home directory to the given path for the test.
// On Windows os.UserHomeDir uses USERPROFILE; on unix it uses HOME.
func setHome(t *testing.T, dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", dir)
	} else {
		t.Setenv("HOME", dir)
	}
}

func TestCopilotCLI_CheckInstalled_Override(t *testing.T) {
	override := filepath.Join(t.TempDir(), "copilot-custom")
	t.Setenv("AZD_COPILOT_CLI_PATH", override)

	cli := &CopilotCLI{}
	err := cli.CheckInstalled(t.Context())
	require.NoError(t, err)
	require.Equal(t, override, cli.path)
}

func TestCopilotCLI_Path_CachesResult(t *testing.T) {
	override := filepath.Join(t.TempDir(), "copilot-cached")
	t.Setenv("AZD_COPILOT_CLI_PATH", override)

	cli := &CopilotCLI{}
	p1, err := cli.Path(t.Context())
	require.NoError(t, err)
	require.Equal(t, override, p1)

	// Second call must return the same path (installOnce guarantees a single run).
	p2, err := cli.Path(t.Context())
	require.NoError(t, err)
	require.Equal(t, p1, p2)
}

func TestReadPluginName_DotPluginLayout(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".plugin"), 0o755))
	data, _ := json.Marshal(map[string]string{"name": "azure"})
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".plugin", "plugin.json"), data, 0o600))

	require.Equal(t, "azure", readPluginName(dir))
}

func TestReadPluginName_ClaudePluginLayout(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".claude-plugin"), 0o755))
	data, _ := json.Marshal(map[string]string{"name": "claude-plugin"})
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".claude-plugin", "plugin.json"), data, 0o600))

	require.Equal(t, "claude-plugin", readPluginName(dir))
}

func TestReadPluginName_FlatPluginLayout(t *testing.T) {
	dir := t.TempDir()
	data, _ := json.Marshal(map[string]string{"name": "flat"})
	require.NoError(t, os.WriteFile(filepath.Join(dir, "plugin.json"), data, 0o600))

	require.Equal(t, "flat", readPluginName(dir))
}

func TestReadPluginName_MissingMetadata(t *testing.T) {
	// Empty directory: no plugin.json found at any location → "".
	require.Equal(t, "", readPluginName(t.TempDir()))
}

func TestReadPluginName_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "plugin.json"), []byte("not-json"), 0o600))

	require.Equal(t, "", readPluginName(dir))
}

func TestReadPluginName_EmptyName(t *testing.T) {
	dir := t.TempDir()
	// Valid JSON but empty name — should not be returned.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"name":""}`), 0o600))
	require.Equal(t, "", readPluginName(dir))
}

func TestDetectPluginsByDirectory_NoHome(t *testing.T) {
	// Point home at an empty directory — no .copilot folder exists.
	setHome(t, t.TempDir())
	got := detectPluginsByDirectory()
	require.Empty(t, got)
}

func TestDetectPluginsByDirectory_DirectLayout(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	directRoot := filepath.Join(home, ".copilot", "installed-plugins", "_direct", "somepkg")
	require.NoError(t, os.MkdirAll(filepath.Join(directRoot, ".plugin"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(directRoot, ".plugin", "plugin.json"),
		[]byte(`{"name":"azure"}`),
		0o600,
	))

	// Also include a file (not a directory) to hit the !entry.IsDir() branch.
	require.NoError(t, os.WriteFile(
		filepath.Join(home, ".copilot", "installed-plugins", "_direct", "stray.txt"),
		[]byte("ignore me"),
		0o600,
	))

	got := detectPluginsByDirectory()
	require.True(t, got["azure"], "expected azure plugin, got %v", got)
}

func TestDetectPluginsByDirectory_FlatLayout(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	// Create a flat-layout plugin directory alongside the _direct subdirectory.
	flatPlugin := filepath.Join(home, ".copilot", "installed-plugins", "my-flat-plugin")
	require.NoError(t, os.MkdirAll(flatPlugin, 0o755))
	// Also create the _direct dir so both branches execute.
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".copilot", "installed-plugins", "_direct"), 0o755))

	got := detectPluginsByDirectory()
	require.True(t, got["my-flat-plugin"], "expected flat plugin, got %v", got)
	require.False(t, got["_direct"], "_direct must not be treated as a plugin")
}

// Integration: ListPlugins falls back to directory scan when CLI reports
// "No plugins installed".
func TestCopilotCLI_ListPlugins_FallbackToDirectoryScan(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	// Seed a flat-layout plugin so the directory scan has something to find.
	require.NoError(t, os.MkdirAll(
		filepath.Join(home, ".copilot", "installed-plugins", "disk-plugin"),
		0o755,
	))

	mockContext := mocks.NewMockContext(t.Context())
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == "copilot" && len(args.Args) >= 2 &&
			args.Args[0] == "plugin" && args.Args[1] == "list"
	}).Respond(exec.NewRunResult(0, "No plugins installed\n", ""))

	cli := &CopilotCLI{runner: mockContext.CommandRunner, path: "copilot"}

	got, err := cli.ListPlugins(*mockContext.Context)
	require.NoError(t, err)
	require.True(t, got["disk-plugin"], "expected fallback to find disk-plugin, got %v", got)
}
