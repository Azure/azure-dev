// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"regexp"
	"strconv"

	"azureaiagent/internal/project"
)

var deploymentEnvironmentReferencePattern = regexp.MustCompile(`^\$\{[A-Za-z_][A-Za-z0-9_]*\}$`)

const (
	deploymentNameEnvKey = "AZURE_AI_MODEL_DEPLOYMENT_NAME"
	modelNameEnvKey      = "AZURE_AI_MODEL_NAME"
	modelFormatEnvKey    = "AZURE_AI_MODEL_FORMAT"
	modelVersionEnvKey   = "AZURE_AI_MODEL_VERSION"
	modelSkuNameEnvKey   = "AZURE_AI_MODEL_SKU_NAME"
	modelCapacityEnvKey  = "AZURE_AI_MODEL_SKU_CAPACITY"
)

type deploymentEnvironmentKeys struct {
	deploymentName string
	modelName      string
	modelFormat    string
	modelVersion   string
	skuName        string
	capacity       string
}

func deploymentKeys(index int) deploymentEnvironmentKeys {
	return deploymentEnvironmentKeys{
		deploymentName: indexedDeploymentKey(deploymentNameEnvKey, index),
		modelName:      indexedDeploymentKey(modelNameEnvKey, index),
		modelFormat:    indexedDeploymentKey(modelFormatEnvKey, index),
		modelVersion:   indexedDeploymentKey(modelVersionEnvKey, index),
		skuName:        indexedDeploymentKey(modelSkuNameEnvKey, index),
		capacity:       indexedDeploymentKey(modelCapacityEnvKey, index),
	}
}

func indexedDeploymentKey(base string, index int) string {
	if index == 0 {
		return base
	}
	return fmt.Sprintf("%s_%d", base, index+1)
}

func deploymentCapacity(value any) (int, error) {
	switch capacity := value.(type) {
	case int:
		return capacity, nil
	case int32:
		return int(capacity), nil
	case int64:
		return int(capacity), nil
	case float64:
		if capacity != float64(int(capacity)) {
			return 0, fmt.Errorf("capacity %v must be an integer", capacity)
		}
		return int(capacity), nil
	case string:
		parsed, err := strconv.Atoi(capacity)
		if err != nil {
			return 0, fmt.Errorf("capacity %q must be an integer", capacity)
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("capacity has unsupported type %T", value)
	}
}

func persistDeploymentEnvironment(
	ctx context.Context,
	setEnv envValueSetter,
	deployments []project.Deployment,
) ([]project.Deployment, error) {
	references := make([]project.Deployment, len(deployments))
	for i, deployment := range deployments {
		if deploymentUsesEnvironmentReferences(deployment) {
			references[i] = deployment
			continue
		}
		keys := deploymentKeys(i)
		capacity, err := deploymentCapacity(deployment.Sku.Capacity)
		if err != nil {
			return nil, fmt.Errorf("deployment %d: %w", i+1, err)
		}
		values := []struct {
			key   string
			value string
		}{
			{keys.deploymentName, deployment.Name},
			{keys.modelName, deployment.Model.Name},
			{keys.modelFormat, deployment.Model.Format},
			{keys.modelVersion, deployment.Model.Version},
			{keys.skuName, deployment.Sku.Name},
			{keys.capacity, strconv.Itoa(capacity)},
		}
		for _, value := range values {
			if err := setEnv(ctx, value.key, value.value); err != nil {
				return nil, fmt.Errorf("set %s: %w", value.key, err)
			}
		}

		references[i] = project.Deployment{
			Name: fmt.Sprintf("${%s}", keys.deploymentName),
			Model: project.DeploymentModel{
				Name:    fmt.Sprintf("${%s}", keys.modelName),
				Format:  fmt.Sprintf("${%s}", keys.modelFormat),
				Version: fmt.Sprintf("${%s}", keys.modelVersion),
			},
			Sku: project.DeploymentSku{
				Name:     fmt.Sprintf("${%s}", keys.skuName),
				Capacity: fmt.Sprintf("${%s}", keys.capacity),
			},
		}
	}
	return references, nil
}

func deploymentUsesEnvironmentReferences(deployment project.Deployment) bool {
	capacity, ok := deployment.Sku.Capacity.(string)
	values := []string{
		deployment.Name,
		deployment.Model.Name,
		deployment.Model.Format,
		deployment.Model.Version,
		deployment.Sku.Name,
	}
	if ok {
		values = append(values, capacity)
	}
	for _, value := range values {
		if !deploymentEnvironmentReferencePattern.MatchString(value) {
			continue
		}
		return true
	}
	return false
}
