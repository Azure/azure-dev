// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

func newPromptCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "prompt",
		Short: "Examples of prompting the user for input.",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create a new context that includes the AZD access token
			ctx := azdext.WithAccessToken(cmd.Context())

			// Create a new AZD client
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}

			defer azdClient.Close()

			confirmResponse, err := azdClient.
				Prompt().
				Confirm(ctx, &azdext.ConfirmRequest{
					Options: &azdext.ConfirmOptions{
						Message:      "Do you want to search for Azure resources?",
						DefaultValue: to.Ptr(true),
					},
				})
			if err != nil {
				return err
			}

			if !*confirmResponse.Value {
				return nil
			}

			azureContext := azdext.AzureContext{
				Scope: &azdext.AzureScope{},
			}

			selectedSubscription, err := azdClient.
				Prompt().
				PromptSubscription(ctx, &azdext.PromptSubscriptionRequest{})
			if err != nil {
				return err
			}

			azureContext.Scope.SubscriptionId = selectedSubscription.Subscription.Id
			azureContext.Scope.TenantId = selectedSubscription.Subscription.TenantId

			filterByResourceTypeResponse, err := azdClient.
				Prompt().
				Confirm(ctx, &azdext.ConfirmRequest{
					Options: &azdext.ConfirmOptions{
						Message:      "Do you want to filter by resource type?",
						DefaultValue: to.Ptr(false),
					},
				})
			if err != nil {
				return err
			}

			fullResourceType := ""
			filterByResourceType := *filterByResourceTypeResponse.Value

			if filterByResourceType {
				credential, err := azidentity.NewAzureDeveloperCLICredential(nil)
				if err != nil {
					return err
				}

				providerList := []*armresources.Provider{}
				providersClient, err := armresources.NewProvidersClient(azureContext.Scope.SubscriptionId, credential, nil)
				if err != nil {
					return err
				}

				providerListPager := providersClient.NewListPager(nil)
				for providerListPager.More() {
					page, err := providerListPager.NextPage(ctx)
					if err != nil {
						return err
					}

					for _, provider := range page.ProviderListResult.Value {
						if *provider.RegistrationState == "Registered" {
							providerList = append(providerList, provider)
						}
					}
				}

				providerOptions := []string{}
				for _, provider := range providerList {
					providerOptions = append(providerOptions, *provider.Namespace)
				}

				providerSelectResponse, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
					Options: &azdext.SelectOptions{
						Message: "Select a resource provider",
						Allowed: providerOptions,
					},
				})
				if err != nil {
					return err
				}

				selectedProvider := providerList[*providerSelectResponse.Value]

				resourceTypesClient, err := armresources.NewProviderResourceTypesClient(
					azureContext.Scope.SubscriptionId,
					credential,
					nil,
				)
				if err != nil {
					return err
				}

				resourceTypesResponse, err := resourceTypesClient.List(ctx, *selectedProvider.Namespace, nil)
				if err != nil {
					return err
				}

				resourceTypeOptions := []string{}
				for _, resourceType := range resourceTypesResponse.Value {
					resourceTypeOptions = append(resourceTypeOptions, *resourceType.ResourceType)
				}

				resourceTypes := []*armresources.ProviderResourceType{}
				resourceTypeSelectResponse, err := azdClient.
					Prompt().
					Select(ctx, &azdext.SelectRequest{
						Options: &azdext.SelectOptions{
							Message: fmt.Sprintf("Select a %s resource type", *selectedProvider.Namespace),
							Allowed: resourceTypeOptions,
						},
					})
				if err != nil {
					return err
				}

				resourceTypes = append(resourceTypes, resourceTypesResponse.Value...)
				selectedResourceType := resourceTypes[*resourceTypeSelectResponse.Value]
				fullResourceType = fmt.Sprintf("%s/%s", *selectedProvider.Namespace, *selectedResourceType.ResourceType)
			}

			filterByResourceGroupResponse, err := azdClient.
				Prompt().
				Confirm(ctx, &azdext.ConfirmRequest{
					Options: &azdext.ConfirmOptions{
						Message:      "Do you want to filter by resource group?",
						DefaultValue: to.Ptr(false),
					},
				})
			if err != nil {
				return err
			}

			filterByResourceGroup := *filterByResourceGroupResponse.Value
			var selectedResource *azdext.ResourceExtended

			if filterByResourceGroup {
				selectedResourceGroup, err := azdClient.
					Prompt().
					PromptResourceGroup(ctx, &azdext.PromptResourceGroupRequest{
						AzureContext: &azureContext,
					})
				if err != nil {
					return err
				}

				azureContext.Scope.ResourceGroup = selectedResourceGroup.ResourceGroup.Name

				selectedResourceResponse, err := azdClient.
					Prompt().
					PromptResourceGroupResource(ctx, &azdext.PromptResourceGroupResourceRequest{
						AzureContext: &azureContext,
						Options: &azdext.PromptResourceOptions{
							ResourceType: fullResourceType,
							SelectOptions: &azdext.PromptResourceSelectOptions{
								AllowNewResource: to.Ptr(false),
							},
						},
					})
				if err != nil {
					return err
				}

				selectedResource = selectedResourceResponse.Resource
			} else {
				selectedResourceResponse, err := azdClient.
					Prompt().
					PromptSubscriptionResource(ctx, &azdext.PromptSubscriptionResourceRequest{
						AzureContext: &azureContext,
						Options: &azdext.PromptResourceOptions{
							ResourceType: fullResourceType,
							SelectOptions: &azdext.PromptResourceSelectOptions{
								AllowNewResource: to.Ptr(false),
							},
						},
					})
				if err != nil {
					return err
				}

				selectedResource = selectedResourceResponse.Resource
			}

			parsedResource, err := arm.ParseResourceID(selectedResource.Id)
			if err != nil {
				return err
			}

			fmt.Println()
			color.Cyan("Selected resource:")
			values := map[string]string{
				"Subscription ID": parsedResource.SubscriptionID,
				"Resource Group":  parsedResource.ResourceGroupName,
				"Name":            parsedResource.Name,
				"Type":            selectedResource.Type,
				"Location":        parsedResource.Location,
				"Kind":            selectedResource.Kind,
			}

			for key, value := range values {
				if value == "" {
					value = "N/A"
				}

				fmt.Printf("%s: %s\n", color.HiWhiteString(key), color.HiBlackString(value))
			}

			return nil
		},
	}
}
