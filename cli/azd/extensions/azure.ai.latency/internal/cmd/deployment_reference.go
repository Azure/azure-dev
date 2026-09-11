// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"strings"

	"azure.ai.latency/internal/model"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

type deploymentFlags struct {
	deploymentID   string
	subscription   string
	resourceGroup  string
	accountName    string
	deploymentName string
}

func resolveDeploymentReference(
	ctx context.Context,
	flags deploymentFlags,
	noPrompt bool,
	client *azdext.AzdClient,
) (model.DeploymentReference, error) {
	if flags.deploymentID != "" {
		if flags.subscription != "" || flags.resourceGroup != "" || flags.accountName != "" ||
			flags.deploymentName != "" {
			return model.DeploymentReference{}, validationError(
				"conflicting_deployment_reference",
				"--deployment-id cannot be combined with --subscription, --resource-group, "+
					"--account-name, or --deployment-name.",
				"Use the complete ARM deployment ID, or provide the four deployment fields.",
			)
		}
		reference, err := parseDeploymentID(flags.deploymentID)
		if err != nil {
			return model.DeploymentReference{}, err
		}
		if client == nil {
			return model.DeploymentReference{}, missingAzdClientError()
		}
		tenant, err := lookupTenant(ctx, client, reference.Subscription)
		if err != nil {
			return model.DeploymentReference{}, err
		}
		reference.UserTenantID = tenant
		return reference, nil
	}

	reference := model.DeploymentReference{
		Subscription:   strings.TrimSpace(flags.subscription),
		ResourceGroup:  strings.TrimSpace(flags.resourceGroup),
		AccountName:    strings.TrimSpace(flags.accountName),
		DeploymentName: strings.TrimSpace(flags.deploymentName),
	}
	if completeReference(reference) {
		if client == nil {
			return model.DeploymentReference{}, missingAzdClientError()
		}
		tenant, err := lookupTenant(ctx, client, reference.Subscription)
		if err != nil {
			return model.DeploymentReference{}, err
		}
		reference.UserTenantID = tenant
		reference.DeploymentID = buildDeploymentID(reference)
		return reference, nil
	}
	if noPrompt {
		return model.DeploymentReference{}, missingDeploymentFieldsError(reference)
	}
	if client == nil {
		return model.DeploymentReference{}, missingAzdClientError()
	}

	azureContext := &azdext.AzureContext{Scope: &azdext.AzureScope{}}
	if reference.Subscription == "" {
		response, err := client.Prompt().PromptSubscription(ctx, &azdext.PromptSubscriptionRequest{})
		if err != nil {
			return model.DeploymentReference{}, fmt.Errorf("prompt for subscription: %w", err)
		}
		reference.Subscription = response.Subscription.Id
		reference.UserTenantID = response.Subscription.UserTenantId
	} else {
		tenant, err := lookupTenant(ctx, client, reference.Subscription)
		if err != nil {
			return model.DeploymentReference{}, err
		}
		reference.UserTenantID = tenant
	}
	azureContext.Scope.SubscriptionId = reference.Subscription
	azureContext.Scope.TenantId = reference.UserTenantID

	if reference.ResourceGroup == "" {
		response, err := client.Prompt().PromptResourceGroup(ctx, &azdext.PromptResourceGroupRequest{
			AzureContext: azureContext,
			Options: &azdext.PromptResourceGroupOptions{
				SelectOptions: &azdext.PromptResourceSelectOptions{
					AllowNewResource: new(false),
					Message:          "Select the resource group containing the model deployment",
					LoadingMessage:   "Fetching resource groups...",
				},
			},
		})
		if err != nil {
			return model.DeploymentReference{}, fmt.Errorf("prompt for resource group: %w", err)
		}
		reference.ResourceGroup = response.ResourceGroup.Name
	}
	azureContext.Scope.ResourceGroup = reference.ResourceGroup

	if reference.AccountName == "" {
		response, err := client.Prompt().PromptResourceGroupResource(
			ctx,
			&azdext.PromptResourceGroupResourceRequest{
				AzureContext: azureContext,
				Options: &azdext.PromptResourceOptions{
					ResourceType:            "Microsoft.CognitiveServices/accounts",
					ResourceTypeDisplayName: "Azure OpenAI account",
					SelectOptions: &azdext.PromptResourceSelectOptions{
						AllowNewResource: new(false),
						Message:          "Select an Azure OpenAI account",
						LoadingMessage:   "Fetching Azure OpenAI accounts...",
					},
				},
			},
		)
		if err != nil {
			return model.DeploymentReference{}, fmt.Errorf("prompt for Azure OpenAI account: %w", err)
		}
		reference.AccountName = lastResourceIDSegment(response.Resource.Id)
	}

	if reference.DeploymentName == "" {
		response, err := client.Prompt().Prompt(ctx, &azdext.PromptRequest{
			Options: &azdext.PromptOptions{
				Message:         "Enter the model deployment name",
				Placeholder:     "my-model-deployment",
				Required:        true,
				RequiredMessage: "Deployment name is required",
			},
		})
		if err != nil {
			return model.DeploymentReference{}, fmt.Errorf("prompt for deployment name: %w", err)
		}
		reference.DeploymentName = strings.TrimSpace(response.Value)
	}

	if !completeReference(reference) {
		return model.DeploymentReference{}, missingDeploymentFieldsError(reference)
	}
	reference.DeploymentID = buildDeploymentID(reference)
	return reference, nil
}

func parseDeploymentID(value string) (model.DeploymentReference, error) {
	parts := strings.Split(strings.Trim(value, "/"), "/")
	if len(parts) != 10 ||
		!strings.EqualFold(parts[0], "subscriptions") ||
		!strings.EqualFold(parts[2], "resourceGroups") ||
		!strings.EqualFold(parts[4], "providers") ||
		!strings.EqualFold(parts[5], "Microsoft.CognitiveServices") ||
		!strings.EqualFold(parts[6], "accounts") ||
		!strings.EqualFold(parts[8], "deployments") {
		return model.DeploymentReference{}, validationError(
			"invalid_deployment_id",
			"--deployment-id must be a complete Microsoft.CognitiveServices account deployment resource ID.",
			"Copy the deployment ARM resource ID, including subscriptions, resourceGroups, accounts, and deployments.",
		)
	}
	for _, index := range []int{1, 3, 7, 9} {
		if strings.TrimSpace(parts[index]) == "" {
			return model.DeploymentReference{}, validationError(
				"invalid_deployment_id",
				"--deployment-id contains an empty resource segment.",
				"Provide a complete Azure deployment resource ID.",
			)
		}
	}
	return model.DeploymentReference{
		DeploymentID:   "/" + strings.Join(parts, "/"),
		Subscription:   parts[1],
		ResourceGroup:  parts[3],
		AccountName:    parts[7],
		DeploymentName: parts[9],
	}, nil
}

func buildDeploymentID(reference model.DeploymentReference) string {
	return fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.CognitiveServices/accounts/%s/deployments/%s",
		reference.Subscription,
		reference.ResourceGroup,
		reference.AccountName,
		reference.DeploymentName,
	)
}

func completeReference(reference model.DeploymentReference) bool {
	return reference.Subscription != "" && reference.ResourceGroup != "" &&
		reference.AccountName != "" && reference.DeploymentName != ""
}

func missingDeploymentFieldsError(reference model.DeploymentReference) error {
	missing := make([]string, 0, 4)
	fields := []struct {
		flag  string
		value string
	}{
		{flag: "--subscription", value: reference.Subscription},
		{flag: "--resource-group", value: reference.ResourceGroup},
		{flag: "--account-name", value: reference.AccountName},
		{flag: "--deployment-name", value: reference.DeploymentName},
	}
	for _, field := range fields {
		if field.value == "" {
			missing = append(missing, field.flag)
		}
	}
	return validationError(
		"missing_deployment_reference",
		fmt.Sprintf("A complete deployment reference is required; missing %s.", strings.Join(missing, ", ")),
		"Provide --deployment-id, or provide --subscription, --resource-group, --account-name, and --deployment-name.",
	)
}

func lookupTenant(ctx context.Context, client *azdext.AzdClient, subscription string) (string, error) {
	response, err := client.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
		SubscriptionId: subscription,
	})
	if err != nil {
		return "", fmt.Errorf("look up the user access tenant for subscription %s: %w", subscription, err)
	}
	if response.TenantId == "" {
		return "", &azdext.LocalError{
			Message:    fmt.Sprintf("No user access tenant was found for subscription %s.", subscription),
			Code:       "subscription_tenant_missing",
			Category:   azdext.LocalErrorCategoryAuth,
			Suggestion: "Run 'azd auth login' and verify access to the subscription.",
		}
	}
	return response.TenantId, nil
}

func missingAzdClientError() error {
	return &azdext.LocalError{
		Message:    "The azd host is required to resolve Azure authentication.",
		Code:       "azd_host_unavailable",
		Category:   azdext.LocalErrorCategoryDependency,
		Suggestion: "Run this command through 'azd ai latency assess' after 'azd auth login'.",
	}
}

func lastResourceIDSegment(value string) string {
	parts := strings.Split(strings.Trim(value, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}
