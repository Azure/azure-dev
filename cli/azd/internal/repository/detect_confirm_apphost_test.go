// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package repository

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/apphost"
	"github.com/stretchr/testify/require"
)

func Test_resolvePublishMode(t *testing.T) {
	tests := []struct {
		name     string
		manifest *apphost.Manifest
		expected apphostPublishMode
	}{
		{
			name: "project.v0 returns full azd mode",
			manifest: &apphost.Manifest{
				Resources: map[string]*apphost.Resource{
					"api": {
						Type: "project.v0",
					},
				},
			},
			expected: publishModeFullAzd,
		},
		{
			name: "container.v0 returns full azd mode",
			manifest: &apphost.Manifest{
				Resources: map[string]*apphost.Resource{
					"web": {
						Type: "container.v0",
					},
				},
			},
			expected: publishModeFullAzd,
		},
		{
			name: "dockerfile.v0 returns full azd mode",
			manifest: &apphost.Manifest{
				Resources: map[string]*apphost.Resource{
					"app": {
						Type: "dockerfile.v0",
					},
				},
			},
			expected: publishModeFullAzd,
		},
		{
			name: "mixed v0 and v1 returns full azd mode",
			manifest: &apphost.Manifest{
				Resources: map[string]*apphost.Resource{
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
			manifest: &apphost.Manifest{
				Resources: map[string]*apphost.Resource{
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
			manifest: &apphost.Manifest{
				Resources: map[string]*apphost.Resource{
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
			manifest: &apphost.Manifest{
				Resources: map[string]*apphost.Resource{
					"web": {
						Type: "container.v1",
					},
				},
			},
			expected: publishModeFullApphost,
		},
		{
			name: "empty manifest returns full apphost mode",
			manifest: &apphost.Manifest{
				Resources: map[string]*apphost.Resource{},
			},
			expected: publishModeFullApphost,
		},
		{
			name: "manifest with only infrastructure resources returns full apphost mode",
			manifest: &apphost.Manifest{
				Resources: map[string]*apphost.Resource{
					"vault": {
						Type: "azure.keyvault.v0",
					},
				},
			},
			expected: publishModeFullApphost,
		},
		{
			name: "complex manifest with v1 and infra outputs returns full apphost mode",
			manifest: &apphost.Manifest{
				Resources: map[string]*apphost.Resource{
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
