// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
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
) error {
	if target == nil || target.Mode != projectModeExistingID ||
		target.ResourceId == "" {
		return nil
	}

	section, err := client.Project().GetConfigSection(ctx,
		&azdext.GetProjectConfigSectionRequest{Path: "services"})
	if err != nil {
		return fmt.Errorf("read project services for deployment reconciliation: %w", err)
	}
	if !section.GetFound() || section.GetSection() == nil {
		return nil
	}
	raw, err := yaml.Marshal(map[string]any{
		"services": section.GetSection().AsMap(),
	})
	if err != nil {
		return fmt.Errorf("marshal project services for deployment reconciliation: %w", err)
	}
	declared, err := synthesis.BrownfieldDeployments(raw, serviceName, projectRoot)
	if err != nil {
		return fmt.Errorf("read adopted project deployments: %w", err)
	}
	if len(declared) == 0 {
		return nil
	}

	credential, err := azidentity.NewAzureDeveloperCLICredential(
		&azidentity.AzureDeveloperCLICredentialOptions{
			TenantID:                   target.UserTenantId,
			AdditionallyAllowedTenants: []string{"*"},
		},
	)
	if err != nil {
		return exterrors.Auth(
			exterrors.CodeCredentialCreationFailed,
			fmt.Sprintf("failed to create Azure credential: %s", err),
			"run `azd auth login` and retry",
		)
	}
	deploymentsClient, err := armcognitiveservices.NewDeploymentsClient(
		target.SubscriptionId, credential, azure.NewArmClientOptions(),
	)
	if err != nil {
		return fmt.Errorf("create model deployments client: %w", err)
	}
	pager := deploymentsClient.NewListPager(
		target.ResourceGroupName, target.AccountName, nil,
	)
	var live []liveProjectDeployment
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return exterrors.ServiceFromAzure(err, exterrors.OpCognitiveDeploymentList)
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
	for _, item := range declared {
		matches := make([]liveProjectDeployment, 0)
		for _, candidate := range live {
			if strings.EqualFold(candidate.Model.Name, item.Model.Name) {
				matches = append(matches, candidate)
			}
		}
		if len(matches) == 0 {
			remaining = append(remaining, item)
			continue
		}
		referenced = append(referenced, synthesis.Deployment{
			Name:  matches[0].Name,
			Model: matches[0].Model,
			Sku:   matches[0].Sku,
		})
	}
	if len(referenced) == 0 {
		return nil
	}

	value, err := deploymentValue(remaining)
	if err != nil {
		return err
	}
	if _, err := client.Project().SetServiceConfigValue(ctx,
		&azdext.SetServiceConfigValueRequest{
			ServiceName: serviceName,
			Path:        "deployments",
			Value:       value,
		}); err != nil {
		return fmt.Errorf("update adopted project deployments: %w", err)
	}
	if _, err := client.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: envName,
		Key:     "AZURE_AI_MODEL_DEPLOYMENT_NAME",
		Value:   referenced[0].Name,
	}); err != nil {
		return fmt.Errorf("set adopted deployment default: %w", err)
	}
	return nil
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
