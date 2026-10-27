// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"testing"

	"azureaiagent/internal/exterrors"

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

func TestConfigureFoundryProjectRestrictedSplitLocation(t *testing.T) {
	for _, location := range []string{"eastus", "japaneast", ""} {
		t.Run(location, func(t *testing.T) {
			values := map[string]string{
				"AZURE_SUBSCRIPTION_ID":         "sub",
				"AZURE_LOCATION":                location,
				"AZURE_AI_DEPLOYMENTS_LOCATION": "westus2",
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
			_, err = configureFoundryProject(t.Context(), client, azureContext, "test", "", "",
				true, true, false, false, promptHarnessRegionsForInit("microsoft_copilot_preview"), "")
			if location == "eastus" {
				require.ErrorContains(t, err, "configured AZURE_LOCATION")
				require.Equal(t, location, values["AZURE_LOCATION"])
			} else {
				require.NoError(t, err)
				if location == "" {
					location = "westus2"
				}
				require.Equal(t, location, values["AZURE_LOCATION"])
			}
			require.Equal(t, "westus2", values["AZURE_AI_DEPLOYMENTS_LOCATION"])
		})
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
				"",
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
		"",
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
		"",
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
		"",
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
		"",
	)

	require.NoError(t, err)
	require.Nil(t, result.FoundryProject)
	require.Equal(t, projectAuthoringCurrent, result.AuthoringMode)
}
