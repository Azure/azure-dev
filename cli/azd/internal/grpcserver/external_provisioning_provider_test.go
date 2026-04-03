// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_convertToProtoOptions(t *testing.T) {
	tests := []struct {
		name    string
		options provisioning.Options
		verify  func(t *testing.T, result *azdext.ProvisioningOptions, err error)
	}{
		{
			name: "FullOptions",
			options: provisioning.Options{
				Provider: provisioning.Bicep,
				Path:     "/infra",
				Module:   "main",
				Name:     "layer1",
				DeploymentStacks: map[string]any{
					"stackName": "my-stack",
					"intVal":    42,
					"boolVal":   true,
				},
				IgnoreDeploymentState: true,
				Config: map[string]any{
					"key1": "value1",
					"nested": map[string]any{
						"key2": "value2",
					},
				},
			},
			verify: func(t *testing.T, result *azdext.ProvisioningOptions, err error) {
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.Equal(t, "bicep", result.Provider)
				assert.Equal(t, "/infra", result.Path)
				assert.Equal(t, "main", result.Module)
				assert.Equal(t, "layer1", result.Name)
				assert.True(t, result.IgnoreDeploymentState)
				assert.Equal(t, "my-stack", result.DeploymentStacks["stackName"])
				assert.Equal(t, "42", result.DeploymentStacks["intVal"])
				assert.Equal(t, "true", result.DeploymentStacks["boolVal"])
				require.NotNil(t, result.Config)
				assert.Equal(t, "value1", result.Config.Fields["key1"].GetStringValue())
				nested := result.Config.Fields["nested"].GetStructValue()
				require.NotNil(t, nested)
				assert.Equal(t, "value2", nested.Fields["key2"].GetStringValue())
			},
		},
		{
			name:    "EmptyOptions",
			options: provisioning.Options{},
			verify: func(t *testing.T, result *azdext.ProvisioningOptions, err error) {
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.Empty(t, result.Provider)
				assert.Empty(t, result.Path)
				assert.Empty(t, result.Module)
				assert.Empty(t, result.Name)
				assert.False(t, result.IgnoreDeploymentState)
				assert.Empty(t, result.DeploymentStacks)
				assert.Nil(t, result.Config)
			},
		},
		{
			name: "NilConfig",
			options: provisioning.Options{
				Provider: provisioning.Terraform,
				Path:     "/terraform",
			},
			verify: func(t *testing.T, result *azdext.ProvisioningOptions, err error) {
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.Equal(t, "terraform", result.Provider)
				assert.Nil(t, result.Config)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := convertToProtoOptions(tt.options)
			tt.verify(t, result, err)
		})
	}
}

func Test_convertFromProtoStateResult(t *testing.T) {
	tests := []struct {
		name   string
		result *azdext.ProvisioningStateResult
		verify func(t *testing.T, result *provisioning.StateResult)
	}{
		{
			name:   "Nil",
			result: nil,
			verify: func(t *testing.T, result *provisioning.StateResult) {
				require.NotNil(t, result)
				assert.Nil(t, result.State)
			},
		},
		{
			name:   "NilState",
			result: &azdext.ProvisioningStateResult{State: nil},
			verify: func(t *testing.T, result *provisioning.StateResult) {
				require.NotNil(t, result)
				assert.Nil(t, result.State)
			},
		},
		{
			name: "EmptyState",
			result: &azdext.ProvisioningStateResult{
				State: &azdext.ProvisioningState{},
			},
			verify: func(t *testing.T, result *provisioning.StateResult) {
				require.NotNil(t, result)
				require.NotNil(t, result.State)
				assert.Empty(t, result.State.Outputs)
				assert.Empty(t, result.State.Resources)
			},
		},
		{
			name: "PopulatedState",
			result: &azdext.ProvisioningStateResult{
				State: &azdext.ProvisioningState{
					Outputs: map[string]*azdext.ProvisioningOutputParameter{
						"ENDPOINT": {Type: "string", Value: "https://example.com"},
						"PORT":     {Type: "number", Value: "8080"},
					},
					Resources: []*azdext.ProvisioningResource{
						{Id: "/subscriptions/sub1/resourceGroups/rg1"},
						{Id: "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Web/sites/app1"},
					},
				},
			},
			verify: func(t *testing.T, result *provisioning.StateResult) {
				require.NotNil(t, result)
				require.NotNil(t, result.State)
				assert.Len(t, result.State.Outputs, 2)

				endpoint := result.State.Outputs["ENDPOINT"]
				assert.Equal(t, provisioning.ParameterType("string"), endpoint.Type)
				assert.Equal(t, "https://example.com", endpoint.Value)

				port := result.State.Outputs["PORT"]
				assert.Equal(t, provisioning.ParameterType("number"), port.Type)
				assert.Equal(t, "8080", port.Value)

				assert.Len(t, result.State.Resources, 2)
				assert.Equal(t, "/subscriptions/sub1/resourceGroups/rg1", result.State.Resources[0].Id)
				assert.Equal(
					t,
					"/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Web/sites/app1",
					result.State.Resources[1].Id,
				)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertFromProtoStateResult(tt.result)
			tt.verify(t, result)
		})
	}
}

func Test_convertFromProtoDeployResult(t *testing.T) {
	tests := []struct {
		name   string
		result *azdext.ProvisioningDeployResult
		verify func(t *testing.T, result *provisioning.DeployResult)
	}{
		{
			name:   "EmptyResult",
			result: &azdext.ProvisioningDeployResult{},
			verify: func(t *testing.T, result *provisioning.DeployResult) {
				require.NotNil(t, result)
				assert.Nil(t, result.Deployment)
				assert.Empty(t, result.SkippedReason)
			},
		},
		{
			name: "FullDeployment",
			result: &azdext.ProvisioningDeployResult{
				Deployment: &azdext.ProvisioningDeployment{
					Parameters: map[string]*azdext.ProvisioningInputParameter{
						"location": {
							Type:         "string",
							DefaultValue: "eastus",
							Value:        "westus2",
						},
						"sku": {
							Type:  "string",
							Value: "B1",
						},
					},
					Outputs: map[string]*azdext.ProvisioningOutputParameter{
						"ENDPOINT": {Type: "string", Value: "https://app.example.com"},
					},
				},
			},
			verify: func(t *testing.T, result *provisioning.DeployResult) {
				require.NotNil(t, result)
				require.NotNil(t, result.Deployment)
				assert.Empty(t, result.SkippedReason)

				location := result.Deployment.Parameters["location"]
				assert.Equal(t, "string", location.Type)
				assert.Equal(t, "eastus", location.DefaultValue)
				assert.Equal(t, "westus2", location.Value)

				sku := result.Deployment.Parameters["sku"]
				assert.Equal(t, "string", sku.Type)
				assert.Nil(t, sku.DefaultValue)
				assert.Equal(t, "B1", sku.Value)

				endpoint := result.Deployment.Outputs["ENDPOINT"]
				assert.Equal(t, provisioning.ParameterType("string"), endpoint.Type)
				assert.Equal(t, "https://app.example.com", endpoint.Value)
			},
		},
		{
			name: "SkippedDeploymentState",
			result: &azdext.ProvisioningDeployResult{
				SkippedReason: azdext.ProvisioningSkippedReason_PROVISIONING_SKIPPED_REASON_DEPLOYMENT_STATE,
			},
			verify: func(t *testing.T, result *provisioning.DeployResult) {
				require.NotNil(t, result)
				assert.Equal(t, provisioning.DeploymentStateSkipped, result.SkippedReason)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertFromProtoDeployResult(tt.result)
			tt.verify(t, result)
		})
	}
}

func Test_convertFromProtoPreviewResult(t *testing.T) {
	tests := []struct {
		name   string
		result *azdext.ProvisioningPreviewResult
		verify func(t *testing.T, result *provisioning.DeployPreviewResult)
	}{
		{
			name:   "Nil",
			result: nil,
			verify: func(t *testing.T, result *provisioning.DeployPreviewResult) {
				require.NotNil(t, result)
				assert.Nil(t, result.Preview)
			},
		},
		{
			name:   "NilPreview",
			result: &azdext.ProvisioningPreviewResult{Preview: nil},
			verify: func(t *testing.T, result *provisioning.DeployPreviewResult) {
				require.NotNil(t, result)
				assert.Nil(t, result.Preview)
			},
		},
		{
			name: "PopulatedPreview",
			result: &azdext.ProvisioningPreviewResult{
				Preview: &azdext.ProvisioningDeploymentPreview{
					Summary: "3 resources to create, 1 to update",
				},
			},
			verify: func(t *testing.T, result *provisioning.DeployPreviewResult) {
				require.NotNil(t, result)
				require.NotNil(t, result.Preview)
				assert.Equal(t, "3 resources to create, 1 to update", result.Preview.Status)
				require.NotNil(t, result.Preview.Properties)
				assert.Empty(t, result.Preview.Properties.Changes)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertFromProtoPreviewResult(tt.result)
			tt.verify(t, result)
		})
	}
}

func Test_convertFromProtoParameters(t *testing.T) {
	tests := []struct {
		name   string
		params []*azdext.ProvisioningParameter
		verify func(t *testing.T, result []provisioning.Parameter)
	}{
		{
			name:   "Nil",
			params: nil,
			verify: func(t *testing.T, result []provisioning.Parameter) {
				assert.Empty(t, result)
			},
		},
		{
			name:   "EmptySlice",
			params: []*azdext.ProvisioningParameter{},
			verify: func(t *testing.T, result []provisioning.Parameter) {
				assert.Empty(t, result)
			},
		},
		{
			name: "MultipleParameters",
			params: []*azdext.ProvisioningParameter{
				{
					Name:  "location",
					Value: "eastus2",
				},
				{
					Name:   "adminPassword",
					Secret: true,
				},
				{
					Name:               "appName",
					Value:              "my-app",
					EnvVarMapping:      []string{"AZURE_APP_NAME", "APP_NAME"},
					LocalPrompt:        true,
					UsingEnvVarMapping: true,
				},
			},
			verify: func(t *testing.T, result []provisioning.Parameter) {
				require.Len(t, result, 3)

				assert.Equal(t, "location", result[0].Name)
				assert.Equal(t, "eastus2", result[0].Value)
				assert.False(t, result[0].Secret)

				assert.Equal(t, "adminPassword", result[1].Name)
				assert.True(t, result[1].Secret)

				assert.Equal(t, "appName", result[2].Name)
				assert.Equal(t, "my-app", result[2].Value)
				assert.Equal(t, []string{"AZURE_APP_NAME", "APP_NAME"}, result[2].EnvVarMapping)
				assert.True(t, result[2].LocalPrompt)
				assert.True(t, result[2].UsingEnvVarMapping)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertFromProtoParameters(tt.params)
			tt.verify(t, result)
		})
	}
}
