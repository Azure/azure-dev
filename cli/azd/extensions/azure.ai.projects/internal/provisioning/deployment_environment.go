// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"azure.ai.projects/internal/azure"
	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/internal/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/status"
)

const agentsV2ModelCapability = "agentsV2"

var exactEnvironmentReference = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`)

var canonicalDeploymentEnvironmentKeyBases = [...]string{
	"AZURE_AI_MODEL_DEPLOYMENT_NAME",
	"AZURE_AI_MODEL_NAME",
	"AZURE_AI_MODEL_FORMAT",
	"AZURE_AI_MODEL_VERSION",
	"AZURE_AI_MODEL_SKU_NAME",
	"AZURE_AI_MODEL_SKU_CAPACITY",
}

type deploymentReferences struct {
	deploymentName string
	modelName      string
	modelFormat    string
	modelVersion   string
	skuName        string
	capacity       string
}

type deploymentEnvironmentEntry struct {
	deployment synthesis.Deployment
	references deploymentReferences
	managed    bool
}

type deploymentQuotaReservations map[string]float64

type modelDeploymentTarget struct {
	subscriptionID string
	resourceGroup  string
	accountName    string
}

type foundryModelDeployment struct {
	name         string
	modelName    string
	modelFormat  string
	modelVersion string
	skuName      string
	capacity     int32
}

func canonicalDeploymentReferences(
	deployment synthesis.Deployment,
) (deploymentReferences, bool, error) {
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
		match := exactEnvironmentReference.FindStringSubmatch(
			strings.TrimSpace(value),
		)
		if len(match) == 2 {
			candidate, ok := canonicalDeploymentEnvironmentKeyIndex(match[1])
			if !ok {
				hasOtherReference = true
				continue
			}
			canonicalCount++
			if index >= 0 && index != candidate {
				return deploymentReferences{}, false, fmt.Errorf(
					"canonical environment references use mismatched suffixes",
				)
			}
			index = candidate
			continue
		}
		if strings.Contains(strings.TrimSpace(value), "${") {
			hasOtherReference = true
		}
	}
	if canonicalCount == 0 {
		return deploymentReferences{}, false, nil
	}
	if hasOtherReference || canonicalCount != len(values) {
		return deploymentReferences{}, false, fmt.Errorf(
			"deployment must use one complete canonical environment tuple",
		)
	}

	expected := deploymentReferences{
		deploymentName: indexedDeploymentKey(
			"AZURE_AI_MODEL_DEPLOYMENT_NAME", index),
		modelName:    indexedDeploymentKey("AZURE_AI_MODEL_NAME", index),
		modelFormat:  indexedDeploymentKey("AZURE_AI_MODEL_FORMAT", index),
		modelVersion: indexedDeploymentKey("AZURE_AI_MODEL_VERSION", index),
		skuName:      indexedDeploymentKey("AZURE_AI_MODEL_SKU_NAME", index),
		capacity:     indexedDeploymentKey("AZURE_AI_MODEL_SKU_CAPACITY", index),
	}
	return expected, true, nil
}

func indexedDeploymentKey(base string, index int) string {
	if index == 0 {
		return base
	}
	return fmt.Sprintf("%s_%d", base, index+1)
}

func environmentReference(value string) string {
	match := exactEnvironmentReference.FindStringSubmatch(strings.TrimSpace(value))
	if len(match) != 2 {
		return ""
	}
	return match[1]
}

func canonicalDeploymentEnvironmentKeyIndex(key string) (int, bool) {
	for _, base := range canonicalDeploymentEnvironmentKeyBases {
		if key == base {
			return 0, true
		}
		suffix, ok := strings.CutPrefix(key, base+"_")
		if !ok || suffix == "" || suffix[0] == '0' {
			continue
		}
		number, err := strconv.Atoi(suffix)
		if err == nil && number >= 2 {
			return number - 1, true
		}
	}
	return 0, false
}

func isCanonicalDeploymentEnvironmentKey(key string) bool {
	_, ok := canonicalDeploymentEnvironmentKeyIndex(key)
	return ok
}

func (r deploymentReferences) keys() []string {
	return []string{
		r.deploymentName,
		r.modelName,
		r.modelFormat,
		r.modelVersion,
		r.skuName,
		r.capacity,
	}
}

func (p *FoundryProvisioningProvider) reconcileDeploymentEnvironment(
	ctx context.Context,
	rawYAML []byte,
	serviceName string,
) error {
	p.resolvedDeploymentEnv = nil
	configuration, err := synthesis.ProjectDeploymentConfiguration(
		rawYAML,
		serviceName,
		p.projectPath,
	)
	if err != nil {
		return foundrySynthesisError(serviceName, err)
	}
	entries, err := deploymentEnvironmentEntries(configuration)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}

	env, err := p.deploymentEnvironmentMap(ctx)
	if err != nil {
		return err
	}
	if p.virtualEnv == nil {
		p.virtualEnv = map[string]string{}
	}
	p.resolvedDeploymentEnv = map[string]string{}
	for key := range p.virtualEnv {
		if isCanonicalDeploymentEnvironmentKey(key) {
			delete(p.virtualEnv, key)
		}
	}
	for key, value := range env {
		if strings.TrimSpace(value) != "" {
			trimmed := strings.TrimSpace(value)
			p.virtualEnv[key] = trimmed
			if isCanonicalDeploymentEnvironmentKey(key) {
				p.resolvedDeploymentEnv[key] = trimmed
			}
		}
	}
	reservations := deploymentQuotaReservations{}
	for i, entry := range entries {
		deploymentIndex := i + 1
		keys := entry.references.keys()

		missing := make([]string, 0, len(keys))
		for _, key := range keys {
			if strings.TrimSpace(env[key]) == "" {
				missing = append(missing, key)
			}
		}
		issue := deploymentEnvironmentMissing
		if len(missing) == 0 {
			valid, err := p.validateDeploymentEnvironment(
				ctx,
				entry,
				env,
				reservations,
			)
			if err != nil {
				return fmt.Errorf(
					"validate model deployment %d environment: %w",
					deploymentIndex,
					err,
				)
			}
			if valid {
				continue
			}
			missing = keys
			issue = deploymentEnvironmentIncompatible
		}

		var resolved resolvedDeploymentEnvironment
		if entry.managed {
			resolved, err = p.promptDeploymentEnvironment(
				ctx,
				entry.deployment,
				entry.references,
				deploymentIndex,
				issue,
				missing,
				env,
				reservations,
			)
		} else {
			resolved, err = p.promptExistingDeploymentEnvironment(
				ctx,
				entry.references,
				deploymentIndex,
				issue,
				missing,
			)
		}
		if err != nil {
			return err
		}
		if err := p.persistResolvedDeploymentEnvironment(
			ctx,
			deploymentIndex,
			entry.references,
			resolved,
			env,
		); err != nil {
			return err
		}
	}
	return nil
}

func deploymentEnvironmentEntries(
	configuration synthesis.ProjectDeploymentConfigurationResult,
) ([]deploymentEnvironmentEntry, error) {
	entries := make(
		[]deploymentEnvironmentEntry,
		0,
		len(configuration.Deployments)+len(configuration.DeploymentReferences),
	)
	entryByKey := map[string]int{}
	appendEntries := func(deployments []synthesis.Deployment, managed bool) error {
		for _, deployment := range deployments {
			references, canonical, err := canonicalDeploymentReferences(deployment)
			if err != nil {
				return fmt.Errorf(
					"validate model deployment %d references: %w",
					len(entries)+1,
					err,
				)
			}
			if !canonical {
				continue
			}
			if index, found := entryByKey[references.deploymentName]; found {
				entries[index].managed = entries[index].managed || managed
				continue
			}
			entryByKey[references.deploymentName] = len(entries)
			entries = append(entries, deploymentEnvironmentEntry{
				deployment: deployment,
				references: references,
				managed:    managed,
			})
		}
		return nil
	}
	if err := appendEntries(configuration.Deployments, true); err != nil {
		return nil, err
	}
	if err := appendEntries(configuration.DeploymentReferences, false); err != nil {
		return nil, err
	}
	return entries, nil
}

func (p *FoundryProvisioningProvider) persistResolvedDeploymentEnvironment(
	ctx context.Context,
	deploymentIndex int,
	references deploymentReferences,
	resolved resolvedDeploymentEnvironment,
	env map[string]string,
) error {
	values := map[string]string{
		references.deploymentName: resolved.deploymentName,
		references.modelName:      resolved.modelName,
		references.modelFormat:    resolved.modelFormat,
		references.modelVersion:   resolved.modelVersion,
		references.skuName:        resolved.skuName,
		references.capacity:       resolved.capacity,
	}
	for _, key := range references.keys() {
		if err := p.setEnv(ctx, key, values[key]); err != nil {
			return exterrors.Dependency(
				exterrors.CodeEnvironmentValuesFailed,
				fmt.Sprintf("persist deployment %d environment value %s: %s", deploymentIndex, key, err),
				"verify the azd environment is writable, then retry",
			)
		}
		env[key] = values[key]
		if p.virtualEnv == nil {
			p.virtualEnv = map[string]string{}
		}
		p.virtualEnv[key] = values[key]
		p.resolvedDeploymentEnv[key] = values[key]
	}
	return nil
}

// deploymentEnvironmentMap reads the active azd environment without falling
// back to process variables. Canonical deployment tuples must not be satisfied
// by values from another environment or shell.
func (p *FoundryProvisioningProvider) deploymentEnvironmentMap(
	ctx context.Context,
) (map[string]string, error) {
	out := make(map[string]string, len(p.virtualEnv))
	for key, value := range p.virtualEnv {
		if !isCanonicalDeploymentEnvironmentKey(key) {
			out[key] = value
		}
	}
	if p.azdClient == nil || p.azdClient.Environment() == nil {
		return nil, exterrors.Dependency(
			exterrors.CodeAzdClientFailed,
			"read model deployment environment: azd environment client is unavailable",
			"restart azd and retry",
		)
	}

	response, err := p.azdClient.Environment().GetValues(
		ctx,
		&azdext.GetEnvironmentRequest{Name: p.envName},
	)
	if err != nil {
		return nil, exterrors.Dependency(
			exterrors.CodeEnvironmentValuesFailed,
			fmt.Sprintf(
				"read model deployment values from azd environment %q: %s",
				p.envName,
				err,
			),
			"verify the azd environment is accessible, then retry",
		)
	}
	for _, keyValue := range response.GetKeyValues() {
		if keyValue == nil {
			continue
		}
		value := strings.TrimSpace(keyValue.Value)
		if isCanonicalDeploymentEnvironmentKey(keyValue.Key) {
			out[keyValue.Key] = value
		} else if _, planned := out[keyValue.Key]; !planned {
			out[keyValue.Key] = value
		}
	}
	return out, nil
}

type resolvedDeploymentEnvironment struct {
	deploymentName string
	modelName      string
	modelFormat    string
	modelVersion   string
	skuName        string
	capacity       string
}

type deploymentEnvironmentIssue string

const (
	deploymentEnvironmentMissing      deploymentEnvironmentIssue = "missing"
	deploymentEnvironmentIncompatible deploymentEnvironmentIssue = "incompatible"
)

func (p *FoundryProvisioningProvider) promptDeploymentEnvironment(
	ctx context.Context,
	deployment synthesis.Deployment,
	references deploymentReferences,
	deploymentIndex int,
	issue deploymentEnvironmentIssue,
	affectedKeys []string,
	env map[string]string,
	reservations deploymentQuotaReservations,
) (resolvedDeploymentEnvironment, error) {
	location := p.modelDeploymentLocation()
	azureContext := &azdext.AzureContext{
		Scope: &azdext.AzureScope{
			SubscriptionId: p.subID,
			Location:       location,
			TenantId:       p.tenantID,
		},
	}
	defaultModel := env[references.modelName]
	if defaultModel == "" && environmentReference(deployment.Model.Name) == "" {
		defaultModel = deployment.Model.Name
	}

	modelResponse, err := p.azdClient.Prompt().PromptAiModel(ctx, &azdext.PromptAiModelRequest{
		AzureContext: azureContext,
		Filter: &azdext.AiModelFilterOptions{
			Locations:    []string{location},
			Capabilities: []string{agentsV2ModelCapability},
		},
		Quota:        &azdext.QuotaCheckOptions{MinRemainingCapacity: 1},
		DefaultValue: defaultModel,
		SelectOptions: &azdext.SelectOptions{
			Message: "Select a model for this azd environment",
		},
	})
	if err != nil {
		return resolvedDeploymentEnvironment{}, p.deploymentPromptError(
			err,
			deploymentIndex,
			issue,
			affectedKeys,
			"select a compatible model",
		)
	}
	model := modelResponse.GetModel()
	if model == nil || strings.TrimSpace(model.GetName()) == "" {
		return resolvedDeploymentEnvironment{}, exterrors.Internal(
			exterrors.CodeMissingModelDeployment,
			"model selection returned an empty model",
		)
	}

	deploymentResponse, err := p.promptAiDeployment(
		ctx, azureContext, model.GetName(), new(int32(50)))
	if err != nil && hasAiErrorReason(
		err,
		azdext.AiErrorReasonNoValidSkus,
		azdext.AiErrorReasonNoDeploymentMatch,
	) {
		deploymentResponse, err = p.promptAiDeployment(
			ctx, azureContext, model.GetName(), nil)
	}
	if err != nil {
		return resolvedDeploymentEnvironment{}, p.deploymentPromptError(
			err,
			deploymentIndex,
			issue,
			affectedKeys,
			"select a compatible model",
		)
	}
	selected := deploymentResponse.GetDeployment()
	if selected == nil || selected.GetSku() == nil {
		return resolvedDeploymentEnvironment{}, exterrors.Internal(
			exterrors.CodeMissingModelDeployment,
			"model deployment selection returned an empty deployment",
		)
	}
	fitsQuota, err := reserveDeploymentQuota(
		selected,
		selected.GetCapacity(),
		reservations,
	)
	if err != nil {
		return resolvedDeploymentEnvironment{}, fmt.Errorf(
			"reserve model deployment %d quota: %w",
			deploymentIndex,
			err,
		)
	}
	if !fitsQuota {
		return resolvedDeploymentEnvironment{}, exterrors.Dependency(
			exterrors.CodeMissingModelDeployment,
			fmt.Sprintf(
				"model deployment %d exceeds the remaining quota after "+
					"other deployments in this environment",
				deploymentIndex,
			),
			"choose a smaller capacity or remove another managed deployment",
		)
	}

	deploymentName := model.GetName()
	if currentName := strings.TrimSpace(env[references.deploymentName]); currentName != "" {
		deploymentName = currentName
	} else if environmentReference(deployment.Name) == "" &&
		strings.TrimSpace(deployment.Name) != "" {
		deploymentName = deployment.Name
	}
	return resolvedDeploymentEnvironment{
		deploymentName: deploymentName,
		modelName:      selected.GetModelName(),
		modelFormat:    selected.GetFormat(),
		modelVersion:   selected.GetVersion(),
		skuName:        selected.GetSku().GetName(),
		capacity:       strconv.Itoa(int(selected.GetCapacity())),
	}, nil
}

func (p *FoundryProvisioningProvider) promptExistingDeploymentEnvironment(
	ctx context.Context,
	references deploymentReferences,
	deploymentIndex int,
	issue deploymentEnvironmentIssue,
	affectedKeys []string,
) (resolvedDeploymentEnvironment, error) {
	target, found, err := p.modelDeploymentTarget(ctx)
	if err != nil {
		return resolvedDeploymentEnvironment{}, err
	}
	if !found {
		return resolvedDeploymentEnvironment{}, exterrors.Dependency(
			exterrors.CodeMissingModelDeployment,
			fmt.Sprintf(
				"model deployment %d is user-managed and has no target Foundry project",
				deploymentIndex,
			),
			"configure an existing Foundry project endpoint and "+
				"AZURE_AI_PROJECT_ID, then retry",
		)
	}
	deployments, err := p.listFoundryModelDeployments(ctx, target)
	if err != nil {
		return resolvedDeploymentEnvironment{}, err
	}

	type deploymentChoice struct {
		label      string
		deployment foundryModelDeployment
	}
	items := make([]deploymentChoice, 0, len(deployments))
	for _, deployment := range deployments {
		if deployment.name == "" || deployment.modelName == "" ||
			deployment.modelFormat == "" || deployment.modelVersion == "" ||
			deployment.skuName == "" || deployment.capacity <= 0 {
			continue
		}
		items = append(items, deploymentChoice{
			label: fmt.Sprintf(
				"%s (%s v%s, %s)",
				deployment.name,
				deployment.modelName,
				deployment.modelVersion,
				deployment.skuName,
			),
			deployment: deployment,
		})
	}
	if len(items) == 0 {
		return resolvedDeploymentEnvironment{}, exterrors.Dependency(
			exterrors.CodeMissingModelDeployment,
			fmt.Sprintf(
				"target Foundry project has no selectable existing model "+
					"deployments for deployment %d",
				deploymentIndex,
			),
			"create a compatible deployment in the target Foundry project, "+
				"then retry",
		)
	}
	slices.SortFunc(items, func(a, b deploymentChoice) int {
		return strings.Compare(a.label, b.label)
	})
	choices := make([]*azdext.SelectChoice, len(items))
	for i, item := range items {
		choices[i] = &azdext.SelectChoice{
			Label: item.label,
			Value: item.deployment.name,
		}
	}
	defaultIndex := int32(0)
	response, err := p.azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:       "Select an existing model deployment for this azd environment",
			Choices:       choices,
			SelectedIndex: &defaultIndex,
		},
	})
	if err != nil {
		return resolvedDeploymentEnvironment{}, p.deploymentPromptError(
			err,
			deploymentIndex,
			issue,
			affectedKeys,
			"select an existing model deployment",
		)
	}
	if response == nil || response.Value == nil ||
		*response.Value < 0 || int(*response.Value) >= len(items) {
		return resolvedDeploymentEnvironment{}, exterrors.Internal(
			exterrors.CodeMissingModelDeployment,
			"existing model deployment selection returned an invalid value",
		)
	}
	selected := items[*response.Value].deployment
	return resolvedDeploymentEnvironment{
		deploymentName: selected.name,
		modelName:      selected.modelName,
		modelFormat:    selected.modelFormat,
		modelVersion:   selected.modelVersion,
		skuName:        selected.skuName,
		capacity:       strconv.Itoa(int(selected.capacity)),
	}, nil
}

func (p *FoundryProvisioningProvider) promptAiDeployment(
	ctx context.Context,
	azureContext *azdext.AzureContext,
	modelName string,
	capacity *int32,
) (*azdext.PromptAiDeploymentResponse, error) {
	location := p.modelDeploymentLocation()
	return p.azdClient.Prompt().PromptAiDeployment(
		ctx,
		&azdext.PromptAiDeploymentRequest{
			AzureContext: azureContext,
			ModelName:    modelName,
			Options: &azdext.AiModelDeploymentOptions{
				Locations: []string{location},
				Capacity:  capacity,
			},
			Quota: &azdext.QuotaCheckOptions{MinRemainingCapacity: 1},
		},
	)
}

func (p *FoundryProvisioningProvider) validateDeploymentEnvironment(
	ctx context.Context,
	entry deploymentEnvironmentEntry,
	env map[string]string,
	reservations deploymentQuotaReservations,
) (bool, error) {
	references := entry.references
	deploymentName := strings.TrimSpace(env[references.deploymentName])
	modelName := strings.TrimSpace(env[references.modelName])
	modelFormat := strings.TrimSpace(env[references.modelFormat])
	modelVersion := strings.TrimSpace(env[references.modelVersion])
	skuName := strings.TrimSpace(env[references.skuName])
	capacityValue, err := strconv.ParseInt(strings.TrimSpace(
		env[references.capacity]), 10, 32)
	if err != nil || deploymentName == "" || modelName == "" || modelFormat == "" ||
		modelVersion == "" || skuName == "" || capacityValue <= 0 {
		return false, nil
	}
	capacity := int32(capacityValue)

	configuration := modelDeploymentConfiguration{
		name:         deploymentName,
		modelName:    modelName,
		modelFormat:  modelFormat,
		modelVersion: modelVersion,
		skuName:      skuName,
		capacity:     capacity,
	}
	target, hasTarget, err := p.modelDeploymentTarget(ctx)
	if err != nil {
		return false, err
	}
	if hasTarget {
		deployments, err := p.listFoundryModelDeployments(ctx, target)
		if err != nil {
			return false, err
		}
		if existing, found := findFoundryModelDeployment(
			deployments,
			configuration.name,
		); found {
			if modelDeploymentMatches(existing, configuration) {
				return true, nil
			}
			if !entry.managed {
				return false, nil
			}
			if sameModelDeploymentConfiguration(existing, configuration) {
				additionalCapacity := configuration.capacity - existing.capacity
				if additionalCapacity <= 0 {
					return true, nil
				}
				return p.modelDeploymentFitsQuota(
					ctx,
					configuration,
					additionalCapacity,
					reservations,
				)
			}
		} else if !entry.managed {
			return false, nil
		}
	} else if !entry.managed {
		return false, nil
	}

	return p.modelDeploymentFitsQuota(
		ctx,
		configuration,
		configuration.capacity,
		reservations,
	)
}

type modelDeploymentConfiguration struct {
	name         string
	modelName    string
	modelFormat  string
	modelVersion string
	skuName      string
	capacity     int32
}

func (p *FoundryProvisioningProvider) modelDeploymentFitsQuota(
	ctx context.Context,
	configuration modelDeploymentConfiguration,
	capacity int32,
	reservations deploymentQuotaReservations,
) (bool, error) {
	location := p.modelDeploymentLocation()
	modelResponse, err := p.azdClient.Ai().ListModels(ctx,
		&azdext.ListModelsRequest{
			AzureContext: &azdext.AzureContext{
				Scope: &azdext.AzureScope{
					SubscriptionId: p.subID,
					Location:       location,
				},
			},
			Filter: &azdext.AiModelFilterOptions{
				Locations:    []string{location},
				Capabilities: []string{agentsV2ModelCapability},
			},
		},
	)
	if err != nil {
		if hasAiErrorReason(
			err,
			azdext.AiErrorReasonNoModelsMatch,
			azdext.AiErrorReasonModelNotFound,
		) {
			return false, nil
		}
		return false, err
	}
	modelAvailable := slices.ContainsFunc(modelResponse.GetModels(),
		func(model *azdext.AiModel) bool {
			return model != nil &&
				strings.EqualFold(model.GetName(), configuration.modelName) &&
				slices.Contains(model.GetCapabilities(), agentsV2ModelCapability)
		})
	if !modelAvailable {
		return false, nil
	}

	response, err := p.azdClient.Ai().ResolveModelDeployments(ctx,
		&azdext.ResolveModelDeploymentsRequest{
			AzureContext: &azdext.AzureContext{
				Scope: &azdext.AzureScope{
					SubscriptionId: p.subID,
					Location:       location,
				},
			},
			ModelName: configuration.modelName,
			Options: &azdext.AiModelDeploymentOptions{
				Locations: []string{location},
				Versions:  []string{configuration.modelVersion},
				Skus:      []string{configuration.skuName},
				Capacity:  new(capacity),
			},
			Quota: &azdext.QuotaCheckOptions{MinRemainingCapacity: 1},
		})
	if err != nil {
		if hasAiErrorReason(
			err,
			azdext.AiErrorReasonModelNotFound,
			azdext.AiErrorReasonNoDeploymentMatch,
		) {
			return false, nil
		}
		return false, err
	}

	for _, candidate := range response.GetDeployments() {
		if candidate.GetFormat() == configuration.modelFormat &&
			candidate.GetVersion() == configuration.modelVersion &&
			candidate.GetSku().GetName() == configuration.skuName &&
			candidate.GetCapacity() == capacity {
			fitsQuota, err := reserveDeploymentQuota(
				candidate,
				capacity,
				reservations,
			)
			if err != nil {
				return false, err
			}
			if fitsQuota {
				return true, nil
			}
		}
	}
	return false, nil
}

func (p *FoundryProvisioningProvider) modelDeploymentLocation() string {
	if location := strings.TrimSpace(p.deploymentLocation); location != "" {
		return location
	}
	return p.location
}

func reserveDeploymentQuota(
	deployment *azdext.AiModelDeployment,
	capacity int32,
	reservations deploymentQuotaReservations,
) (bool, error) {
	if deployment == nil {
		return false, fmt.Errorf("model deployment quota response is empty")
	}
	if capacity <= 0 {
		return false, fmt.Errorf(
			"model deployment quota response has invalid capacity %d",
			capacity,
		)
	}
	if deployment.RemainingQuota == nil {
		return true, nil
	}
	if deployment.GetSku() == nil {
		return false, fmt.Errorf("model deployment quota response has no SKU")
	}
	if reservations == nil {
		return false, fmt.Errorf("model deployment quota reservations are required")
	}
	usageName := strings.TrimSpace(deployment.GetSku().GetUsageName())
	if usageName == "" {
		return false, fmt.Errorf(
			"model deployment quota response has no usage name",
		)
	}
	key := strings.ToLower(usageName)
	required := reservations[key] + float64(capacity)
	if required > deployment.GetRemainingQuota() {
		return false, nil
	}
	reservations[key] = required
	return true, nil
}

func (p *FoundryProvisioningProvider) modelDeploymentTarget(
	ctx context.Context,
) (modelDeploymentTarget, bool, error) {
	if p.existingProjectID != "" {
		projectID, err := arm.ParseResourceID(p.existingProjectID)
		if err != nil || projectID.Parent == nil || projectID.Parent.Name == "" {
			return modelDeploymentTarget{}, false, exterrors.Validation(
				exterrors.CodeInvalidServiceConfig,
				"AZURE_AI_PROJECT_ID is not a valid Foundry project resource ID",
				"re-run `azd ai agent init` against the configured existing project",
			)
		}
		return modelDeploymentTarget{
			subscriptionID: projectID.SubscriptionID,
			resourceGroup:  projectID.ResourceGroupName,
			accountName:    projectID.Parent.Name,
		}, true, nil
	}

	accountName, err := p.envValue(ctx, envKeyAccountName)
	if err != nil {
		return modelDeploymentTarget{}, false, exterrors.Dependency(
			exterrors.CodeEnvironmentValuesFailed,
			fmt.Sprintf(
				"read %s from azd environment %q: %s",
				envKeyAccountName,
				p.envName,
				err,
			),
			"verify the azd environment is accessible, then retry",
		)
	}
	if accountName == "" || p.subID == "" || p.rgName == "" {
		return modelDeploymentTarget{}, false, nil
	}
	return modelDeploymentTarget{
		subscriptionID: p.subID,
		resourceGroup:  p.rgName,
		accountName:    accountName,
	}, true, nil
}

func (p *FoundryProvisioningProvider) listFoundryModelDeployments(
	ctx context.Context,
	target modelDeploymentTarget,
) ([]foundryModelDeployment, error) {
	if p.modelDeploymentLister != nil {
		return p.modelDeploymentLister(ctx, target)
	}
	if err := p.ensureCredential(ctx); err != nil {
		return nil, err
	}
	client, err := armcognitiveservices.NewDeploymentsClient(
		target.subscriptionID,
		p.credential,
		azure.NewArmClientOptions(),
	)
	if err != nil {
		return nil, exterrors.Internal(
			exterrors.CodeAzdClientFailed,
			fmt.Sprintf("create Cognitive Services deployments client: %s", err),
		)
	}

	var deployments []foundryModelDeployment
	pager := client.NewListPager(target.resourceGroup, target.accountName, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isNotFound(err) {
				return nil, nil
			}
			return nil, exterrors.ServiceFromAzure(
				err,
				exterrors.OpCognitiveDeploymentList,
			)
		}
		for _, deployment := range page.Value {
			if deployment == nil {
				continue
			}
			item := foundryModelDeployment{}
			if deployment.Name != nil {
				item.name = *deployment.Name
			}
			if deployment.Properties != nil && deployment.Properties.Model != nil {
				model := deployment.Properties.Model
				if model.Name != nil {
					item.modelName = *model.Name
				}
				if model.Format != nil {
					item.modelFormat = *model.Format
				}
				if model.Version != nil {
					item.modelVersion = *model.Version
				}
			}
			if deployment.SKU != nil {
				if deployment.SKU.Name != nil {
					item.skuName = *deployment.SKU.Name
				}
				if deployment.SKU.Capacity != nil {
					item.capacity = *deployment.SKU.Capacity
				}
			}
			deployments = append(deployments, item)
		}
	}
	return deployments, nil
}

func findFoundryModelDeployment(
	deployments []foundryModelDeployment,
	name string,
) (foundryModelDeployment, bool) {
	for _, deployment := range deployments {
		if strings.EqualFold(deployment.name, name) {
			return deployment, true
		}
	}
	return foundryModelDeployment{}, false
}

func modelDeploymentMatches(
	deployment foundryModelDeployment,
	configuration modelDeploymentConfiguration,
) bool {
	return strings.EqualFold(deployment.name, configuration.name) &&
		sameModelDeploymentConfiguration(deployment, configuration) &&
		deployment.capacity == configuration.capacity
}

func sameModelDeploymentConfiguration(
	deployment foundryModelDeployment,
	configuration modelDeploymentConfiguration,
) bool {
	return strings.EqualFold(deployment.modelName, configuration.modelName) &&
		strings.EqualFold(deployment.modelFormat, configuration.modelFormat) &&
		strings.EqualFold(deployment.modelVersion, configuration.modelVersion) &&
		strings.EqualFold(deployment.skuName, configuration.skuName)
}

func hasAiErrorReason(err error, reasons ...string) bool {
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	for _, detail := range st.Details() {
		info, ok := detail.(*errdetails.ErrorInfo)
		if !ok || info.Domain != azdext.AiErrorDomain {
			continue
		}
		if slices.Contains(reasons, info.Reason) {
			return true
		}
	}
	return false
}

func (p *FoundryProvisioningProvider) deploymentPromptError(
	err error,
	deploymentIndex int,
	issue deploymentEnvironmentIssue,
	affectedKeys []string,
	action string,
) error {
	if exterrors.IsCancellation(err) {
		return exterrors.Cancelled("model deployment selection was cancelled")
	}
	if exterrors.IsPromptRequired(err) {
		return exterrors.Dependency(
			exterrors.CodeMissingModelDeployment,
			fmt.Sprintf(
				"model deployment %d environment values are %s in azd environment %q: %s",
				deploymentIndex,
				issue,
				p.envName,
				strings.Join(affectedKeys, ", "),
			),
			"set the complete deployment tuple with `azd env set <name> <value>`, "+
				"or run interactively to "+action,
		)
	}
	return exterrors.Dependency(
		exterrors.CodeMissingModelDeployment,
		fmt.Sprintf(
			"%s for deployment %d with %s "+
				"environment values (%s): %s",
			action,
			deploymentIndex,
			issue,
			strings.Join(affectedKeys, ", "),
			err,
		),
		"retry, or set the model deployment values in the azd environment",
	)
}
