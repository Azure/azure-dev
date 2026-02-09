// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package prompt

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"dario.cat/mergo"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
)

var (
	ErrNoResourcesFound   = fmt.Errorf("no resources found")
	ErrNoResourceSelected = fmt.Errorf("no resource selected")
)

// ResourceOptions contains options for prompting the user to select a resource.
type ResourceOptions struct {
	// ResourceType is the type of resource to select.
	ResourceType *azapi.AzureResourceType
	// Kinds is a list of resource kinds to filter by.
	Kinds []string
	// ResourceTypeDisplayName is the display name of the resource type.
	ResourceTypeDisplayName string
	// SelectorOptions contains options for the resource selector.
	SelectorOptions *SelectOptions
	// Selected is a function that determines if a resource is selected
	Selected func(resource *azapi.ResourceExtended) bool
}

// CustomResourceOptions contains options for prompting the user to select a custom resource.
type CustomResourceOptions[T any] struct {
	// SelectorOptions contains options for the resource selector.
	SelectorOptions *SelectOptions
	// LoadData is a function that loads the resource data.
	LoadData func(ctx context.Context) ([]*T, error)
	// DisplayResource is a function that displays the resource.
	DisplayResource func(resource *T) (string, error)
	// SortResource is a function that sorts the resources.
	SortResource func(a *T, b *T) int
	// Selected is a function that determines if a resource is selected
	Selected func(resource *T) bool
	// NewResourceValue is the default value returned when creating a new resource.
	NewResourceValue T
}

// ResourceGroupOptions contains options for prompting the user to select a resource group.
type ResourceGroupOptions struct {
	// SelectorOptions contains options for the resource group selector.
	SelectorOptions *SelectOptions
}

// SelectOptions contains options for prompting the user to select a resource.
type SelectOptions struct {
	// ForceNewResource specifies whether to force the user to create a new resource.
	ForceNewResource *bool
	// AllowNewResource specifies whether to allow the user to create a new resource.
	AllowNewResource *bool
	// NewResourceMessage is the message to display to the user when creating a new resource.
	NewResourceMessage string
	// Message is the message to display to the user.
	Message string
	// HelpMessage is the help message to display to the user.
	HelpMessage string
	// LoadingMessage is the loading message to display to the user.
	LoadingMessage string
	// DisplayNumbers specifies whether to display numbers next to the choices.
	DisplayNumbers *bool
	// DisplayCount is the number of choices to display at a time.
	DisplayCount int
	// Hint is the hint to display to the user.
	Hint string
	// EnableFiltering specifies whether to enable filtering of choices.
	EnableFiltering *bool
	// Writer is the writer to use for output.
	Writer io.Writer
}

type AuthManager interface {
	ClaimsForCurrentUser(ctx context.Context, options *auth.ClaimsForCurrentUserOptions) (auth.TokenClaims, error)
}

// ResourceService defines the methods that the ResourceService must implement.
type ResourceService interface {
	ListResourceGroup(
		ctx context.Context,
		subscriptionId string,
		listOptions *azapi.ListResourceGroupOptions,
	) ([]*azapi.Resource, error)
	ListResourceGroupResources(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		listOptions *azapi.ListResourceGroupResourcesOptions,
	) ([]*azapi.ResourceExtended, error)
	ListSubscriptionResources(
		ctx context.Context,
		subscriptionId string,
		listOptions *armresources.ClientListOptions,
	) ([]*azapi.ResourceExtended, error)
	CreateOrUpdateResourceGroup(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		location string,
		tags map[string]*string,
	) (*azapi.ResourceGroup, error)
	GetResource(
		ctx context.Context,
		subscriptionId string,
		resourceId string,
		apiVersion string,
	) (azapi.ResourceExtended, error)
}

// SubscriptionManager defines the methods that the SubscriptionManager must implement.
type SubscriptionManager interface {
	GetSubscriptions(ctx context.Context) ([]account.Subscription, error)
	GetLocations(ctx context.Context, subscriptionId string) ([]account.Location, error)
}

// PromptServiceInterface defines the methods that the PromptService must implement.
type PromptService interface {
	PromptSubscription(ctx context.Context, selectorOptions *SelectOptions) (*account.Subscription, error)
	PromptLocation(
		ctx context.Context,
		azureContext *AzureContext,
		selectorOptions *SelectOptions,
	) (*account.Location, error)
	PromptResourceGroup(
		ctx context.Context,
		azureContext *AzureContext,
		options *ResourceGroupOptions,
	) (*azapi.ResourceGroup, error)
	PromptSubscriptionResource(
		ctx context.Context,
		azureContext *AzureContext,
		options ResourceOptions,
	) (*azapi.ResourceExtended, error)
	PromptResourceGroupResource(
		ctx context.Context,
		azureContext *AzureContext,
		options ResourceOptions,
	) (*azapi.ResourceExtended, error)
}

// PromptService provides methods for prompting the user to select various Azure resources.
type promptService struct {
	authManager         AuthManager
	userConfigManager   config.UserConfigManager
	resourceService     ResourceService
	subscriptionManager SubscriptionManager
	globalOptions       *internal.GlobalCommandOptions
}

// NewPromptService creates a new prompt service.
func NewPromptService(
	authManager AuthManager,
	userConfigManager config.UserConfigManager,
	subscriptionManager SubscriptionManager,
	resourceService ResourceService,
	globalOptions *internal.GlobalCommandOptions,
) PromptService {
	return &promptService{
		authManager:         authManager,
		userConfigManager:   userConfigManager,
		subscriptionManager: subscriptionManager,
		resourceService:     resourceService,
		globalOptions:       globalOptions,
	}
}

// PromptSubscription prompts the user to select an Azure subscription.
func (ps *promptService) PromptSubscription(
	ctx context.Context,
	selectorOptions *SelectOptions,
) (*account.Subscription, error) {
	mergedOptions := &SelectOptions{}
	if selectorOptions == nil {
		selectorOptions = &SelectOptions{}
	}

	defaultOptions := &SelectOptions{
		Message:          "Select subscription",
		LoadingMessage:   "Loading subscriptions...",
		HelpMessage:      "Choose an Azure subscription for your project.",
		AllowNewResource: ux.Ptr(false),
	}

	if err := mergo.Merge(mergedOptions, selectorOptions, mergo.WithoutDereference); err != nil {
		return nil, err
	}

	if err := mergo.Merge(mergedOptions, defaultOptions, mergo.WithoutDereference); err != nil {
		return nil, err
	}

	// Get default subscription from user config
	var defaultSubscriptionId = ""
	userConfig, err := ps.userConfigManager.Load()
	if err == nil {
		userSubscription, exists := userConfig.GetString("defaults.subscription")
		if exists && userSubscription != "" {
			defaultSubscriptionId = userSubscription
		}
	}

	// Handle --no-prompt mode
	if ps.globalOptions.NoPrompt {
		if defaultSubscriptionId == "" {
			return nil, fmt.Errorf(
				"subscription selection required but cannot prompt in --no-prompt mode. " +
					"Set a default subscription using 'azd config set defaults.subscription <subscription-id>'")
		}

		// Load subscriptions and find the default
		subscriptionList, err := ps.subscriptionManager.GetSubscriptions(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to load subscriptions: %w", err)
		}

		for _, subscription := range subscriptionList {
			if strings.EqualFold(subscription.Id, defaultSubscriptionId) {
				return &subscription, nil
			}
		}

		return nil, fmt.Errorf(
			"default subscription '%s' not found. "+
				"Update your default subscription using 'azd config set defaults.subscription <subscription-id>'",
			defaultSubscriptionId)
	}

	return PromptCustomResource(ctx, CustomResourceOptions[account.Subscription]{
		SelectorOptions: mergedOptions,
		LoadData: func(ctx context.Context) ([]*account.Subscription, error) {
			subscriptionList, err := ps.subscriptionManager.GetSubscriptions(ctx)
			if err != nil {
				return nil, err
			}

			subscriptions := make([]*account.Subscription, len(subscriptionList))
			for i, subscription := range subscriptionList {
				subscriptions[i] = &subscription
			}

			return subscriptions, nil
		},
		DisplayResource: func(subscription *account.Subscription) (string, error) {
			return fmt.Sprintf("%s %s", subscription.Name, output.WithGrayFormat("(%s)", subscription.Id)), nil
		},
		Selected: func(subscription *account.Subscription) bool {
			return strings.EqualFold(subscription.Id, defaultSubscriptionId)
		},
	})
}

// PromptLocation prompts the user to select an Azure location.
func (ps *promptService) PromptLocation(
	ctx context.Context,
	azureContext *AzureContext,
	selectorOptions *SelectOptions,
) (*account.Location, error) {
	if azureContext == nil {
		azureContext = NewEmptyAzureContext()
	}

	if err := azureContext.EnsureSubscription(ctx); err != nil {
		return nil, err
	}

	mergedOptions := &SelectOptions{}

	if selectorOptions == nil {
		selectorOptions = &SelectOptions{}
	}

	defaultOptions := &SelectOptions{
		Message:          "Select location",
		LoadingMessage:   "Loading locations...",
		HelpMessage:      "Choose an Azure location for your project.",
		AllowNewResource: ux.Ptr(false),
	}

	if err := mergo.Merge(mergedOptions, selectorOptions, mergo.WithoutDereference); err != nil {
		return nil, err
	}

	if err := mergo.Merge(mergedOptions, defaultOptions, mergo.WithoutDereference); err != nil {
		return nil, err
	}

	// Get default location from user config
	var defaultLocation = "eastus2"
	userConfig, err := ps.userConfigManager.Load()
	if err == nil {
		userLocation, exists := userConfig.GetString("defaults.location")
		if exists && userLocation != "" {
			defaultLocation = userLocation
		}
	}

	// Handle --no-prompt mode
	if ps.globalOptions.NoPrompt {
		// Default location always exists (fallback to eastus2), so we can use it
		// Load locations and find the default
		locationList, err := ps.subscriptionManager.GetLocations(
			ctx,
			azureContext.Scope.SubscriptionId,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to load locations: %w", err)
		}

		for _, location := range locationList {
			if location.Name == defaultLocation {
				return &account.Location{
					Name:                location.Name,
					DisplayName:         location.DisplayName,
					RegionalDisplayName: location.RegionalDisplayName,
				}, nil
			}
		}

		return nil, fmt.Errorf(
			"default location '%s' not found. "+
				"Update your default location using 'azd config set defaults.location <location-name>'",
			defaultLocation)
	}

	return PromptCustomResource(ctx, CustomResourceOptions[account.Location]{
		SelectorOptions: mergedOptions,
		LoadData: func(ctx context.Context) ([]*account.Location, error) {
			locationList, err := ps.subscriptionManager.GetLocations(
				ctx,
				azureContext.Scope.SubscriptionId,
			)
			if err != nil {
				return nil, err
			}

			locations := make([]*account.Location, len(locationList))
			for i, location := range locationList {
				locations[i] = &account.Location{
					Name:                location.Name,
					DisplayName:         location.DisplayName,
					RegionalDisplayName: location.RegionalDisplayName,
				}
			}

			return locations, nil
		},
		DisplayResource: func(location *account.Location) (string, error) {
			return fmt.Sprintf("%s %s", location.RegionalDisplayName, output.WithGrayFormat("(%s)", location.Name)), nil
		},
		Selected: func(resource *account.Location) bool {
			return resource.Name == defaultLocation
		},
	})
}

// PromptResourceGroup prompts the user to select an Azure resource group.
func (ps *promptService) PromptResourceGroup(
	ctx context.Context,
	azureContext *AzureContext,
	options *ResourceGroupOptions,
) (*azapi.ResourceGroup, error) {
	if azureContext == nil {
		azureContext = NewEmptyAzureContext()
	}

	if err := azureContext.EnsureSubscription(ctx); err != nil {
		return nil, err
	}

	mergedSelectorOptions := &SelectOptions{}

	if options == nil {
		options = &ResourceGroupOptions{}
	}

	if options.SelectorOptions == nil {
		options.SelectorOptions = &SelectOptions{}
	}

	defaultSelectorOptions := &SelectOptions{
		Message:            "Select resource group",
		LoadingMessage:     "Loading resource groups...",
		HelpMessage:        "Choose an Azure resource group for your project.",
		AllowNewResource:   ux.Ptr(true),
		NewResourceMessage: "Create new resource group",
	}

	if err := mergo.Merge(mergedSelectorOptions, options.SelectorOptions, mergo.WithoutDereference); err != nil {
		return nil, err
	}

	if err := mergo.Merge(mergedSelectorOptions, defaultSelectorOptions, mergo.WithoutDereference); err != nil {
		return nil, err
	}

	// Handle --no-prompt mode
	if ps.globalOptions.NoPrompt {
		if azureContext.Scope.ResourceGroup == "" {
			return nil, fmt.Errorf(
				"resource group selection required but cannot prompt in --no-prompt mode. " +
					"Specify a resource group explicitly in your configuration or environment")
		}

		// Load resource groups and find the one specified in context
		resourceGroupList, err := ps.resourceService.ListResourceGroup(ctx, azureContext.Scope.SubscriptionId, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to load resource groups: %w", err)
		}

		for _, resourceGroup := range resourceGroupList {
			if resourceGroup.Name == azureContext.Scope.ResourceGroup {
				return &azapi.ResourceGroup{
					Id:       resourceGroup.Id,
					Name:     resourceGroup.Name,
					Location: resourceGroup.Location,
				}, nil
			}
		}

		return nil, fmt.Errorf(
			"resource group '%s' not found in subscription",
			azureContext.Scope.ResourceGroup)
	}

	return PromptCustomResource(ctx, CustomResourceOptions[azapi.ResourceGroup]{
		NewResourceValue: azapi.ResourceGroup{Id: "new"},
		SelectorOptions:  mergedSelectorOptions,
		LoadData: func(ctx context.Context) ([]*azapi.ResourceGroup, error) {
			resourceGroupList, err := ps.resourceService.ListResourceGroup(ctx, azureContext.Scope.SubscriptionId, nil)
			if err != nil {
				return nil, err
			}

			resourceGroups := make([]*azapi.ResourceGroup, len(resourceGroupList))
			for i, resourceGroup := range resourceGroupList {
				resourceGroups[i] = &azapi.ResourceGroup{
					Id:       resourceGroup.Id,
					Name:     resourceGroup.Name,
					Location: resourceGroup.Location,
				}
			}

			return resourceGroups, nil
		},
		DisplayResource: func(resourceGroup *azapi.ResourceGroup) (string, error) {
			return fmt.Sprintf(
				"%s %s",
				resourceGroup.Name,
				output.WithGrayFormat("(Location: %s)", resourceGroup.Location),
			), nil
		},
		Selected: func(resourceGroup *azapi.ResourceGroup) bool {
			return resourceGroup.Name == azureContext.Scope.ResourceGroup
		},
	})
}

// PromptSubscriptionResource prompts the user to select an Azure resource from the subscription specified in the context.
func (ps *promptService) PromptSubscriptionResource(
	ctx context.Context,
	azureContext *AzureContext,
	options ResourceOptions,
) (*azapi.ResourceExtended, error) {
	if azureContext == nil {
		azureContext = NewEmptyAzureContext()
	}

	if err := azureContext.EnsureSubscription(ctx); err != nil {
		return nil, err
	}

	mergedSelectorOptions := &SelectOptions{}

	if options.SelectorOptions == nil {
		options.SelectorOptions = &SelectOptions{}
	}

	var existingResource *arm.ResourceID
	var resourceType string
	if options.ResourceType != nil {
		resourceType = string(*options.ResourceType)
		match, has := azureContext.Resources.FindByTypeAndKind(ctx, *options.ResourceType, options.Kinds)
		if has {
			existingResource = match
		}
	}

	if options.Selected == nil {
		options.Selected = func(resource *azapi.ResourceExtended) bool {
			if existingResource == nil {
				return false
			}

			if strings.EqualFold(resource.Id, existingResource.String()) {
				return true
			}

			return false
		}
	}

	resourceName := options.ResourceTypeDisplayName

	if resourceName == "" && options.ResourceType != nil {
		resourceName = string(*options.ResourceType)
	}

	if resourceName == "" {
		resourceName = "resource"
	}

	defaultSelectorOptions := &SelectOptions{
		Message:            fmt.Sprintf("Select %s", resourceName),
		LoadingMessage:     fmt.Sprintf("Loading %s resources...", resourceName),
		HelpMessage:        fmt.Sprintf("Choose an Azure %s for your project.", resourceName),
		AllowNewResource:   ux.Ptr(true),
		NewResourceMessage: fmt.Sprintf("Create new %s", resourceName),
	}

	if err := mergo.Merge(mergedSelectorOptions, options.SelectorOptions, mergo.WithoutDereference); err != nil {
		return nil, err
	}

	if err := mergo.Merge(mergedSelectorOptions, defaultSelectorOptions, mergo.WithoutDereference); err != nil {
		return nil, err
	}

	// Handle --no-prompt mode
	if ps.globalOptions.NoPrompt {
		return nil, fmt.Errorf(
			"%s selection required but cannot prompt in --no-prompt mode. "+
				"Please specify the resource using an environment variable or configuration file",
			resourceName)
	}

	allowNewResource := mergedSelectorOptions.AllowNewResource != nil && *mergedSelectorOptions.AllowNewResource

	resource, err := PromptCustomResource(ctx, CustomResourceOptions[azapi.ResourceExtended]{
		NewResourceValue: azapi.ResourceExtended{
			Resource: azapi.Resource{
				Id:   "new",
				Type: resourceType,
			},
		},
		SelectorOptions: mergedSelectorOptions,
		LoadData: func(ctx context.Context) ([]*azapi.ResourceExtended, error) {
			var resourceListOptions *armresources.ClientListOptions
			if options.ResourceType != nil {
				resourceListOptions = &armresources.ClientListOptions{
					Filter: to.Ptr(fmt.Sprintf("resourceType eq '%s'", string(*options.ResourceType))),
				}
			}

			resourceList, err := ps.resourceService.ListSubscriptionResources(
				ctx,
				azureContext.Scope.SubscriptionId,
				resourceListOptions,
			)
			if err != nil {
				return nil, err
			}

			filteredResources := []*azapi.ResourceExtended{}
			hasKindFilter := len(options.Kinds) > 0

			for _, resource := range resourceList {
				if !hasKindFilter || slices.Contains(options.Kinds, resource.Kind) {
					filteredResources = append(filteredResources, resource)
				}
			}

			if len(filteredResources) == 0 && !allowNewResource {
				if options.ResourceType == nil {
					return nil, ErrNoResourcesFound
				}

				return nil, fmt.Errorf("no resources found with type '%v'", *options.ResourceType)
			}

			return filteredResources, nil
		},
		DisplayResource: func(resource *azapi.ResourceExtended) (string, error) {
			parsedResource, err := arm.ParseResourceID(resource.Id)
			if err != nil {
				return "", fmt.Errorf("parsing resource id: %w", err)
			}

			return fmt.Sprintf(
				"%s %s",
				parsedResource.Name,
				output.WithGrayFormat("(%s)", parsedResource.ResourceGroupName),
			), nil
		},
		Selected: options.Selected,
	})

	if err != nil {
		return nil, err
	}

	if err := azureContext.Resources.Add(resource.Id); err != nil {
		return nil, err
	}

	return resource, nil
}

// PromptResourceGroupResource prompts the user to select an Azure resource from the resource group specified in the context.
func (ps *promptService) PromptResourceGroupResource(
	ctx context.Context,
	azureContext *AzureContext,
	options ResourceOptions,
) (*azapi.ResourceExtended, error) {
	if azureContext == nil {
		azureContext = NewEmptyAzureContext()
	}

	if err := azureContext.EnsureResourceGroup(ctx); err != nil {
		return nil, err
	}

	mergedSelectorOptions := &SelectOptions{}

	if options.SelectorOptions == nil {
		options.SelectorOptions = &SelectOptions{}
	}

	var existingResource *arm.ResourceID
	var resourceType string
	if options.ResourceType != nil {
		resourceType = string(*options.ResourceType)
		match, has := azureContext.Resources.FindByTypeAndKind(ctx, *options.ResourceType, options.Kinds)
		if has {
			existingResource = match
		}
	}

	if options.Selected == nil {
		options.Selected = func(resource *azapi.ResourceExtended) bool {
			if existingResource == nil {
				return false
			}

			return strings.EqualFold(resource.Id, existingResource.String())
		}
	}

	resourceName := options.ResourceTypeDisplayName

	if resourceName == "" && options.ResourceType != nil {
		resourceName = string(*options.ResourceType)
	}

	if resourceName == "" {
		resourceName = "resource"
	}

	defaultSelectorOptions := &SelectOptions{
		Message:            fmt.Sprintf("Select %s", resourceName),
		LoadingMessage:     fmt.Sprintf("Loading %s resources...", resourceName),
		HelpMessage:        fmt.Sprintf("Choose an Azure %s for your project.", resourceName),
		AllowNewResource:   ux.Ptr(true),
		NewResourceMessage: fmt.Sprintf("Create new %s", resourceName),
	}

	if err := mergo.Merge(mergedSelectorOptions, options.SelectorOptions, mergo.WithoutDereference); err != nil {
		return nil, err
	}

	if err := mergo.Merge(mergedSelectorOptions, defaultSelectorOptions, mergo.WithoutDereference); err != nil {
		return nil, err
	}

	// Handle --no-prompt mode
	if ps.globalOptions.NoPrompt {
		return nil, fmt.Errorf(
			"%s selection required but cannot prompt in --no-prompt mode. "+
				"Please specify the resource using an environment variable or configuration file",
			resourceName)
	}

	allowNewResource := mergedSelectorOptions.AllowNewResource != nil && *mergedSelectorOptions.AllowNewResource

	resource, err := PromptCustomResource(ctx, CustomResourceOptions[azapi.ResourceExtended]{
		NewResourceValue: azapi.ResourceExtended{
			Resource: azapi.Resource{
				Id:   "new",
				Type: resourceType,
			},
		},
		Selected:        options.Selected,
		SelectorOptions: mergedSelectorOptions,
		LoadData: func(ctx context.Context) ([]*azapi.ResourceExtended, error) {
			var resourceListOptions *azapi.ListResourceGroupResourcesOptions
			if options.ResourceType != nil {
				resourceListOptions = &azapi.ListResourceGroupResourcesOptions{
					Filter: to.Ptr(fmt.Sprintf("resourceType eq '%s'", *options.ResourceType)),
				}
			}

			resourceList, err := ps.resourceService.ListResourceGroupResources(
				ctx,
				azureContext.Scope.SubscriptionId,
				azureContext.Scope.ResourceGroup,
				resourceListOptions,
			)
			if err != nil {
				return nil, err
			}

			filteredResources := []*azapi.ResourceExtended{}
			hasKindFilter := len(options.Kinds) > 0

			for _, resource := range resourceList {
				if !hasKindFilter || slices.Contains(options.Kinds, resource.Kind) {
					filteredResources = append(filteredResources, resource)
				}
			}

			if len(filteredResources) == 0 && !allowNewResource {
				if options.ResourceType == nil {
					return nil, ErrNoResourcesFound
				}

				return nil, fmt.Errorf("no resources found with type '%v'", *options.ResourceType)
			}

			return filteredResources, nil
		},
		DisplayResource: func(resource *azapi.ResourceExtended) (string, error) {
			return resource.Name, nil
		},
	})

	if err != nil {
		return nil, err
	}

	if err := azureContext.Resources.Add(resource.Id); err != nil {
		return nil, err
	}

	return resource, nil
}

// PromptCustomResource prompts the user to select a custom resource from a list of resources.
// This function is used internally to power selection of subscriptions, resource groups and other resources.
// This can be used directly when the list of resources require integration with other Azure SDKs for resource selection.
func PromptCustomResource[T any](ctx context.Context, options CustomResourceOptions[T]) (*T, error) {
	mergedSelectorOptions := &SelectOptions{}

	if options.SelectorOptions == nil {
		options.SelectorOptions = &SelectOptions{}
	}

	defaultSelectorOptions := &SelectOptions{
		Message:            "Select resource",
		LoadingMessage:     "Loading resources...",
		HelpMessage:        "Choose a resource for your project.",
		AllowNewResource:   ux.Ptr(true),
		ForceNewResource:   ux.Ptr(false),
		NewResourceMessage: "Create new resource",
		DisplayNumbers:     ux.Ptr(true),
		DisplayCount:       10,
	}

	if err := mergo.Merge(mergedSelectorOptions, options.SelectorOptions, mergo.WithoutDereference); err != nil {
		return nil, err
	}

	if err := mergo.Merge(mergedSelectorOptions, defaultSelectorOptions, mergo.WithoutDereference); err != nil {
		return nil, err
	}

	allowNewResource := mergedSelectorOptions.AllowNewResource != nil && *mergedSelectorOptions.AllowNewResource
	forceNewResource := mergedSelectorOptions.ForceNewResource != nil && *mergedSelectorOptions.ForceNewResource

	var resources []*T
	var selectedIndex *int

	if forceNewResource {
		allowNewResource = true
		selectedIndex = ux.Ptr(0)
	} else {
		loadingSpinner := ux.NewSpinner(&ux.SpinnerOptions{
			Text: options.SelectorOptions.LoadingMessage,
		})

		err := loadingSpinner.Run(ctx, func(ctx context.Context) error {
			resourceList, err := options.LoadData(ctx)
			if err != nil {
				return err
			}

			resources = resourceList
			return nil
		})
		if err != nil {
			return nil, err
		}

		if !allowNewResource && len(resources) == 0 {
			return nil, ErrNoResourcesFound
		}

		if options.SortResource != nil {
			slices.SortFunc(resources, options.SortResource)
		}

		var defaultIndex *int
		if options.Selected != nil {
			for i, resource := range resources {
				if options.Selected(resource) {
					defaultIndex = &i
					break
				}
			}
		}

		hasCustomDisplay := options.DisplayResource != nil

		var choices []*ux.SelectChoice

		if allowNewResource {
			choices = make([]*ux.SelectChoice, len(resources)+1)
			choices[0] = &ux.SelectChoice{
				Label: mergedSelectorOptions.NewResourceMessage,
			}

			if defaultIndex != nil {
				*defaultIndex++
			}
		} else {
			choices = make([]*ux.SelectChoice, len(resources))
		}

		for i, resource := range resources {
			var displayValue string

			if hasCustomDisplay {
				customDisplayValue, err := options.DisplayResource(resource)
				if err != nil {
					return nil, err
				}

				displayValue = customDisplayValue
			} else {
				displayValue = fmt.Sprintf("%v", resource)
			}

			choice := &ux.SelectChoice{
				Value: displayValue,
				Label: displayValue,
			}

			if allowNewResource {
				choices[i+1] = choice
			} else {
				choices[i] = choice
			}
		}

		resourceSelector := ux.NewSelect(&ux.SelectOptions{
			Message:         mergedSelectorOptions.Message,
			HelpMessage:     mergedSelectorOptions.HelpMessage,
			DisplayCount:    mergedSelectorOptions.DisplayCount,
			DisplayNumbers:  mergedSelectorOptions.DisplayNumbers,
			Hint:            mergedSelectorOptions.Hint,
			EnableFiltering: mergedSelectorOptions.EnableFiltering,
			Writer:          mergedSelectorOptions.Writer,
			Choices:         choices,
			SelectedIndex:   defaultIndex,
		})

		userSelectedIndex, err := resourceSelector.Ask(ctx)
		if err != nil {
			return nil, err
		}

		if userSelectedIndex == nil {
			return nil, ErrNoResourceSelected
		}

		selectedIndex = userSelectedIndex
	}

	var selectedResource *T

	// Create new resource
	if allowNewResource && *selectedIndex == 0 {
		selectedResource = &options.NewResourceValue
	} else {
		// If a new resource is allowed, decrement the selected index
		if allowNewResource {
			*selectedIndex--
		}

		selectedResource = resources[*selectedIndex]
	}

	return selectedResource, nil
}
