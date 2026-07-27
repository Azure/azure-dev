// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/processutil"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/azure/azure-dev/cli/azd/test/proctest"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	if proctest.RunRequestedHelper() {
		return
	}

	os.Exit(m.Run())
}

// T31: --force has to be registered on upgrade and actually bound, or the flag would
// parse and then be ignored.
func TestExtensionUpgradeFlags_Force(t *testing.T) {
	t.Parallel()

	command := &cobra.Command{Use: "upgrade"}
	flags := newExtensionUpgradeFlags(command, &internal.GlobalCommandOptions{})

	definition := command.Flags().Lookup("force")
	require.NotNil(t, definition, "upgrade must accept --force")
	require.Equal(t, "f", definition.Shorthand, "--force must share install's -f shorthand")
	require.Equal(t, "false", definition.DefValue, "stopping processes must be opt-in")
	require.Contains(t, definition.Usage, "Stop running extension processes")

	require.False(t, flags.force)
	require.NoError(t, command.Flags().Parse([]string{"--force"}))
	require.True(t, flags.force, "--force must bind to the field the action reads")
}

// T32: the same contract on uninstall, so --force means one thing across the extension
// commands rather than being upgrade-only trivia.
func TestExtensionUninstallFlags_Force(t *testing.T) {
	t.Parallel()

	command := &cobra.Command{Use: "uninstall"}
	flags := newExtensionUninstallFlags(command)

	definition := command.Flags().Lookup("force")
	require.NotNil(t, definition, "uninstall must accept --force")
	require.Equal(t, "f", definition.Shorthand)
	require.Equal(t, "false", definition.DefValue)
	require.Contains(t, definition.Usage, "Stop running extension processes")

	require.False(t, flags.force)
	require.NoError(t, command.Flags().Parse([]string{"-f"}))
	require.True(t, flags.force, "the shorthand must bind the same field as the long form")
}

// T33: --force has to compose with the flags people actually pair it with. A batch
// upgrade of everything is the case where locked binaries are most likely.
func TestExtensionUpgradeFlags_ForceComposesWithOtherFlags(t *testing.T) {
	t.Parallel()

	command := &cobra.Command{Use: "upgrade"}
	flags := newExtensionUpgradeFlags(command, &internal.GlobalCommandOptions{})

	require.NoError(t, command.Flags().Parse([]string{
		"--all", "--force", "--no-dependency-upgrades", "--version", "2.0.0",
	}))

	require.True(t, flags.all)
	require.True(t, flags.force)
	require.True(t, flags.noDependencyUpgrades)
	require.Equal(t, "2.0.0", flags.version)
}

func TestExtensionUninstallFlags_ForceComposesWithAll(t *testing.T) {
	t.Parallel()

	command := &cobra.Command{Use: "uninstall"}
	flags := newExtensionUninstallFlags(command)

	require.NoError(t, command.Flags().Parse([]string{"--all", "--force"}))

	require.True(t, flags.all)
	require.True(t, flags.force)
}

// T34: the whole command path with --force. The flag has to reach the manager, the
// running extension has to be stopped, the removal has to complete, the user has to be
// told what azd did, and none of it may prompt. Opting into --force is the confirmation,
// so a prompt here would break CI and scripted upgrades.
func TestExtensionUninstallAction_ForceStopsProcessesWithoutPrompting(t *testing.T) {
	const extensionId = "test.forcecmd"

	extensionDir, process := installedRunningExtension(t, extensionId, "azd-ext-forcecmd")

	mockContext := mocks.NewMockContext(t.Context())
	console := refuseToPrompt(t, mockContext.Console)
	manager, _ := forceTestManager(t, mockContext, extensionId, process.Path, testRegistry())

	action := newExtensionUninstallAction(
		[]string{extensionId},
		&extensionUninstallFlags{force: true},
		console,
		manager,
	)

	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.NotNil(t, result)

	process.RequireStopped(t)
	require.NoDirExists(t, extensionDir, "--force must complete the removal rather than defer it")

	installed, err := manager.ListInstalled()
	require.NoError(t, err)
	require.NotContains(t, installed, extensionId)

	reported := strings.Join(console.Output(), "\n")
	require.Contains(t, reported, "Stopped", "the user must be told which processes azd stopped")
	require.Contains(t, reported, filepath.Base(process.Path))
}

// T63: --force has to survive the --all fan-out. --all only expands the id list and then
// reuses the same per-extension path, so a bug that read the flag once outside the loop,
// or reset it between extensions, would leave everything after the first one running.
// Two live processes, one command, both have to be gone.
func TestExtensionUninstallAction_ForceComposesWithAllAcrossExtensions(t *testing.T) {
	const firstId = "test.bulkforce1"
	const secondId = "test.bulkforce2"

	configDir := isolatedConfigDir(t)
	firstDir, firstProcess := runningExtensionIn(t, configDir, firstId, "azd-ext-bulkforce1")
	secondDir, secondProcess := runningExtensionIn(t, configDir, secondId, "azd-ext-bulkforce2")

	mockContext := mocks.NewMockContext(t.Context())
	console := refuseToPrompt(t, mockContext.Console)
	manager, _ := createUpgradeTestManager(t, mockContext, map[string]*extensions.Extension{
		firstId:  installedAt(firstId, firstProcess.Path),
		secondId: installedAt(secondId, secondProcess.Path),
	}, "https://test.example.com/registry.json", testRegistry())

	action := newExtensionUninstallAction(
		nil,
		&extensionUninstallFlags{all: true, force: true},
		console,
		manager,
	)

	_, err := action.Run(t.Context())
	require.NoError(t, err)

	firstProcess.RequireStopped(t)
	secondProcess.RequireStopped(t)
	require.NoDirExists(t, firstDir)
	require.NoDirExists(t, secondDir)

	installed, err := manager.ListInstalled()
	require.NoError(t, err)
	require.Empty(t, installed, "--all must clear every extension it stopped")
}

// Without --force the command must leave the process running, which keeps the destructive
// behavior opt-in at the command layer and not only inside the manager.
func TestExtensionUninstallAction_WithoutForceLeavesProcessRunning(t *testing.T) {
	const extensionId = "test.gentlecmd"

	_, process := installedRunningExtension(t, extensionId, "azd-ext-gentlecmd")

	mockContext := mocks.NewMockContext(t.Context())
	console := refuseToPrompt(t, mockContext.Console)
	manager, _ := forceTestManager(t, mockContext, extensionId, process.Path, testRegistry())

	action := newExtensionUninstallAction(
		[]string{extensionId},
		&extensionUninstallFlags{},
		console,
		manager,
	)

	_, err := action.Run(t.Context())
	require.NoError(t, err, "uninstall must succeed even while the extension is running")

	process.RequireRunning(t)
	require.NotContains(t, strings.Join(console.Output(), "\n"), "Stopped")
}

// The upgrade command has its own wiring into UpgradeOptions, separate from uninstall.
// A --force that parses but is never passed through would leave the original bug in place
// on the exact command the issue was filed against.
func TestExtensionUpgradeAction_ForceStopsRunningProcess(t *testing.T) {
	const extensionId = "test.forceupgrade"

	_, process := installedRunningExtension(t, extensionId, "azd-ext-forceupgrade")

	mockContext := mocks.NewMockContext(t.Context())
	console := refuseToPrompt(t, mockContext.Console)
	manager, sourceManager := forceTestManager(
		t, mockContext, extensionId, process.Path,
		testRegistry(testExtMeta(extensionId, "2.0.0", "test")),
	)

	action := &extensionUpgradeAction{
		args: []string{extensionId},
		flags: &extensionUpgradeFlags{
			force:  true,
			global: &internal.GlobalCommandOptions{NoPrompt: true},
		},
		formatter:        &output.JsonFormatter{},
		writer:           &bytes.Buffer{},
		console:          console,
		sourceManager:    sourceManager,
		extensionManager: manager,
	}

	// Fetching the new version cannot succeed against the mock registry, so the upgrade
	// itself fails. That is deliberate. The process must already be stopped by then,
	// because the uninstall runs first and that is where --force applies.
	result := action.upgradeOneExtension(t.Context(), extensionId, 0, nil, true)
	require.Equal(t, extensions.UpgradeStatusFailed, result.Status)

	process.RequireStopped(t)
}

// Without --force the upgrade must leave the process alone, so the destructive behavior
// stays opt-in on upgrade and not just on uninstall.
func TestExtensionUpgradeAction_WithoutForceLeavesProcessRunning(t *testing.T) {
	const extensionId = "test.gentleupgrade"

	_, process := installedRunningExtension(t, extensionId, "azd-ext-gentleupgrade")

	mockContext := mocks.NewMockContext(t.Context())
	console := refuseToPrompt(t, mockContext.Console)
	manager, sourceManager := forceTestManager(
		t, mockContext, extensionId, process.Path,
		testRegistry(testExtMeta(extensionId, "2.0.0", "test")),
	)

	action := &extensionUpgradeAction{
		args: []string{extensionId},
		flags: &extensionUpgradeFlags{
			global: &internal.GlobalCommandOptions{NoPrompt: true},
		},
		formatter:        &output.JsonFormatter{},
		writer:           &bytes.Buffer{},
		console:          console,
		sourceManager:    sourceManager,
		extensionManager: manager,
	}

	// Assert the outcome rather than discarding it. Without this the test would pass even
	// if the action bailed out early, which would prove nothing about whether the process
	// was deliberately left alone. The status is Failed because this fixture's registry
	// serves no downloadable artifact, so the assertion pins "the upgrade was reached and
	// ran", not "locking caused the failure".
	result := action.upgradeOneExtension(t.Context(), extensionId, 0, nil, true)
	require.Equal(t, extensions.UpgradeStatusFailed, result.Status,
		"the upgrade must actually run for the no-force behavior to mean anything")

	process.RequireRunning(t)
}

// install --force already meant "override what is blocking me", and reinstalling over a
// running extension is the same block. This drives the whole install action so the
// already-installed branch is exercised, not just the flag definition.
func TestExtensionInstallAction_ForceStopsRunningProcessOnReinstall(t *testing.T) {
	const extensionId = "test.forceinstall"

	_, process := installedRunningExtension(t, extensionId, "azd-ext-forceinstall")

	mockContext := mocks.NewMockContext(t.Context())
	console := refuseToPrompt(t, mockContext.Console)
	manager, sourceManager := forceTestManager(
		t, mockContext, extensionId, process.Path,
		testRegistry(testExtMeta(extensionId, "2.0.0", "test")),
	)

	action := &extensionInstallAction{
		args: []string{extensionId},
		flags: &extensionInstallFlags{
			force:  true,
			source: "test",
			global: &internal.GlobalCommandOptions{NoPrompt: true},
		},
		console:          console,
		extensionManager: manager,
		sourceManager:    sourceManager,
	}

	// Same source and a newer version, so this reaches the upgrade path without asking
	// anything. refuseToPrompt turns a future prompt here into a failure. The install
	// itself cannot finish because the mock registry serves no artifact; the point is
	// that --force already stopped the process by the time it got there.
	_, err := action.Run(t.Context())
	require.Error(t, err, "the mock registry serves no artifact, so the reinstall must fail")

	process.RequireStopped(t)
}

// A forced upgrade that stops the extension and then fails must still say what it killed.
// Otherwise azd terminates the user's running extension, reports only the download
// failure, and leaves them with no idea their process is gone.
func TestExtensionUpgradeAction_ForceReportsStoppedProcessesOnFailure(t *testing.T) {
	const extensionId = "test.forcefail"

	_, process := installedRunningExtension(t, extensionId, "azd-ext-forcefail")

	mockContext := mocks.NewMockContext(t.Context())
	console := refuseToPrompt(t, mockContext.Console)
	manager, sourceManager := forceTestManager(
		t, mockContext, extensionId, process.Path,
		testRegistry(testExtMeta(extensionId, "2.0.0", "test")),
	)

	action := &extensionUpgradeAction{
		args: []string{extensionId},
		flags: &extensionUpgradeFlags{
			force:  true,
			global: &internal.GlobalCommandOptions{NoPrompt: true},
		},
		formatter:        &output.NoneFormatter{},
		writer:           &bytes.Buffer{},
		console:          console,
		sourceManager:    sourceManager,
		extensionManager: manager,
	}

	// isJsonOutput false, so this exercises the console reporting path. The upgrade
	// fails because the mock registry serves no artifact, which is exactly the case
	// where silence would be worst.
	result := action.upgradeOneExtension(t.Context(), extensionId, 0, nil, false)
	require.Equal(t, extensions.UpgradeStatusFailed, result.Status)

	process.RequireStopped(t)

	reported := strings.Join(console.Output(), "\n")
	require.Contains(t, reported, "Stopped",
		"a failed forced upgrade must still report the processes it terminated")
	require.Contains(t, reported, filepath.Base(process.Path))
}

// Nothing stopped means nothing said, so a routine upgrade does not grow noise.
func TestReportStoppedProcesses_SilentWhenNothingStopped(t *testing.T) {
	t.Parallel()

	console := mockinput.NewMockConsole()
	reportStoppedProcesses(t.Context(), console, nil)

	require.Empty(t, console.Output())
}

func TestReportStoppedProcesses_NamesEachStoppedProcess(t *testing.T) {
	t.Parallel()

	console := mockinput.NewMockConsole()
	reportStoppedProcesses(t.Context(), console, []processutil.ProcessInfo{
		{PID: 4321, Name: "azd-ext-demo", Executable: filepath.Join("tmp", "demo", "azd-ext-demo")},
		{PID: 8765, Name: "azd-ext-other", Executable: filepath.Join("tmp", "demo", "azd-ext-other")},
	})

	reported := strings.Join(console.Output(), "\n")
	require.Contains(t, reported, "4321")
	require.Contains(t, reported, "azd-ext-demo")
	require.Contains(t, reported, "8765")
	require.Contains(t, reported, "azd-ext-other")
}

// --force must also reach the branch that runs when the parent is already at the target
// version. That branch only reconciles stale dependencies, and a dependency's process
// holds its binary open exactly like the parent's would, so omitting Force there made
// `upgrade --force` silently leave a running dependency in place and unreported.
func TestExtensionUpgradeAction_ForceReachesStaleDependencyOfCurrentParent(t *testing.T) {
	const parentId = "test.current-parent"
	const childId = "test.stale-child"

	configDir := isolatedConfigDir(t)
	_, parentProcess := runningExtensionIn(t, configDir, parentId, "azd-ext-current-parent")
	_, childProcess := runningExtensionIn(t, configDir, childId, "azd-ext-stale-child")

	mockContext := mocks.NewMockContext(t.Context())
	console := refuseToPrompt(t, mockContext.Console)

	// The parent's only version matches what is installed, so the upgrade takes the
	// "already up to date" path. Its dependency constraint is not satisfied by the
	// installed child, which is what makes that path do real work.
	registry := testRegistry(
		&extensions.ExtensionMetadata{
			Id:     parentId,
			Source: "test",
			Versions: []extensions.ExtensionVersion{
				{
					Version:      "1.0.0",
					Dependencies: []extensions.ExtensionDependency{{Id: childId, Version: ">=2.0.0"}},
				},
			},
		},
		testExtMeta(childId, "2.0.0", "test"),
	)

	manager, sourceManager := createUpgradeTestManager(t, mockContext, map[string]*extensions.Extension{
		parentId: installedAt(parentId, parentProcess.Path),
		childId:  installedAt(childId, childProcess.Path),
	}, "https://test.example.com/registry.json", registry)

	action := &extensionUpgradeAction{
		args: []string{parentId},
		flags: &extensionUpgradeFlags{
			force:  true,
			global: &internal.GlobalCommandOptions{NoPrompt: true},
		},
		formatter:        &output.JsonFormatter{},
		writer:           &bytes.Buffer{},
		console:          console,
		sourceManager:    sourceManager,
		extensionManager: manager,
	}

	result := action.upgradeOneExtension(t.Context(), parentId, 0, nil, true)

	require.Equal(t, extensions.UpgradeStatusSkipped, result.Status, "the parent itself is already current")

	// Fetching the child's new version cannot succeed against the mock registry, so its
	// upgrade fails. The stop still has to have happened, because the uninstall runs
	// first and that is where --force applies.
	childProcess.RequireStopped(t)
	require.False(t, parentProcess.HasExited(), "only the stale dependency's process may be stopped")

	require.Len(t, result.StoppedProcesses, 1, "the reported result must name what --force stopped")
	require.Equal(t, childProcess.PID, result.StoppedProcesses[0].PID)

	// --force prints nothing under --output json, so the structured result is the only
	// place a scripted caller can learn what azd terminated on its behalf.
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"stoppedProcesses"`)
	require.Contains(t, string(encoded), fmt.Sprintf(`"pid":%d`, childProcess.PID))
}

// installedRunningExtension redirects azd configuration at a temporary directory and puts
// a real running executable in the extension's install directory, which is the situation
// that makes uninstall and upgrade fail today.
func installedRunningExtension(t *testing.T, extensionId string, name string) (string, *proctest.Handle) {
	t.Helper()

	return runningExtensionIn(t, isolatedConfigDir(t), extensionId, name)
}

// isolatedConfigDir points AZD_CONFIG_DIR at a temporary directory and proves the
// redirection took effect, so a test can never remove extensions from the real profile.
func isolatedConfigDir(t *testing.T) string {
	t.Helper()

	configDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configDir)

	resolved, err := config.GetUserConfigDir()
	require.NoError(t, err)
	require.Equal(t, configDir, resolved, "the test must never touch the real azd configuration")

	return configDir
}

// runningExtensionIn installs a live extension process under an existing config directory,
// so one test can hold more than one installed extension at a time.
func runningExtensionIn(
	t *testing.T,
	configDir string,
	extensionId string,
	name string,
) (string, *proctest.Handle) {
	t.Helper()

	extensionDir := filepath.Join(configDir, "extensions", extensionId)
	require.NoError(t, os.MkdirAll(extensionDir, osutil.PermissionDirectory))

	return extensionDir, proctest.StartIn(t, extensionDir, name, proctest.ModeIdle)
}

// installedAt describes an extension whose recorded path is the executable actually
// running, which is what makes the command resolve the directory the process runs from.
func installedAt(extensionId string, executable string) *extensions.Extension {
	return &extensions.Extension{
		Id:      extensionId,
		Version: "1.0.0",
		Source:  "test",
		Path:    filepath.Join("extensions", extensionId, filepath.Base(executable)),
	}
}

// forceTestManager builds a manager whose installed state points at the running
// executable, so the command under test resolves the same directory the process runs from.
func forceTestManager(
	t *testing.T,
	mockContext *mocks.MockContext,
	extensionId string,
	executable string,
	registry extensions.Registry,
) (*extensions.Manager, *extensions.SourceManager) {
	t.Helper()

	return createUpgradeTestManager(t, mockContext, map[string]*extensions.Extension{
		extensionId: installedAt(extensionId, executable),
	}, "https://test.example.com/registry.json", registry)
}

// refuseToPrompt turns any interactive request into a test failure, so a prompt added to
// the uninstall path in the future is caught rather than silently hanging CI.
func refuseToPrompt(t *testing.T, console *mockinput.MockConsole) *mockinput.MockConsole {
	t.Helper()

	fail := func(kind string) func(input.ConsoleOptions) bool {
		return func(options input.ConsoleOptions) bool {
			t.Errorf("uninstall must never %s, but asked: %q", kind, options.Message)

			return false
		}
	}

	console.WhenConfirm(fail("confirm")).Respond(false)
	console.WhenPrompt(fail("prompt")).Respond("")
	console.WhenSelect(fail("select")).Respond(0)

	return console
}
