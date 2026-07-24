// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazapi"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
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

// TestResolveFunctionAppRemoteBuild_JavaScriptMatrix covers the full 3×3 matrix of remoteBuild
// (nil, true, false) × funcignore state (excludes node_modules, doesn't exclude, absent).
// Customer case numbers refer to the 9 scenarios reported in the GitHub issue for traceability.
func TestResolveFunctionAppRemoteBuild_JavaScriptMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		remoteBuild       *bool
		funcIgnoreContent string // empty string = no funcignore file
		expectRemoteBuild bool
		expectError       string
	}{
		// Customer Case 1: No flag + funcignore excludes node_modules → auto-detect remote build
		{
			name:              "NilRemoteBuild_FuncignoreExcludesNodeModules",
			remoteBuild:       nil,
			funcIgnoreContent: "node_modules\n",
			expectRemoteBuild: true,
		},
		// Customer Case 2: No flag + funcignore doesn't exclude node_modules → local build
		{
			name:              "NilRemoteBuild_FuncignoreDoesNotExcludeNodeModules",
			remoteBuild:       nil,
			funcIgnoreContent: "dist\n",
			expectRemoteBuild: false,
		},
		// Customer Case 3: No flag + no funcignore → defaults to remote build
		{
			name:              "NilRemoteBuild_NoFuncignore",
			remoteBuild:       nil,
			funcIgnoreContent: "",
			expectRemoteBuild: true,
		},
		// Customer Case 4: Explicit false + no funcignore → local build
		{
			name:              "FalseRemoteBuild_NoFuncignore",
			remoteBuild:       new(false),
			funcIgnoreContent: "",
			expectRemoteBuild: false,
		},
		// Customer Case 5: Explicit false + funcignore excludes node_modules → ERROR
		// (remoteBuild: false conflicts with funcignore excluding node_modules)
		{
			name:              "FalseRemoteBuild_FuncignoreExcludesNodeModules_Errors",
			remoteBuild:       new(false),
			funcIgnoreContent: "node_modules\n",
			expectError:       "'remoteBuild: false' cannot be used when '.funcignore' excludes node_modules",
		},
		// Customer Case 6: Explicit false + funcignore doesn't exclude node_modules → local build
		{
			name:              "FalseRemoteBuild_FuncignoreDoesNotExcludeNodeModules",
			remoteBuild:       new(false),
			funcIgnoreContent: "dist\n",
			expectRemoteBuild: false,
		},
		// Customer Case 7: Explicit true + funcignore excludes node_modules → remote build
		{
			name:              "TrueRemoteBuild_FuncignoreExcludesNodeModules",
			remoteBuild:       new(true),
			funcIgnoreContent: "node_modules\n",
			expectRemoteBuild: true,
		},
		// Customer Case 8: Explicit true + funcignore doesn't exclude node_modules → ERROR
		// (remoteBuild: true requires funcignore to exclude node_modules so server can npm install)
		{
			name:              "TrueRemoteBuild_FuncignoreDoesNotExcludeNodeModules_Errors",
			remoteBuild:       new(true),
			funcIgnoreContent: "dist\n",
			expectError:       "'remoteBuild: true' requires '.funcignore' to exclude node_modules",
		},
		// Customer Case 9: Explicit true + no funcignore → remote build
		// (hardcoded defaults exclude node_modules, consistent with remoteBuild: true)
		{
			name:              "TrueRemoteBuild_NoFuncignore",
			remoteBuild:       new(true),
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

func TestResolveFunctionAppRemoteBuild_Go(t *testing.T) {
	t.Parallel()

	// Default: Go should return false (no remote build)
	goConfig := createTestServiceConfig(t.TempDir(), AzureFunctionTarget, ServiceLanguageGo)
	remoteBuild, err := resolveFunctionAppRemoteBuild(goConfig)
	require.NoError(t, err)
	require.False(t, remoteBuild, "Go should default to local build (remoteBuild=false)")

	// Explicit false: should succeed
	goConfig.RemoteBuild = new(false)
	remoteBuild, err = resolveFunctionAppRemoteBuild(goConfig)
	require.NoError(t, err)
	require.False(t, remoteBuild)

	// Explicit true: should error (remote build not supported for Go)
	goConfig.RemoteBuild = new(true)
	_, err = resolveFunctionAppRemoteBuild(goConfig)
	require.Error(t, err, "Go should reject remoteBuild=true")
	require.Contains(t, err.Error(), "remote build is not supported for Go")
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

func Test_NewFunctionAppTarget(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)
	target := NewFunctionAppTarget(env, nil, nil, nil, nil)
	require.NotNil(t, target)
}

func Test_functionAppTarget_RequiredExternalTools(t *testing.T) {
	target := NewFunctionAppTarget(nil, nil, nil, nil, nil)
	result := target.RequiredExternalTools(t.Context(), nil)
	assert.Empty(t, result)
}

func Test_functionAppTarget_Initialize(t *testing.T) {
	target := NewFunctionAppTarget(nil, nil, nil, nil, nil)
	err := target.Initialize(t.Context(), nil)
	require.NoError(t, err)
}

func Test_functionAppTarget_Publish(t *testing.T) {
	target := NewFunctionAppTarget(nil, nil, nil, nil, nil)
	result, err := target.Publish(t.Context(), nil, nil, nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func Test_functionAppTarget_RequiredExternalTools_Container(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	dockerCli := docker.NewCli(mockContext.CommandRunner)
	containerHelper := NewContainerHelper(
		nil, nil, nil, nil, dockerCli, nil, mockContext.Console, nil)
	target := NewFunctionAppTarget(nil, nil, containerHelper, nil, mockContext.Console)

	tools := target.RequiredExternalTools(*mockContext.Context, &ServiceConfig{
		Language: ServiceLanguageTypeScript,
		Docker:   DockerProjectOptions{Path: "./Dockerfile"},
	})

	assert.NotEmpty(t, tools)
}

func Test_functionAppTarget_Package_Container(t *testing.T) {
	serviceContext := NewServiceContext()
	require.NoError(t, serviceContext.Package.Add(&Artifact{
		Kind:         ArtifactKindContainer,
		Location:     "function:latest",
		LocationKind: LocationKindLocal,
	}))

	target := NewFunctionAppTarget(nil, nil, nil, nil, nil)
	result, err := target.Package(
		t.Context(),
		&ServiceConfig{Language: ServiceLanguageDocker},
		serviceContext,
		async.NewNoopProgress[ServiceProgress](),
	)

	require.NoError(t, err)
	require.Empty(t, result.Artifacts)
}

func Test_functionAppTarget_Publish_PreBuiltImage(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	env := environment.New("test")
	envManager := &mockenv.MockEnvManager{}
	envManager.On("Save", mock.Anything, mock.Anything).Return(nil)

	serviceContext := NewServiceContext()
	require.NoError(t, serviceContext.Package.Add(&Artifact{
		Kind:         ArtifactKindContainer,
		Location:     "registry.azurecr.io/function:latest",
		LocationKind: LocationKindLocal,
	}))

	target := NewFunctionAppTarget(env, envManager, nil, nil, mockContext.Console)
	result, err := target.Publish(
		*mockContext.Context,
		&ServiceConfig{
			Name:  "function",
			Image: osutil.NewExpandableString("registry.azurecr.io/function:latest"),
		},
		serviceContext,
		environment.NewTargetResource(
			"SUB_ID",
			"RG_ID",
			"FUNCTION_APP_NAME",
			string(azapi.AzureResourceTypeWebSite),
		),
		async.NewNoopProgress[ServiceProgress](),
		nil,
	)

	require.NoError(t, err)
	artifact, found := result.Artifacts.FindFirst(WithKind(ArtifactKindContainer))
	require.True(t, found)
	assert.Equal(t, "registry.azurecr.io/function:latest", artifact.Location)
	assert.Equal(t, artifact.Location, env.GetServiceProperty("function", "IMAGE_NAME"))
}

func Test_functionAppTarget_Deploy_Container(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	azCli := mockazapi.NewAzureClientFromMockContext(mockContext)
	updateCalled := false

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPatch &&
			strings.Contains(request.URL.Path, "/sites/FUNCTION_APP_NAME") &&
			!strings.Contains(request.URL.Path, "/slots")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		updateCalled = true
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, armappservice.Site{})
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(request.URL.Path, "/sites/FUNCTION_APP_NAME") &&
			!strings.Contains(request.URL.Path, "/slots")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, armappservice.Site{
			Kind: new("functionapp,linux,container"),
			Properties: &armappservice.SiteProperties{
				DefaultHostName: new("function.azurewebsites.net"),
				SiteConfig: &armappservice.SiteConfig{
					LinuxFxVersion: new("DOCKER|placeholder:latest"),
				},
			},
		})
	})
	mockSlotsResponse(mockContext, nil)

	serviceContext := NewServiceContext()
	require.NoError(t, serviceContext.Publish.Add(&Artifact{
		Kind:         ArtifactKindContainer,
		Location:     "registry.azurecr.io/function:latest",
		LocationKind: LocationKindRemote,
	}))
	target := NewFunctionAppTarget(
		environment.New("test"),
		nil,
		nil,
		azCli,
		mockContext.Console,
	)

	result, err := target.Deploy(
		*mockContext.Context,
		&ServiceConfig{Name: "function"},
		serviceContext,
		environment.NewTargetResource(
			"SUB_ID",
			"RG_ID",
			"FUNCTION_APP_NAME",
			string(azapi.AzureResourceTypeWebSite),
		),
		async.NewNoopProgress[ServiceProgress](),
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, updateCalled)
}

func Test_functionAppTarget_Deploy_ContainerMismatchFailsBeforeZip(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	azCli := mockazapi.NewAzureClientFromMockContext(mockContext)

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(request.URL.Path, "/sites/FUNCTION_APP_NAME")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, armappservice.Site{
			Kind: new("functionapp,linux,container"),
			Properties: &armappservice.SiteProperties{
				DefaultHostName: new("function.azurewebsites.net"),
				ServerFarmID: new(
					"/subscriptions/SUB_ID/resourceGroups/RG_ID/providers/Microsoft.Web/serverfarms/PLAN",
				),
				SiteConfig: &armappservice.SiteConfig{
					LinuxFxVersion: new("DOCKER|registry.azurecr.io/function:latest"),
				},
			},
		})
	})

	target := NewFunctionAppTarget(nil, nil, nil, azCli, mockContext.Console)
	_, err := target.Deploy(
		*mockContext.Context,
		&ServiceConfig{Name: "function", Language: ServiceLanguageTypeScript},
		NewServiceContext(),
		environment.NewTargetResource(
			"SUB_ID",
			"RG_ID",
			"FUNCTION_APP_NAME",
			string(azapi.AzureResourceTypeWebSite),
		),
		async.NewNoopProgress[ServiceProgress](),
	)

	require.Error(t, err)
	assert.ErrorContains(t, err, "configured for container deployment")
	assert.ErrorContains(t, err, "configured for zip deployment")
}

func Test_functionAppTarget_Deploy_CodeSiteUsesZipPath(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	azCli := mockazapi.NewAzureClientFromMockContext(mockContext)

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(request.URL.Path, "/sites/FUNCTION_APP_NAME")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, armappservice.Site{
			Kind: new("functionapp,linux"),
			Properties: &armappservice.SiteProperties{
				DefaultHostName: new("function.azurewebsites.net"),
				ServerFarmID: new(
					"/subscriptions/SUB_ID/resourceGroups/RG_ID/providers/Microsoft.Web/serverfarms/PLAN",
				),
				SiteConfig: &armappservice.SiteConfig{LinuxFxVersion: new("NODE|20-lts")},
			},
		})
	})

	target := NewFunctionAppTarget(nil, nil, nil, azCli, mockContext.Console)
	_, err := target.Deploy(
		*mockContext.Context,
		&ServiceConfig{Name: "function", Language: ServiceLanguageTypeScript},
		NewServiceContext(),
		environment.NewTargetResource(
			"SUB_ID",
			"RG_ID",
			"FUNCTION_APP_NAME",
			string(azapi.AzureResourceTypeWebSite),
		),
		async.NewNoopProgress[ServiceProgress](),
	)

	require.Error(t, err)
	assert.ErrorContains(t, err, "no zip package found")
}
