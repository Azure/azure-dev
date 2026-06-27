// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azd

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/stretchr/testify/require"
)

func Test_DefaultPlatform_IsEnabled(t *testing.T) {
	t.Run("Enabled", func(t *testing.T) {
		defaultPlatform := NewDefaultPlatform()
		require.True(t, defaultPlatform.IsEnabled())
	})
}

func Test_DefaultPlatform_ConfigureContainer(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		defaultPlatform := NewDefaultPlatform()
		container := ioc.NewNestedContainer(nil)
		err := defaultPlatform.ConfigureContainer(container)
		require.NoError(t, err)

		var provisionResolver provisioning.DefaultProviderResolver
		err = container.Resolve(&provisionResolver)
		require.NoError(t, err)
		require.NotNil(t, provisionResolver)

		expected := provisioning.Bicep
		actual, err := provisionResolver()
		require.NoError(t, err)
		require.Equal(t, expected, actual)
	})
}

func Test_providerFromInstalledExtensions(t *testing.T) {
	foundryExt := func() *extensions.Extension {
		return &extensions.Extension{
			Id:           "azure.ai.agents",
			Capabilities: []extensions.CapabilityType{extensions.ProvisioningProviderCapability},
			Providers: []extensions.Provider{
				{Name: "azure.ai.agent", Type: extensions.ServiceTargetProviderType},
				{Name: "azure.ai.project", Type: extensions.ServiceTargetProviderType},
				{Name: "microsoft.foundry", Type: extensions.ProvisioningProviderType},
			},
		}
	}

	tests := []struct {
		name      string
		services  map[string]*project.ServiceConfig
		installed map[string]*extensions.Extension
		want      provisioning.ProviderKind
		wantOk    bool
	}{
		{
			name: "foundry project routes to microsoft.foundry",
			services: map[string]*project.ServiceConfig{
				"ai-project": {Host: "azure.ai.project"},
				"assistant":  {Host: "azure.ai.agent"},
			},
			installed: map[string]*extensions.Extension{"azure.ai.agents": foundryExt()},
			want:      provisioning.ProviderKind("microsoft.foundry"),
			wantOk:    true,
		},
		{
			name: "extension without provisioning capability is ignored",
			services: map[string]*project.ServiceConfig{
				"ai-project": {Host: "azure.ai.project"},
			},
			installed: map[string]*extensions.Extension{
				"azure.ai.agents": {
					Id:        "azure.ai.agents",
					Providers: foundryExt().Providers,
				},
			},
			wantOk: false,
		},
		{
			name: "no installed extensions falls back",
			services: map[string]*project.ServiceConfig{
				"ai-project": {Host: "azure.ai.project"},
			},
			installed: map[string]*extensions.Extension{},
			wantOk:    false,
		},
		{
			name: "host not served by the provisioning extension falls back",
			services: map[string]*project.ServiceConfig{
				"web": {Host: "containerapp"},
			},
			installed: map[string]*extensions.Extension{"azure.ai.agents": foundryExt()},
			wantOk:    false,
		},
		{
			name:      "no services falls back",
			services:  map[string]*project.ServiceConfig{},
			installed: map[string]*extensions.Extension{"azure.ai.agents": foundryExt()},
			wantOk:    false,
		},
		{
			name: "multiple qualifying extensions selects lowest id",
			services: map[string]*project.ServiceConfig{
				"ai-project": {Host: "azure.ai.project"},
			},
			installed: map[string]*extensions.Extension{
				"zzz.ext": {
					Id:           "zzz.ext",
					Capabilities: []extensions.CapabilityType{extensions.ProvisioningProviderCapability},
					Providers: []extensions.Provider{
						{Name: "azure.ai.project", Type: extensions.ServiceTargetProviderType},
						{Name: "zzz.provider", Type: extensions.ProvisioningProviderType},
					},
				},
				"azure.ai.agents": foundryExt(),
			},
			want:   provisioning.ProviderKind("microsoft.foundry"),
			wantOk: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := providerFromInstalledExtensions(tt.services, tt.installed)
			require.Equal(t, tt.wantOk, ok)
			if tt.wantOk {
				require.Equal(t, tt.want, got)
			}
		})
	}
}
