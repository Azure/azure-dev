// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package copilot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

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

// ---------------------------------------------------------------------------
// CheckInstalled / ensureInstalled override path.
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// detectPluginsByDirectory + readPluginName
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// CopilotClientManager.Stop with no client.
// ---------------------------------------------------------------------------

func TestCopilotClientManager_Stop_NilClient(t *testing.T) {
	t.Parallel()
	m := NewCopilotClientManager(nil, nil)
	require.NoError(t, m.Stop())
	require.Nil(t, m.Client())
}

func TestCopilotClientManager_NewWithOptions(t *testing.T) {
	t.Parallel()
	opts := &CopilotClientOptions{LogLevel: "info", CLIPath: "/tmp/copilot"}
	m := NewCopilotClientManager(opts, nil)
	require.NotNil(t, m)
	require.Equal(t, opts, m.options)
}
