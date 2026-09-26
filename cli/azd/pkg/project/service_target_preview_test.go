// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/stretchr/testify/require"
)

func TestBuiltInServiceTargetsImplementResourcePreview(t *testing.T) {
	t.Parallel()

	targets := map[ServiceTargetKind]ServiceTarget{
		AppServiceTarget:    &appServiceTarget{},
		ContainerAppTarget:  &containerAppTarget{},
		AzureFunctionTarget: &functionAppTarget{},
		StaticWebAppTarget:  &staticWebAppTarget{},
		AksTarget:           &aksTarget{},
		AiEndpointTarget:    &aiEndpointTarget{},
	}

	for _, kind := range BuiltInServiceTargetKinds() {
		_, ok := targets[kind].(ServiceTargetResourcePreviewer)
		require.Truef(t, ok, "built-in service target %q must support deployment preview", kind)
	}
}

func TestBuiltInServiceTargetPreviews(t *testing.T) {
	t.Parallel()

	projectConfig := &ProjectConfig{Name: "test-project", Path: t.TempDir()}
	manifestsPath := filepath.Join(projectConfig.Path, "src", "api", defaultDeploymentPath)
	require.NoError(t, os.MkdirAll(manifestsPath, 0o755))

	env := environment.New("test")
	env.SetSubscriptionId("subscription-id")
	env.SetLocation("eastus")
	env.DotenvSet("AZUREAI_PROJECT_NAME", "ai-workspace")
	env.DotenvSet(slotEnvVarNameForService("web"), "staging")

	tests := []struct {
		name            string
		serviceConfig   *ServiceConfig
		targetResource  *environment.TargetResource
		previewer       ServiceTargetResourcePreviewer
		wantMessage     []string
		wantOperation   string
		wantDetailKey   string
		wantDetailValue any
		wantFirstDeploy bool
		wantTargetType  azapi.AzureResourceType
	}{
		{
			name: "AppServiceSlot",
			serviceConfig: &ServiceConfig{
				Project: projectConfig, Name: "web", Host: AppServiceTarget, Language: ServiceLanguageJavaScript,
			},
			targetResource:  targetResource("web-app", azapi.AzureResourceTypeWebSite),
			previewer:       &appServiceTarget{env: env},
			wantMessage:     []string{"Service web", "deployment slot staging", "web-app", "resource-group"},
			wantOperation:   "deploy application content",
			wantDetailKey:   "slot",
			wantDetailValue: "staging",
			wantTargetType:  azapi.AzureResourceTypeWebSite,
		},
		{
			name: "ContainerApp",
			serviceConfig: &ServiceConfig{
				Project: projectConfig, Name: "api", Host: ContainerAppTarget, Language: ServiceLanguageDocker,
			},
			targetResource: targetResource("container-app", azapi.AzureResourceTypeContainerApp),
			previewer:      &containerAppTarget{},
			wantMessage:    []string{"Service api", "create a new revision", "container-app"},
			wantOperation:  "publish a container image and create a new revision",
			wantTargetType: azapi.AzureResourceTypeContainerApp,
		},
		{
			name: "ContainerAppFirstDeployment",
			serviceConfig: &ServiceConfig{
				Project: projectConfig, Name: "api", Host: ContainerAppTarget, Language: ServiceLanguageDocker,
			},
			targetResource: environment.NewTargetResource(
				"subscription-id",
				"resource-group",
				"",
				string(azapi.AzureResourceTypeContainerApp),
			),
			previewer:       &containerAppTarget{},
			wantMessage:     []string{"new Azure Container App", "first deployment"},
			wantOperation:   "provision the target and deploy a container image",
			wantFirstDeploy: true,
			wantTargetType:  azapi.AzureResourceTypeContainerApp,
		},
		{
			name: "FunctionContainer",
			serviceConfig: &ServiceConfig{
				Project: projectConfig, Name: "functions", Host: AzureFunctionTarget, Language: ServiceLanguageDocker,
			},
			targetResource: targetResource("function-app", azapi.AzureResourceTypeWebSite),
			previewer:      &functionAppTarget{},
			wantMessage:    []string{"Service functions", "publish a container image", "function-app"},
			wantOperation:  "publish a container image and update the app",
			wantTargetType: azapi.AzureResourceTypeWebSite,
		},
		{
			name: "StaticWebApp",
			serviceConfig: &ServiceConfig{
				Project: projectConfig, Name: "site", Host: StaticWebAppTarget,
			},
			targetResource:  targetResource("static-site", azapi.AzureResourceTypeStaticWebSite),
			previewer:       &staticWebAppTarget{},
			wantMessage:     []string{"Service site", "production environment", "static-site"},
			wantOperation:   "deploy built application content",
			wantDetailKey:   "environment",
			wantDetailValue: "production",
			wantTargetType:  azapi.AzureResourceTypeStaticWebSite,
		},
		{
			name: "AksManifests",
			serviceConfig: &ServiceConfig{
				Project:      projectConfig,
				Name:         "api",
				Host:         AksTarget,
				RelativePath: filepath.Join("src", "api"),
				K8s:          AksOptions{Namespace: "apps"},
			},
			targetResource:  targetResource("aks-cluster", azapi.AzureResourceTypeManagedCluster),
			previewer:       &aksTarget{},
			wantMessage:     []string{"Service api", "Kubernetes manifests", "namespace apps", "aks-cluster"},
			wantOperation:   "apply Kubernetes manifests in namespace apps",
			wantDetailKey:   "namespace",
			wantDetailValue: "apps",
			wantTargetType:  azapi.AzureResourceTypeManagedCluster,
		},
		{
			name: "AiEndpoint",
			serviceConfig: &ServiceConfig{
				Project: projectConfig,
				Name:    "model",
				Host:    AiEndpointTarget,
				Config: map[string]any{
					"model":      map[string]any{"name": "model"},
					"deployment": map[string]any{"name": "blue"},
				},
			},
			targetResource: targetResource(
				"/subscriptions/subscription-id/resourceGroups/resource-group/providers/"+
					"Microsoft.MachineLearningServices/workspaces/ai-workspace/onlineEndpoints/model-endpoint",
				azapi.AzureResourceTypeMachineLearningEndpoint,
			),
			previewer:       &aiEndpointTarget{env: env},
			wantMessage:     []string{"Service model", "model and online deployment and traffic", "model-endpoint"},
			wantOperation:   "configure model and online deployment and traffic",
			wantDetailKey:   "workspace",
			wantDetailValue: "ai-workspace",
			wantTargetType:  azapi.AzureResourceTypeMachineLearningEndpoint,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result, err := tt.previewer.PreviewWithTarget(t.Context(), tt.serviceConfig, tt.targetResource)
			require.NoError(t, err)
			for _, want := range tt.wantMessage {
				require.Contains(t, result.Message, want)
			}
			require.Equal(t, tt.wantOperation, result.Data["operation"])
			require.Equal(t, tt.serviceConfig.Name, result.Data["name"])
			require.Equal(t, tt.serviceConfig.Host, result.Data["host"])
			if tt.wantFirstDeploy {
				require.Equal(t, true, result.Data["firstDeployment"])
			} else {
				require.NotContains(t, result.Data, "firstDeployment")
			}
			if tt.wantDetailKey != "" {
				require.Equal(t, tt.wantDetailValue, result.Data[tt.wantDetailKey])
			}

			target, ok := result.Data["target"].(map[string]any)
			require.True(t, ok)
			require.Equal(t, string(tt.wantTargetType), target["resourceType"])
			require.Equal(t, "resource-group", target["resourceGroup"])
		})
	}
}

func targetResource(name string, resourceType azapi.AzureResourceType) *environment.TargetResource {
	return environment.NewTargetResource(
		"subscription-id",
		"resource-group",
		name,
		string(resourceType),
	)
}
