// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/stretchr/testify/require"
)

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
