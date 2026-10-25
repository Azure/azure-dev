// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"azure.ai.projects/internal/azure"
	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/internal/provisioning"
	"azure.ai.projects/internal/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	armcognitiveservices "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"go.yaml.in/yaml/v3"
)

func reconcileAdoptedDeployments(
	ctx context.Context,
	client *azdext.AzdClient,
	projectRoot string,
	envName string,
	target *resolvedProject,
	serviceName string,
	noPrompt bool,
) (string, bool, func() error, error) {
	if target == nil || target.Mode != projectModeExistingID ||
		target.ResourceId == "" {
		return "", false, func() error { return nil }, nil
	}

	section, err := client.Project().GetConfigSection(ctx,
		&azdext.GetProjectConfigSectionRequest{Path: "services"})
	if err != nil {
		return "", false, nil, fmt.Errorf(
			"read project services for deployment reconciliation: %w", err,
		)
	}
	if !section.GetFound() || section.GetSection() == nil {
		return "", false, func() error { return nil }, nil
	}
	raw, err := yaml.Marshal(map[string]any{
		"services": section.GetSection().AsMap(),
	})
	if err != nil {
		return "", false, nil, fmt.Errorf(
			"marshal project services for deployment reconciliation: %w", err,
		)
	}
	declared, err := synthesis.BrownfieldDeployments(raw, serviceName, projectRoot)
	if err != nil {
		return "", false, nil, fmt.Errorf("read adopted project deployments: %w", err)
	}
	oldEnvironment, err := client.Environment().GetValues(
		ctx, &azdext.GetEnvironmentRequest{Name: envName},
	)
	if err != nil {
		return "", false, nil, fmt.Errorf("read adopted deployment default: %w", err)
	}
	environmentValues := make(map[string]string, len(oldEnvironment.GetKeyValues()))
	for _, item := range oldEnvironment.GetKeyValues() {
		environmentValues[item.GetKey()] = item.GetValue()
	}
	if effective, present, err := provisioning.DecodeProjectDeployments(
		environmentValues[projectDeploymentsEnvKey],
	); err != nil {
		return "", false, nil, err
	} else if present {
		declared = effective
	}
	if len(declared) == 0 {
		return "", false, func() error { return nil }, nil
	}

	credential, err := azidentity.NewAzureDeveloperCLICredential(
		&azidentity.AzureDeveloperCLICredentialOptions{
			TenantID:                   target.UserTenantId,
			AdditionallyAllowedTenants: []string{"*"},
		},
	)
	if err != nil {
		return "", false, nil, exterrors.Auth(
			exterrors.CodeCredentialCreationFailed,
			fmt.Sprintf("failed to create Azure credential: %s", err),
			"run `azd auth login` and retry",
		)
	}
	deploymentsClient, err := armcognitiveservices.NewDeploymentsClient(
		target.SubscriptionId, credential, azure.NewArmClientOptions(),
	)
	if err != nil {
		return "", false, nil, fmt.Errorf("create model deployments client: %w", err)
	}
	pager := deploymentsClient.NewListPager(
		target.ResourceGroupName, target.AccountName, nil,
	)
	var live []liveProjectDeployment
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return "", false, nil, exterrors.ServiceFromAzure(
				err, exterrors.OpCognitiveDeploymentList,
			)
		}
		for _, item := range page.Value {
			if item == nil || item.Name == nil || item.Properties == nil ||
				item.Properties.Model == nil || item.Properties.Model.Name == nil {
				continue
			}
			deployment := liveProjectDeployment{Name: *item.Name}
			deployment.Model.Name = *item.Properties.Model.Name
			if item.Properties.Model.Format != nil {
				deployment.Model.Format = *item.Properties.Model.Format
			}
			if item.Properties.Model.Version != nil {
				deployment.Model.Version = *item.Properties.Model.Version
			}
			if item.SKU != nil {
				if item.SKU.Name != nil {
					deployment.Sku.Name = *item.SKU.Name
				}
				if item.SKU.Capacity != nil {
					deployment.Sku.Capacity = int(*item.SKU.Capacity)
				}
			}
			live = append(live, deployment)
		}
	}
	slices.SortFunc(live, func(a, b liveProjectDeployment) int {
		return strings.Compare(a.Name, b.Name)
	})

	return reconcileAdoptedDeploymentChoices(
		ctx, client, envName, target, declared, live, environmentValues, noPrompt,
	)
}

func reconcileAdoptedDeploymentChoices(
	ctx context.Context,
	client *azdext.AzdClient,
	envName string,
	target *resolvedProject,
	declared []synthesis.Deployment,
	live []liveProjectDeployment,
	environmentValues map[string]string,
	noPrompt bool,
) (string, bool, func() error, error) {
	originalDefault := environmentValues["AZURE_AI_MODEL_DEPLOYMENT_NAME"]
	bindings, err := synthesis.DecodeDeploymentBindings(
		environmentValues[synthesis.DeploymentBindingsEnvKey],
	)
	if err != nil {
		return "", false, nil, err
	}
	envDeployments, present, err := provisioning.DecodeProjectDeployments(
		environmentValues[projectDeploymentsEnvKey],
	)
	if err != nil {
		return "", false, nil, err
	}
	if present {
		declared = envDeployments
	}
	remaining := make([]synthesis.Deployment, 0, len(declared))
	referenced := make([]synthesis.Deployment, 0, len(declared))
	changed := false
	explicitDefault := ""
	for _, item := range declared {
		expandedItem, matches, err := matchingLiveDeployments(
			item, live, environmentValues,
		)
		if err != nil {
			return "", false, nil, fmt.Errorf(
				"expand adopted deployment %q: %w", item.Name, err,
			)
		}
		// Without a prompt, unmatched declarations are kept as configured.
		if len(matches) == 0 && noPrompt {
			synthesis.SetDeploymentBinding(bindings, expandedItem.Name, expandedItem.Name)
			remaining = append(remaining, item)
			referenced = append(referenced, expandedItem)
			continue
		}
		var selected liveProjectDeployment
		if len(matches) > 0 {
			selected = matches[0]
		}
		if !noPrompt {
			choices := make([]*azdext.SelectChoice, 0, len(matches)+3)
			for _, candidate := range matches {
				choices = append(choices, &azdext.SelectChoice{
					Value: "use:" + candidate.Name,
					Label: fmt.Sprintf(
						"Use existing deployment %q (version: %s, SKU: %s)",
						candidate.Name, candidate.Model.Version, candidate.Sku.Name,
					),
				})
			}
			choices = append(choices,
				&azdext.SelectChoice{
					Value: "deploy",
					Label: "Deploy as configured",
				},
				&azdext.SelectChoice{
					Value: "change",
					Label: "Choose a different model",
				},
				&azdext.SelectChoice{
					Value: "skip",
					Label: "Skip this model",
				},
			)
			prompt, promptErr := client.Prompt().Select(ctx,
				&azdext.SelectRequest{Options: &azdext.SelectOptions{
					Message: fmt.Sprintf(
						"How would you like to proceed with deployment %q "+
							"(model %q)?",
						expandedItem.Name,
						expandedItem.Model.Name,
					),
					Choices: choices,
				}},
			)
			if promptErr != nil {
				return "", false, nil, fmt.Errorf(
					"select an adopted model deployment: %w", promptErr,
				)
			}
			index := int(prompt.GetValue())
			if index < 0 || index >= len(choices) {
				return "", false, nil, fmt.Errorf("invalid adopted deployment selection: %d", index)
			}
			choice := choices[index].GetValue()
			switch {
			case choice == "deploy":
				synthesis.SetDeploymentBinding(bindings, expandedItem.Name, expandedItem.Name)
				remaining = append(remaining, item)
				referenced = append(referenced, expandedItem)
				if !present {
					changed = true
				}
				continue
			case choice == "skip":
				synthesis.SetDeploymentBinding(bindings, expandedItem.Name, "")
				changed = true
				continue
			case choice == "change":
				alternative, provision, err := selectAlternativeAdoptedDeployment(
					ctx, client, target, expandedItem, live,
				)
				if err != nil {
					return "", false, nil, err
				}
				if provision {
					remaining = append(remaining, alternative)
				}
				referenced = append(referenced, alternative)
				synthesis.SetDeploymentBinding(bindings, expandedItem.Name, alternative.Name)
				if explicitDefault == "" {
					explicitDefault = alternative.Name
				}
				changed = true
				continue
			default:
				name := strings.TrimPrefix(choice, "use:")
				for _, candidate := range matches {
					if candidate.Name == name {
						selected = candidate
						break
					}
				}
			}
		}
		referenced = append(referenced, synthesis.Deployment{
			Name:  selected.Name,
			Model: selected.Model,
			Sku:   selected.Sku,
		})
		synthesis.SetDeploymentBinding(bindings, expandedItem.Name, selected.Name)
		changed = true
	}
	if len(referenced) == 0 && !changed {
		return "", false, func() error { return nil }, nil
	}

	restore := func() error {
		return withProjectRollbackContext(ctx, func(rollbackCtx context.Context) error {
			_, defaultErr := client.Environment().SetValue(rollbackCtx, &azdext.SetEnvRequest{
				EnvName: envName,
				Key:     "AZURE_AI_MODEL_DEPLOYMENT_NAME",
				Value:   originalDefault,
			})
			var environmentErr error
			if changed {
				_, err := client.Environment().SetValue(rollbackCtx, &azdext.SetEnvRequest{
					EnvName: envName,
					Key:     projectDeploymentsEnvKey,
					Value:   environmentValues[projectDeploymentsEnvKey],
				})
				environmentErr = err
			}
			_, bindingsErr := client.Environment().SetValue(rollbackCtx, &azdext.SetEnvRequest{
				EnvName: envName,
				Key:     synthesis.DeploymentBindingsEnvKey,
				Value:   environmentValues[synthesis.DeploymentBindingsEnvKey],
			})
			return errors.Join(defaultErr, environmentErr, bindingsErr)
		})
	}
	encodedBindings, err := synthesis.EncodeDeploymentBindings(bindings)
	if err != nil {
		return "", false, nil, err
	}
	if _, err := client.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: envName, Key: synthesis.DeploymentBindingsEnvKey, Value: encodedBindings,
	}); err != nil {
		return "", false, nil, errors.Join(
			fmt.Errorf("set adopted deployment bindings: %w", err), restore(),
		)
	}
	if changed {
		encoded, err := provisioning.EncodeProjectDeployments(remaining)
		if err != nil {
			return "", false, nil, errors.Join(err, restore())
		}
		if _, err := client.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: envName, Key: projectDeploymentsEnvKey, Value: encoded,
		}); err != nil {
			return "", false, nil, errors.Join(
				fmt.Errorf("set adopted project deployments: %w", err), restore(),
			)
		}
	}
	defaultName := explicitDefault
	if defaultName == "" && len(referenced) > 0 {
		defaultName = selectReconciledDefault(originalDefault, live, referenced)
	}
	if _, err := client.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: envName,
		Key:     "AZURE_AI_MODEL_DEPLOYMENT_NAME",
		Value:   defaultName,
	}); err != nil {
		operationErr := fmt.Errorf("set adopted deployment default: %w", err)
		if restoreErr := restore(); restoreErr != nil {
			return "", false, nil, errors.Join(operationErr, restoreErr)
		}
		return "", false, nil, operationErr
	}
	return defaultName, changed, restore, nil
}

// selectAlternativeAdoptedDeployment lets the user pick any live deployment in
// the project, or a catalog model deployed under the declared deployment name.
// The returned flag reports whether the deployment still needs provisioning.
func selectAlternativeAdoptedDeployment(
	ctx context.Context,
	client *azdext.AzdClient,
	target *resolvedProject,
	original synthesis.Deployment,
	live []liveProjectDeployment,
) (synthesis.Deployment, bool, error) {
	choices := make([]*azdext.SelectChoice, 0, len(live)+1)
	for _, candidate := range live {
		choices = append(choices, &azdext.SelectChoice{
			Value: candidate.Name,
			Label: fmt.Sprintf(
				"Use existing deployment %q (model: %s, version: %s)",
				candidate.Name, candidate.Model.Name, candidate.Model.Version,
			),
		})
	}
	choices = append(choices, &azdext.SelectChoice{
		Value: "", Label: "Deploy a new model from the catalog",
	})
	response, err := client.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: "How would you like to choose a model?",
			Choices: choices,
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return synthesis.Deployment{}, false,
				exterrors.Cancelled("model selection was cancelled")
		}
		return synthesis.Deployment{}, false, fmt.Errorf(
			"select an alternative model: %w", err,
		)
	}
	index := int(response.GetValue())
	if index < 0 || index >= len(choices) {
		return synthesis.Deployment{}, false, fmt.Errorf(
			"invalid alternative model selection: %d", index,
		)
	}
	if index < len(live) {
		selected := live[index]
		return synthesis.Deployment{
			Name: selected.Name, Model: selected.Model, Sku: selected.Sku,
		}, false, nil
	}

	var tenantID, subscriptionID, location string
	if target != nil {
		tenantID, subscriptionID, location =
			target.UserTenantId, target.SubscriptionId, target.Location
	}
	azureContext := &azdext.AzureContext{Scope: &azdext.AzureScope{
		SubscriptionId: subscriptionID,
		TenantId:       tenantID,
		Location:       location,
	}}
	model, err := client.Prompt().PromptAiModel(ctx, &azdext.PromptAiModelRequest{
		AzureContext:  azureContext,
		Filter:        &azdext.AiModelFilterOptions{Locations: []string{location}},
		SelectOptions: &azdext.SelectOptions{Message: "Select a model"},
		Quota:         &azdext.QuotaCheckOptions{MinRemainingCapacity: 1},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return synthesis.Deployment{}, false,
				exterrors.Cancelled("model selection was cancelled")
		}
		return synthesis.Deployment{}, false, fmt.Errorf(
			"select a model from the catalog: %w", err,
		)
	}
	selected, err := selectModelDeployment(
		ctx, client, azureContext,
		modelSelection{
			Name:           model.GetModel().GetName(),
			DeploymentName: original.Name,
		},
		deploymentSelectionOptions{}, false,
	)
	if err != nil {
		return synthesis.Deployment{}, false, err
	}
	return selected.Deployment, true, nil
}

func selectReconciledDefault(
	originalDefault string,
	live []liveProjectDeployment,
	referenced []synthesis.Deployment,
) string {
	originalDefault = strings.TrimSpace(originalDefault)
	for _, deployment := range live {
		if strings.EqualFold(deployment.Name, originalDefault) {
			return deployment.Name
		}
	}
	return referenced[0].Name
}

func matchingLiveDeployments(
	deployment synthesis.Deployment,
	live []liveProjectDeployment,
	environment map[string]string,
) (synthesis.Deployment, []liveProjectDeployment, error) {
	expanded, err := expandDeployment(deployment, environment)
	if err != nil {
		return synthesis.Deployment{}, nil, err
	}

	matches := make([]liveProjectDeployment, 0)
	for _, candidate := range live {
		if strings.EqualFold(candidate.Model.Name, expanded.Model.Name) {
			matches = append(matches, candidate)
		}
	}
	return expanded, matches, nil
}

func expandDeployment(
	deployment synthesis.Deployment,
	environment map[string]string,
) (synthesis.Deployment, error) {
	var err error
	if deployment.Name, err = synthesis.ResolveEnvironmentValue(
		deployment.Name, environment,
	); err != nil {
		return synthesis.Deployment{}, err
	}
	if deployment.Model.Name, err = synthesis.ResolveEnvironmentValue(
		deployment.Model.Name, environment,
	); err != nil {
		return synthesis.Deployment{}, err
	}
	if deployment.Model.Format, err = synthesis.ResolveEnvironmentValue(
		deployment.Model.Format, environment,
	); err != nil {
		return synthesis.Deployment{}, err
	}
	if deployment.Model.Version, err = synthesis.ResolveEnvironmentValue(
		deployment.Model.Version, environment,
	); err != nil {
		return synthesis.Deployment{}, err
	}
	if deployment.Sku.Name, err = synthesis.ResolveEnvironmentValue(
		deployment.Sku.Name, environment,
	); err != nil {
		return synthesis.Deployment{}, err
	}
	return deployment, nil
}

type liveProjectDeployment struct {
	Name  string
	Model synthesis.DeploymentModel
	Sku   synthesis.DeploymentSku
}
