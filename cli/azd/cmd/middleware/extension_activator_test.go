// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"errors"
	"net/http"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	provisioningTest "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/test"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

const testExtensionRegistryURL = "https://aka.ms/azd/extensions/registry"

// stubProviderRegistry makes the extension registry respond with a single extension that declares
// the given provisioning provider, so registry lookups resolve deterministically without network.
func stubProviderRegistry(mockCtx *mocks.MockContext, extensionID, providerName string) {
	stubProviderRegistryVersions(mockCtx, extensionID, []extensions.ExtensionVersion{{
		Version:      "1.0.0",
		Capabilities: []extensions.CapabilityType{extensions.ProvisioningProviderCapability},
		Providers: []extensions.Provider{{
			Name: providerName,
			Type: extensions.ProvisioningProviderType,
		}},
	}})
}

func stubProviderRegistryVersions(
	mockCtx *mocks.MockContext,
	extensionID string,
	versions []extensions.ExtensionVersion,
) {
	registry := extensions.Registry{
		Extensions: []*extensions.ExtensionMetadata{
			{
				Id:       extensionID,
				Versions: versions,
			},
		},
	}

	mockCtx.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == testExtensionRegistryURL
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, registry)
	})
}

// newTestExtensionActivator builds an ExtensionActivator backed by an in-memory extension manager
// seeded with the given installed extensions, using the mock context's container as the service
// locator so tests can register (or omit) resolvable provisioning providers.
func newTestExtensionActivator(
	t *testing.T,
	mockCtx *mocks.MockContext,
	installed map[string]*extensions.Extension,
) *ExtensionActivator {
	t.Helper()
	manager := createExtensionsManager(t, mockCtx, installed)
	runner := extensions.NewRunner(exec.NewCommandRunner(nil))
	return NewExtensionActivator(mockCtx.Container, manager, runner, &internal.GlobalCommandOptions{})
}

func Test_NewExtensionActivator(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(t.Context())
	activator := newTestExtensionActivator(t, mockCtx, nil)
	require.NotNil(t, activator)
}

func Test_ExtensionActivator_providerResolvable(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(t.Context())

	// Register a named provisioning provider so it resolves from the container, as if the owning
	// extension host were already running.
	ioc.RegisterNamedInstance[provisioning.Provider](
		mockCtx.Container, "microsoft.foundry", provisioningTest.NewTestProvider(nil, nil, nil, nil))

	activator := newTestExtensionActivator(t, mockCtx, nil)

	require.True(t, activator.providerResolvable("microsoft.foundry"))
	require.False(t, activator.providerResolvable("bicep"))
}

func Test_EnsureProvisioningProviders_NoMatchingExtension(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(t.Context())

	// The installed extension declares a different provider, so nothing is started for the
	// requested name; it is left to native resolution.
	installed := map[string]*extensions.Extension{
		"other.ext": {
			Id:           "other.ext",
			Capabilities: []extensions.CapabilityType{extensions.ProvisioningProviderCapability},
			Providers:    []extensions.Provider{{Name: "other.provider"}},
		},
	}
	activator := newTestExtensionActivator(t, mockCtx, installed)

	cleanup, err := activator.EnsureProvisioningProviders(t.Context(), []string{"microsoft.foundry"}, "env1")
	require.NoError(t, err)
	require.NotNil(t, cleanup)
	cleanup()
}

func Test_EnsureProvisioningProviders_EmptyRequest(t *testing.T) {
	t.Parallel()

	mockCtx := mocks.NewMockContext(t.Context())
	activator := newTestExtensionActivator(t, mockCtx, nil)

	cleanup, err := activator.EnsureProvisioningProviders(t.Context(), []string{"", ""}, "env1")
	require.NoError(t, err)
	require.NotNil(t, cleanup)
	cleanup()
}

func Test_EnsureProvisioningProviders_InvalidInstalledConfig(t *testing.T) {
	t.Parallel()

	mockCtx := mocks.NewMockContext(t.Context())
	userConfigManager := config.NewUserConfigManager(mockCtx.ConfigManager)
	userConfig, err := userConfigManager.Load()
	require.NoError(t, err)
	require.NoError(t, userConfig.Set("extension.installed", "not-a-map"))

	activator := newTestExtensionActivator(t, mockCtx, nil)
	cleanup, err := activator.EnsureProvisioningProviders(
		t.Context(),
		[]string{"microsoft.foundry"},
		"env1",
	)

	require.ErrorContains(t, err, "failed to get extensions section")
	require.NotNil(t, cleanup)
	cleanup()
}

func Test_EnsureProvisioningProviders_MissingGrpcServer(t *testing.T) {
	t.Parallel()

	mockCtx := mocks.NewMockContext(t.Context())
	installed := map[string]*extensions.Extension{
		"azure.ai.agents": {
			Id:           "azure.ai.agents",
			Capabilities: []extensions.CapabilityType{extensions.ProvisioningProviderCapability},
			Providers:    []extensions.Provider{{Name: "microsoft.foundry"}},
		},
	}
	activator := newTestExtensionActivator(t, mockCtx, installed)

	cleanup, err := activator.EnsureProvisioningProviders(
		t.Context(),
		[]string{"microsoft.foundry"},
		"env1",
	)

	require.ErrorContains(t, err, "container:")
	require.NotNil(t, cleanup)
	cleanup()
}

func Test_EnsureProvisioningProviders_AlreadyResolvable(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(t.Context())

	// The provider is already registered (extension host already running), so activation is a no-op
	// and no extension process is started.
	ioc.RegisterNamedInstance[provisioning.Provider](
		mockCtx.Container, "microsoft.foundry", provisioningTest.NewTestProvider(nil, nil, nil, nil))

	installed := map[string]*extensions.Extension{
		"azure.ai.agents": {
			Id:           "azure.ai.agents",
			Capabilities: []extensions.CapabilityType{extensions.ProvisioningProviderCapability},
			Providers:    []extensions.Provider{{Name: "microsoft.foundry"}},
		},
	}
	activator := newTestExtensionActivator(t, mockCtx, installed)

	cleanup, err := activator.EnsureProvisioningProviders(t.Context(), []string{"microsoft.foundry"}, "env1")
	require.NoError(t, err)
	require.NotNil(t, cleanup)
	cleanup()
}

func Test_SuggestExtensionForProvider(t *testing.T) {
	t.Parallel()

	t.Run("EmptyName", func(t *testing.T) {
		mockCtx := mocks.NewMockContext(t.Context())
		activator := newTestExtensionActivator(t, mockCtx, nil)
		requireSuggestedExtension(t, activator, "  ", "")
	})

	// An installed extension already declares the provider, so an install suggestion would be
	// misleading and none is returned.
	t.Run("InstalledDeclaresProvider", func(t *testing.T) {
		mockCtx := mocks.NewMockContext(t.Context())
		installed := map[string]*extensions.Extension{
			"azure.ai.agents": {
				Id:           "azure.ai.agents",
				Capabilities: []extensions.CapabilityType{extensions.ProvisioningProviderCapability},
				Providers:    []extensions.Provider{{Name: "microsoft.foundry"}},
			},
		}
		activator := newTestExtensionActivator(t, mockCtx, installed)
		requireSuggestedExtension(t, activator, "microsoft.foundry", "")
	})

	// No installed extension declares the provider, but the registry does: suggest that extension.
	t.Run("RegistryMatch", func(t *testing.T) {
		mockCtx := mocks.NewMockContext(t.Context())
		stubProviderRegistry(mockCtx, "azure.ai.agents", "microsoft.foundry")
		activator := newTestExtensionActivator(t, mockCtx, nil)
		requireSuggestedExtension(t, activator, "microsoft.foundry", "azure.ai.agents")
	})

	// No installed extension and no registry match yields no suggestion.
	t.Run("NoRegistryMatch", func(t *testing.T) {
		mockCtx := mocks.NewMockContext(t.Context())
		stubProviderRegistry(mockCtx, "azure.ai.agents", "microsoft.foundry")
		activator := newTestExtensionActivator(t, mockCtx, nil)
		requireSuggestedExtension(t, activator, "unknown.provider", "")
	})

	t.Run("UsesCompatibleVersion", func(t *testing.T) {
		mockCtx := mocks.NewMockContext(t.Context())
		foundry := []extensions.Provider{{
			Name: "microsoft.foundry",
			Type: extensions.ProvisioningProviderType,
		}}
		stubProviderRegistryVersions(mockCtx, "azure.ai.agents", []extensions.ExtensionVersion{
			{
				Version:      "1.0.0",
				Capabilities: []extensions.CapabilityType{extensions.ProvisioningProviderCapability},
				Providers:    foundry,
			},
			{
				Version:            "2.0.0",
				RequiredAzdVersion: ">=2.0.0",
				Capabilities:       []extensions.CapabilityType{extensions.ProvisioningProviderCapability},
				Providers:          foundry,
			},
		})
		manager := createExtensionsManagerWithOptions(
			t,
			mockCtx,
			nil,
			extensions.ManagerOptions{AzdVersion: semver.MustParse("1.0.0")},
		)
		activator := NewExtensionActivator(
			mockCtx.Container,
			manager,
			extensions.NewRunner(exec.NewCommandRunner(nil)),
			&internal.GlobalCommandOptions{},
		)

		requireSuggestedExtension(t, activator, "microsoft.foundry", "azure.ai.agents")
	})

	t.Run("IncompatibleOnly", func(t *testing.T) {
		mockCtx := mocks.NewMockContext(t.Context())
		stubProviderRegistryVersions(mockCtx, "azure.ai.agents", []extensions.ExtensionVersion{
			{
				Version:            "2.0.0",
				RequiredAzdVersion: ">=2.0.0",
				Capabilities:       []extensions.CapabilityType{extensions.ProvisioningProviderCapability},
				Providers: []extensions.Provider{{
					Name: "microsoft.foundry",
					Type: extensions.ProvisioningProviderType,
				}},
			},
		})
		manager := createExtensionsManagerWithOptions(
			t,
			mockCtx,
			nil,
			extensions.ManagerOptions{AzdVersion: semver.MustParse("1.0.0")},
		)
		activator := NewExtensionActivator(
			mockCtx.Container,
			manager,
			extensions.NewRunner(exec.NewCommandRunner(nil)),
			&internal.GlobalCommandOptions{},
		)

		id, err := activator.SuggestExtensionForProvider(t.Context(), "microsoft.foundry")
		require.Empty(t, id)
		compatibilityErr, ok := errors.AsType[*extensions.ExtensionAzdVersionIncompatibleError](err)
		require.True(t, ok)
		require.Contains(t, compatibilityErr.Error(), "microsoft.foundry")
		require.Contains(t, compatibilityErr.Suggestion(), `satisfies ">=2.0.0"`)
	})
}

func requireSuggestedExtension(t *testing.T, activator *ExtensionActivator, provider, wantID string) {
	t.Helper()
	id, err := activator.SuggestExtensionForProvider(t.Context(), provider)
	require.NoError(t, err)
	require.Equal(t, wantID, id)
}

func Test_providerFromExtension(t *testing.T) {
	t.Parallel()

	ext := &extensions.Extension{
		Id: "azure.ai.agents",
		Providers: []extensions.Provider{
			{Name: "microsoft.foundry", Type: "provisioning-provider"},
			{Name: "azure.ai.agent", Type: extensions.ServiceTargetProviderType},
		},
	}

	require.True(t, providerFromExtension(ext, "microsoft.foundry"))
	// Matching is case-insensitive.
	require.True(t, providerFromExtension(ext, "Microsoft.Foundry"))
	require.False(t, providerFromExtension(ext, "some.other.provider"))
	require.False(t, providerFromExtension(&extensions.Extension{}, "microsoft.foundry"))
}

func Test_distinctProviderNames(t *testing.T) {
	t.Parallel()

	// Empty names (NotSpecified provider) are dropped; duplicates are removed case-insensitively,
	// preserving the first-seen order and spelling.
	got := distinctProviderNames([]string{"", "bicep", "Bicep", "microsoft.foundry", "bicep", ""})
	require.Equal(t, []string{"bicep", "microsoft.foundry"}, got)

	require.Empty(t, distinctProviderNames(nil))
	require.Empty(t, distinctProviderNames([]string{"", ""}))
}

func Test_extensionsForProviders(t *testing.T) {
	t.Parallel()

	withCapability := &extensions.Extension{
		Id:           "azure.ai.agents",
		Capabilities: []extensions.CapabilityType{extensions.ProvisioningProviderCapability},
		Providers:    []extensions.Provider{{Name: "microsoft.foundry"}},
	}

	// Declares the provider but does NOT advertise the provisioning-provider capability.
	withoutCapability := &extensions.Extension{
		Id:        "other.ext",
		Providers: []extensions.Provider{{Name: "microsoft.foundry"}},
	}

	t.Run("MatchesCapabilityAndProvider", func(t *testing.T) {
		installed := map[string]*extensions.Extension{"azure.ai.agents": withCapability}
		got := extensionsForProviders(installed, []string{"microsoft.foundry"})
		require.Len(t, got, 1)
		require.Equal(t, "azure.ai.agents", got[0].Id)
	})

	t.Run("CaseInsensitiveMatch", func(t *testing.T) {
		installed := map[string]*extensions.Extension{"azure.ai.agents": withCapability}
		require.Len(t, extensionsForProviders(installed, []string{"Microsoft.Foundry"}), 1)
	})

	t.Run("IgnoresProviderWithoutCapability", func(t *testing.T) {
		installed := map[string]*extensions.Extension{"other.ext": withoutCapability}
		require.Empty(t, extensionsForProviders(installed, []string{"microsoft.foundry"}))
	})

	// Native or unknown provider names are not declared by any installed extension and must be
	// left alone - they resolve (or fail) natively, exactly as in every other command.
	t.Run("IgnoresUndeclaredProviders", func(t *testing.T) {
		installed := map[string]*extensions.Extension{"azure.ai.agents": withCapability}
		require.Empty(t, extensionsForProviders(installed, []string{"bicep", "terraform", "devcenter", "bicpe"}))
	})

	t.Run("SingleExtensionForMultipleProviders", func(t *testing.T) {
		multi := &extensions.Extension{
			Id:           "multi.ext",
			Capabilities: []extensions.CapabilityType{extensions.ProvisioningProviderCapability},
			Providers:    []extensions.Provider{{Name: "provider.one"}, {Name: "provider.two"}},
		}
		installed := map[string]*extensions.Extension{"multi.ext": multi}
		got := extensionsForProviders(installed, []string{"provider.one", "provider.two"})
		require.Len(t, got, 1)
	})

	// When several installed extensions declare the same provider, the lexically smallest id wins
	// so the choice is deterministic regardless of map iteration order.
	t.Run("DeterministicChoiceAcrossExtensions", func(t *testing.T) {
		first := &extensions.Extension{
			Id:           "a.ext",
			Capabilities: []extensions.CapabilityType{extensions.ProvisioningProviderCapability},
			Providers:    []extensions.Provider{{Name: "microsoft.foundry"}},
		}
		second := &extensions.Extension{
			Id:           "b.ext",
			Capabilities: []extensions.CapabilityType{extensions.ProvisioningProviderCapability},
			Providers:    []extensions.Provider{{Name: "microsoft.foundry"}},
		}
		installed := map[string]*extensions.Extension{"b.ext": second, "a.ext": first}

		for range 10 {
			got := extensionsForProviders(installed, []string{"microsoft.foundry"})
			require.Len(t, got, 1)
			require.Equal(t, "a.ext", got[0].Id)
		}
	})
}

func Test_declaredProviders(t *testing.T) {
	t.Parallel()

	ext := &extensions.Extension{
		Id:        "multi.ext",
		Providers: []extensions.Provider{{Name: "provider.one"}, {Name: "provider.two"}},
	}

	require.Equal(t,
		[]string{"provider.one", "provider.two"},
		declaredProviders(ext, []string{"provider.one", "bicep", "provider.two"}))
	require.Empty(t, declaredProviders(ext, []string{"bicep"}))
}

// Requests that reduce to no candidate provider names (empty/NotSpecified) must short-circuit
// before touching the extension manager, so a zero-value activator is sufficient here.
func Test_EnsureProvisioningProviders_NoProviderNamesIsNoop(t *testing.T) {
	t.Parallel()

	activator := &ExtensionActivator{}
	cleanup, err := activator.EnsureProvisioningProviders(t.Context(), []string{"", ""}, "test-env")

	require.NoError(t, err)
	require.NotNil(t, cleanup)
	// Cleanup must be safe to call even when nothing was started.
	cleanup()
}
