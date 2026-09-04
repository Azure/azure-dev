// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

// installedRecord builds an installed extension record with a dependency snapshot.
func installedRecord(id, version string, asDependency bool, dependencies ...string) *Extension {
	record := &Extension{
		Id:                    id,
		Version:               version,
		Source:                MainRegistryName,
		InstalledAsDependency: asDependency,
	}
	for _, dependency := range dependencies {
		record.Dependencies = append(record.Dependencies, ExtensionDependency{Id: dependency})
	}
	return record
}

// foundryShapedInstall mirrors the microsoft.foundry pack: an explicit pack over dependencies
// installed for it, where agents itself requires inspector and projects.
func foundryShapedInstall() map[string]*Extension {
	return map[string]*Extension{
		"microsoft.foundry": installedRecord("microsoft.foundry", "1.0.0", false,
			"azure.ai.agents", "azure.ai.projects", "azure.ai.inspector", "azure.ai.skills"),
		"azure.ai.agents":    installedRecord("azure.ai.agents", "2.0.0", true, "azure.ai.inspector", "azure.ai.projects"),
		"azure.ai.projects":  installedRecord("azure.ai.projects", "3.0.0", true),
		"azure.ai.inspector": installedRecord("azure.ai.inspector", "4.0.0", true),
		"azure.ai.skills":    installedRecord("azure.ai.skills", "5.0.0", true),
	}
}

func newPlanTestManager(t *testing.T, installed map[string]*Extension) *Manager {
	t.Helper()

	manager := newTestManager(t)
	require.NoError(t, manager.userConfig.Set(installedConfigKey, installed))
	manager.installed = nil
	return manager
}

func extensionIds(extensions []*Extension) []string {
	ids := make([]string, 0, len(extensions))
	for _, extension := range extensions {
		ids = append(ids, extension.Id)
	}
	return ids
}

func Test_PlanUninstall_PackRemovesOrphanedDependencies(t *testing.T) {
	t.Parallel()
	manager := newPlanTestManager(t, foundryShapedInstall())

	plan, err := manager.PlanUninstall([]string{"microsoft.foundry"}, UninstallPlanOptions{})
	require.NoError(t, err)
	require.Equal(t, []string{"microsoft.foundry"}, extensionIds(plan.Targets))
	require.Equal(t,
		[]string{"azure.ai.agents", "azure.ai.projects", "azure.ai.inspector", "azure.ai.skills"},
		extensionIds(plan.Orphaned),
	)
	require.Empty(t, plan.Retained)
	require.Empty(t, plan.Blocked)
}

func Test_PlanUninstall_SharedDependenciesStayWithExplicitSibling(t *testing.T) {
	t.Parallel()
	installed := foundryShapedInstall()
	installed["azure.ai.agents"].InstalledAsDependency = false
	manager := newPlanTestManager(t, installed)

	plan, err := manager.PlanUninstall([]string{"microsoft.foundry"}, UninstallPlanOptions{})
	require.NoError(t, err)
	require.Equal(t, []string{"azure.ai.skills"}, extensionIds(plan.Orphaned))

	require.Len(t, plan.Retained, 3)
	require.Equal(t, "azure.ai.agents", plan.Retained[0].Extension.Id)
	require.Empty(t, plan.Retained[0].RequiredBy, "explicit installs are retained without dependents")
	require.Equal(t, "azure.ai.inspector", plan.Retained[1].Extension.Id)
	require.Equal(t, []string{"azure.ai.agents"}, plan.Retained[1].RequiredBy)
	require.Equal(t, "azure.ai.projects", plan.Retained[2].Extension.Id)
	require.Equal(t, []string{"azure.ai.agents"}, plan.Retained[2].RequiredBy)
}

func Test_PlanUninstall_DependencyFreedBySiblingRemovedLater(t *testing.T) {
	t.Parallel()
	// A -> B, A -> C, C -> B: B is first kept because C needs it, then freed once C goes.
	manager := newPlanTestManager(t, map[string]*Extension{
		"a": installedRecord("a", "1.0.0", false, "b", "c"),
		"b": installedRecord("b", "1.0.0", true),
		"c": installedRecord("c", "1.0.0", true, "b"),
	})

	plan, err := manager.PlanUninstall([]string{"a"}, UninstallPlanOptions{})
	require.NoError(t, err)
	require.Equal(t, []string{"c", "b"}, extensionIds(plan.Orphaned))
	require.Empty(t, plan.Retained)
}

func Test_PlanUninstall_BlockedByDependents(t *testing.T) {
	t.Parallel()
	manager := newPlanTestManager(t, foundryShapedInstall())

	plan, err := manager.PlanUninstall([]string{"azure.ai.projects"}, UninstallPlanOptions{})
	require.Nil(t, plan, "a blocked request yields no plan")
	requiredErr, ok := errors.AsType[*ExtensionRequiredError](err)
	require.True(t, ok)
	require.Equal(t,
		map[string][]string{"azure.ai.projects": {"azure.ai.agents", "microsoft.foundry"}},
		requiredErr.Blocked,
	)
	require.EqualError(t, err,
		"extension azure.ai.projects is required by installed extensions: azure.ai.agents, microsoft.foundry")
	require.Contains(t, requiredErr.Suggestion(), "azd extension uninstall azure.ai.agents microsoft.foundry")
	require.Contains(t, requiredErr.Suggestion(), "--force")
}

func Test_PlanUninstall_BlockedReportsEveryTarget(t *testing.T) {
	t.Parallel()
	manager := newPlanTestManager(t, foundryShapedInstall())

	_, err := manager.PlanUninstall(
		[]string{"azure.ai.projects", "azure.ai.inspector"}, UninstallPlanOptions{},
	)
	requiredErr, ok := errors.AsType[*ExtensionRequiredError](err)
	require.True(t, ok)
	require.Len(t, requiredErr.Blocked, 2)
	require.Contains(t, err.Error(), "azure.ai.inspector: required by azure.ai.agents, microsoft.foundry")
	require.Contains(t, err.Error(), "azure.ai.projects: required by azure.ai.agents, microsoft.foundry")
	require.Equal(t, []string{"azure.ai.agents", "microsoft.foundry"}, requiredErr.dependents())
}

func Test_PlanUninstall_IgnoreDependents(t *testing.T) {
	t.Parallel()
	manager := newPlanTestManager(t, foundryShapedInstall())

	plan, err := manager.PlanUninstall(
		[]string{"azure.ai.projects"}, UninstallPlanOptions{IgnoreDependents: true},
	)
	require.NoError(t, err)
	require.Equal(t, []string{"azure.ai.projects"}, extensionIds(plan.Targets))
	require.Equal(t, []string{"azure.ai.agents", "microsoft.foundry"}, plan.Blocked["azure.ai.projects"])
	require.Empty(t, plan.Orphaned)
}

func Test_PlanUninstall_TargetsUnblockEachOther(t *testing.T) {
	t.Parallel()
	manager := newPlanTestManager(t, foundryShapedInstall())

	plan, err := manager.PlanUninstall(
		[]string{"microsoft.foundry", "azure.ai.agents"}, UninstallPlanOptions{},
	)
	require.NoError(t, err)
	require.Empty(t, plan.Blocked)
	require.Equal(t, []string{"microsoft.foundry", "azure.ai.agents"}, extensionIds(plan.Targets))
	require.Equal(t,
		[]string{"azure.ai.projects", "azure.ai.inspector", "azure.ai.skills"},
		extensionIds(plan.Orphaned),
	)
}

func Test_PlanUninstall_KeepDependencies(t *testing.T) {
	t.Parallel()
	manager := newPlanTestManager(t, foundryShapedInstall())

	plan, err := manager.PlanUninstall(
		[]string{"microsoft.foundry"}, UninstallPlanOptions{KeepDependencies: true},
	)
	require.NoError(t, err)
	require.Equal(t, []string{"microsoft.foundry"}, extensionIds(plan.Targets))
	require.Empty(t, plan.Orphaned)
	require.Empty(t, plan.Retained)
}

func Test_PlanUninstall_LegacyRecordsNeverRemovedOrBlocking(t *testing.T) {
	t.Parallel()
	// Records written before dependency tracking carry neither a snapshot nor a flag.
	manager := newPlanTestManager(t, map[string]*Extension{
		"microsoft.foundry": {Id: "microsoft.foundry", Version: "1.0.0", Source: MainRegistryName},
		"azure.ai.agents":   {Id: "azure.ai.agents", Version: "2.0.0", Source: MainRegistryName},
	})

	plan, err := manager.PlanUninstall([]string{"microsoft.foundry"}, UninstallPlanOptions{})
	require.NoError(t, err)
	require.Equal(t, []string{"microsoft.foundry"}, extensionIds(plan.Targets))
	require.Empty(t, plan.Orphaned)

	plan, err = manager.PlanUninstall([]string{"azure.ai.agents"}, UninstallPlanOptions{})
	require.NoError(t, err)
	require.Empty(t, plan.Blocked)
}

func Test_PlanUninstall_LegacyDependencyStaysWhenParentHasSnapshot(t *testing.T) {
	t.Parallel()
	manager := newPlanTestManager(t, map[string]*Extension{
		"microsoft.foundry": installedRecord("microsoft.foundry", "1.0.0", false, "azure.ai.agents"),
		"azure.ai.agents":   {Id: "azure.ai.agents", Version: "2.0.0", Source: MainRegistryName},
	})

	plan, err := manager.PlanUninstall([]string{"microsoft.foundry"}, UninstallPlanOptions{})
	require.NoError(t, err)
	require.Empty(t, plan.Orphaned)
	require.Len(t, plan.Retained, 1)
	require.Equal(t, "azure.ai.agents", plan.Retained[0].Extension.Id)
	require.Empty(t, plan.Retained[0].RequiredBy)
}

func Test_PlanUninstall_RejectsBlankId(t *testing.T) {
	t.Parallel()
	manager := newPlanTestManager(t, foundryShapedInstall())

	// A blank id matches an arbitrary installed record through the installed filter.
	_, err := manager.PlanUninstall([]string{" "}, UninstallPlanOptions{})
	require.ErrorIs(t, err, ErrEmptyExtensionId)
	require.ErrorIs(t, manager.Uninstall(t.Context(), ""), ErrEmptyExtensionId)

	// A blank dependency id in a snapshot is ignored rather than resolved to a random record.
	installed := foundryShapedInstall()
	installed["microsoft.foundry"].Dependencies = append(
		installed["microsoft.foundry"].Dependencies, ExtensionDependency{Id: ""},
	)
	manager = newPlanTestManager(t, installed)
	plan, err := manager.PlanUninstall([]string{"microsoft.foundry"}, UninstallPlanOptions{})
	require.NoError(t, err)
	require.Len(t, plan.Orphaned, 4)
}

func Test_PlanUninstall_UnknownExtension(t *testing.T) {
	t.Parallel()
	manager := newPlanTestManager(t, foundryShapedInstall())

	_, err := manager.PlanUninstall([]string{"missing"}, UninstallPlanOptions{})
	require.ErrorIs(t, err, ErrInstalledExtensionNotFound)
}

func Test_PlanUninstall_DuplicateIds(t *testing.T) {
	t.Parallel()
	manager := newPlanTestManager(t, foundryShapedInstall())

	plan, err := manager.PlanUninstall(
		[]string{"microsoft.foundry", "MICROSOFT.FOUNDRY"}, UninstallPlanOptions{},
	)
	require.NoError(t, err)
	require.Len(t, plan.Targets, 1)
}

func Test_InstalledDependents(t *testing.T) {
	t.Parallel()
	manager := newPlanTestManager(t, foundryShapedInstall())

	dependents, err := manager.InstalledDependents("Azure.AI.Projects")
	require.NoError(t, err)
	require.Equal(t, []string{"azure.ai.agents", "microsoft.foundry"}, extensionIds(dependents))

	dependents, err = manager.InstalledDependents("microsoft.foundry")
	require.NoError(t, err)
	require.Empty(t, dependents)
}

// newInstallTestManager builds a manager over the given sources whose artifact downloads are
// served by the mock HTTP client and whose install directory is a temp dir.
func newInstallTestManager(t *testing.T, sources ...Source) *Manager {
	t.Helper()
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())

	mockContext := mocks.NewMockContext(t.Context())
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return strings.HasPrefix(request.URL.String(), "https://aka.ms/azd/extensions/registry/")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, []byte("test data"))
	})

	userConfigManager := config.NewUserConfigManager(mockContext.ConfigManager)
	sourceManager := NewSourceManager(mockContext.Container, userConfigManager, mockContext.HttpClient)
	lazyRunner := lazy.NewLazy(func() (*Runner, error) {
		return NewRunner(mockContext.CommandRunner), nil
	})
	manager, err := NewManager(userConfigManager, sourceManager, lazyRunner, mockContext.HttpClient)
	require.NoError(t, err)
	manager.sources = sources
	return manager
}

// packWithLeaf returns a pack that depends on a leaf extension published at the given versions.
func packWithLeaf(packVersion string, leafVersions ...string) (*ExtensionMetadata, *ExtensionMetadata) {
	pack := &ExtensionMetadata{
		Id:     "test.pack",
		Source: MainRegistryName,
		Versions: []ExtensionVersion{{
			Version:      packVersion,
			Dependencies: []ExtensionDependency{{Id: "test.leaf", Version: ">=1.0.0"}},
		}},
	}
	leaf := &ExtensionMetadata{Id: "test.leaf", Source: MainRegistryName}
	for _, version := range leafVersions {
		leaf.Versions = append(leaf.Versions, ExtensionVersion{Version: version, Artifacts: sampleArtifacts})
	}
	return pack, leaf
}

func Test_Install_RecordsDependenciesAndOwnership(t *testing.T) {
	pack, leaf := packWithLeaf("1.0.0", "1.0.0")
	manager := newInstallTestManager(t, &mockSource{
		name:       MainRegistryName,
		extensions: []*ExtensionMetadata{pack, leaf},
	})

	_, err := manager.Install(t.Context(), pack, "")
	require.NoError(t, err)

	packRecord, err := manager.GetInstalled(FilterOptions{Id: "test.pack"})
	require.NoError(t, err)
	require.False(t, packRecord.InstalledAsDependency)
	require.Equal(t, []ExtensionDependency{{Id: "test.leaf", Version: ">=1.0.0"}}, packRecord.Dependencies)

	leafRecord, err := manager.GetInstalled(FilterOptions{Id: "test.leaf"})
	require.NoError(t, err)
	require.True(t, leafRecord.InstalledAsDependency)
	require.Empty(t, leafRecord.Dependencies)
}

func Test_Install_SkipDependencies_StillRecordsDeclaredDependencies(t *testing.T) {
	pack, leaf := packWithLeaf("1.0.0", "1.0.0")
	manager := newInstallTestManager(t, &mockSource{
		name:       MainRegistryName,
		extensions: []*ExtensionMetadata{pack, leaf},
	})

	_, err := manager.InstallWithOptions(t.Context(), pack, InstallOptions{SkipDependencies: true})
	require.NoError(t, err)

	packRecord, err := manager.GetInstalled(FilterOptions{Id: "test.pack"})
	require.NoError(t, err)
	require.Equal(t, []ExtensionDependency{{Id: "test.leaf", Version: ">=1.0.0"}}, packRecord.Dependencies)

	_, err = manager.GetInstalled(FilterOptions{Id: "test.leaf"})
	require.ErrorIs(t, err, ErrInstalledExtensionNotFound)
}

func Test_Upgrade_PreservesOwnershipOfDependencies(t *testing.T) {
	pack, leaf := packWithLeaf("1.0.0", "1.0.0", "2.0.0")
	manager := newInstallTestManager(t, &mockSource{
		name:       MainRegistryName,
		extensions: []*ExtensionMetadata{pack, leaf},
	})
	require.NoError(t, manager.userConfig.Set(installedConfigKey, map[string]*Extension{
		"test.pack": installedRecord("test.pack", "1.0.0", false, "test.leaf"),
		"test.leaf": installedRecord("test.leaf", "1.0.0", true),
	}))
	manager.installed = nil

	_, results, err := manager.Upgrade(t.Context(), pack, DefaultUpgradeOptions(""))
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, UpgradeStatusUpgraded, results[0].Status)
	require.Equal(t, "2.0.0", results[0].ToVersion)

	leafRecord, err := manager.GetInstalled(FilterOptions{Id: "test.leaf"})
	require.NoError(t, err)
	require.True(t, leafRecord.InstalledAsDependency, "a dependency update keeps the dependency flag")

	packRecord, err := manager.GetInstalled(FilterOptions{Id: "test.pack"})
	require.NoError(t, err)
	require.False(t, packRecord.InstalledAsDependency)
	require.Equal(t, []ExtensionDependency{{Id: "test.leaf", Version: ">=1.0.0"}}, packRecord.Dependencies)
}

func Test_Upgrade_PromoteToExplicit(t *testing.T) {
	pack, leaf := packWithLeaf("1.0.0", "1.0.0", "2.0.0")
	manager := newInstallTestManager(t, &mockSource{
		name:       MainRegistryName,
		extensions: []*ExtensionMetadata{pack, leaf},
	})
	require.NoError(t, manager.userConfig.Set(installedConfigKey, map[string]*Extension{
		"test.leaf": installedRecord("test.leaf", "1.0.0", true),
	}))
	manager.installed = nil

	// `azd extension install test.leaf` over a dependency-installed record.
	_, _, err := manager.Upgrade(t.Context(), leaf, UpgradeOptions{PromoteToExplicit: true})
	require.NoError(t, err)

	leafRecord, err := manager.GetInstalled(FilterOptions{Id: "test.leaf"})
	require.NoError(t, err)
	require.Equal(t, "2.0.0", leafRecord.Version)
	require.False(t, leafRecord.InstalledAsDependency)
}

func Test_Upgrade_BackfillsDependencySnapshotOfCurrentChildren(t *testing.T) {
	// pack -> child -> leaf, every record written before dependency tracking existed and
	// already at the published version, so the update reinstalls only the pack.
	pack := &ExtensionMetadata{
		Id:     "test.pack",
		Source: MainRegistryName,
		Versions: []ExtensionVersion{{
			Version:      "1.0.0",
			Dependencies: []ExtensionDependency{{Id: "test.child", Version: ">=1.0.0"}},
		}},
	}
	child := &ExtensionMetadata{
		Id:     "test.child",
		Source: MainRegistryName,
		Versions: []ExtensionVersion{{
			Version:      "1.0.0",
			Artifacts:    sampleArtifacts,
			Dependencies: []ExtensionDependency{{Id: "test.leaf", Version: ">=1.0.0"}},
		}},
	}
	leaf := &ExtensionMetadata{
		Id:       "test.leaf",
		Source:   MainRegistryName,
		Versions: []ExtensionVersion{{Version: "1.0.0", Artifacts: sampleArtifacts}},
	}
	manager := newInstallTestManager(t, &mockSource{
		name:       MainRegistryName,
		extensions: []*ExtensionMetadata{pack, child, leaf},
	})
	require.NoError(t, manager.userConfig.Set(installedConfigKey, map[string]*Extension{
		"test.pack":  {Id: "test.pack", Version: "1.0.0", Source: MainRegistryName},
		"test.child": {Id: "test.child", Version: "1.0.0", Source: MainRegistryName},
		"test.leaf":  {Id: "test.leaf", Version: "1.0.0", Source: MainRegistryName},
	}))
	manager.installed = nil

	_, results, err := manager.Upgrade(t.Context(), pack, DefaultUpgradeOptions(""))
	require.NoError(t, err)
	require.Empty(t, results, "every child is already current")

	childRecord, err := manager.GetInstalled(FilterOptions{Id: "test.child"})
	require.NoError(t, err)
	require.Equal(t, []ExtensionDependency{{Id: "test.leaf", Version: ">=1.0.0"}}, childRecord.Dependencies)
	require.False(t, childRecord.InstalledAsDependency, "backfill never guesses ownership")

	// The uninstall planner now protects the leaf through the child's snapshot.
	_, err = manager.PlanUninstall([]string{"test.leaf"}, UninstallPlanOptions{})
	require.Error(t, err)
	var requiredErr *ExtensionRequiredError
	require.ErrorAs(t, err, &requiredErr)
	require.Equal(t, []string{"test.child"}, requiredErr.Blocked["test.leaf"])
}

func Test_ReconcileDependencies_BackfillsLegacyDependencySnapshot(t *testing.T) {
	pack, leaf := packWithLeaf("1.0.0", "1.0.0")
	manager := newInstallTestManager(t, &mockSource{
		name:       MainRegistryName,
		extensions: []*ExtensionMetadata{pack, leaf},
	})
	require.NoError(t, manager.userConfig.Set(installedConfigKey, map[string]*Extension{
		"test.pack": {Id: "test.pack", Version: "1.0.0", Source: MainRegistryName},
		"test.leaf": {Id: "test.leaf", Version: "1.0.0", Source: MainRegistryName},
	}))
	manager.installed = nil

	_, results, err := manager.ReconcileDependencies(t.Context(), pack, DefaultUpgradeOptions(""))
	require.NoError(t, err)
	require.Empty(t, results)

	packRecord, err := manager.GetInstalled(FilterOptions{Id: "test.pack"})
	require.NoError(t, err)
	require.Equal(t, []ExtensionDependency{{Id: "test.leaf", Version: ">=1.0.0"}}, packRecord.Dependencies)
	require.False(t, packRecord.InstalledAsDependency, "backfill never guesses ownership")

	leafRecord, err := manager.GetInstalled(FilterOptions{Id: "test.leaf"})
	require.NoError(t, err)
	require.False(t, leafRecord.InstalledAsDependency)
}

func Test_MarkExplicitlyInstalled(t *testing.T) {
	t.Parallel()
	manager := newPlanTestManager(t, map[string]*Extension{
		"test.leaf": installedRecord("test.leaf", "1.0.0", true),
	})

	require.NoError(t, manager.MarkExplicitlyInstalled("test.leaf"))
	record, err := manager.GetInstalled(FilterOptions{Id: "test.leaf"})
	require.NoError(t, err)
	require.False(t, record.InstalledAsDependency)

	require.NoError(t, manager.MarkExplicitlyInstalled("test.leaf"))
	require.ErrorIs(t, manager.MarkExplicitlyInstalled("missing"), ErrInstalledExtensionNotFound)
}
