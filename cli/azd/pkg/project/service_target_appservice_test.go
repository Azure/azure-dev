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

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazapi"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
)

type serviceTargetValidationTest struct {
	targetResource *environment.TargetResource
	expectError    bool
}

func TestNewAppServiceTargetTypeValidation(t *testing.T) {
	t.Parallel()

	tests := map[string]*serviceTargetValidationTest{
		"ValidateTypeSuccess": {
			targetResource: environment.NewTargetResource("SUB_ID", "RG_ID", "res", string(azapi.AzureResourceTypeWebSite)),
			expectError:    false,
		},
		"ValidateTypeLowerCaseSuccess": {
			targetResource: environment.NewTargetResource(
				"SUB_ID",
				"RG_ID",
				"res",
				strings.ToLower(string(azapi.AzureResourceTypeWebSite)),
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
			serviceTarget := &appServiceTarget{}

			err := serviceTarget.validateTargetResource(data.targetResource)
			if data.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// mockSlotsResponse registers a mock HTTP response for the GetAppServiceSlots call.
func mockSlotsResponse(mockContext *mocks.MockContext, slotNames []string) {
	sites := make([]*armappservice.Site, len(slotNames))
	for i, name := range slotNames {
		sites[i] = &armappservice.Site{
			Name: new("WEB_APP_NAME/" + name),
		}
	}
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(request.URL.Path, "/slots")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		response := armappservice.WebAppsClientListSlotsResponse{
			WebAppCollection: armappservice.WebAppCollection{
				Value: sites,
			},
		}
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
	})
}

func TestDetermineDeploymentTargets(t *testing.T) {
	t.Parallel()

	serviceConfig := &ServiceConfig{Name: "my-api"}
	targetResource := environment.NewTargetResource(
		"SUB_ID", "RG_ID", "WEB_APP_NAME", string(azapi.AzureResourceTypeWebSite),
	)

	type testCase struct {
		name           string
		slotNames      []string
		envVars        map[string]string
		noPrompt       bool
		selectIndex    int // for interactive prompt mock (-1 = no prompt expected)
		expectError    bool
		expectErrorMsg string
		expectTargets  []string // empty string = main app
	}

	tests := []testCase{
		{
			name:          "SlotName_Production_DeploysToMainApp",
			slotNames:     []string{"staging"},
			envVars:       map[string]string{"AZD_DEPLOY_MY_API_SLOT_NAME": "production"},
			noPrompt:      true,
			selectIndex:   -1,
			expectTargets: []string{""},
		},
		{
			name:          "SlotName_Production_CaseInsensitive",
			slotNames:     []string{"staging"},
			envVars:       map[string]string{"AZD_DEPLOY_MY_API_SLOT_NAME": "Production"},
			noPrompt:      true,
			selectIndex:   -1,
			expectTargets: []string{""},
		},
		{
			name:          "SlotName_Staging_DeploysToSlot",
			slotNames:     []string{"staging"},
			envVars:       map[string]string{"AZD_DEPLOY_MY_API_SLOT_NAME": "staging"},
			noPrompt:      true,
			selectIndex:   -1,
			expectTargets: []string{"staging"},
		},
		{
			name:           "SlotName_Invalid_ReturnsError",
			slotNames:      []string{"staging", "canary"},
			envVars:        map[string]string{"AZD_DEPLOY_MY_API_SLOT_NAME": "nonexistent"},
			noPrompt:       true,
			selectIndex:    -1,
			expectError:    true,
			expectErrorMsg: "not found",
		},
		{
			name:          "NoSlots_DeploysToMainApp",
			slotNames:     []string{},
			envVars:       map[string]string{},
			noPrompt:      true,
			selectIndex:   -1,
			expectTargets: []string{""},
		},
		{
			name:           "SlotsExist_NoPrompt_NoSlotName_FailsWithError",
			slotNames:      []string{"staging"},
			envVars:        map[string]string{},
			noPrompt:       true,
			selectIndex:    -1,
			expectError:    true,
			expectErrorMsg: "no target specified",
		},
		{
			name:           "MultipleSlots_NoPrompt_NoSlotName_FailsWithError",
			slotNames:      []string{"staging", "canary"},
			envVars:        map[string]string{},
			noPrompt:       true,
			selectIndex:    -1,
			expectError:    true,
			expectErrorMsg: "no target specified",
		},
		{
			name:          "SlotsExist_Interactive_SelectMainApp",
			slotNames:     []string{"staging"},
			envVars:       map[string]string{},
			noPrompt:      false,
			selectIndex:   0, // Index 0 = "production (main app)"
			expectTargets: []string{""},
		},
		{
			name:          "SlotsExist_Interactive_SelectSlot",
			slotNames:     []string{"staging"},
			envVars:       map[string]string{},
			noPrompt:      false,
			selectIndex:   1, // Index 1 = "staging"
			expectTargets: []string{"staging"},
		},
		{
			name:          "MultipleSlots_Interactive_SelectSecondSlot",
			slotNames:     []string{"staging", "canary"},
			envVars:       map[string]string{},
			noPrompt:      false,
			selectIndex:   2, // Index 0=production, 1=staging, 2=canary
			expectTargets: []string{"canary"},
		},
		{
			name:          "SlotName_Production_NoSlotsExist_StillWorks",
			slotNames:     []string{},
			envVars:       map[string]string{"AZD_DEPLOY_MY_API_SLOT_NAME": "production"},
			noPrompt:      true,
			selectIndex:   -1,
			expectTargets: []string{""},
		},
		{
			name:           "SlotName_Invalid_ErrorIncludesProductionHint",
			slotNames:      []string{"staging"},
			envVars:        map[string]string{"AZD_DEPLOY_MY_API_SLOT_NAME": "bad"},
			noPrompt:       true,
			selectIndex:    -1,
			expectError:    true,
			expectErrorMsg: "production",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockContext := mocks.NewMockContext(t.Context())
			azCli := mockazapi.NewAzureClientFromMockContext(mockContext)

			// Set up env vars
			env := environment.New("test")
			for k, v := range tc.envVars {
				env.DotenvSet(k, v)
			}

			// Mock slots response
			mockSlotsResponse(mockContext, tc.slotNames)

			// Set no-prompt mode
			mockContext.Console.SetNoPromptMode(tc.noPrompt)

			// Mock interactive select if needed
			if tc.selectIndex >= 0 {
				mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
					return true
				}).Respond(tc.selectIndex)
			}

			target := &appServiceTarget{
				env:     env,
				cli:     azCli,
				console: mockContext.Console,
			}

			progress := async.NewNoopProgress[ServiceProgress]()
			targets, err := target.determineDeploymentTargets(
				*mockContext.Context,
				serviceConfig,
				targetResource,
				progress,
			)

			if tc.expectError {
				require.Error(t, err)
				if tc.expectErrorMsg != "" {
					require.Contains(t, err.Error(), tc.expectErrorMsg)
				}
			} else {
				require.NoError(t, err)
				require.Len(t, targets, len(tc.expectTargets))
				for i, expected := range tc.expectTargets {
					require.Equal(t, expected, targets[i].SlotName)
				}
			}
		})
	}
}

func Test_appServiceTarget_Package(t *testing.T) {
	t.Run("Success_CreatesZip", func(t *testing.T) {
		tmpDir := t.TempDir()
		pkgDir := filepath.Join(tmpDir, "pkg")
		require.NoError(t, os.MkdirAll(pkgDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "app.py"), []byte("print('hi')"), 0o600))

		sc := &ServiceConfig{
			Name:     "web",
			Language: ServiceLanguagePython,
			Project:  &ProjectConfig{Path: tmpDir},
		}

		sctx := NewServiceContext()
		require.NoError(t, sctx.Package.Add(&Artifact{
			Kind:         ArtifactKindDirectory,
			Location:     pkgDir,
			LocationKind: LocationKindLocal,
		}))

		st := &appServiceTarget{}
		progress := async.NewProgress[ServiceProgress]()
		go func() {
			for range progress.Progress() {
			}
		}()

		result, err := st.Package(t.Context(), sc, sctx, progress)
		progress.Done()

		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotEmpty(t, result.Artifacts)

		zipArtifact, found := result.Artifacts.FindFirst(WithKind(ArtifactKindArchive))
		require.True(t, found)
		assert.FileExists(t, zipArtifact.Location)
		assert.Equal(t, pkgDir, zipArtifact.Metadata["packagePath"])
	})

	t.Run("NoArtifact_Error", func(t *testing.T) {
		sc := &ServiceConfig{
			Name:     "web",
			Language: ServiceLanguagePython,
			Project:  &ProjectConfig{Path: t.TempDir()},
		}

		sctx := NewServiceContext()
		st := &appServiceTarget{}
		progress := async.NewProgress[ServiceProgress]()
		go func() {
			for range progress.Progress() {
			}
		}()

		_, err := st.Package(t.Context(), sc, sctx, progress)
		progress.Done()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "no package artifacts found")
	})
}

func Test_appServiceTarget_Publish(t *testing.T) {
	t.Run("NoContainerArtifact_ReturnsEmpty", func(t *testing.T) {
		target := &appServiceTarget{}
		sctx := NewServiceContext()
		result, err := target.Publish(t.Context(), &ServiceConfig{}, sctx, nil, nil, nil)
		require.NoError(t, err)
		require.NotNil(t, result)
	})

	t.Run("ContainerDeploy_RemoteImageShortCircuit", func(t *testing.T) {
		// When a container artifact with a registry is present, Publish should
		// short-circuit (no ContainerHelper call) and return the remote image reference.
		mockContext := mocks.NewMockContext(t.Context())
		azCli := mockazapi.NewAzureClientFromMockContext(mockContext)
		env := environment.New("test")
		envManager := &mockenv.MockEnvManager{}
		envManager.On("Save", mock.Anything, mock.Anything).Return(nil)

		targetResource := environment.NewTargetResource(
			"SUB_ID", "RG_ID", "WEB_APP_NAME", string(azapi.AzureResourceTypeWebSite),
		)

		sctx := NewServiceContext()
		require.NoError(t, sctx.Package.Add(&Artifact{
			Kind:         ArtifactKindContainer,
			Location:     "myregistry.azurecr.io/myapp:abc123",
			LocationKind: LocationKindLocal,
		}))

		st := &appServiceTarget{
			env:        env,
			envManager: envManager,
			cli:        azCli,
			console:    mockContext.Console,
		}

		progress := async.NewNoopProgress[ServiceProgress]()
		result, err := st.Publish(*mockContext.Context, &ServiceConfig{
			Name:     "web",
			Language: ServiceLanguageDocker,
		}, sctx, targetResource, progress, nil)

		require.NoError(t, err)
		require.NotNil(t, result)

		// Should have a remote container artifact
		artifact, found := result.Artifacts.FindFirst(WithKind(ArtifactKindContainer))
		require.True(t, found, "should produce container artifact")
		assert.Equal(t, "myregistry.azurecr.io/myapp:abc123", artifact.Location)
		assert.Equal(t, LocationKindRemote, artifact.LocationKind)

		// Should have saved IMAGE_NAME to environment
		envManager.AssertCalled(t, "Save", mock.Anything, mock.Anything)
		assert.Equal(t, "myregistry.azurecr.io/myapp:abc123",
			env.GetServiceProperty("web", "IMAGE_NAME"))
	})
}

func Test_NewAppServiceTarget(t *testing.T) {
	env := environment.NewWithValues("test-env", nil)
	target := NewAppServiceTarget(env, nil, nil, nil, nil)
	require.NotNil(t, target)
}

func Test_appServiceTarget_RequiredExternalTools(t *testing.T) {
	t.Run("NonDocker_ReturnsEmpty", func(t *testing.T) {
		target := NewAppServiceTarget(nil, nil, nil, nil, nil)
		result := target.RequiredExternalTools(t.Context(), &ServiceConfig{
			Language: ServiceLanguagePython,
		})
		assert.Empty(t, result)
	})
}

func Test_appServiceTarget_Initialize(t *testing.T) {
	target := NewAppServiceTarget(nil, nil, nil, nil, nil)
	err := target.Initialize(t.Context(), nil)
	require.NoError(t, err)
}

func Test_appServiceTarget_Package_ContainerArtifact(t *testing.T) {
	t.Run("ContainerArtifact_PassesThrough", func(t *testing.T) {
		sc := &ServiceConfig{
			Name:     "web",
			Language: ServiceLanguageDocker,
			Project:  &ProjectConfig{Path: t.TempDir()},
		}

		sctx := NewServiceContext()
		require.NoError(t, sctx.Package.Add(&Artifact{
			Kind:         ArtifactKindContainer,
			Location:     "myimage:latest",
			LocationKind: LocationKindLocal,
		}))

		st := &appServiceTarget{}
		progress := async.NewProgress[ServiceProgress]()
		go func() {
			for range progress.Progress() {
			}
		}()

		result, err := st.Package(t.Context(), sc, sctx, progress)
		progress.Done()

		require.NoError(t, err)
		require.NotNil(t, result)
		// Container artifacts pass through; no zip should be created
		_, foundZip := result.Artifacts.FindFirst(WithKind(ArtifactKindArchive))
		assert.False(t, foundZip, "should not create zip for container deployments")
	})

	t.Run("RemoteBuild_NoArtifacts_NoopNoError", func(t *testing.T) {
		// Remote build and dotnet-publish docker flows produce no package artifacts.
		// Package() should be a no-op (no zip, no error) when service is configured for docker.
		sc := &ServiceConfig{
			Name:     "web",
			Language: ServiceLanguageDocker,
			Docker:   DockerProjectOptions{RemoteBuild: true},
			Project:  &ProjectConfig{Path: t.TempDir()},
		}

		sctx := NewServiceContext()
		// Empty package artifacts (simulates remote build behavior)

		st := &appServiceTarget{}
		progress := async.NewProgress[ServiceProgress]()
		go func() {
			for range progress.Progress() {
			}
		}()

		result, err := st.Package(t.Context(), sc, sctx, progress)
		progress.Done()

		require.NoError(t, err, "should not error for remote-build docker service with no artifacts")
		require.NotNil(t, result)
		assert.Empty(t, result.Artifacts, "should produce no artifacts for remote build")
	})
}

func Test_appServiceTarget_Deploy_ContainerPath(t *testing.T) {
	t.Run("ContainerArtifact_UpdatesContainerConfig", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		azCli := mockazapi.NewAzureClientFromMockContext(mockContext)

		targetResource := environment.NewTargetResource(
			"SUB_ID", "RG_ID", "WEB_APP_NAME", string(azapi.AzureResourceTypeWebSite),
		)

		// Mock the Update call for container config
		updateCalled := false
		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodPatch &&
				strings.Contains(request.URL.Path, "/sites/WEB_APP_NAME") &&
				!strings.Contains(request.URL.Path, "/slots")
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			updateCalled = true
			site := armappservice.Site{
				Properties: &armappservice.SiteProperties{
					DefaultHostName: new("webapp.azurewebsites.net"),
				},
			}
			return mocks.CreateHttpResponseWithBody(request, http.StatusOK, site)
		})

		// Mock GetAppServiceProperties (for Validation + Endpoints)
		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet &&
				strings.Contains(request.URL.Path, "/sites/WEB_APP_NAME") &&
				!strings.Contains(request.URL.Path, "/slots")
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			response := armappservice.WebAppsClientGetResponse{
				Site: armappservice.Site{
					Kind: new("app,linux,container"),
					Properties: &armappservice.SiteProperties{
						DefaultHostName: new("webapp.azurewebsites.net"),
						SiteConfig: &armappservice.SiteConfig{
							LinuxFxVersion: new("DOCKER|placeholder:latest"),
						},
					},
				},
			}
			return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
		})

		// Mock slots (empty)
		mockSlotsResponse(mockContext, []string{})

		sctx := NewServiceContext()
		require.NoError(t, sctx.Publish.Add(&Artifact{
			Kind:         ArtifactKindContainer,
			Location:     "myregistry.azurecr.io/myapp:abc123",
			LocationKind: LocationKindRemote,
		}))

		st := &appServiceTarget{
			env:     environment.New("test"),
			cli:     azCli,
			console: mockContext.Console,
		}

		progress := async.NewNoopProgress[ServiceProgress]()
		result, err := st.Deploy(*mockContext.Context, &ServiceConfig{Name: "web"}, sctx, targetResource, progress)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, updateCalled, "should have called Update to set container config")

		// Verify endpoints are returned
		endpoints := result.Artifacts.Find(WithKind(ArtifactKindEndpoint))
		assert.NotEmpty(t, endpoints, "should have endpoint artifacts")
	})
}

func Test_appServiceTarget_Deploy_ZipPath(t *testing.T) {
	t.Run("ZipArtifact_UsesZipDeploy", func(t *testing.T) {
		// Verify that the zip deploy path is still used when no container artifact is present
		sctx := NewServiceContext()
		// No container artifact in Publish, and no zip in Package means error
		st := &appServiceTarget{
			env:     environment.New("test"),
			console: nil,
		}

		targetResource := environment.NewTargetResource(
			"SUB_ID", "RG_ID", "WEB_APP_NAME", string(azapi.AzureResourceTypeWebSite),
		)

		progress := async.NewNoopProgress[ServiceProgress]()
		_, err := st.Deploy(t.Context(), &ServiceConfig{Name: "web"}, sctx, targetResource, progress)

		// Should fail because there are no zip artifacts (proving it took the zip path, not container path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no zip artifacts found")
	})
}

func Test_appServiceTarget_Deploy_ContainerSlotPath(t *testing.T) {
	t.Run("ContainerArtifact_DeploysToSlot", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		azCli := mockazapi.NewAzureClientFromMockContext(mockContext)

		targetResource := environment.NewTargetResource(
			"SUB_ID", "RG_ID", "WEB_APP_NAME", string(azapi.AzureResourceTypeWebSite),
		)

		// Mock the slot PATCH call for container config
		slotUpdateCalled := false
		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodPatch &&
				strings.Contains(request.URL.Path, "/slots/staging")
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			slotUpdateCalled = true
			site := armappservice.Site{
				Properties: &armappservice.SiteProperties{
					DefaultHostName: new("webapp-staging.azurewebsites.net"),
				},
			}
			return mocks.CreateHttpResponseWithBody(request, http.StatusOK, site)
		})

		// Mock GetAppServiceProperties (for Validation + Endpoints)
		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet &&
				strings.Contains(request.URL.Path, "/sites/WEB_APP_NAME") &&
				!strings.Contains(request.URL.Path, "/slots")
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			response := armappservice.WebAppsClientGetResponse{
				Site: armappservice.Site{
					Kind: new("app,linux,container"),
					Properties: &armappservice.SiteProperties{
						DefaultHostName: new("webapp.azurewebsites.net"),
						SiteConfig: &armappservice.SiteConfig{
							LinuxFxVersion: new("DOCKER|placeholder:latest"),
						},
					},
				},
			}
			return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
		})

		// Mock slots response with "staging" slot
		mockSlotsResponse(mockContext, []string{"staging"})

		// Mock GetAppServiceSlotProperties (for Endpoints)
		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet &&
				strings.Contains(request.URL.Path, "/slots/staging")
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			site := armappservice.Site{
				Properties: &armappservice.SiteProperties{
					DefaultHostName: new("webapp-staging.azurewebsites.net"),
				},
			}
			return mocks.CreateHttpResponseWithBody(request, http.StatusOK, site)
		})

		// Set slot name env var to target the staging slot
		env := environment.New("test")
		env.DotenvSet("AZD_DEPLOY_WEB_SLOT_NAME", "staging")

		sctx := NewServiceContext()
		require.NoError(t, sctx.Publish.Add(&Artifact{
			Kind:         ArtifactKindContainer,
			Location:     "myregistry.azurecr.io/myapp:abc123",
			LocationKind: LocationKindRemote,
		}))

		st := &appServiceTarget{
			env:     env,
			cli:     azCli,
			console: mockContext.Console,
		}

		progress := async.NewNoopProgress[ServiceProgress]()
		result, err := st.Deploy(*mockContext.Context, &ServiceConfig{Name: "web"}, sctx, targetResource, progress)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, slotUpdateCalled, "should have called slot PATCH to set container config")
	})
}

func Test_appServiceTarget_RequiredExternalTools_Docker(t *testing.T) {
	t.Run("DockerLanguage_DelegatesToContainerHelper", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		dockerCli := docker.NewCli(mockContext.CommandRunner)
		containerHelper := NewContainerHelper(
			nil, nil, nil, nil, dockerCli, nil, mockContext.Console, nil)
		target := &appServiceTarget{
			containerHelper: containerHelper,
		}
		sc := &ServiceConfig{
			Language: ServiceLanguageDocker,
		}
		tools := target.RequiredExternalTools(*mockContext.Context, sc)
		assert.NotEmpty(t, tools, "should return docker tools for docker language")
	})

	t.Run("DockerPath_DelegatesToContainerHelper", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		dockerCli := docker.NewCli(mockContext.CommandRunner)
		containerHelper := NewContainerHelper(
			nil, nil, nil, nil, dockerCli, nil, mockContext.Console, nil)
		target := &appServiceTarget{
			containerHelper: containerHelper,
		}
		sc := &ServiceConfig{
			Language: ServiceLanguagePython,
			Docker:   DockerProjectOptions{Path: "./Dockerfile"},
		}
		tools := target.RequiredExternalTools(*mockContext.Context, sc)
		assert.NotEmpty(t, tools, "should return docker tools when docker.path is set")
	})

	t.Run("NonDocker_ReturnsEmpty", func(t *testing.T) {
		target := &appServiceTarget{}
		sc := &ServiceConfig{
			Language: ServiceLanguagePython,
		}
		tools := target.RequiredExternalTools(t.Context(), sc)
		assert.Empty(t, tools, "should return no tools for non-docker language")
	})
}
