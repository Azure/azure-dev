// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

type projectSetupAccountServer struct {
	azdext.UnimplementedAccountServiceServer
}

func (s *projectSetupAccountServer) LookupTenant(
	context.Context, *azdext.LookupTenantRequest,
) (*azdext.LookupTenantResponse, error) {
	return &azdext.LookupTenantResponse{TenantId: "tenant"}, nil
}

func TestValidateNewProjectLocation(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, project, model string
		wantError            bool
	}{
		{"matching", "westus2", "westus2", false},
		{"normalized", " WestUS2 ", "westus2", false},
		{"unset project", "", "westus2", false},
		{"unset model", "japaneast", "", false},
		{"recovery changed location", "japaneast", "westus2", true},
		{"internal mismatch", "eastus2euap", "westus2", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := validateNewProjectLocation(tt.project, tt.model)
			if !tt.wantError {
				require.NoError(t, err)
				return
			}
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			require.Equal(t, exterrors.CodeLocationMismatch, localErr.Code)
			require.Contains(t, localErr.Suggestion, "same supported region")
			require.NotContains(t, localErr.Message+localErr.Suggestion, "eastus2euap")
		})
	}
}

func TestConfigureFoundryProjectRestrictedSplitLocation(t *testing.T) {
	resetAgentRegionsCache(t, &internalHarnessRegionsCache, []string{"eastus2euap"})
	allowed, err := parseAgentRegionsManifest(embeddedHarnessAgentRegionsJSON)
	require.NoError(t, err)
	resetAgentRegionsCache(t, &harnessRegionsCache, allowed)
	for _, flow := range []string{"generated", "adopted"} {
		for _, tt := range []struct {
			name, location, deploymentLocation, wantLocation, wantDeploymentLocation, wantError string
		}{
			{"unsupported project", "eastus", "westus2", "eastus", "westus2", "configured AZURE_LOCATION"},
			{"public split", "japaneast", "westus2", "japaneast", "westus2", "must match"},
			{"public both", "westus2", "westus2", "westus2", "westus2", ""},
			{"missing project location", "", "westus2", "westus2", "westus2", ""},
			{"internal split", "eastus2euap", "westus2", "eastus2euap", "westus2", "must match"},
			{"internal both", "eastus2euap", "eastus2euap", "eastus2euap", "eastus2euap", ""},
			{"internal fallback", "eastus2euap", "", "eastus2euap", "eastus2euap", ""},
			{"unsupported fallback", "eastus", "", "eastus", "", "configured AZURE_LOCATION"},
			{"unsupported deployment", "japaneast", "eastus", "japaneast", "eastus", "location \"eastus\""},
		} {
			t.Run(flow+"/"+tt.name, func(t *testing.T) {
				values := map[string]string{
					"AZURE_SUBSCRIPTION_ID":         "sub",
					"AZURE_LOCATION":                tt.location,
					"AZURE_AI_DEPLOYMENTS_LOCATION": tt.deploymentLocation,
				}
				envServer := &testEnvironmentServiceServer{values: map[string]map[string]string{"test": values}}
				address := newInvokeRemoteContextTestAzdServer(
					t, &helpersProjectServer{}, envServer, &projectSetupAccountServer{},
				)
				client, err := azdext.NewAzdClient(azdext.WithAddress(address))
				require.NoError(t, err)
				t.Cleanup(func() { client.Close() })
				azureContext, err := loadAzureContext(t.Context(), client, "test")
				require.NoError(t, err)
				if flow == "generated" {
					_, _, _, _, err = resolvePromptHarnessTarget(t.Context(), client,
						&initFlags{noPrompt: true, model: "gpt-4.1-mini"}, &azdext.Environment{Name: "test"},
						&project.PromptAgentSettings{}, "microsoft_copilot_preview")
				} else {
					_, err = configureFoundryProject(withHarnessRegionPolicy(t.Context(), "microsoft_copilot_preview"),
						client, azureContext, "test", "", "", true, true, false, false, allowed)
				}
				if tt.wantError != "" {
					require.ErrorContains(t, err, tt.wantError)
					require.NotContains(t, err.Error(), "PromptLocation", "headless validation must not attempt a location prompt")
				} else if flow == "generated" {
					// No model service is registered: reaching it proves location setup
					// succeeded without prompting or rewriting configured locations.
					require.ErrorContains(t, err, "AiModelService")
				} else {
					require.NoError(t, err)
				}
				require.Equal(t, tt.wantLocation, values["AZURE_LOCATION"])
				require.Equal(t, tt.wantDeploymentLocation, values["AZURE_AI_DEPLOYMENTS_LOCATION"])
			})
		}
	}
}

func TestValidateAcrConnectionInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		acrConnection     string
		skipACR           bool
		createsNewProject bool
		wantMessage       string
	}{
		{name: "empty connection is allowed", skipACR: true},
		{name: "existing container project is allowed", acrConnection: "registry-connection"},
		{
			name:          "code deployment is rejected",
			acrConnection: "registry-connection",
			skipACR:       true,
			wantMessage:   "skips Azure Container Registry configuration",
		},
		{
			name:              "new project is rejected",
			acrConnection:     "registry-connection",
			createsNewProject: true,
			wantMessage:       "requires an existing Foundry project",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateAcrConnectionInput(tt.acrConnection, tt.skipACR, tt.createsNewProject)
			if tt.wantMessage == "" {
				require.NoError(t, err)
				return
			}

			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			require.Equal(t, exterrors.CodeInvalidParameter, localErr.Code)
			require.Contains(t, localErr.Message, tt.wantMessage)
		})
	}
}

func TestConfigureFoundryProjectRejectsAcrConnectionForHeadlessNewProject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		azureContext *azdext.AzureContext
	}{
		{
			name:         "deferred Azure context",
			azureContext: &azdext.AzureContext{Scope: &azdext.AzureScope{}},
		},
		{
			name: "resolved Azure context",
			azureContext: &azdext.AzureContext{Scope: &azdext.AzureScope{
				SubscriptionId: "subscription-id",
				Location:       "eastus2",
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := configureFoundryProject(
				t.Context(),
				nil,
				tt.azureContext,
				"test-env",
				"",
				"registry-connection",
				true,
				false,
				false,
				false,
				nil,
			)
			require.Error(t, err)
			require.Contains(t, err.Error(), "requires an existing Foundry project")
		})
	}
}

func TestConfigureFoundryProjectRejectsAcrConnectionForInteractiveNewProject(t *testing.T) {
	promptServer := &helpersPromptServer{selectIndex: 1}
	azdClient := newHelpersTestAzdClient(t, &helpersProjectServer{}, promptServer)

	_, err := configureFoundryProject(
		t.Context(),
		azdClient,
		&azdext.AzureContext{Scope: &azdext.AzureScope{}},
		"test-env",
		"",
		"registry-connection",
		false,
		false,
		false,
		false,
		nil,
	)

	require.ErrorContains(t, err, "requires an existing Foundry project")
	require.Equal(t, int32(1), promptServer.selectCalls.Load())
}

func TestConfigureFoundryProjectDefersAuthoringWhenAzureContextIsMissing(t *testing.T) {
	envServer := &testEnvironmentServiceServer{
		values: map[string]map[string]string{
			"test-env": {},
		},
	}
	azdClient := newHelpersTestAzdClient(
		t,
		&helpersProjectServer{},
		&helpersPromptServer{},
		envServer,
	)

	result, err := configureFoundryProject(
		t.Context(),
		azdClient,
		&azdext.AzureContext{Scope: &azdext.AzureScope{}},
		"test-env",
		"",
		"",
		true,
		false,
		false,
		false,
		nil,
	)

	require.NoError(t, err)
	require.Nil(t, result.FoundryProject)
	require.Equal(t, projectAuthoringCurrent, result.AuthoringMode)
}

func TestConfigureFoundryProjectPreservesExistingServiceWhenDeferred(t *testing.T) {
	envServer := &testEnvironmentServiceServer{
		values: map[string]map[string]string{
			"test-env": {},
		},
	}
	projectServer := &helpersProjectServer{
		project: &azdext.ProjectConfig{
			Services: map[string]*azdext.ServiceConfig{
				"my-project": {Host: AiProjectHost},
			},
		},
	}
	azdClient := newHelpersTestAzdClient(
		t,
		projectServer,
		&helpersPromptServer{},
		envServer,
	)

	_, err := configureFoundryProject(
		t.Context(),
		azdClient,
		&azdext.AzureContext{Scope: &azdext.AzureScope{}},
		"test-env",
		"",
		"",
		true,
		false,
		false,
		true,
		nil,
	)

	require.NoError(t, err)
	require.Equal(
		t,
		"true",
		envServer.values["test-env"]["USE_EXISTING_AI_PROJECT"],
	)
}

func TestConfigureFoundryProjectPreservesExistingServiceWithAzureContext(
	t *testing.T,
) {
	t.Parallel()

	envServer := &testEnvironmentServiceServer{
		values: map[string]map[string]string{
			"test-env": {},
		},
	}
	azdClient := newHelpersTestAzdClient(
		t,
		&helpersProjectServer{},
		&helpersPromptServer{},
		envServer,
	)

	result, err := configureFoundryProject(
		t.Context(),
		azdClient,
		&azdext.AzureContext{Scope: &azdext.AzureScope{
			SubscriptionId: "subscription-id",
			Location:       "eastus2",
		}},
		"test-env",
		"",
		"",
		true,
		false,
		false,
		true,
		nil,
	)

	require.NoError(t, err)
	require.Nil(t, result.FoundryProject)
	require.Equal(t, projectAuthoringCurrent, result.AuthoringMode)
}
