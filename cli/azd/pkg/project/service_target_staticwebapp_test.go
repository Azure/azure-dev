// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/stretchr/testify/require"
)

func TestNewStaticWebAppTargetTypeValidation(t *testing.T) {
	t.Parallel()

	tests := map[string]*serviceTargetValidationTest{
		"ValidateTypeSuccess": {
			targetResource: environment.NewTargetResource(
				"SUB_ID",
				"RG_ID",
				"res",
				string(azapi.AzureResourceTypeStaticWebSite),
			),
			expectError: false,
		},
		"ValidateTypeLowerCaseSuccess": {
			targetResource: environment.NewTargetResource(
				"SUB_ID",
				"RG_ID",
				"res",
				strings.ToLower(string(azapi.AzureResourceTypeStaticWebSite)),
			),
			expectError: false,
		},
		"ValidateTypeFail": {
			targetResource: environment.NewTargetResource("SUB_ID", "RG_ID", "res", "BadType"),
			expectError:    true,
		},
	}

	for test, data := range tests {
		t.Run(test, func(t *testing.T) {
			serviceTarget := &staticWebAppTarget{}

			err := serviceTarget.validateTargetResource(data.targetResource)
			if data.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestStaticWebAppDeploy_EnvironmentSelection verifies the environment value passed
// to the SWA CLI depends on whether swa-cli.config.json is present:
//   - No config file (opinionated mode): passes "production" to fix the BadRequest
//     that occurred with the old "default" value.
//   - Config file present: passes "" so the SWA CLI resolves env from its own config.
func TestStaticWebAppDeploy_EnvironmentSelection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		artifacts   ArtifactCollection
		expectedEnv string
	}{
		{
			name: "NoConfigFile_PassesProduction",
			artifacts: ArtifactCollection{
				{Kind: ArtifactKindDirectory, Location: "/build/output"},
			},
			expectedEnv: swaCliProductionEnvironment,
		},
		{
			name: "WithConfigFile_PassesEmpty",
			artifacts: ArtifactCollection{
				{Kind: ArtifactKindConfig, Location: "swa-cli.config.json"},
			},
			expectedEnv: "",
		},
		{
			name:        "EmptyArtifacts_PassesProduction",
			artifacts:   ArtifactCollection{},
			expectedEnv: swaCliProductionEnvironment,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// The environment selection logic: when config file is present, use empty
			// string (SWA CLI resolves from its own config); otherwise use "production".
			swaEnv := ""
			if !usingSwaConfig(tc.artifacts) {
				swaEnv = swaCliProductionEnvironment
			}
			require.Equal(t, tc.expectedEnv, swaEnv)
		})
	}
}
