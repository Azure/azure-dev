// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/require"
)

func uninstallTestRecord(id, version string, asDependency bool, dependencies ...string) *extensions.Extension {
	record := &extensions.Extension{
		Id:                    id,
		Version:               version,
		Source:                "test",
		InstalledAsDependency: asDependency,
	}
	for _, dependency := range dependencies {
		record.Dependencies = append(record.Dependencies, extensions.ExtensionDependency{Id: dependency})
	}
	return record
}

// uninstallTestInstall mirrors the microsoft.foundry pack shape: an explicit pack whose
// dependencies were installed for it, where agents itself requires inspector and projects.
func uninstallTestInstall() map[string]*extensions.Extension {
	return map[string]*extensions.Extension{
		"microsoft.foundry": uninstallTestRecord("microsoft.foundry", "1.0.0", false,
			"azure.ai.agents", "azure.ai.projects", "azure.ai.inspector", "azure.ai.skills"),
		"azure.ai.agents": uninstallTestRecord("azure.ai.agents", "2.0.0", true,
			"azure.ai.inspector", "azure.ai.projects"),
		"azure.ai.projects":  uninstallTestRecord("azure.ai.projects", "3.0.0", true),
		"azure.ai.inspector": uninstallTestRecord("azure.ai.inspector", "4.0.0", true),
		"azure.ai.skills":    uninstallTestRecord("azure.ai.skills", "5.0.0", true),
	}
}

func newUninstallTestAction(
	t *testing.T,
	installed map[string]*extensions.Extension,
	flags extensionUninstallFlags,
	args ...string,
) (*extensionUninstallAction, *mockinput.MockConsole) {
	t.Helper()
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())

	mockCtx := mocks.NewMockContext(t.Context())
	manager, _ := createUpgradeTestManager(
		t, mockCtx, installed, "https://test.example.com/registry.json", testRegistry(),
	)
	console := mockinput.NewMockConsole()

	return &extensionUninstallAction{
		args:             args,
		flags:            &flags,
		console:          console,
		extensionManager: manager,
	}, console
}

func remainingInstalledIds(t *testing.T, manager *extensions.Manager) []string {
	t.Helper()
	installed, err := manager.ListInstalled()
	require.NoError(t, err)
	return slices.Sorted(maps.Keys(installed))
}

func TestExtensionUninstallAction_BlockedByDependents(t *testing.T) {
	action, _ := newUninstallTestAction(t, uninstallTestInstall(), extensionUninstallFlags{}, "azure.ai.projects")

	_, err := action.Run(t.Context())
	require.ErrorContains(t, err,
		"extension azure.ai.projects is required by installed extensions: azure.ai.agents, microsoft.foundry")
	suggestionErr, ok := errors.AsType[*internal.ErrorWithSuggestion](err)
	require.True(t, ok)
	require.Contains(t, suggestionErr.Suggestion, "azd extension uninstall azure.ai.agents microsoft.foundry")
	require.Contains(t, suggestionErr.Suggestion, "--force")
	require.Len(t, remainingInstalledIds(t, action.extensionManager), 5, "nothing is removed when blocked")
}

func TestExtensionUninstallAction_ForceWarnsAboutDependents(t *testing.T) {
	action, console := newUninstallTestAction(
		t, uninstallTestInstall(), extensionUninstallFlags{force: true}, "azure.ai.projects",
	)

	_, err := action.Run(t.Context())
	require.NoError(t, err)
	require.Equal(t,
		[]string{"azure.ai.agents", "azure.ai.inspector", "azure.ai.skills", "microsoft.foundry"},
		remainingInstalledIds(t, action.extensionManager),
	)
	require.Contains(t, strings.Join(console.Output(), "\n"),
		"azure.ai.projects is required by azure.ai.agents, microsoft.foundry")
}

func TestExtensionUninstallAction_PackRemovesOrphanedDependencies(t *testing.T) {
	action, console := newUninstallTestAction(t, uninstallTestInstall(), extensionUninstallFlags{}, "microsoft.foundry")
	console.WhenConfirm(func(input.ConsoleOptions) bool { return true }).Respond(true)

	_, err := action.Run(t.Context())
	require.NoError(t, err)
	require.Empty(t, remainingInstalledIds(t, action.extensionManager))

	output := strings.Join(console.Output(), "\n")
	require.Contains(t, output, "Remove these 4 dependencies as well?")
	for _, id := range []string{"azure.ai.agents", "azure.ai.projects", "azure.ai.inspector", "azure.ai.skills"} {
		require.Contains(t, output, id)
	}
	require.Contains(t, output, "no longer required")
}

func TestExtensionUninstallAction_DeclinedDependencyRemovalKeepsThem(t *testing.T) {
	action, console := newUninstallTestAction(t, uninstallTestInstall(), extensionUninstallFlags{}, "microsoft.foundry")
	console.WhenConfirm(func(input.ConsoleOptions) bool { return true }).Respond(false)

	_, err := action.Run(t.Context())
	require.NoError(t, err)
	require.Equal(t,
		[]string{"azure.ai.agents", "azure.ai.inspector", "azure.ai.projects", "azure.ai.skills"},
		remainingInstalledIds(t, action.extensionManager),
	)

	// Declining is not a claim of ownership: the records stay dependency installs.
	installed, err := action.extensionManager.ListInstalled()
	require.NoError(t, err)
	require.True(t, installed["azure.ai.agents"].InstalledAsDependency)

	output := strings.Join(console.Output(), "\n")
	require.Contains(t, output, "kept")
	require.Contains(t, output,
		"azd extension uninstall azure.ai.agents azure.ai.projects azure.ai.inspector azure.ai.skills")
}

func TestExtensionUninstallAction_NoPromptWithoutOrphans(t *testing.T) {
	// Nothing beyond the named target goes, so there is nothing to confirm.
	installed := uninstallTestInstall()
	action, console := newUninstallTestAction(t, installed, extensionUninstallFlags{force: true}, "azure.ai.skills")

	_, err := action.Run(t.Context())
	require.NoError(t, err)
	require.NotContains(t, strings.Join(console.Output(), "\n"), "as well?")
}

func TestExtensionUninstallAction_RetainedDependenciesAreExplained(t *testing.T) {
	installed := uninstallTestInstall()
	installed["azure.ai.agents"].InstalledAsDependency = false
	action, console := newUninstallTestAction(t, installed, extensionUninstallFlags{}, "microsoft.foundry")
	console.WhenConfirm(func(input.ConsoleOptions) bool { return true }).Respond(true)

	_, err := action.Run(t.Context())
	require.NoError(t, err)
	require.Equal(t,
		[]string{"azure.ai.agents", "azure.ai.inspector", "azure.ai.projects"},
		remainingInstalledIds(t, action.extensionManager),
	)

	output := strings.Join(console.Output(), "\n")
	require.Contains(t, output, "not installed as a dependency")
	require.Contains(t, output, "required by azure.ai.agents")
}

func TestExtensionUninstallAction_NoDependencies(t *testing.T) {
	action, _ := newUninstallTestAction(
		t, uninstallTestInstall(), extensionUninstallFlags{noDependencies: true}, "microsoft.foundry",
	)

	_, err := action.Run(t.Context())
	require.NoError(t, err)
	require.Equal(t,
		[]string{"azure.ai.agents", "azure.ai.inspector", "azure.ai.projects", "azure.ai.skills"},
		remainingInstalledIds(t, action.extensionManager),
	)
}

func TestExtensionUninstallAction_All(t *testing.T) {
	action, _ := newUninstallTestAction(t, uninstallTestInstall(), extensionUninstallFlags{all: true})

	_, err := action.Run(t.Context())
	require.NoError(t, err)
	require.Empty(t, remainingInstalledIds(t, action.extensionManager))
}

func TestExtensionUninstallAction_RejectsBlankId(t *testing.T) {
	// An unset shell variable yields an empty argument; it must not match an arbitrary record.
	action, _ := newUninstallTestAction(t, uninstallTestInstall(), extensionUninstallFlags{}, "")

	_, err := action.Run(t.Context())
	require.ErrorIs(t, err, extensions.ErrEmptyExtensionId)
	require.Len(t, remainingInstalledIds(t, action.extensionManager), 5)
}

func TestExtensionUninstallAction_NotInstalled(t *testing.T) {
	action, _ := newUninstallTestAction(t, uninstallTestInstall(), extensionUninstallFlags{}, "missing")

	_, err := action.Run(t.Context())
	require.ErrorContains(t, err, "failed to get installed extension")
	require.Len(t, remainingInstalledIds(t, action.extensionManager), 5)
}
