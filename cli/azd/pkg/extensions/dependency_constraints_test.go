// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/stretchr/testify/require"
)

// constraintTestExtension returns an extension from the main registry published at versions.
func constraintTestExtension(id string, versions ...ExtensionVersion) *ExtensionMetadata {
	for i := range versions {
		versions[i].Artifacts = sampleArtifacts
	}
	return &ExtensionMetadata{Id: id, Source: MainRegistryName, Versions: versions}
}

func requireInstalledVersion(t *testing.T, manager *Manager, id, version string) {
	t.Helper()
	installed, err := manager.GetInstalled(FilterOptions{Id: id})
	require.NoError(t, err)
	require.Equal(t, version, installed.Version, id)
}

func Test_Install_PackDependency_KeepsSiblingConstraints(t *testing.T) {
	leafRange := ExtensionDependency{Id: "test.leaf", Version: "~1.0.0"}
	childRange := ExtensionDependency{Id: "test.child", Version: "~1.0.0"}
	tests := []struct {
		name         string
		dependencies []ExtensionDependency
		installed    map[string]*Extension
	}{
		{
			name:         "installed sibling",
			dependencies: []ExtensionDependency{leafRange, childRange},
			installed: map[string]*Extension{
				"test.leaf":  installedRecord("test.leaf", "1.0.0", false),
				"test.child": installedRecord("test.child", "1.0.0-beta.1", false),
			},
		},
		{
			name:         "fresh sibling",
			dependencies: []ExtensionDependency{leafRange, childRange},
			installed: map[string]*Extension{
				"test.child": installedRecord("test.child", "1.0.0-beta.1", false),
			},
		},
		{
			name:         "sibling installed by a fresh member",
			dependencies: []ExtensionDependency{childRange, leafRange},
			installed:    map[string]*Extension{},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			leaf := constraintTestExtension("test.leaf",
				ExtensionVersion{Version: "1.0.0"},
				ExtensionVersion{Version: "1.1.0"},
			)
			child := constraintTestExtension("test.child",
				ExtensionVersion{Version: "1.0.0-beta.1"},
				ExtensionVersion{
					Version:      "1.0.0",
					Dependencies: []ExtensionDependency{{Id: "test.leaf", Version: ">=1.0.0"}},
				},
			)
			pack := &ExtensionMetadata{Id: "test.pack", Source: MainRegistryName, Versions: []ExtensionVersion{
				{Version: "1.0.0", Dependencies: test.dependencies},
			}}
			manager := newInstallTestManager(t, &mockSource{
				name: MainRegistryName, extensions: []*ExtensionMetadata{pack, child, leaf},
			})
			require.NoError(t, manager.userConfig.Set(installedConfigKey, test.installed))

			_, err := manager.Install(t.Context(), pack, "")
			require.NoError(t, err)
			requireInstalledVersion(t, manager, "test.pack", "1.0.0")
			requireInstalledVersion(t, manager, "test.child", "1.0.0")
			requireInstalledVersion(t, manager, "test.leaf", "1.0.0")
		})
	}
}

func Test_Upgrade_NewMemberRespectsDependencyUpdatePolicy(t *testing.T) {
	for _, upgradeDependencies := range []bool{false, true} {
		t.Run(map[bool]string{false: "updates disabled", true: "updates enabled"}[upgradeDependencies], func(t *testing.T) {
			leaf := constraintTestExtension("test.leaf",
				ExtensionVersion{Version: "1.0.0"},
				ExtensionVersion{Version: "2.0.0"},
			)
			member := constraintTestExtension("test.member", ExtensionVersion{
				Version:      "1.0.0",
				Dependencies: []ExtensionDependency{{Id: "test.leaf", Version: "~2.0.0"}},
			})
			pack := &ExtensionMetadata{Id: "test.pack", Source: MainRegistryName, Versions: []ExtensionVersion{
				{Version: "1.0.0", Dependencies: []ExtensionDependency{{Id: "test.leaf"}}},
				{Version: "2.0.0", Dependencies: []ExtensionDependency{{Id: "test.leaf"}, {Id: "test.member"}}},
			}}
			manager := newInstallTestManager(t, &mockSource{
				name: MainRegistryName, extensions: []*ExtensionMetadata{pack, member, leaf},
			})
			require.NoError(t, manager.userConfig.Set(installedConfigKey, map[string]*Extension{
				"test.pack": installedRecord("test.pack", "1.0.0", false, "test.leaf"),
				"test.leaf": installedRecord("test.leaf", "1.0.0", true),
			}))

			_, results, err := manager.Upgrade(t.Context(), pack, UpgradeOptions{UpgradeDependencies: upgradeDependencies})
			if !upgradeDependencies {
				require.ErrorContains(t, err, "dependency updates are disabled")
				require.ErrorContains(t, err, `test.leaf 1.0.0 is installed, but test.member requires "~2.0.0"`)
				requireInstalledVersion(t, manager, "test.pack", "1.0.0")
				requireInstalledVersion(t, manager, "test.leaf", "1.0.0")
				return
			}
			require.NoError(t, err)
			requireInstalledVersion(t, manager, "test.leaf", "2.0.0")
			require.Len(t, results, 1)
			require.Equal(t, "test.leaf", results[0].ExtensionId)
			require.Equal(t, UpgradeStatusUpgraded, results[0].Status)
			require.Equal(t, "1.0.0", results[0].FromVersion)
			require.Equal(t, "2.0.0", results[0].ToVersion)
		})
	}
}

func Test_BestVersionForConstraints(t *testing.T) {
	t.Parallel()

	versions := []ExtensionVersion{
		{Version: "1.0.0"},
		{Version: "1.5.0"},
		{Version: "2.0.0", RequiredAzdVersion: ">=99.0.0"},
		{Version: "3.0.0"},
	}
	tests := []struct {
		name        string
		constraints []string
		want        string
	}{
		{name: "no constraints", want: "3.0.0"},
		{
			name:        "each constraint checked separately",
			constraints: []string{">=1.0.0 <2.0.0 || >=3.0.0", "~1.0.0"},
			want:        "1.0.0",
		},
		{name: "incompatible release skipped", constraints: []string{"<3.0.0"}, want: "1.5.0"},
		{name: "conflicting constraints", constraints: []string{"~1.0.0", ">=3.0.0"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var constraints []DependencyConstraint
			for _, constraint := range test.constraints {
				constraints = append(constraints, DependencyConstraint{ParentId: "test.pack", Constraint: constraint})
			}
			best := bestVersionForConstraints(constraints, versions, semver.MustParse("1.0.0"))
			if test.want == "" {
				require.Nil(t, best)
				return
			}
			require.NotNil(t, best)
			require.Equal(t, test.want, best.Version)
		})
	}
}

func Test_DependencyConstraintsAndConflicts(t *testing.T) {
	manager := newInstallTestManager(t)
	require.NoError(t, manager.userConfig.Set(installedConfigKey, map[string]*Extension{
		"Test.Pack": {
			Id: "Test.Pack", Version: "1.0.0",
			Dependencies: []ExtensionDependency{{Id: "test.child", Version: "~1.0.0"}},
		},
		"other.pack": {
			Id: "other.pack", Version: "1.0.0",
			Dependencies: []ExtensionDependency{{Id: "TEST.CHILD", Version: ">=1.0.0"}, {Id: "test.leaf"}},
		},
		"unrelated.pack": {
			Id: "unrelated.pack", Version: "1.0.0",
			Dependencies: []ExtensionDependency{{Id: "test.leaf", Version: "~1.0.0"}},
		},
		"test.child": {Id: "test.child", Version: "1.1.0"},
		"test.leaf":  {Id: "test.leaf", Version: "2.0.0"},
	}))

	constraints, err := manager.dependencyConstraints("Test.Child", newDependencyWalk())
	require.NoError(t, err)
	require.Equal(t, []DependencyConstraint{
		{ParentId: "other.pack", Constraint: ">=1.0.0"},
		{ParentId: "Test.Pack", Constraint: "~1.0.0"},
	}, constraints)

	conflicts, err := manager.DependencyConflicts([]string{"TEST.CHILD"})
	require.NoError(t, err)
	require.Len(t, conflicts, 1)
	require.Equal(t, "Test.Pack", conflicts[0].ParentId)
	require.Equal(t, "test.child", conflicts[0].Result.ExtensionId)
	require.Equal(t, UpgradeStatusFailed, conflicts[0].Result.Status)
	require.Equal(t, &DependencyVersionConflictError{
		DependencyId: "test.child", ParentId: "Test.Pack", Constraint: "~1.0.0", InstalledVersion: "1.1.0",
	}, conflicts[0].Result.Error)
	require.Contains(t, conflicts[0].Result.Suggestion, `Install a version of test.child that satisfies "~1.0.0"`)

	conflicts, err = manager.DependencyConflicts([]string{"unrelated.pack"})
	require.NoError(t, err)
	require.Len(t, conflicts, 1)
	require.Equal(t, "test.leaf", conflicts[0].Result.ExtensionId)
}

func Test_IsDependencyConflict(t *testing.T) {
	t.Parallel()

	conflict := &DependencyVersionConflictError{DependencyId: "test.child"}
	constraints := &DependencyConstraintsConflictError{DependencyId: "test.leaf"}
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil"},
		{name: "other error", err: errors.New("network unreachable")},
		{name: "wrapped conflict", err: fmt.Errorf("updating: %w", conflict), want: true},
		{name: "joined conflicts", err: errors.Join(conflict, fmt.Errorf("nested: %w", constraints)), want: true},
		{name: "conflict joined with other error", err: errors.Join(conflict, errors.New("network unreachable"))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, test.want, IsDependencyConflict(test.err))
		})
	}
}
