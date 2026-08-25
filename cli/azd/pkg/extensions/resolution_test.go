// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"errors"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/stretchr/testify/require"
)

func Test_CreateExtensionFilter_AzdCompatibilityUsesSelectedVersion(t *testing.T) {
	foundry := []Provider{{Type: ProvisioningProviderType, Name: "microsoft.foundry"}}
	ext := &ExtensionMetadata{
		Id: "test.compatibility",
		Versions: []ExtensionVersion{
			{
				Version:      "1.0.0",
				Capabilities: []CapabilityType{ProvisioningProviderCapability},
				Providers:    foundry,
			},
			{
				Version:            "2.0.0",
				RequiredAzdVersion: ">=2.0.0",
			},
		},
	}

	// Raw catalogue capability searches retain their historical any-version semantics.
	require.True(t, createExtensionFilter(&FilterOptions{
		Capability: ProvisioningProviderCapability,
	})(ext))

	testCases := []struct {
		name       string
		azdVersion *semver.Version
		version    string
		shouldFind bool
	}{
		{
			name: "latest release does not provide requested metadata",
		},
		{
			name:       "compatible release provides requested metadata",
			azdVersion: semver.MustParse("1.0.0"),
			shouldFind: true,
		},
		{
			name:       "newer azd selects latest release",
			azdVersion: semver.MustParse("2.0.0"),
		},
		{
			name:       "no compatible release",
			azdVersion: semver.MustParse("1.0.0"),
			version:    "2.0.0",
		},
	}

	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			manager := newTestManager(t)
			manager.azdVersion = test.azdVersion
			manager.sources = []Source{&mockSource{name: "test", extensions: []*ExtensionMetadata{ext}}}

			result, err := manager.ResolveExtensions(t.Context(), &InstallResolutionOptions{
				FilterOptions: FilterOptions{
					Version:    test.version,
					Capability: ProvisioningProviderCapability,
					Provider:   "microsoft.foundry",
				},
			})
			require.NoError(t, err)
			require.Equal(t, test.shouldFind, len(result.Matches) == 1)
		})
	}
}

func Test_ResolveExtensions_MissingVersionReportsCompatibleAlternative(t *testing.T) {
	extension := &ExtensionMetadata{
		Id:     "test.extension",
		Source: "local",
		Versions: []ExtensionVersion{
			{Version: "1.0.0"},
			{Version: "2.0.0", RequiredAzdVersion: ">=2.0.0"},
		},
	}
	source := &mockSource{name: "local", extensions: []*ExtensionMetadata{extension}}
	manager := newTestManager(t)
	manager.azdVersion = semver.MustParse("1.0.0")
	manager.sources = []Source{source}

	matches, err := manager.FindInstallableExtensions(t.Context(), &InstallResolutionOptions{
		FilterOptions: FilterOptions{
			Id:      extension.Id,
			Version: "0.1.0",
			Source:  source.name,
		},
	})

	require.Nil(t, matches)
	versionErr, ok := errors.AsType[*ExtensionVersionNotFoundError](err)
	require.True(t, ok)
	require.Equal(t, 1, source.listCalls, "resolution should enumerate each source once")
	require.Equal(
		t,
		`extension "test.extension" version "0.1.0" was not found; latest compatible version is "1.0.0"`,
		versionErr.Error(),
	)
	require.Contains(t, versionErr.Suggestion(), "--version 1.0.0 --source local")
	require.NotContains(t, versionErr.Suggestion(), "--version 2.0.0")
}

func Test_ResolveExtensions_ReportsAzdIncompatibility(t *testing.T) {
	extension := &ExtensionMetadata{
		Id: "test.extension",
		Versions: []ExtensionVersion{{
			Version:            "2.0.0",
			RequiredAzdVersion: ">=2.0.0",
			Capabilities:       []CapabilityType{ProvisioningProviderCapability},
			Providers: []Provider{{
				Type: ProvisioningProviderType,
				Name: "microsoft.foundry",
			}},
		}},
	}
	manager := newTestManager(t)
	manager.azdVersion = semver.MustParse("1.0.0")
	manager.sources = []Source{&mockSource{name: "test", extensions: []*ExtensionMetadata{extension}}}

	t.Run("extension id", func(t *testing.T) {
		matches, err := manager.FindInstallableExtensions(t.Context(), &InstallResolutionOptions{
			FilterOptions: FilterOptions{Id: extension.Id},
		})

		require.Nil(t, matches)
		compatibilityErr, ok := errors.AsType[*ExtensionAzdVersionIncompatibleError](err)
		require.True(t, ok)
		require.Equal(
			t,
			`no version of extension "test.extension" is compatible with azd 1.0.0`,
			compatibilityErr.Error(),
		)
		require.Contains(t, compatibilityErr.Suggestion(), `satisfies ">=2.0.0"`)
	})

	t.Run("provider", func(t *testing.T) {
		matches, err := manager.FindInstallableExtensions(t.Context(), &InstallResolutionOptions{
			FilterOptions: FilterOptions{
				Capability: ProvisioningProviderCapability,
				Provider:   "microsoft.foundry",
			},
		})

		require.Nil(t, matches)
		compatibilityErr, ok := errors.AsType[*ExtensionAzdVersionIncompatibleError](err)
		require.True(t, ok)
		require.Equal(
			t,
			`no extension compatible with azd 1.0.0 provides provisioning provider "microsoft.foundry"`,
			compatibilityErr.Error(),
		)
	})
}

func Test_ManagerCompatibilityDefaultsAndOptOut(t *testing.T) {
	newManager := func(t *testing.T) (*Manager, *ExtensionMetadata) {
		t.Helper()
		manager := newTestManager(t)
		manager.azdVersion = semver.MustParse("1.0.0")
		extension := &ExtensionMetadata{
			Id: "test.pack",
			Versions: []ExtensionVersion{
				{
					Version:      "1.0.0",
					Dependencies: []ExtensionDependency{{Id: "test.child"}},
				},
				{
					Version:            "2.0.0",
					RequiredAzdVersion: ">=2.0.0",
					Dependencies:       []ExtensionDependency{{Id: "test.child"}},
				},
			},
		}
		require.NoError(t, manager.userConfig.Set(installedConfigKey, map[string]*Extension{
			"test.child": {Id: "test.child", Version: "1.0.0"},
		}))
		manager.installed = nil
		return manager, extension
	}

	t.Run("install defaults to manager version", func(t *testing.T) {
		manager, extension := newManager(t)

		version, err := manager.InstallWithOptions(t.Context(), extension, InstallOptions{})

		require.NoError(t, err)
		require.Equal(t, "1.0.0", version.Version)
	})

	t.Run("manager opt out installs latest overall", func(t *testing.T) {
		manager := newTestManagerWithOptions(t, ManagerOptions{
			IgnoreAzdCompatibility: true,
		})
		extension := &ExtensionMetadata{
			Id: "test.pack",
			Versions: []ExtensionVersion{
				{
					Version:      "1.0.0",
					Dependencies: []ExtensionDependency{{Id: "test.child"}},
				},
				{
					Version:            "2.0.0",
					RequiredAzdVersion: ">=2.0.0",
					Dependencies:       []ExtensionDependency{{Id: "test.child"}},
				},
			},
		}
		require.NoError(t, manager.userConfig.Set(installedConfigKey, map[string]*Extension{
			"test.child": {Id: "test.child", Version: "1.0.0"},
		}))
		manager.installed = nil

		version, err := manager.InstallWithOptions(t.Context(), extension, InstallOptions{})

		require.NoError(t, err)
		require.Equal(t, "2.0.0", version.Version)
	})

	t.Run("manager opt out overrides explicit version", func(t *testing.T) {
		manager := newTestManagerWithOptions(t, ManagerOptions{
			AzdVersion:             semver.MustParse("1.0.0"),
			IgnoreAzdCompatibility: true,
		})

		require.Nil(t, manager.AzdVersion())
	})
}

func TestClassifyInstallResolution_SelectedCandidate(t *testing.T) {
	extension := &ExtensionMetadata{
		Id:     "test.extension",
		Source: "local",
		Versions: []ExtensionVersion{
			{Version: "1.0.0"},
			{Version: "2.0.0", RequiredAzdVersion: ">=2.0.0"},
		},
	}

	result := ClassifyInstallResolution(
		[]*ExtensionMetadata{extension},
		&InstallResolutionOptions{FilterOptions: FilterOptions{Id: extension.Id}},
		semver.MustParse("1.0.0"),
	)

	require.Len(t, result.Matches, 1)
	candidate := result.Candidate(extension)
	require.NotNil(t, candidate)
	require.Equal(t, "1.0.0", candidate.Version.Version)
	require.Equal(t, "2.0.0", candidate.LatestOverall.Version)
	require.Equal(t, "1.0.0", candidate.LatestCompatible.Version)
	require.True(t, candidate.HasNewerIncompatible)
}

func TestClassifyInstallResolution_ProviderUsesSelectedRelease(t *testing.T) {
	provisioningDemo := ExtensionVersion{
		Version:      "2.0.0",
		Capabilities: []CapabilityType{ProvisioningProviderCapability},
		Providers:    []Provider{{Type: ProvisioningProviderType, Name: "demo"}},
	}
	serviceTargetDemo := ExtensionVersion{
		Version:      "1.0.0",
		Capabilities: []CapabilityType{ServiceTargetProviderCapability},
		Providers:    []Provider{{Type: ServiceTargetProviderType, Name: "demo"}},
	}
	current := &ExtensionMetadata{
		Id:       "current",
		Versions: []ExtensionVersion{provisioningDemo, serviceTargetDemo},
	}
	superseded := &ExtensionMetadata{
		Id:       "superseded",
		Versions: []ExtensionVersion{serviceTargetDemo, {Version: "3.0.0"}},
	}

	t.Run("keeps extensions whose selected version provides the provider", func(t *testing.T) {
		result := ClassifyInstallResolution(
			[]*ExtensionMetadata{current, superseded},
			&InstallResolutionOptions{FilterOptions: FilterOptions{
				Capability: ProvisioningProviderCapability,
				Provider:   "demo",
			}},
			nil,
		)

		require.Len(t, result.Matches, 1)
		require.Equal(t, "current", result.Matches[0].Id)
		require.Len(t, result.Matches[0].Versions, 2, "published versions should not be narrowed")
		require.Empty(t, result.IncompatibleMatches)
	})

	t.Run("ignores versions other than the selected one", func(t *testing.T) {
		result := ClassifyInstallResolution(
			[]*ExtensionMetadata{current},
			&InstallResolutionOptions{FilterOptions: FilterOptions{
				Capability: ServiceTargetProviderCapability,
				Provider:   "demo",
			}},
			nil,
		)

		require.Empty(t, result.Matches, "only the superseded 1.0.0 publishes the service target")
		require.Empty(t, result.IncompatibleMatches)
	})

	t.Run("requires the provider type to match the capability", func(t *testing.T) {
		result := ClassifyInstallResolution(
			[]*ExtensionMetadata{{
				Id:       "provisioning.only",
				Versions: []ExtensionVersion{provisioningDemo},
			}},
			&InstallResolutionOptions{FilterOptions: FilterOptions{
				Capability: ServiceTargetProviderCapability,
				Provider:   "demo",
			}},
			nil,
		)

		require.Empty(t, result.Matches)
		require.Empty(t, result.IncompatibleMatches)
	})

	t.Run("silent drop when newest compatible release dropped the provider", func(t *testing.T) {
		extension := &ExtensionMetadata{
			Id: "dropped.provider",
			Versions: []ExtensionVersion{
				{
					Version:      "1.0.0",
					Capabilities: []CapabilityType{ProvisioningProviderCapability},
					Providers:    []Provider{{Type: ProvisioningProviderType, Name: "demo"}},
				},
				{Version: "2.0.0"},
			},
		}

		result := ClassifyInstallResolution(
			[]*ExtensionMetadata{extension},
			&InstallResolutionOptions{FilterOptions: FilterOptions{
				Capability: ProvisioningProviderCapability,
				Provider:   "demo",
			}},
			semver.MustParse("1.0.0"),
		)

		require.Empty(t, result.Matches)
		require.Empty(t, result.IncompatibleMatches)
		require.Nil(t, result.Error())
	})
}
