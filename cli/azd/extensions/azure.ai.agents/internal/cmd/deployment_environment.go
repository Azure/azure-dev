// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"gopkg.in/yaml.v3"
)

var deploymentEnvironmentReferencePattern = regexp.MustCompile(
	`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`,
)

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

type persistedDeploymentConfigurations struct {
	managed       []project.Deployment
	references    []project.Deployment
	allReferences []project.Deployment
}

func environmentName(environment *azdext.Environment) string {
	if environment == nil {
		return ""
	}
	return environment.Name
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
	return persistDeploymentEnvironmentWithReserved(
		ctx,
		setEnv,
		deployments,
		nil,
	)
}

func persistDeploymentEnvironmentWithReserved(
	ctx context.Context,
	setEnv envValueSetter,
	deployments []project.Deployment,
	reservedDeployments []project.Deployment,
) ([]project.Deployment, error) {
	references := make([]project.Deployment, len(deployments))
	reserved := map[int]bool{}
	canonical := make([]bool, len(deployments))
	for i, deployment := range reservedDeployments {
		index, isCanonical, err := canonicalDeploymentIndex(deployment)
		if err != nil {
			return nil, fmt.Errorf("existing deployment %d: %w", i+1, err)
		}
		if isCanonical {
			reserved[index] = true
		}
	}
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
	reservedDeployments []project.Deployment,
) (persistedDeploymentConfigurations, error) {
	deployments := deploymentReferences
	if len(deployments) == 0 {
		deployments = fallbackDeployments
	}
	references, err := persistDeploymentEnvironmentWithReserved(
		ctx,
		setEnv,
		deployments,
		reservedDeployments,
	)
	if err != nil {
		return persistedDeploymentConfigurations{}, err
	}

	managedByIndex := make(map[int]bool, len(deployments))
	if len(deploymentReferences) == 0 {
		for index := range deployments {
			managedByIndex[index] = true
		}
	}
	for _, index := range managedIndices {
		if index < 0 || index >= len(references) {
			return persistedDeploymentConfigurations{},
				fmt.Errorf("deployment reference index %d is out of range", index)
		}
		managedByIndex[index] = true
	}

	managed := make([]project.Deployment, 0, len(managedByIndex))
	userManagedReferences := make(
		[]project.Deployment,
		0,
		len(references)-len(managedByIndex),
	)
	for index, reference := range references {
		if managedByIndex[index] {
			managed = append(managed, reference)
			continue
		}
		userManagedReferences = append(userManagedReferences, reference)
	}
	return persistedDeploymentConfigurations{
		managed:       managed,
		references:    userManagedReferences,
		allReferences: references,
	}, nil
}

func canonicalDeploymentReferenceReplacer(
	deployments []project.Deployment,
) (*strings.Replacer, error) {
	replacements := map[string]string{}
	for sourceIndex, deployment := range deployments {
		targetIndex, canonical, err := canonicalDeploymentIndex(deployment)
		if err != nil {
			return nil, fmt.Errorf(
				"deployment %d: %w",
				sourceIndex+1,
				err,
			)
		}
		if !canonical || sourceIndex == targetIndex {
			continue
		}

		sourceKeys := deploymentKeys(sourceIndex)
		targetKeys := deploymentKeys(targetIndex)
		for _, keys := range [][2]string{
			{sourceKeys.deploymentName, targetKeys.deploymentName},
			{sourceKeys.modelName, targetKeys.modelName},
			{sourceKeys.modelFormat, targetKeys.modelFormat},
			{sourceKeys.modelVersion, targetKeys.modelVersion},
			{sourceKeys.skuName, targetKeys.skuName},
			{sourceKeys.capacity, targetKeys.capacity},
		} {
			replacements[fmt.Sprintf("${%s}", keys[0])] =
				fmt.Sprintf("${%s}", keys[1])
		}
	}
	if len(replacements) == 0 {
		return nil, nil
	}

	keys := slices.Sorted(maps.Keys(replacements))
	arguments := make([]string, 0, len(keys)*2)
	for _, key := range keys {
		arguments = append(arguments, key, replacements[key])
	}
	return strings.NewReplacer(arguments...), nil
}

func rewriteManifestDeploymentReferences(
	manifest *agent_yaml.AgentManifest,
	deployments []project.Deployment,
) error {
	replacer, err := canonicalDeploymentReferenceReplacer(deployments)
	if err != nil {
		return err
	}
	if replacer == nil {
		return nil
	}

	data, err := yaml.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("marshal agent manifest: %w", err)
	}
	updated, err := agent_yaml.LoadAndValidateAgentManifest(
		[]byte(replacer.Replace(string(data))),
	)
	if err != nil {
		return fmt.Errorf("reload agent manifest: %w", err)
	}
	*manifest = *updated
	return nil
}

func rewriteContainerDeploymentReferences(
	definition *agent_yaml.ContainerAgent,
	deployments []project.Deployment,
) error {
	if definition == nil {
		return fmt.Errorf("agent definition is required")
	}
	replacer, err := canonicalDeploymentReferenceReplacer(deployments)
	if err != nil {
		return err
	}
	if replacer == nil || definition.EnvironmentVariables == nil {
		return nil
	}
	for index := range *definition.EnvironmentVariables {
		(*definition.EnvironmentVariables)[index].Value = replacer.Replace(
			(*definition.EnvironmentVariables)[index].Value,
		)
	}
	return nil
}

func persistProjectDeploymentConfigurations(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	envName string,
	setEnv envValueSetter,
	deploymentReferences []project.Deployment,
	managedIndices []int,
	fallbackDeployments []project.Deployment,
) (persistedDeploymentConfigurations, error) {
	existing, err := loadExistingProjectDeploymentConfig(
		ctx,
		azdClient,
	)
	if err != nil {
		return persistedDeploymentConfigurations{}, err
	}
	deployments := deploymentReferences
	if len(deployments) == 0 && len(fallbackDeployments) > 0 {
		deployments = slices.Clone(fallbackDeployments)
		managedIndices = make([]int, len(deployments))
		for index := range deployments {
			managedIndices[index] = index
		}
		fallbackDeployments = nil
	}
	var reservedDeployments []project.Deployment
	if existing != nil {
		reservedDeployments = append(
			slices.Clone(existing.deployments),
			existing.deploymentReferences...,
		)
		deployments, err = reuseProjectDeploymentReferences(
			ctx,
			azdClient,
			envName,
			deployments,
			managedIndices,
			existing.deployments,
			existing.deploymentReferences,
		)
		if err != nil {
			return persistedDeploymentConfigurations{}, err
		}
	}
	return persistDeploymentConfigurations(
		ctx,
		setEnv,
		deployments,
		managedIndices,
		fallbackDeployments,
		reservedDeployments,
	)
}

// Reuse canonical references for the same user-managed deployment.
func reuseProjectDeploymentReferences(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	envName string,
	incoming []project.Deployment,
	managedIndices []int,
	existingManaged []project.Deployment,
	existingReferences []project.Deployment,
) ([]project.Deployment, error) {
	if len(incoming) == 0 {
		return incoming, nil
	}
	hasCanonical := false
	for _, deployments := range [][]project.Deployment{
		existingManaged,
		existingReferences,
	} {
		for _, deployment := range deployments {
			_, canonical, err := canonicalDeploymentIndex(deployment)
			if err != nil {
				return nil, fmt.Errorf("existing deployment: %w", err)
			}
			hasCanonical = hasCanonical || canonical
		}
	}
	if !hasCanonical {
		return incoming, nil
	}

	response, err := azdClient.Environment().GetValues(
		ctx,
		&azdext.GetEnvironmentRequest{Name: envName},
	)
	if err != nil {
		return nil, fmt.Errorf(
			"reading azd environment %q deployment values: %w",
			envName,
			err,
		)
	}
	env := make(map[string]string, len(response.GetKeyValues()))
	for _, value := range response.GetKeyValues() {
		if value != nil {
			env[value.Key] = value.Value
		}
	}

	reusableManaged := map[string]project.Deployment{}
	reusableReferences := map[string]project.Deployment{}
	addReusable := func(
		deployments []project.Deployment,
		managed bool,
	) error {
		for _, deployment := range deployments {
			index, isCanonical, err := canonicalDeploymentIndex(deployment)
			if err != nil {
				return fmt.Errorf("existing deployment: %w", err)
			}
			if !isCanonical {
				continue
			}
			resolved, ok := resolveCanonicalDeployment(index, env)
			if !ok {
				continue
			}
			identity, ok, err := concreteDeploymentIdentity(resolved)
			if err != nil {
				return fmt.Errorf(
					"existing deployment %q: %w",
					indexedDeploymentKey(deploymentNameEnvKey, index),
					err,
				)
			}
			if !ok {
				continue
			}
			if managed {
				reusableManaged[identity] = deployment
				reusableReferences[identity] = deployment
			} else if _, found := reusableReferences[identity]; !found {
				reusableReferences[identity] = deployment
			}
		}
		return nil
	}
	if err := addReusable(existingManaged, true); err != nil {
		return nil, err
	}
	if err := addReusable(existingReferences, false); err != nil {
		return nil, err
	}
	if len(reusableManaged) == 0 && len(reusableReferences) == 0 {
		return incoming, nil
	}

	managed := map[int]bool{}
	for _, index := range managedIndices {
		managed[index] = true
	}
	references := slices.Clone(incoming)
	for index, deployment := range references {
		identity, ok, err := concreteDeploymentIdentity(deployment)
		if err != nil {
			return nil, fmt.Errorf("deployment %d: %w", index+1, err)
		}
		if !ok {
			continue
		}
		reusable := reusableReferences
		if managed[index] {
			reusable = reusableManaged
		}
		if reference, found := reusable[identity]; found {
			references[index] = reference
		}
	}
	return references, nil
}

func resolveCanonicalDeployment(
	index int,
	env map[string]string,
) (project.Deployment, bool) {
	keys := deploymentKeys(index)
	values := []string{
		env[keys.deploymentName],
		env[keys.modelName],
		env[keys.modelFormat],
		env[keys.modelVersion],
		env[keys.skuName],
		env[keys.capacity],
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return project.Deployment{}, false
		}
	}
	capacity, err := deploymentCapacity(values[5])
	if err != nil {
		return project.Deployment{}, false
	}
	return project.Deployment{
		Name: values[0],
		Model: project.DeploymentModel{
			Name:    values[1],
			Format:  values[2],
			Version: values[3],
		},
		Sku: project.DeploymentSku{
			Name:     values[4],
			Capacity: capacity,
		},
	}, true
}

func concreteDeploymentIdentity(
	deployment project.Deployment,
) (string, bool, error) {
	if deploymentUsesEnvironmentReferences(deployment) {
		return "", false, nil
	}
	values := []string{
		strings.TrimSpace(deployment.Name),
		strings.TrimSpace(deployment.Model.Name),
		strings.TrimSpace(deployment.Model.Format),
		strings.TrimSpace(deployment.Model.Version),
		strings.TrimSpace(deployment.Sku.Name),
	}
	for _, value := range values {
		if value == "" {
			return "", false, nil
		}
	}
	capacity, err := deploymentCapacity(deployment.Sku.Capacity)
	if err != nil {
		return "", false, err
	}
	for index := range values {
		values[index] = strings.ToLower(values[index])
	}
	return fmt.Sprintf(
		"%s\x00%s\x00%s\x00%s\x00%s\x00%d",
		values[0],
		values[1],
		values[2],
		values[3],
		values[4],
		capacity,
	), true, nil
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
