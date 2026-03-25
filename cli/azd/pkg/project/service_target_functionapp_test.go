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

func TestResolveFunctionAppRemoteBuild_BOMHandling(t *testing.T) {
	t.Parallel()

	// UTF-8 BOM is commonly added by Windows editors (Notepad, etc.)
	bom := "\xef\xbb\xbf"

	tests := []struct {
		name              string
		remoteBuild       *bool
		funcIgnoreContent string
		expectRemoteBuild bool
		expectError       string
	}{
		{
			name:              "BOM_NodeModulesExcluded_NoRemoteBuild",
			remoteBuild:       nil,
			funcIgnoreContent: bom + "node_modules\n",
			expectRemoteBuild: true,
		},
		{
			name:              "BOM_NodeModulesExcluded_RemoteBuildTrue",
			remoteBuild:       new(true),
			funcIgnoreContent: bom + "node_modules\n",
			expectRemoteBuild: true,
		},
		{
			name:              "BOM_NodeModulesExcluded_RemoteBuildFalse",
			remoteBuild:       new(false),
			funcIgnoreContent: bom + "node_modules\n",
			expectError:       "'remoteBuild: false' cannot be used when '.funcignore' excludes node_modules",
		},
		{
			name:              "BOM_MultiplePatterns_NodeModulesFirst",
			remoteBuild:       nil,
			funcIgnoreContent: bom + "node_modules\ndist\n.env\n",
			expectRemoteBuild: true,
		},
		{
			name:              "BOM_MultiplePatterns_NodeModulesNotFirst",
			remoteBuild:       nil,
			funcIgnoreContent: bom + "dist\nnode_modules\n.env\n",
			expectRemoteBuild: true,
		},
		{
			name:              "BOM_OnlyDist_NoNodeModules",
			remoteBuild:       nil,
			funcIgnoreContent: bom + "dist\n",
			expectRemoteBuild: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			serviceConfig := createTestServiceConfig(t.TempDir(), AzureFunctionTarget, ServiceLanguageJavaScript)
			serviceConfig.RemoteBuild = tt.remoteBuild

			err := os.WriteFile(
				filepath.Join(serviceConfig.Path(), ".funcignore"),
				[]byte(tt.funcIgnoreContent),
				0600,
			)
			require.NoError(t, err)

			remoteBuild, err := resolveFunctionAppRemoteBuild(serviceConfig)
			if tt.expectError != "" {
				require.Error(t, err)
				require.ErrorContains(t, err, tt.expectError)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.expectRemoteBuild, remoteBuild)
		})
	}
}

func TestStripUTF8BOM(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    []byte
		expected []byte
	}{
		{"NoBOM", []byte("node_modules\n"), []byte("node_modules\n")},
		{"WithBOM", []byte("\xef\xbb\xbfnode_modules\n"), []byte("node_modules\n")},
		{"EmptyWithBOM", []byte("\xef\xbb\xbf"), []byte{}},
		{"Empty", []byte{}, []byte{}},
		{"PartialBOM", []byte("\xef\xbb"), []byte("\xef\xbb")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripUTF8BOM(tt.input)
			require.Equal(t, tt.expected, result)
		})
	}
}

// TestResolveFunctionAppRemoteBuild_CustomerScenarios maps 1:1 to the 9 scenarios reported by the
// customer in the GitHub issue, using the same numbering for traceability.
func TestResolveFunctionAppRemoteBuild_CustomerScenarios(t *testing.T) {
	t.Parallel()

	ptrTrue := new(true)
	ptrFalse := new(false)

	tests := []struct {
		name              string
		remoteBuild       *bool
		funcIgnoreContent string // empty string = no funcignore file
		expectRemoteBuild bool
		expectError       string
	}{
		// Customer Case 1: No flag + funcignore excludes node_modules → deploys successfully
		{
			name:              "Case1_NilRemoteBuild_FuncignoreExcludesNodeModules",
			remoteBuild:       nil,
			funcIgnoreContent: "node_modules\n",
			expectRemoteBuild: true,
		},
		// Customer Case 2: No flag + funcignore doesn't exclude node_modules → deploys successfully
		{
			name:              "Case2_NilRemoteBuild_FuncignoreDoesNotExcludeNodeModules",
			remoteBuild:       nil,
			funcIgnoreContent: "dist\n",
			expectRemoteBuild: false,
		},
		// Customer Case 3: No flag + no funcignore file → deploys successfully
		{
			name:              "Case3_NilRemoteBuild_NoFuncignore",
			remoteBuild:       nil,
			funcIgnoreContent: "",
			expectRemoteBuild: true,
		},
		// Customer Case 4: Explicit false + no funcignore → deploys successfully
		{
			name:              "Case4_FalseRemoteBuild_NoFuncignore",
			remoteBuild:       ptrFalse,
			funcIgnoreContent: "",
			expectRemoteBuild: false,
		},
		// Customer Case 5: Explicit false + funcignore excludes node_modules → ERROR
		// (remoteBuild: false conflicts with funcignore excluding node_modules)
		{
			name:              "Case5_FalseRemoteBuild_FuncignoreExcludesNodeModules",
			remoteBuild:       ptrFalse,
			funcIgnoreContent: "node_modules\n",
			expectError:       "'remoteBuild: false' cannot be used when '.funcignore' excludes node_modules",
		},
		// Customer Case 6: Explicit false + funcignore doesn't exclude node_modules → deploys successfully
		{
			name:              "Case6_FalseRemoteBuild_FuncignoreDoesNotExcludeNodeModules",
			remoteBuild:       ptrFalse,
			funcIgnoreContent: "dist\n",
			expectRemoteBuild: false,
		},
		// Customer Case 7: Explicit true + funcignore excludes node_modules → deploys successfully
		{
			name:              "Case7_TrueRemoteBuild_FuncignoreExcludesNodeModules",
			remoteBuild:       ptrTrue,
			funcIgnoreContent: "node_modules\n",
			expectRemoteBuild: true,
		},
		// Customer Case 8: Explicit true + funcignore doesn't exclude node_modules → ERROR
		// (remoteBuild: true requires funcignore to exclude node_modules so server can npm install)
		{
			name:              "Case8_TrueRemoteBuild_FuncignoreDoesNotExcludeNodeModules",
			remoteBuild:       ptrTrue,
			funcIgnoreContent: "dist\n",
			expectError:       "'remoteBuild: true' requires '.funcignore' to exclude node_modules",
		},
		// Customer Case 9: Explicit true + no funcignore → deploys successfully
		// (hardcoded defaults exclude node_modules, consistent with remoteBuild: true)
		{
			name:              "Case9_TrueRemoteBuild_NoFuncignore",
			remoteBuild:       ptrTrue,
			funcIgnoreContent: "",
			expectRemoteBuild: true,
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

// TestResolveFunctionAppRemoteBuild_EdgeCases covers additional edge cases for funcignore patterns.
func TestResolveFunctionAppRemoteBuild_EdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		remoteBuild       *bool
		funcIgnoreContent string
		expectRemoteBuild bool
		expectError       string
	}{
		{
			name:              "EmptyFuncignore_NilRemoteBuild",
			remoteBuild:       nil,
			funcIgnoreContent: "\n",
			expectRemoteBuild: false,
		},
		{
			name:              "EmptyFuncignore_TrueRemoteBuild",
			remoteBuild:       new(true),
			funcIgnoreContent: "\n",
			expectError:       "'remoteBuild: true' requires '.funcignore' to exclude node_modules",
		},
		{
			name:              "EmptyFuncignore_FalseRemoteBuild",
			remoteBuild:       new(false),
			funcIgnoreContent: "\n",
			expectRemoteBuild: false,
		},
		{
			name:              "NodeModulesWithTrailingSlash",
			remoteBuild:       nil,
			funcIgnoreContent: "node_modules/\n",
			expectRemoteBuild: true,
		},
		{
			name:              "NodeModulesInComment_NotExcluded",
			remoteBuild:       nil,
			funcIgnoreContent: "# node_modules\n",
			expectRemoteBuild: false,
		},
		{
			name:              "MultiplePatterns_NodeModulesInMiddle",
			remoteBuild:       nil,
			funcIgnoreContent: "dist\nnode_modules\n.env\n",
			expectRemoteBuild: true,
		},
		{
			name:              "CRLFLineEndings",
			remoteBuild:       nil,
			funcIgnoreContent: "dist\r\nnode_modules\r\n.env\r\n",
			expectRemoteBuild: true,
		},
		{
			name:              "GlobPattern_NodeModules",
			remoteBuild:       nil,
			funcIgnoreContent: "node_modules/**\n",
			expectRemoteBuild: true,
		},
		{
			name:              "LeadingSlash_NodeModules",
			remoteBuild:       nil,
			funcIgnoreContent: "/node_modules\n",
			expectRemoteBuild: true,
		},
		{
			name:              "DoubleStarPrefix_NodeModules",
			remoteBuild:       nil,
			funcIgnoreContent: "**/node_modules\n",
			expectRemoteBuild: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			serviceConfig := createTestServiceConfig(t.TempDir(), AzureFunctionTarget, ServiceLanguageJavaScript)
			serviceConfig.RemoteBuild = tt.remoteBuild

			err := os.WriteFile(
				filepath.Join(serviceConfig.Path(), ".funcignore"),
				[]byte(tt.funcIgnoreContent),
				0600,
			)
			require.NoError(t, err)

			remoteBuild, err := resolveFunctionAppRemoteBuild(serviceConfig)
			if tt.expectError != "" {
				require.Error(t, err)
				require.ErrorContains(t, err, tt.expectError)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.expectRemoteBuild, remoteBuild)
		})
	}
}

// TestResolveFunctionAppRemoteBuild_TypeScriptParity verifies TypeScript follows the same
// code path as JavaScript for funcignore validation.
func TestResolveFunctionAppRemoteBuild_TypeScriptParity(t *testing.T) {
	t.Parallel()

	// TypeScript should behave identically to JavaScript
	serviceConfig := createTestServiceConfig(t.TempDir(), AzureFunctionTarget, ServiceLanguageTypeScript)
	err := os.WriteFile(
		filepath.Join(serviceConfig.Path(), ".funcignore"),
		[]byte("node_modules\n"),
		0600,
	)
	require.NoError(t, err)

	remoteBuild, err := resolveFunctionAppRemoteBuild(serviceConfig)
	require.NoError(t, err)
	require.True(t, remoteBuild, "TypeScript should auto-detect remoteBuild=true when funcignore excludes node_modules")

	// Also verify error case
	serviceConfig2 := createTestServiceConfig(t.TempDir(), AzureFunctionTarget, ServiceLanguageTypeScript)
	serviceConfig2.RemoteBuild = new(true)
	err = os.WriteFile(
		filepath.Join(serviceConfig2.Path(), ".funcignore"),
		[]byte("dist\n"),
		0600,
	)
	require.NoError(t, err)

	_, err = resolveFunctionAppRemoteBuild(serviceConfig2)
	require.Error(t, err)
	require.ErrorContains(t, err, "'remoteBuild: true' requires '.funcignore' to exclude node_modules")
}
