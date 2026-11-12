// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/ai"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

func (st *appServiceTarget) PreviewWithTarget(
	_ context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) (*ServiceDeployPreviewResult, error) {
	if err := st.validateTargetResource(targetResource); err != nil {
		return nil, fmt.Errorf("validating target resource: %w", err)
	}

	operation := "deploy application content"
	if containerConfigured(serviceConfig) {
		operation = "publish a container image and update the app"
	}

	details := map[string]any{}
	targetDescription := "Azure Web App"
	slotName := st.env.Getenv(slotEnvVarNameForService(serviceConfig.Name))
	if slotName != "" && !strings.EqualFold(slotName, productionSlotName) {
		targetDescription = fmt.Sprintf(
			"deployment slot %s on Azure Web App",
			output.WithHighLightFormat(slotName),
		)
		details["slot"] = slotName
	}

	return newServiceTargetPreview(serviceConfig, targetResource, operation, "", targetDescription, details), nil
}

func (f *functionAppTarget) PreviewWithTarget(
	_ context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) (*ServiceDeployPreviewResult, error) {
	if err := f.validateTargetResource(targetResource); err != nil {
		return nil, fmt.Errorf("validating target resource: %w", err)
	}
	if err := validateFunctionAppContainerConfig(serviceConfig); err != nil {
		return nil, err
	}

	operation := "deploy application content"
	if containerConfigured(serviceConfig) {
		operation = "publish a container image and update the app"
	}

	return newServiceTargetPreview(serviceConfig, targetResource, operation, "", "Azure Function App", nil), nil
}

func (at *containerAppTarget) PreviewWithTarget(
	_ context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) (*ServiceDeployPreviewResult, error) {
	if targetResource.ResourceName() == "" {
		return newServiceTargetPreview(
			serviceConfig,
			targetResource,
			"provision the target and deploy a container image",
			"",
			"new Azure Container App",
			map[string]any{"firstDeployment": true},
		), nil
	}

	if err := at.validateTargetResource(targetResource); err != nil {
		return nil, fmt.Errorf("validating target resource: %w", err)
	}

	operation := "publish a container image and create a new revision"
	targetDescription := "Azure Container App"
	if strings.EqualFold(targetResource.ResourceType(), string(azapi.AzureResourceTypeContainerAppJob)) {
		operation = "publish a container image and update the job"
		targetDescription = "Azure Container Apps job"
	}

	return newServiceTargetPreview(serviceConfig, targetResource, operation, "", targetDescription, nil), nil
}

func (at *staticWebAppTarget) PreviewWithTarget(
	_ context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) (*ServiceDeployPreviewResult, error) {
	if err := at.validateTargetResource(targetResource); err != nil {
		return nil, fmt.Errorf("validating target resource: %w", err)
	}

	return newServiceTargetPreview(
		serviceConfig,
		targetResource,
		"deploy built application content",
		"",
		"production environment of Azure Static Web App",
		map[string]any{"environment": "production"},
	), nil
}

func (t *aksTarget) PreviewWithTarget(
	_ context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) (*ServiceDeployPreviewResult, error) {
	if err := t.validateTargetResource(targetResource); err != nil {
		return nil, fmt.Errorf("validating target resource: %w", err)
	}

	mechanisms := make([]string, 0, 3)
	if serviceConfig.K8s.Helm != nil {
		mechanisms = append(mechanisms, "Helm releases")
	}
	if serviceConfig.K8s.Kustomize != nil {
		mechanisms = append(mechanisms, "Kustomize resources")
	}

	deploymentPath := serviceConfig.K8s.DeploymentPath
	if deploymentPath == "" {
		deploymentPath = defaultDeploymentPath
	}
	if _, err := os.Stat(filepath.Join(serviceConfig.Path(), deploymentPath)); err == nil {
		mechanisms = append(mechanisms, "Kubernetes manifests")
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("checking Kubernetes deployment path: %w", err)
	}

	if len(mechanisms) == 0 {
		return nil, errors.New("no deployment manifests found")
	}

	namespace := t.getK8sNamespace(serviceConfig)
	details := map[string]any{
		"namespace":  namespace,
		"mechanisms": mechanisms,
	}
	operation := fmt.Sprintf("apply %s in namespace %s", joinPreviewItems(mechanisms), namespace)
	messageOperation := fmt.Sprintf(
		"apply %s in namespace %s",
		joinPreviewItems(mechanisms),
		output.WithHighLightFormat(namespace),
	)

	return newServiceTargetPreview(
		serviceConfig,
		targetResource,
		operation,
		messageOperation,
		"Azure Kubernetes Service cluster",
		details,
	), nil
}

func (m *aiEndpointTarget) PreviewWithTarget(
	_ context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) (*ServiceDeployPreviewResult, error) {
	if err := checkResourceType(targetResource, azapi.AzureResourceTypeMachineLearningEndpoint); err != nil {
		return nil, fmt.Errorf("validating target resource: %w", err)
	}

	endpointConfig, err := ai.ParseConfig[ai.EndpointDeploymentConfig](serviceConfig.Config)
	if err != nil {
		return nil, err
	}
	workspaceScope, err := m.getWorkspaceScope(serviceConfig, targetResource)
	if err != nil {
		return nil, err
	}

	components := make([]string, 0, 4)
	if endpointConfig.Flow != nil {
		components = append(components, "prompt flow")
	}
	if endpointConfig.Environment != nil {
		components = append(components, "environment")
	}
	if endpointConfig.Model != nil {
		components = append(components, "model")
	}

	details := map[string]any{"workspace": workspaceScope.Workspace()}
	if endpointConfig.Deployment != nil {
		components = append(components, "online deployment and traffic")
		deploymentName, err := endpointConfig.Deployment.Name.Envsubst(m.env.Getenv)
		if err != nil {
			return nil, fmt.Errorf("expanding deployment name: %w", err)
		}
		if deploymentName != "" {
			details["deploymentName"] = deploymentName
		}
	}
	if len(components) == 0 {
		return nil, errors.New("AI endpoint deployment configuration has no components")
	}
	details["components"] = components

	operation := fmt.Sprintf("configure %s", joinPreviewItems(components))
	endpointTarget := environment.NewTargetResource(
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		filepath.Base(targetResource.ResourceName()),
		targetResource.ResourceType(),
	)
	return newServiceTargetPreview(
		serviceConfig,
		endpointTarget,
		operation,
		"",
		"Azure Machine Learning online endpoint",
		details,
	), nil
}

func newServiceTargetPreview(
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
	operation string,
	messageOperation string,
	targetDescription string,
	details map[string]any,
) *ServiceDeployPreviewResult {
	target := map[string]any{
		"subscriptionId": targetResource.SubscriptionId(),
		"resourceGroup":  targetResource.ResourceGroupName(),
		"resourceType":   targetResource.ResourceType(),
		"resourceName":   targetResource.ResourceName(),
	}
	data := map[string]any{
		"name":      serviceConfig.Name,
		"host":      serviceConfig.Host,
		"operation": operation,
		"target":    target,
	}
	maps.Copy(data, details)

	targetName := ""
	if targetResource.ResourceName() != "" {
		targetName = " " + output.WithHighLightFormat(targetResource.ResourceName())
	}
	if messageOperation == "" {
		messageOperation = operation
	}
	message := fmt.Sprintf(
		"Service %s will %s to the %s%s in resource group %s.",
		output.WithHighLightFormat(serviceConfig.Name),
		messageOperation,
		targetDescription,
		targetName,
		output.WithHighLightFormat(targetResource.ResourceGroupName()),
	)
	if firstDeployment, ok := data["firstDeployment"].(bool); ok && firstDeployment {
		message += " This will be the first deployment."
	}

	return &ServiceDeployPreviewResult{Message: message, Data: data}
}

func joinPreviewItems(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	default:
		return strings.Join(items[:len(items)-1], ", ") + ", and " + items[len(items)-1]
	}
}
