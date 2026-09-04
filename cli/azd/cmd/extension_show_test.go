// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/require"
)

const showTestRegistryURL = "https://test.example.com/show-registry.json"

// runShowJSON runs `azd extension show <id> --output json` and decodes the result.
func runShowJSON(
	t *testing.T,
	manager *extensions.Manager,
	sourceManager *extensions.SourceManager,
	extensionId string,
) extensionShowItem {
	t.Helper()

	var buf bytes.Buffer
	action := &extensionShowAction{
		args:             []string{extensionId},
		flags:            &extensionShowFlags{global: &internal.GlobalCommandOptions{NoPrompt: true}},
		console:          mockinput.NewMockConsole(),
		formatter:        &output.JsonFormatter{},
		writer:           &buf,
		sourceManager:    sourceManager,
		extensionManager: manager,
	}
	_, err := action.Run(t.Context())
	require.NoError(t, err)

	var item extensionShowItem
	require.NoError(t, json.Unmarshal(buf.Bytes(), &item))
	return item
}

func TestExtensionShowAction_ExplainsDependenciesAndDependents(t *testing.T) {
	t.Parallel()

	mockCtx := mocks.NewMockContext(t.Context())
	registry := testRegistry(&extensions.ExtensionMetadata{
		Id:          "azure.ai.agents",
		Source:      "test",
		DisplayName: "Agents",
		Namespace:   "ai.agent",
		Versions: []extensions.ExtensionVersion{
			{
				Version:      "1.0.0",
				Dependencies: []extensions.ExtensionDependency{{Id: "azure.ai.projects", Version: "~1.0.0"}},
			},
			{
				Version:            "1.1.0",
				RequiredAzdVersion: ">=1.0.0",
				Dependencies:       []extensions.ExtensionDependency{{Id: "azure.ai.projects", Version: "~1.0.0"}},
			},
			{Version: "2.0.0", RequiredAzdVersion: ">=9.0.0"},
		},
	})
	installed := map[string]*extensions.Extension{
		"microsoft.foundry": {
			Id: "microsoft.foundry", Version: "1.0.0", Source: "test",
			Dependencies: []extensions.ExtensionDependency{{Id: "azure.ai.agents", Version: "~1.0.0"}},
		},
		"azure.ai.agents": {
			Id: "azure.ai.agents", Version: "1.0.0", Source: "test", InstalledAsDependency: true,
			Dependencies: []extensions.ExtensionDependency{{Id: "azure.ai.projects", Version: "~1.0.0"}},
		},
		"azure.ai.projects": {
			Id: "azure.ai.projects", Version: "2.0.0", Source: "test", InstalledAsDependency: true,
		},
	}
	manager, sourceManager := createUpgradeTestManagerWithOptions(
		t, mockCtx, installed, showTestRegistryURL, registry,
		extensions.ManagerOptions{AzdVersion: semver.MustParse("1.5.0")},
	)

	item := runShowJSON(t, manager, sourceManager, "azure.ai.agents")
	require.Equal(t, "azure.ai.agents", item.Id)
	require.Equal(t, "test", item.Source)
	require.Equal(t, "1.0.0", item.InstalledVersion)
	require.True(t, item.InstalledAsDependency)
	require.Equal(t, "2.0.0", item.LatestVersion)
	require.Equal(t, "1.1.0", item.LatestCompatibleVersion)
	require.True(t, item.UpdateAvailable)
	require.Equal(t, ">=9.0.0", item.RequiresAzd)
	require.Equal(t, []string{"1.1.0", "1.0.0"}, item.OtherVersions)
	require.Equal(t, []extensionShowDependency{
		{Id: "azure.ai.projects", Version: "~1.0.0", InstalledVersion: "2.0.0", Satisfied: false},
	}, item.Dependencies)
	require.Equal(t, []extensionShowDependent{{Id: "microsoft.foundry", Version: "1.0.0"}}, item.RequiredBy)

	// Dependency rows come from the installed snapshot, which the registry cannot override.
	projects := runShowJSON(t, manager, sourceManager, "azure.ai.projects")
	require.Empty(t, projects.LatestVersion, "not listed by any source")
	require.Equal(t, []extensionShowDependent{{Id: "azure.ai.agents", Version: "1.0.0"}}, projects.RequiredBy)
}

func TestExtensionShowAction_JSONKeys(t *testing.T) {
	t.Parallel()

	// Decoding into the tagged struct would accept the old PascalCase keys, so the contract
	// is checked on the raw object: camelCase names and no empty fields.
	mockCtx := mocks.NewMockContext(t.Context())
	installed := map[string]*extensions.Extension{
		"ext-a": {Id: "ext-a", Version: "1.0.0", Source: "test", InstalledAsDependency: true},
	}
	manager, sourceManager := createUpgradeTestManager(
		t, mockCtx, installed, showTestRegistryURL,
		testRegistry(&extensions.ExtensionMetadata{
			Id:          "ext-a",
			Source:      "test",
			DisplayName: "A",
			Versions:    []extensions.ExtensionVersion{{Version: "1.0.0"}},
		}),
	)

	var buf bytes.Buffer
	action := &extensionShowAction{
		args:             []string{"ext-a"},
		flags:            &extensionShowFlags{global: &internal.GlobalCommandOptions{NoPrompt: true}},
		console:          mockinput.NewMockConsole(),
		formatter:        &output.JsonFormatter{},
		writer:           &buf,
		sourceManager:    sourceManager,
		extensionManager: manager,
	}
	_, err := action.Run(t.Context())
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &raw))
	require.Equal(t, "ext-a", raw["id"])
	require.Equal(t, "1.0.0", raw["installedVersion"])
	require.Equal(t, "1.0.0", raw["latestVersion"])
	require.Equal(t, true, raw["installedAsDependency"])
	for _, legacyKey := range []string{"Id", "Name", "InstalledVersion", "LatestVersion", "AvailableVersions"} {
		require.NotContains(t, raw, legacyKey)
	}
	for _, emptyKey := range []string{"website", "namespace", "tags", "otherVersions", "dependencies", "requiredBy"} {
		require.NotContains(t, raw, emptyKey, "empty fields are omitted")
	}
}

func TestExtensionShowAction_InstalledWithoutRegistryEntry(t *testing.T) {
	t.Parallel()

	mockCtx := mocks.NewMockContext(t.Context())
	installed := map[string]*extensions.Extension{
		"bundled.ext": {
			Id: "bundled.ext", DisplayName: "Bundled", Description: "From a bundle",
			Version: "0.1.0", Source: extensions.BundleSourceName, Namespace: "bundled", Usage: "azd bundled",
			Capabilities: []extensions.CapabilityType{extensions.CustomCommandCapability},
			Dependencies: []extensions.ExtensionDependency{{Id: "other.ext"}},
		},
		"other.ext": {
			Id: "other.ext", DisplayName: "Other", Version: "0.1.0",
			Source: extensions.BundleSourceName, InstalledAsDependency: true,
		},
	}
	manager, sourceManager := createUpgradeTestManager(t, mockCtx, installed, showTestRegistryURL, testRegistry())

	item := runShowJSON(t, manager, sourceManager, "BUNDLED.EXT")
	require.Equal(t, "bundled.ext", item.Id, "the record's id wins over the typed casing")
	require.Equal(t, "Bundled", item.Name)
	require.Equal(t, "From a bundle", item.Description)
	require.Equal(t, extensions.BundleSourceName, item.Source)
	require.Equal(t, "bundled", item.Namespace)
	require.Equal(t, "0.1.0", item.InstalledVersion)
	require.Empty(t, item.LatestVersion)
	require.Equal(t, "azd bundled", item.Usage)
	require.Equal(t, []extensions.CapabilityType{extensions.CustomCommandCapability}, item.Capabilities)
	require.Equal(t, []extensionShowDependency{
		{Id: "other.ext", InstalledVersion: "0.1.0", Satisfied: true},
	}, item.Dependencies)

	other := runShowJSON(t, manager, sourceManager, "other.ext")
	require.True(t, other.InstalledAsDependency)
	require.Equal(t, []extensionShowDependent{{Id: "bundled.ext", Version: "0.1.0"}}, other.RequiredBy)
}

func TestExtensionShowAction_PrefersInstalledSource(t *testing.T) {
	t.Parallel()

	mockCtx := mocks.NewMockContext(t.Context())
	installed := map[string]*extensions.Extension{
		"dup.ext": {Id: "dup.ext", Version: "1.0.0", Source: "other"},
	}
	manager, sourceManager := createUpgradeTestManagerWithSources(
		t, mockCtx, installed,
		map[string]upgradeTestSource{
			"test": {
				url:      showTestRegistryURL,
				registry: testRegistry(testExtMeta("dup.ext", "1.0.0", "test")),
			},
			"other": {
				url:      "https://other.example.com/registry.json",
				registry: testRegistry(testExtMeta("dup.ext", "1.0.0", "other")),
			},
		},
		extensions.ManagerOptions{},
	)

	// Under --no-prompt two matching sources fail unless the installed source is chosen.
	item := runShowJSON(t, manager, sourceManager, "dup.ext")
	require.Equal(t, "other", item.Source)
	require.Equal(t, "1.0.0", item.InstalledVersion)
	require.Empty(t, item.InstalledSource)
}

func TestExtensionShowAction_InstalledFromAnotherSource(t *testing.T) {
	t.Parallel()

	mockCtx := mocks.NewMockContext(t.Context())
	installed := map[string]*extensions.Extension{
		"ext-a": {Id: "ext-a", Version: "1.0.0", Source: "removed-registry"},
	}
	manager, sourceManager := createUpgradeTestManager(
		t, mockCtx, installed, showTestRegistryURL, testRegistry(testExtMeta("ext-a", "2.0.0", "test")),
	)

	item := runShowJSON(t, manager, sourceManager, "ext-a")
	require.Equal(t, "test", item.Source)
	require.Equal(t, "1.0.0", item.InstalledVersion)
	require.Equal(t, "removed-registry", item.InstalledSource)
	require.False(t, item.UpdateAvailable, "update state is only reported against the installed source")
}

func TestExtensionShowAction_SourceFilterIsNotBypassedByInstalledRecord(t *testing.T) {
	t.Parallel()

	mockCtx := mocks.NewMockContext(t.Context())
	installed := map[string]*extensions.Extension{
		"ext-a": {Id: "ext-a", Version: "1.0.0", Source: "azd"},
	}
	manager, sourceManager := createUpgradeTestManager(
		t, mockCtx, installed, showTestRegistryURL, testRegistry(testExtMeta("other-ext", "1.0.0", "test")),
	)

	var buf bytes.Buffer
	action := &extensionShowAction{
		args:             []string{"ext-a"},
		flags:            &extensionShowFlags{source: "test", global: &internal.GlobalCommandOptions{NoPrompt: true}},
		console:          mockinput.NewMockConsole(),
		formatter:        &output.JsonFormatter{},
		writer:           &buf,
		sourceManager:    sourceManager,
		extensionManager: manager,
	}

	// The requested source does not carry ext-a; the installed record must not stand in for it.
	_, err := action.Run(t.Context())
	require.ErrorContains(t, err, "no extensions found")
}

func TestExtensionShowAction_UpdateStateWithoutCompatibilityPolicy(t *testing.T) {
	t.Parallel()

	mockCtx := mocks.NewMockContext(t.Context())
	installed := map[string]*extensions.Extension{
		"ext-a": {Id: "ext-a", Version: "1.0.0", Source: "test"},
	}
	manager, sourceManager := createUpgradeTestManagerWithOptions(
		t, mockCtx, installed, showTestRegistryURL,
		testRegistry(&extensions.ExtensionMetadata{
			Id:     "ext-a",
			Source: "test",
			Versions: []extensions.ExtensionVersion{
				{Version: "1.0.0"},
				{Version: "2.0.0", RequiredAzdVersion: ">=9.0.0"},
			},
		}),
		extensions.ManagerOptions{IgnoreAzdCompatibility: true},
	)

	// Dev builds have no azd version to check against: every release is installable.
	item := runShowJSON(t, manager, sourceManager, "ext-a")
	require.True(t, item.UpdateAvailable)
	require.Equal(t, "2.0.0", item.LatestCompatibleVersion)
}

func TestExtensionShowItem_Display_Layout(t *testing.T) {
	t.Parallel()

	t.Run("installed_with_update_and_dependencies", func(t *testing.T) {
		t.Parallel()
		item := &extensionShowItem{
			Id:                      "azure.ai.agents",
			Name:                    "Agents",
			Description:             "Agents extension",
			Source:                  "azd",
			Tags:                    []string{"ai"},
			LatestVersion:           "2.0.0",
			LatestCompatibleVersion: "1.1.0",
			RequiresAzd:             ">=9.0.0",
			OtherVersions:           []string{"1.1.0", "1.0.0"},
			InstalledVersion:        "1.0.0",
			InstalledAsDependency:   true,
			UpdateAvailable:         true,
			Dependencies: []extensionShowDependency{
				{Id: "azure.ai.projects", Version: "~1.0.0", InstalledVersion: "2.0.0"},
				{Id: "azure.ai.inspector", InstalledVersion: "1.0.0", Satisfied: true},
				{Id: "azure.ai.skills", Version: "^1.0.0"},
			},
			RequiredBy:         []extensionShowDependent{{Id: "microsoft.foundry", Version: "1.0.0"}},
			azdVersion:         "1.5.0",
			latestIncompatible: true,
			newerIncompatible:  true,
		}

		var buf bytes.Buffer
		require.NoError(t, item.Display(&buf))
		out := buf.String()

		require.Contains(t, out, "Tags")
		require.NotContains(t, out, "Namespace")
		require.NotContains(t, out, "Website")
		require.NotContains(t, out, "Usage")
		require.Contains(t, out, "1.0.0 (update available: 1.1.0)")
		require.Contains(t, out, ">=9.0.0 (not compatible with azd 1.5.0; latest compatible is 1.1.0)")
		require.Contains(t, out, "Other Versions")
		require.Contains(t, out, "1.1.0, 1.0.0")
		require.Contains(t, out, "Dependencies")
		require.Contains(t, out, "~1.0.0 (installed 2.0.0, outside constraint)")
		require.Contains(t, out, "any version (installed 1.0.0)")
		require.Contains(t, out, "^1.0.0 (not installed)")
		require.Contains(t, out, "Required By")
		require.Contains(t, out, "microsoft.foundry")
	})

	t.Run("not_installed_pack", func(t *testing.T) {
		t.Parallel()
		item := &extensionShowItem{
			Id:            "microsoft.foundry",
			Name:          "Foundry",
			Description:   "Pack",
			Source:        "azd",
			LatestVersion: "1.0.0",
		}

		var buf bytes.Buffer
		require.NoError(t, item.Display(&buf))
		out := buf.String()

		require.Contains(t, out, "Not installed")
		require.NotContains(t, out, "N/A")
		require.NotContains(t, out, "Usage")
		require.NotContains(t, out, "Requires azd")
		require.NotContains(t, out, "Other Versions")
	})

	t.Run("installed_latest_is_incompatible", func(t *testing.T) {
		t.Parallel()
		item := &extensionShowItem{
			Id:                      "ext-a",
			Name:                    "A",
			Description:             "A",
			Source:                  "azd",
			LatestVersion:           "2.0.0",
			LatestCompatibleVersion: "1.1.0",
			RequiresAzd:             ">=9.0.0",
			InstalledVersion:        "2.0.0",
			azdVersion:              "1.5.0",
			latestIncompatible:      true,
		}

		var buf bytes.Buffer
		require.NoError(t, item.Display(&buf))
		out := buf.String()

		// The installed row has nothing newer to mention, but the constraint is still explained.
		require.Contains(t, out, ">=9.0.0 (not compatible with azd 1.5.0; latest compatible is 1.1.0)")
		require.NotContains(t, out, "requires a newer azd")
	})

	t.Run("installed_as_dependency", func(t *testing.T) {
		t.Parallel()
		item := &extensionShowItem{
			Id:                    "azure.ai.projects",
			Name:                  "Projects",
			Description:           "Projects extension",
			Source:                "azd",
			LatestVersion:         "1.0.0",
			InstalledVersion:      "1.0.0",
			InstalledAsDependency: true,
		}

		var buf bytes.Buffer
		require.NoError(t, item.Display(&buf))
		require.Contains(t, buf.String(), "1.0.0 (installed as a dependency)")
	})

	t.Run("installed_from_other_source", func(t *testing.T) {
		t.Parallel()
		item := &extensionShowItem{
			Id:               "ext-a",
			Name:             "A",
			Description:      "A",
			Source:           "test",
			LatestVersion:    "2.0.0",
			InstalledVersion: "1.0.0",
			InstalledSource:  "removed-registry",
		}

		var buf bytes.Buffer
		require.NoError(t, item.Display(&buf))
		require.Contains(t, buf.String(), "1.0.0 (from removed-registry)")
	})
}
