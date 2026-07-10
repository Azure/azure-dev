// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"net/http"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
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
	registry := extensions.Registry{
		Extensions: []*extensions.ExtensionMetadata{
			{
				Id: extensionID,
				Versions: []extensions.ExtensionVersion{
					{
						Version:      "1.0.0",
						Capabilities: []extensions.CapabilityType{extensions.ProvisioningProviderCapability},
						Providers:    []extensions.Provider{{Name: providerName}},
					},
				},
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
		require.Empty(t, activator.SuggestExtensionForProvider(t.Context(), "  "))
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
		require.Empty(t, activator.SuggestExtensionForProvider(t.Context(), "microsoft.foundry"))
	})

	// No installed extension declares the provider, but the registry does: suggest that extension.
	t.Run("RegistryMatch", func(t *testing.T) {
		mockCtx := mocks.NewMockContext(t.Context())
		stubProviderRegistry(mockCtx, "azure.ai.agents", "microsoft.foundry")
		activator := newTestExtensionActivator(t, mockCtx, nil)
		require.Equal(t, "azure.ai.agents", activator.SuggestExtensionForProvider(t.Context(), "microsoft.foundry"))
	})

	// No installed extension and no registry match yields no suggestion.
	t.Run("NoRegistryMatch", func(t *testing.T) {
		mockCtx := mocks.NewMockContext(t.Context())
		stubProviderRegistry(mockCtx, "azure.ai.agents", "microsoft.foundry")
		activator := newTestExtensionActivator(t, mockCtx, nil)
		require.Empty(t, activator.SuggestExtensionForProvider(t.Context(), "unknown.provider"))
	})
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
