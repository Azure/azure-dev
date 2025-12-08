// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package apphost

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_resolvePublishMode(t *testing.T) {
	tests := []struct {
		name     string
		manifest *Manifest
		expected apphostPublishMode
	}{
		{
			name: "project.v0 returns full azd mode",
			manifest: &Manifest{
				Resources: map[string]*Resource{
					"api": {
						Type: "project.v0",
					},
				},
			},
			expected: publishModeFullAzd,
		},
		{
			name: "container.v0 returns full azd mode",
			manifest: &Manifest{
				Resources: map[string]*Resource{
					"web": {
						Type: "container.v0",
					},
				},
			},
			expected: publishModeFullAzd,
		},
		{
			name: "dockerfile.v0 returns full azd mode",
			manifest: &Manifest{
				Resources: map[string]*Resource{
					"app": {
						Type: "dockerfile.v0",
					},
				},
			},
			expected: publishModeFullAzd,
		},
		{
			name: "mixed v0 and v1 returns full azd mode",
			manifest: &Manifest{
				Resources: map[string]*Resource{
					"api": {
						Type: "project.v0",
					},
					"web": {
						Type: "project.v1",
					},
				},
			},
			expected: publishModeFullAzd,
		},
		{
			name: "manifest with global outputs reference returns hybrid mode",
			manifest: &Manifest{
				Resources: map[string]*Resource{
					"api": {
						Type: "project.v1",
						Env: map[string]string{
							"CONNECTION_STRING": "{.outputs.connectionString}",
						},
					},
				},
			},
			expected: publishModeHybrid,
		},
		{
			name: "project.v1 without global outputs returns full apphost mode",
			manifest: &Manifest{
				Resources: map[string]*Resource{
					"api": {
						Type: "project.v1",
						Env: map[string]string{
							"CONNECTION_STRING": "{infra.outputs.connectionString}",
						},
					},
				},
			},
			expected: publishModeFullApphost,
		},
		{
			name: "container.v1 without global outputs returns full apphost mode",
			manifest: &Manifest{
				Resources: map[string]*Resource{
					"web": {
						Type: "container.v1",
					},
				},
			},
			expected: publishModeFullApphost,
		},
		{
			name: "empty manifest returns full apphost mode",
			manifest: &Manifest{
				Resources: map[string]*Resource{},
			},
			expected: publishModeFullApphost,
		},
		{
			name: "manifest with only infrastructure resources returns full apphost mode",
			manifest: &Manifest{
				Resources: map[string]*Resource{
					"vault": {
						Type: "azure.keyvault.v0",
					},
				},
			},
			expected: publishModeFullApphost,
		},
		{
			name: "complex manifest with v1 and infra outputs returns full apphost mode",
			manifest: &Manifest{
				Resources: map[string]*Resource{
					"api": {
						Type: "project.v1",
						Env: map[string]string{
							"DB_CONNECTION": "{infra.outputs.dbConnectionString}",
						},
					},
					"vault": {
						Type: "azure.keyvault.v0",
					},
				},
			},
			expected: publishModeFullApphost,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resolvePublishMode(tt.manifest)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestManifest_Warnings(t *testing.T) {
	tests := []struct {
		name        string
		publishMode apphostPublishMode
		expected    string
	}{
		{
			name:        "full azd mode shows limited mode warning",
			publishMode: publishModeFullAzd,
			//nolint:lll
			expected: "  Limited mode Warning: Your Aspire project is delegating the services' host infrastructure to azd.\n" +
				//nolint:lll
				"  This mode is limited. You will not be able to manage the host infrastructure from your AppHost. You'll need to use `azd infra gen` " +
				"to customize the Azure Container Environment and/or Azure Container Apps" +
				"  See more: https://learn.microsoft.com/dotnet/aspire/azure/configure-aca-environments",
		},
		{
			name:        "hybrid mode shows deprecation warning",
			publishMode: publishModeHybrid,
			expected: "  Deprecation Warning: " + "Your Aspire project is on hybrid mode. While you can use the AppHost" +
				" to define the Azure Container App, azd defines the Azure Container Environment.\n  This mode is " +
				"deprecated since Aspire 9.4.  " +
				//nolint:lll
				"See more: https://learn.microsoft.com/dotnet/aspire/whats-new/dotnet-aspire-9.4#-azure-container-apps-hybrid-mode-removal",
		},
		{
			name:        "full apphost mode shows no warnings",
			publishMode: publishModeFullApphost,
			expected:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := &Manifest{
				publishMode: tt.publishMode,
			}
			result := manifest.Warnings()
			require.Equal(t, tt.expected, result)
		})
	}
}
