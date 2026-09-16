// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"azure.ai.projects/internal/azure"
	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/internal/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	armcognitiveservices "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"go.yaml.in/yaml/v3"
	"google.golang.org/protobuf/types/known/structpb"
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
	if len(declared) == 0 {
		return "", false, func() error { return nil }, nil
	}
	services := section.GetSection().AsMap()
	serviceConfig, _ := services[serviceName].(map[string]any)
	originalDeployment, hadDeployment := serviceConfig["deployments"]
	originalValue, err := structpb.NewValue(originalDeployment)
	if err != nil && hadDeployment {
		return "", false, nil, fmt.Errorf("save adopted project deployments: %w", err)
	}
	oldEnvironment, err := client.Environment().GetValues(
		ctx, &azdext.GetEnvironmentRequest{Name: envName},
	)
	if err != nil {
		return "", false, nil, fmt.Errorf("read adopted deployment default: %w", err)
	}
	originalDefault := ""
	environmentValues := make(map[string]string, len(oldEnvironment.GetKeyValues()))
	for _, item := range oldEnvironment.GetKeyValues() {
		environmentValues[item.GetKey()] = item.GetValue()
		if item.GetKey() == "AZURE_AI_MODEL_DEPLOYMENT_NAME" {
			originalDefault = item.GetValue()
		}
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

	remaining := make([]synthesis.Deployment, 0, len(declared))
	referenced := make([]synthesis.Deployment, 0, len(declared))
	changed := false
	for _, item := range declared {
		expandedItem, matches, err := matchingLiveDeployments(
			item, live, environmentValues,
		)
		if err != nil {
			return "", false, nil, fmt.Errorf(
				"expand adopted deployment %q: %w", item.Name, err,
			)
		}
		if len(matches) == 0 {
			remaining = append(remaining, item)
			referenced = append(referenced, expandedItem)
			continue
		}
		selected := matches[0]
		if !noPrompt {
			choices := make([]*azdext.SelectChoice, 0, len(matches)+2)
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
					Label: "Deploy as specified in azure.yaml",
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
			choice := choices[prompt.GetValue()].GetValue()
			switch {
			case choice == "deploy":
				remaining = append(remaining, item)
				referenced = append(referenced, expandedItem)
				continue
			case choice == "skip":
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
		changed = true
	}
	if len(referenced) == 0 && !changed {
		return "", false, func() error { return nil }, nil
	}

	value, err := deploymentValue(remaining)
	if err != nil {
		return "", false, nil, err
	}
	if changed {
		if _, err := client.Project().SetServiceConfigValue(ctx,
			&azdext.SetServiceConfigValueRequest{
				ServiceName: serviceName,
				Path:        "deployments",
				Value:       value,
			}); err != nil {
			return "", false, nil, fmt.Errorf("update adopted project deployments: %w", err)
		}
	}
	if len(referenced) == 0 {
		return "", changed, func() error {
			return restoreAdoptedDeploymentState(
				ctx,
				client,
				serviceName,
				envName,
				changed,
				hadDeployment,
				originalValue,
				originalDefault,
			)
		}, nil
	}
	defaultName := selectReconciledDefault(
		originalDefault,
		live,
		referenced,
	)
	if _, err := client.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: envName,
		Key:     "AZURE_AI_MODEL_DEPLOYMENT_NAME",
		Value:   defaultName,
	}); err != nil {
		operationErr := fmt.Errorf("set adopted deployment default: %w", err)
		if restoreErr := restoreAdoptedDeploymentState(
			ctx,
			client,
			serviceName,
			envName,
			changed,
			hadDeployment,
			originalValue,
			originalDefault,
		); restoreErr != nil {
			return "", false, nil, errors.Join(operationErr, restoreErr)
		}
		return "", false, nil, operationErr
	}
	restore := func() error {
		return restoreAdoptedDeploymentState(
			ctx,
			client,
			serviceName,
			envName,
			changed,
			hadDeployment,
			originalValue,
			originalDefault,
		)
	}
	return defaultName, changed, restore, nil
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

func restoreAdoptedDeploymentState(
	ctx context.Context,
	client *azdext.AzdClient,
	serviceName string,
	envName string,
	restoreService bool,
	hadDeployment bool,
	originalValue *structpb.Value,
	originalDefault string,
) error {
	return withProjectRollbackContext(ctx, func(rollbackCtx context.Context) error {
		var restoreErrs []error
		if restoreService && hadDeployment {
			if _, err := client.Project().SetServiceConfigValue(
				rollbackCtx,
				&azdext.SetServiceConfigValueRequest{
					ServiceName: serviceName,
					Path:        "deployments",
					Value:       originalValue,
				},
			); err != nil {
				restoreErrs = append(restoreErrs, err)
			}
		} else if restoreService {
			if _, err := client.Project().UnsetServiceConfig(
				rollbackCtx,
				&azdext.UnsetServiceConfigRequest{
					ServiceName: serviceName,
					Path:        "deployments",
				},
			); err != nil {
				restoreErrs = append(restoreErrs, err)
			}
		}
		if _, err := client.Environment().SetValue(
			rollbackCtx,
			&azdext.SetEnvRequest{
				EnvName: envName,
				Key:     "AZURE_AI_MODEL_DEPLOYMENT_NAME",
				Value:   originalDefault,
			},
		); err != nil {
			restoreErrs = append(restoreErrs, err)
		}
		return errors.Join(restoreErrs...)
	})
}

type liveProjectDeployment struct {
	Name  string
	Model synthesis.DeploymentModel
	Sku   synthesis.DeploymentSku
}

func deploymentValue(deployments []synthesis.Deployment) (*structpb.Value, error) {
	body, err := json.Marshal(deployments)
	if err != nil {
		return nil, fmt.Errorf("marshal adopted deployments: %w", err)
	}
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, fmt.Errorf("decode adopted deployments: %w", err)
	}
	return structpb.NewValue(value)
}
