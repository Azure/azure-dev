// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package resources

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"azure.resources/internal/resources/scaffold"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// ParameterResolver supplies resource-specific deployment parameter values.
type ParameterResolver struct {
	client       *azdext.AzdClient
	locationKey  string
	requirements []*azdext.QuotaRequirement
}

// NewParameterResolver creates a resolver using the host's prompts and environment storage.
func NewParameterResolver(client *azdext.AzdClient) *ParameterResolver {
	return &ParameterResolver{client: client}
}

// Configure validates the resource requirements for one infrastructure entry without prompting.
func (r *ParameterResolver) Configure(options *azdext.ProvisioningOptions) error {
	if options == nil {
		return errors.New("parameter resolution requires provisioning options")
	}
	var requirements []*azdext.QuotaRequirement
	if options.Config != nil {
		config, err := Decode(options.Config.AsMap())
		if err != nil {
			return err
		}
		requirements, err = aiQuotaRequirements(config)
		if err != nil {
			return err
		}
	}
	r.locationKey = scaffold.EntryEnvKey(options.Name, "AZURE_AI_DEPLOYMENTS_LOCATION")
	r.requirements = requirements
	return nil
}

// ResolveParameter returns a resource-specific value and whether it required a local prompt,
// recording its environment mappings. A nil value with no error leaves normal parameter resolution in effect.
func (r *ParameterResolver) ResolveParameter(
	ctx context.Context, name string, values map[string]string, mapping *[]string,
) (any, bool, error) {
	if name != "aiDeploymentsLocation" || r.requirements == nil {
		return nil, false, nil
	}

	*mapping = append(*mapping, r.locationKey)
	if location := values[r.locationKey]; location != "" {
		return location, false, nil
	}

	location, err := r.promptAILocation(ctx, values)
	if err != nil {
		return nil, true, err
	}
	_, err = r.client.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: values["AZURE_ENV_NAME"], Key: r.locationKey, Value: location,
	})
	if err != nil {
		return nil, true, fmt.Errorf("saving environment value %s: %w", r.locationKey, err)
	}
	values[r.locationKey] = location
	return location, true, nil
}

func (r *ParameterResolver) promptAILocation(ctx context.Context, values map[string]string) (string, error) {
	response, err := r.client.Prompt().PromptAiLocationWithQuota(ctx, &azdext.PromptAiLocationWithQuotaRequest{
		AzureContext: &azdext.AzureContext{Scope: &azdext.AzureScope{SubscriptionId: values["AZURE_SUBSCRIPTION_ID"]}},
		Requirements: r.requirements, DefaultValue: values["AZURE_LOCATION"],
		SelectOptions: &azdext.SelectOptions{Message: "Select a location for AI models"},
	})
	if err != nil {
		return "", fmt.Errorf("selecting AI deployment location: %w", err)
	}
	location := response.GetLocation().GetName()
	if location == "" {
		return "", errors.New("AI deployment location selection is empty")
	}
	return location, nil
}

func aiQuotaRequirements(config Config) ([]*azdext.QuotaRequirement, error) {
	for _, name := range slices.Sorted(maps.Keys(config)) {
		resource := config[name]
		if resource.Type != AIProject || resource.Existing {
			continue
		}

		// All models share one account and location, so models using the same quota pool add up.
		capacities := map[string]float64{"OpenAI.S0.AccountCount": 1}
		for _, model := range resource.Models {
			usage := strings.TrimSpace(model.Sku.UsageName)
			if usage == "" || model.Sku.Capacity <= 0 {
				return nil, fmt.Errorf("AI model %q in resource %q requires a quota usage name and positive capacity",
					model.Name, name)
			}
			capacities[usage] += float64(model.Sku.Capacity)
		}
		requirements := make([]*azdext.QuotaRequirement, 0, len(capacities))
		for _, usage := range slices.Sorted(maps.Keys(capacities)) {
			requirements = append(requirements, &azdext.QuotaRequirement{
				UsageName: usage, MinCapacity: capacities[usage],
			})
		}
		return requirements, nil
	}
	return nil, nil
}
