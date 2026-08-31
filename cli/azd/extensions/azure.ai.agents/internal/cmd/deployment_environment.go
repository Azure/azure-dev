// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

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
		return validateDeploymentCapacity(capacity)
	case int32:
		return validateDeploymentCapacity(int(capacity))
	case int64:
		maxInt := int64(^uint(0) >> 1)
		minInt := -maxInt - 1
		if capacity > maxInt || capacity < minInt {
			return 0, fmt.Errorf("capacity %d exceeds the supported integer range", capacity)
		}
		return validateDeploymentCapacity(int(capacity))
	case uint64:
		if capacity > uint64(^uint(0)>>1) {
			return 0, fmt.Errorf("capacity %d exceeds the supported integer range", capacity)
		}
		return validateDeploymentCapacity(int(capacity))
	case float64:
		maxInt := float64(^uint(0) >> 1)
		minInt := -maxInt - 1
		if capacity != math.Trunc(capacity) ||
			capacity > maxInt || capacity < minInt {
			return 0, fmt.Errorf("capacity %v must be an integer", capacity)
		}
		return validateDeploymentCapacity(int(capacity))
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(capacity))
		if err != nil {
			return 0, fmt.Errorf("capacity %q must be an integer", capacity)
		}
		return validateDeploymentCapacity(parsed)
	default:
		return 0, fmt.Errorf("capacity has unsupported type %T", value)
	}
}

func validateDeploymentCapacity(capacity int) (int, error) {
	if capacity <= 0 {
		return 0, fmt.Errorf("capacity must be a positive integer")
	}
	return capacity, nil
}

func canonicalDeploymentEnvironmentKeyIndex(key string) (int, bool) {
	bases := [...]string{
		deploymentNameEnvKey,
		modelNameEnvKey,
		modelFormatEnvKey,
		modelVersionEnvKey,
		modelSkuNameEnvKey,
		modelCapacityEnvKey,
	}
	for _, base := range bases {
		if key == base {
			return 0, true
		}
		suffix, ok := strings.CutPrefix(key, base+"_")
		if !ok || suffix == "" || suffix[0] == '0' {
			continue
		}
		index, err := strconv.Atoi(suffix)
		if err == nil && index >= 2 {
			return index - 1, true
		}
	}
	return 0, false
}

func canonicalDeploymentIndex(
	deployment project.Deployment,
) (int, bool, error) {
	values := []string{
		deployment.Name,
		deployment.Model.Name,
		deployment.Model.Format,
		deployment.Model.Version,
		deployment.Sku.Name,
	}
	if capacity, ok := deployment.Sku.Capacity.(string); ok {
		values = append(values, capacity)
	} else {
		values = append(values, "")
	}

	index := -1
	canonicalCount := 0
	hasOtherReference := false
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		match := deploymentEnvironmentReferencePattern.FindStringSubmatch(trimmed)
		if len(match) == 2 {
			candidate, ok := canonicalDeploymentEnvironmentKeyIndex(match[1])
			if !ok {
				hasOtherReference = true
				continue
			}
			canonicalCount++
			if index >= 0 && index != candidate {
				return 0, false, fmt.Errorf(
					"canonical environment references use mismatched suffixes",
				)
			}
			index = candidate
			continue
		}
		if strings.Contains(trimmed, "${") {
			hasOtherReference = true
		}
	}
	if canonicalCount == 0 {
		return 0, false, nil
	}
	if hasOtherReference || canonicalCount != len(values) {
		return 0, false, fmt.Errorf(
			"deployment must use one complete canonical environment tuple",
		)
	}
	return index, true, nil
}

func persistDeploymentEnvironment(
	ctx context.Context,
	setEnv envValueSetter,
	deployments []project.Deployment,
) ([]project.Deployment, error) {
	references := make([]project.Deployment, len(deployments))
	reserved := map[int]bool{}
	canonical := make([]bool, len(deployments))
	for i, deployment := range deployments {
		index, isCanonical, err := canonicalDeploymentIndex(deployment)
		if err != nil {
			return nil, fmt.Errorf("deployment %d: %w", i+1, err)
		}
		if isCanonical {
			reserved[index] = true
			canonical[i] = true
		}
	}

	nextIndex := 0
	for i, deployment := range deployments {
		if canonical[i] {
			references[i] = deployment
			continue
		}
		if deploymentUsesEnvironmentReferences(deployment) {
			references[i] = deployment
			continue
		}
		for reserved[nextIndex] {
			nextIndex++
		}
		index := nextIndex
		reserved[index] = true
		nextIndex++
		keys := deploymentKeys(index)
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

func persistDeploymentConfigurations(
	ctx context.Context,
	setEnv envValueSetter,
	deploymentReferences []project.Deployment,
	managedIndices []int,
	fallbackDeployments []project.Deployment,
) ([]project.Deployment, error) {
	deployments := deploymentReferences
	if len(deployments) == 0 {
		deployments = fallbackDeployments
	}
	references, err := persistDeploymentEnvironment(ctx, setEnv, deployments)
	if err != nil {
		return nil, err
	}
	if len(deploymentReferences) == 0 {
		return references, nil
	}

	managed := make([]project.Deployment, 0, len(managedIndices))
	for _, index := range managedIndices {
		if index < 0 || index >= len(references) {
			return nil, fmt.Errorf("deployment reference index %d is out of range", index)
		}
		managed = append(managed, references[index])
	}
	return managed, nil
}

func deploymentUsesEnvironmentReferences(deployment project.Deployment) bool {
	values := []string{
		deployment.Name,
		deployment.Model.Name,
		deployment.Model.Format,
		deployment.Model.Version,
		deployment.Sku.Name,
	}
	if capacity, ok := deployment.Sku.Capacity.(string); ok {
		values = append(values, capacity)
	}
	for _, value := range values {
		if strings.Contains(value, "${") {
			return true
		}
	}
	return false
}
