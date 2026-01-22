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

			_, err = azdClient.Prompt().MultiSelect(ctx, &azdext.MultiSelectRequest{
				Options: &azdext.MultiSelectOptions{
					Message: "Which Azure services do you use most with AZD?",
					Choices: []*azdext.MultiSelectChoice{
						{Label: "Container Apps", Value: "container-apps"},
						{Label: "Functions", Value: "functions"},
						{Label: "Static Web Apps", Value: "static-web-apps"},
						{Label: "App Service", Value: "app-service"},
						{Label: "Cosmos DB", Value: "cosmos-db"},
						{Label: "SQL Database", Value: "sql-db"},
						{Label: "Storage", Value: "storage"},
						{Label: "Key Vault", Value: "key-vault"},
						{Label: "Kubernetes Service", Value: "kubernetes-service"},
					},
				},
			})
			if err != nil {
				return nil
			}

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
				credential, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
					TenantID: azureContext.Scope.TenantId,
				})
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

				providerOptions := []*azdext.SelectChoice{}
				for _, provider := range providerList {
					providerOptions = append(providerOptions, &azdext.SelectChoice{
						Label: *provider.Namespace,
						Value: *provider.ID,
					})
				}

				providerSelectResponse, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
					Options: &azdext.SelectOptions{
						Message: "Select a resource provider",
						Choices: providerOptions,
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

				resourceTypeOptions := []*azdext.SelectChoice{}
				for _, resourceType := range resourceTypesResponse.Value {
					resourceTypeOptions = append(resourceTypeOptions, &azdext.SelectChoice{
						Label: *resourceType.ResourceType,
						Value: *resourceType.ResourceType,
					})
				}

				resourceTypes := []*armresources.ProviderResourceType{}
				resourceTypeSelectResponse, err := azdClient.
					Prompt().
					Select(ctx, &azdext.SelectRequest{
						Options: &azdext.SelectOptions{
							Message: fmt.Sprintf("Select a %s resource type", *selectedProvider.Namespace),
							Choices: resourceTypeOptions,
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
						Options: &azdext.PromptResourceGroupOptions{
							SelectOptions: &azdext.PromptResourceSelectOptions{
								AllowNewResource: to.Ptr(false),
							},
						},
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
