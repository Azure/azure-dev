package prompt

import (
	"context"
	"errors"
	"fmt"
	"log"

	"dario.cat/mergo"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"github.com/azure/azure-dev/cli/sdk/azdcore/azure"
	"github.com/azure/azure-dev/cli/sdk/azdcore/ext"
	"github.com/azure/azure-dev/cli/sdk/azdcore/ux"
)

var (
	ErrNoResourcesFound = fmt.Errorf("no resources found")
)

type PromptResourceOptions struct {
	ResourceType            *azure.ResourceType
	ResourceTypeDisplayName string
	AllowNewResource        bool
	SelectorOptions         *PromptSelectOptions
}

type PromptResourceGroupOptions struct {
	AllowNewResource bool
	SelectorOptions  *PromptSelectOptions
}

type PromptSelectOptions struct {
	Message        string
	HelpMessage    string
	LoadingMessage string
	DisplayNumbers *bool
	DisplayCount   int
}

func PromptSubscription(ctx context.Context, selectorOptions *PromptSelectOptions) (*azure.Subscription, error) {
	if selectorOptions == nil {
		selectorOptions = &PromptSelectOptions{}
	}

	mergo.Merge(selectorOptions, &PromptSelectOptions{
		Message:        "Select subscription",
		LoadingMessage: "Loading subscriptions...",
		HelpMessage:    "Choose an Azure subscription for your project.",
		DisplayNumbers: ux.Ptr(true),
		DisplayCount:   10,
	})

	azdContext, err := ext.CurrentContext(ctx)
	if err != nil {
		return nil, err
	}

	principal, err := azdContext.Principal(ctx)
	if err != nil {
		return nil, err
	}

	credential, err := azdContext.Credential()
	if err != nil {
		return nil, err
	}

	userConfig, err := azdContext.UserConfig(ctx)
	if err != nil {
		log.Println("User config not found")
	}

	subscriptionService := azure.NewSubscriptionsService(credential, nil)

	loadingSpinner := ux.NewSpinner(&ux.SpinnerConfig{
		Text:        selectorOptions.LoadingMessage,
		ClearOnStop: true,
	})

	var subscriptions []*armsubscriptions.Subscription

	err = loadingSpinner.Run(ctx, func(ctx context.Context) error {
		subscriptionList, err := subscriptionService.ListSubscriptions(ctx, principal.TenantId)
		if err != nil {
			return err
		}

		subscriptions = subscriptionList
		return nil
	})
	if err != nil {
		return nil, err
	}

	var defaultIndex *int
	var defaultSubscriptionId = ""
	if userConfig != nil {
		subscriptionId, has := userConfig.GetString("defaults.subscription")
		if has {
			defaultSubscriptionId = subscriptionId
		}
	}

	for i, subscription := range subscriptions {
		if *subscription.SubscriptionID == defaultSubscriptionId {
			defaultIndex = &i
			break
		}
	}

	choices := make([]string, len(subscriptions))
	for i, subscription := range subscriptions {
		choices[i] = fmt.Sprintf("%s (%s)", *subscription.DisplayName, *subscription.SubscriptionID)
	}

	subscriptionSelector := ux.NewSelect(&ux.SelectConfig{
		Message:        selectorOptions.Message,
		HelpMessage:    selectorOptions.HelpMessage,
		DisplayCount:   selectorOptions.DisplayCount,
		DisplayNumbers: selectorOptions.DisplayNumbers,
		Allowed:        choices,
		DefaultIndex:   defaultIndex,
	})

	selectedIndex, err := subscriptionSelector.Ask()
	if err != nil {
		return nil, err
	}

	if selectedIndex == nil {
		return nil, nil
	}

	selectedSubscription := subscriptions[*selectedIndex]

	return &azure.Subscription{
		Id:                 *selectedSubscription.SubscriptionID,
		Name:               *selectedSubscription.DisplayName,
		TenantId:           *selectedSubscription.TenantID,
		UserAccessTenantId: principal.TenantId,
	}, nil
}

func PromptLocation(
	ctx context.Context,
	subscription *azure.Subscription,
	selectorOptions *PromptSelectOptions,
) (*azure.Location, error) {
	if selectorOptions == nil {
		selectorOptions = &PromptSelectOptions{}
	}

	mergo.Merge(selectorOptions, &PromptSelectOptions{
		Message:        "Select location",
		LoadingMessage: "Loading locations...",
		HelpMessage:    "Choose an Azure location for your project.",
		DisplayNumbers: ux.Ptr(true),
		DisplayCount:   10,
	})

	azdContext, err := ext.CurrentContext(ctx)
	if err != nil {
		return nil, err
	}

	credential, err := azdContext.Credential()
	if err != nil {
		return nil, err
	}

	userConfig, err := azdContext.UserConfig(ctx)
	if errors.Is(err, ext.ErrUserConfigNotFound) {
		log.Println("User config not found")
	}

	loadingSpinner := ux.NewSpinner(&ux.SpinnerConfig{
		Text:        selectorOptions.LoadingMessage,
		ClearOnStop: true,
	})

	var locations []azure.Location

	err = loadingSpinner.Run(ctx, func(ctx context.Context) error {
		subscriptionService := azure.NewSubscriptionsService(credential, nil)
		locationList, err := subscriptionService.ListSubscriptionLocations(ctx, subscription.Id, subscription.TenantId)
		if err != nil {
			return err
		}

		locations = locationList

		return nil
	})

	if err != nil {
		return nil, err
	}

	var defaultIndex *int
	var defaultLocation = "eastus2"
	if userConfig != nil {
		location, has := userConfig.GetString("defaults.location")
		if has {
			defaultLocation = location
		}
	}

	for i, location := range locations {
		if location.Name == defaultLocation {
			defaultIndex = &i
			break
		}
	}

	choices := make([]string, len(locations))
	for i, location := range locations {
		choices[i] = fmt.Sprintf("%s (%s)", location.RegionalDisplayName, location.Name)
	}

	locationSelector := ux.NewSelect(&ux.SelectConfig{
		Message:        selectorOptions.Message,
		HelpMessage:    selectorOptions.HelpMessage,
		DisplayCount:   selectorOptions.DisplayCount,
		DisplayNumbers: selectorOptions.DisplayNumbers,
		Allowed:        choices,
		DefaultIndex:   defaultIndex,
	})

	selectedIndex, err := locationSelector.Ask()
	if err != nil {
		return nil, err
	}

	if selectedIndex == nil {
		return nil, nil
	}

	selectedLocation := locations[*selectedIndex]

	return &azure.Location{
		Name:                selectedLocation.Name,
		DisplayName:         selectedLocation.DisplayName,
		RegionalDisplayName: selectedLocation.RegionalDisplayName,
	}, nil
}

func PromptResourceGroup(
	ctx context.Context,
	subscription *azure.Subscription,
	options *PromptResourceGroupOptions,
) (*azure.ResourceGroup, error) {
	if options == nil {
		options = &PromptResourceGroupOptions{}
	}

	if options.SelectorOptions == nil {
		options.SelectorOptions = &PromptSelectOptions{}
	}

	mergo.Merge(options.SelectorOptions, &PromptSelectOptions{
		Message:        "Select resource group",
		LoadingMessage: "Loading resource groups...",
		HelpMessage:    "Choose an Azure resource group for your project.",
		DisplayNumbers: ux.Ptr(true),
		DisplayCount:   10,
	})

	azdContext, err := ext.CurrentContext(ctx)
	if err != nil {
		return nil, err
	}

	credential, err := azdContext.Credential()
	if err != nil {
		return nil, err
	}

	loadingSpinner := ux.NewSpinner(&ux.SpinnerConfig{
		Text: options.SelectorOptions.LoadingMessage,
	})

	var resourceGroups []*azure.Resource

	err = loadingSpinner.Run(ctx, func(ctx context.Context) error {
		resourceService := azure.NewResourceService(credential, nil)
		resourceGroupList, err := resourceService.ListResourceGroup(ctx, subscription.Id, nil)
		if err != nil {
			return err
		}

		resourceGroups = resourceGroupList
		return nil
	})

	if err != nil {
		return nil, err
	}

	choices := make([]string, len(resourceGroups))
	for i, resourceGroup := range resourceGroups {
		choices[i] = resourceGroup.Name
	}

	resourceGroupSelector := ux.NewSelect(&ux.SelectConfig{
		Message:        options.SelectorOptions.Message,
		HelpMessage:    options.SelectorOptions.HelpMessage,
		DisplayCount:   options.SelectorOptions.DisplayCount,
		DisplayNumbers: options.SelectorOptions.DisplayNumbers,
		Allowed:        choices,
	})

	selectedIndex, err := resourceGroupSelector.Ask()
	if err != nil {
		return nil, err
	}

	if selectedIndex == nil {
		return nil, nil
	}

	selectedResourceGroup := resourceGroups[*selectedIndex]

	return &azure.ResourceGroup{
		Id:       selectedResourceGroup.Id,
		Name:     selectedResourceGroup.Name,
		Location: selectedResourceGroup.Location,
	}, nil
}

func PromptSubscriptionResource(
	ctx context.Context,
	subscription *azure.Subscription,
	options PromptResourceOptions,
) (*azure.Resource, error) {
	if options.SelectorOptions == nil {
		resourceName := options.ResourceTypeDisplayName

		if resourceName == "" && options.ResourceType != nil {
			resourceName = string(*options.ResourceType)
		}

		if resourceName == "" {
			resourceName = "resource"
		}

		options.SelectorOptions = &PromptSelectOptions{
			Message:        fmt.Sprintf("Select %s", resourceName),
			LoadingMessage: fmt.Sprintf("Loading %s resources...", resourceName),
			HelpMessage:    fmt.Sprintf("Choose an Azure %s for your project.", resourceName),
			DisplayNumbers: ux.Ptr(true),
			DisplayCount:   10,
		}
	}

	azdContext, err := ext.CurrentContext(ctx)
	if err != nil {
		return nil, err
	}

	credential, err := azdContext.Credential()
	if err != nil {
		return nil, err
	}

	var resourceListOptions *armresources.ClientListOptions
	if options.ResourceType != nil {
		resourceListOptions = &armresources.ClientListOptions{
			Filter: to.Ptr(fmt.Sprintf("resourceType eq '%s'", string(*options.ResourceType))),
		}
	}

	loadingSpinner := ux.NewSpinner(&ux.SpinnerConfig{
		Text: options.SelectorOptions.LoadingMessage,
	})

	var resources []*azure.Resource

	err = loadingSpinner.Run(ctx, func(ctx context.Context) error {
		resourceService := azure.NewResourceService(credential, nil)
		resourceList, err := resourceService.ListSubscriptionResources(ctx, subscription.Id, resourceListOptions)
		if err != nil {
			return err
		}

		resources = resourceList
		return nil
	})

	if err != nil {
		return nil, err
	}

	if len(resources) == 0 {
		return nil, fmt.Errorf("no resources found with type '%v'", options.ResourceType)
	}

	choices := make([]string, len(resources))
	for i, resource := range resources {
		parsedResource, err := arm.ParseResourceID(resource.Id)
		if err != nil {
			return nil, fmt.Errorf("parsing resource id: %w", err)
		}

		choices[i] = fmt.Sprintf("%s (%s)", parsedResource.Name, parsedResource.ResourceGroupName)
	}

	resourceSelector := ux.NewSelect(&ux.SelectConfig{
		Message:        options.SelectorOptions.Message,
		HelpMessage:    options.SelectorOptions.HelpMessage,
		DisplayCount:   options.SelectorOptions.DisplayCount,
		DisplayNumbers: options.SelectorOptions.DisplayNumbers,
		Allowed:        choices,
	})

	selectedIndex, err := resourceSelector.Ask()
	if err != nil {
		return nil, err
	}

	if selectedIndex == nil {
		return nil, nil
	}

	return resources[*selectedIndex], nil
}

func PromptResourceGroupResource(
	ctx context.Context,
	resourceGroup *azure.ResourceGroup,
	options PromptResourceOptions,
) (*azure.Resource, error) {
	if options.SelectorOptions == nil {
		resourceName := options.ResourceTypeDisplayName

		if resourceName == "" && options.ResourceType != nil {
			resourceName = string(*options.ResourceType)
		}

		if resourceName == "" {
			resourceName = "resource"
		}

		options.SelectorOptions = &PromptSelectOptions{
			Message:        fmt.Sprintf("Select %s", resourceName),
			LoadingMessage: fmt.Sprintf("Loading %s resources...", resourceName),
			HelpMessage:    fmt.Sprintf("Choose an Azure %s for your project.", resourceName),
			DisplayNumbers: ux.Ptr(true),
			DisplayCount:   10,
		}
	}

	azdContext, err := ext.CurrentContext(ctx)
	if err != nil {
		return nil, err
	}

	credential, err := azdContext.Credential()
	if err != nil {
		return nil, err
	}

	parsedResourceGroup, err := arm.ParseResourceID(resourceGroup.Id)
	if err != nil {
		return nil, fmt.Errorf("parsing resource group id: %w", err)
	}

	var resourceListOptions *azure.ListResourceGroupResourcesOptions
	if options.ResourceType != nil {
		resourceListOptions = &azure.ListResourceGroupResourcesOptions{
			Filter: to.Ptr(fmt.Sprintf("resourceType eq '%s'", *options.ResourceType)),
		}
	}

	loadingSpinner := ux.NewSpinner(&ux.SpinnerConfig{
		Text: options.SelectorOptions.LoadingMessage,
	})

	var resources []*azure.Resource

	err = loadingSpinner.Run(ctx, func(ctx context.Context) error {
		resourceService := azure.NewResourceService(credential, nil)
		resourceList, err := resourceService.ListResourceGroupResources(
			ctx,
			parsedResourceGroup.SubscriptionID,
			resourceGroup.Name,
			resourceListOptions,
		)
		if err != nil {
			return err
		}

		resources = resourceList
		return nil
	})

	if err != nil {
		return nil, err
	}

	if len(resources) == 0 {
		return nil, fmt.Errorf("no resources found with type '%v'", options.ResourceType)
	}

	choices := make([]string, len(resources))
	for i, resource := range resources {
		choices[i] = resource.Name
	}

	resourceSelector := ux.NewSelect(&ux.SelectConfig{
		Message:        options.SelectorOptions.Message,
		HelpMessage:    options.SelectorOptions.HelpMessage,
		DisplayCount:   options.SelectorOptions.DisplayCount,
		DisplayNumbers: options.SelectorOptions.DisplayNumbers,
		Allowed:        choices,
	})

	selectedIndex, err := resourceSelector.Ask()
	if err != nil {
		return nil, err
	}

	if selectedIndex == nil {
		return nil, nil
	}

	return resources[*selectedIndex], nil
}
