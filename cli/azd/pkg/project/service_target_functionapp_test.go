// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/stretchr/testify/require"
)

func TestNewFunctionAppTargetTypeValidation(t *testing.T) {
	t.Parallel()

	tests := map[string]*serviceTargetValidationTest{
		"ValidateTypeSuccess": {
			targetResource: environment.NewTargetResource("SUB_ID", "RG_ID", "res", string(azapi.AzureResourceTypeWebSite)),
			expectError:    false,
		},
		"ValidateTypeLowerCaseSuccess": {
			targetResource: environment.NewTargetResource(
				"SUB_ID", "RG_ID", "res", strings.ToLower(string(azapi.AzureResourceTypeWebSite)),
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
			serviceTarget := &functionAppTarget{}

			err := serviceTarget.validateTargetResource(data.targetResource)
			if data.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestResolveFunctionAppRemoteBuild_JavaScriptMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		remoteBuild       *bool
		funcIgnoreContent string
		expectRemoteBuild bool
		expectError       string
	}{
		{
			name:              "NoRemoteBuildAndFuncIgnoreExcludesNodeModules_RemoteBuildEnabled",
			remoteBuild:       nil,
			funcIgnoreContent: "node_modules\n",
			expectRemoteBuild: true,
		},
		{
			name:              "NoRemoteBuildAndFuncIgnoreDoesNotExcludeNodeModules_RemoteBuildDisabled",
			remoteBuild:       nil,
			funcIgnoreContent: "dist\n",
			expectRemoteBuild: false,
		},
		{
			name:              "NoRemoteBuildAndMissingFuncIgnore_RemoteBuildEnabled",
			remoteBuild:       nil,
			funcIgnoreContent: "",
			expectRemoteBuild: true,
		},
		{
			name:              "RemoteBuildFalseAndMissingFuncIgnore_RemoteBuildDisabled",
			remoteBuild:       new(false),
			funcIgnoreContent: "",
			expectRemoteBuild: false,
		},
		{
			name:              "RemoteBuildFalseAndFuncIgnoreExcludesNodeModules_Errors",
			remoteBuild:       new(false),
			funcIgnoreContent: "node_modules\n",
			expectError:       "'remoteBuild: false' cannot be used when '.funcignore' excludes node_modules",
		},
		{
			name:              "RemoteBuildFalseAndFuncIgnoreDoesNotExcludeNodeModules_Succeeds",
			remoteBuild:       new(false),
			funcIgnoreContent: "dist\n",
			expectRemoteBuild: false,
		},
		{
			name:              "RemoteBuildTrueAndFuncIgnoreExcludesNodeModules_Succeeds",
			remoteBuild:       new(true),
			funcIgnoreContent: "node_modules\n",
			expectRemoteBuild: true,
		},
		{
			name:              "RemoteBuildTrueAndFuncIgnoreDoesNotExcludeNodeModules_Errors",
			remoteBuild:       new(true),
			funcIgnoreContent: "dist\n",
			expectError:       "'remoteBuild: true' requires '.funcignore' to exclude node_modules",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			serviceConfig := createTestServiceConfig(t.TempDir(), AzureFunctionTarget, ServiceLanguageJavaScript)
			serviceConfig.RemoteBuild = tt.remoteBuild

			if tt.funcIgnoreContent != "" {
				err := os.WriteFile(
					filepath.Join(serviceConfig.Path(), ".funcignore"),
					[]byte(tt.funcIgnoreContent),
					0600,
				)
				require.NoError(t, err)
			}

			remoteBuild, err := resolveFunctionAppRemoteBuild(serviceConfig)
			if tt.expectError != "" {
				require.Error(t, err)
				require.ErrorContains(t, err, tt.expectError)

				var suggestionErr *internal.ErrorWithSuggestion
				require.ErrorAs(t, err, &suggestionErr)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.expectRemoteBuild, remoteBuild)
		})
	}
}

func TestResolveFunctionAppRemoteBuild_NonJavaScriptDefaults(t *testing.T) {
	t.Parallel()

	pythonConfig := createTestServiceConfig(t.TempDir(), AzureFunctionTarget, ServiceLanguagePython)
	remoteBuild, err := resolveFunctionAppRemoteBuild(pythonConfig)
	require.NoError(t, err)
	require.True(t, remoteBuild)

	pythonConfig.RemoteBuild = new(false)
	remoteBuild, err = resolveFunctionAppRemoteBuild(pythonConfig)
	require.NoError(t, err)
	require.False(t, remoteBuild)

	csharpConfig := createTestServiceConfig(t.TempDir(), AzureFunctionTarget, ServiceLanguageCsharp)
	remoteBuild, err = resolveFunctionAppRemoteBuild(csharpConfig)
	require.NoError(t, err)
	require.False(t, remoteBuild)
}
