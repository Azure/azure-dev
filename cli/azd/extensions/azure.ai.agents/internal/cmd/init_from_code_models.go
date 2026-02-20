// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
)

func (a *InitFromCodeAction) loadAiCatalog(ctx context.Context) error {
	if a.modelCatalog != nil {
		return nil
	}

	spinner := ux.NewSpinner(&ux.SpinnerOptions{
		Text:        "Loading the model catalog",
		ClearOnStop: true,
	})

	if err := spinner.Start(ctx); err != nil {
		return fmt.Errorf("failed to start spinner: %w", err)
	}

	modelResp, err := a.azdClient.Ai().ListModels(ctx, &azdext.ListModelsRequest{
		AzureContext: a.azureContext,
	})
	stopErr := spinner.Stop(ctx)
	if err != nil {
		return fmt.Errorf("failed to load the model catalog: %w", err)
	}
	if stopErr != nil {
		return stopErr
	}

	a.modelCatalog = mapModelsByName(modelResp.Models)

	return nil
}

func (a *InitFromCodeAction) resolveModelDeploymentNoPrompt(
	ctx context.Context,
	model *azdext.AiModel,
	location string,
) (*azdext.AiModelDeployment, error) {
	resolveResp, err := a.azdClient.Ai().ResolveModelDeployments(ctx, &azdext.ResolveModelDeploymentsRequest{
		AzureContext: a.azureContext,
		ModelName:    model.Name,
		Options: &azdext.AiModelDeploymentOptions{
			Locations: []string{location},
		},
		Quota: &azdext.QuotaCheckOptions{
			MinRemainingCapacity: 1,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to resolve model deployment: %w", err)
	}

	if len(resolveResp.Deployments) == 0 {
		return nil, fmt.Errorf("no deployment candidates found for model '%s' in location '%s'", model.Name, location)
	}

	orderedCandidates := slices.Clone(resolveResp.Deployments)
	defaultVersions := make(map[string]struct{}, len(model.Versions))
	for _, version := range model.Versions {
		if version.IsDefault {
			defaultVersions[version.Version] = struct{}{}
		}
	}

	slices.SortFunc(orderedCandidates, func(a, b *azdext.AiModelDeployment) int {
		_, aDefault := defaultVersions[a.Version]
		_, bDefault := defaultVersions[b.Version]
		if aDefault != bDefault {
			if aDefault {
				return -1
			}
			return 1
		}

		aSkuPriority := skuPriority(a.Sku.Name)
		bSkuPriority := skuPriority(b.Sku.Name)
		if aSkuPriority != bSkuPriority {
			if aSkuPriority < bSkuPriority {
				return -1
			}
			return 1
		}

		if cmp := strings.Compare(a.Version, b.Version); cmp != 0 {
			return cmp
		}

		if cmp := strings.Compare(a.Sku.Name, b.Sku.Name); cmp != 0 {
			return cmp
		}

		return strings.Compare(a.Sku.UsageName, b.Sku.UsageName)
	})

	for _, candidate := range orderedCandidates {
		capacity, ok := resolveNoPromptCapacity(candidate)
		if !ok {
			continue
		}

		return cloneDeploymentWithCapacity(candidate, capacity), nil
	}

	return nil, fmt.Errorf("no deployment candidates found for model '%s' with a valid non-interactive capacity", model.Name)
}
