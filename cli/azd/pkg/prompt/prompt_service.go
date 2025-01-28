// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package prompt

import (
	"context"
	"fmt"
	"log"
	"slices"
	"strings"

	"dario.cat/mergo"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/fatih/color"
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
	// CreateResource is a function that creates a new resource.
	CreateResource func(ctx context.Context) (*azapi.ResourceExtended, error)
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
	// CreateResource is a function that creates a new resource.
	CreateResource func(ctx context.Context) (*T, error)
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
	// CreatingMessage is the message to display to the user when creating a new resource.
	CreatingMessage string
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
}

type AzureScope struct {
	TenantId       string
	SubscriptionId string
	Location       string
	ResourceGroup  string
}

type AzureResourceList struct {
	resourceService *azapi.ResourceService
	resources       []*arm.ResourceID
}

func NewAzureResourceList(resourceService *azapi.ResourceService, resources []*arm.ResourceID) *AzureResourceList {
	if resources == nil {
		resources = []*arm.ResourceID{}
	}

	return &AzureResourceList{
		resourceService: resourceService,
		resources:       resources,
	}
}

func (arl *AzureResourceList) Add(resourceId string) error {
	if _, has := arl.FindById(resourceId); has {
		return nil
	}

	parsedResource, err := arm.ParseResourceID(resourceId)
	if err != nil {
		return err
	}

	arl.resources = append(arl.resources, parsedResource)
	log.Printf("Added resource: %s", resourceId)

	return nil
}

func (arl *AzureResourceList) Find(predicate func(resourceId *arm.ResourceID) bool) (*arm.ResourceID, bool) {
	for _, resource := range arl.resources {
		if predicate(resource) {
			return resource, true
		}
	}

	return nil, false
}

func (arl *AzureResourceList) FindAll(predicate func(resourceId *arm.ResourceID) bool) ([]*arm.ResourceID, bool) {
	matches := []*arm.ResourceID{}

	for _, resource := range arl.resources {
		if predicate(resource) {
			matches = append(matches, resource)
		}
	}

	return matches, len(matches) > 0
}

func (arl *AzureResourceList) FindByType(resourceType azapi.AzureResourceType) (*arm.ResourceID, bool) {
	return arl.Find(func(resourceId *arm.ResourceID) bool {
		return strings.EqualFold(resourceId.ResourceType.String(), string(resourceType))
	})
}

func (arl *AzureResourceList) FindAllByType(resourceType azapi.AzureResourceType) ([]*arm.ResourceID, bool) {
	return arl.FindAll(func(resourceId *arm.ResourceID) bool {
		return strings.EqualFold(resourceId.ResourceType.String(), string(resourceType))
	})
}

func (arl *AzureResourceList) FindByTypeAndKind(
	ctx context.Context,
	resourceType azapi.AzureResourceType,
	kinds []string,
) (*arm.ResourceID, bool) {
	typeMatches, has := arl.FindAllByType(resourceType)
	if !has {
		return nil, false
	}

	// When no kinds are specified, return the first resource found
	if len(kinds) == 0 {
		return typeMatches[0], true
	}

	// When kinds are specified, check if the resource kind matches
	for _, typeMatch := range typeMatches {
		resource, err := arl.resourceService.GetResource(ctx, typeMatch.SubscriptionID, typeMatch.String(), "")
		if err != nil {
			return nil, false
		}

		for _, kind := range kinds {
			if strings.EqualFold(kind, resource.Kind) {
				return typeMatch, true
			}
		}
	}

	return nil, false
}

func (arl *AzureResourceList) FindById(resourceId string) (*arm.ResourceID, bool) {
	return arl.Find(func(resource *arm.ResourceID) bool {
		return strings.EqualFold(resource.String(), resourceId)
	})
}

func (arl *AzureResourceList) FindByTypeAndName(
	resourceType azapi.AzureResourceType,
	resourceName string,
) (*arm.ResourceID, bool) {
	return arl.Find(func(resource *arm.ResourceID) bool {
		return strings.EqualFold(resource.ResourceType.String(), string(resourceType)) &&
			strings.EqualFold(resource.Name, resourceName)
	})
}

type AzureContext struct {
	Scope         AzureScope
	Resources     *AzureResourceList
	promptService *PromptService
}

func NewEmptyAzureContext() *AzureContext {
	return &AzureContext{
		Scope:     AzureScope{},
		Resources: &AzureResourceList{},
	}
}

func NewAzureContext(promptService *PromptService, scope AzureScope, resourceList *AzureResourceList) *AzureContext {
	return &AzureContext{
		Scope:         scope,
		Resources:     resourceList,
		promptService: promptService,
	}
}

func (pc *AzureContext) EnsureSubscription(ctx context.Context) error {
	if pc.Scope.SubscriptionId == "" {
		subscription, err := pc.promptService.PromptSubscription(context.Background(), nil)
		if err != nil {
			return err
		}

		pc.Scope.TenantId = subscription.TenantId
		pc.Scope.SubscriptionId = subscription.Id
	}

	return nil
}

func (pc *AzureContext) EnsureResourceGroup(ctx context.Context) error {
	if pc.Scope.ResourceGroup == "" {
		resourceGroup, err := pc.promptService.PromptResourceGroup(ctx, pc, nil)
		if err != nil {
			return err
		}

		pc.Scope.ResourceGroup = resourceGroup.Name
	}

	return nil
}

func (pc *AzureContext) EnsureLocation(ctx context.Context) error {
	if pc.Scope.Location == "" {
		location, err := pc.promptService.PromptLocation(ctx, pc, nil)
		if err != nil {
			return err
		}

		pc.Scope.Location = location.Name
	}

	return nil
}

type ResourceSelection[T any] struct {
	Resource *T
	Exists   bool
}

type PromptService struct {
	authManager         *auth.Manager
	userConfigManager   config.UserConfigManager
	resourceService     *azapi.ResourceService
	subscriptionService *account.SubscriptionsService
}

func NewPromptService(
	authManager *auth.Manager,
	userConfigManager config.UserConfigManager,
	subscriptionService *account.SubscriptionsService,
	resourceService *azapi.ResourceService,
) *PromptService {
	return &PromptService{
		authManager:         authManager,
		userConfigManager:   userConfigManager,
		subscriptionService: subscriptionService,
		resourceService:     resourceService,
	}
}

func (ps *PromptService) PromptSubscription(
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

	return PromptCustomResource(ctx, CustomResourceOptions[account.Subscription]{
		SelectorOptions: mergedOptions,
		LoadData: func(ctx context.Context) ([]*account.Subscription, error) {
			userClaims, err := ps.authManager.ClaimsForCurrentUser(ctx, nil)
			if err != nil {
				return nil, err
			}

			subscriptionList, err := ps.subscriptionService.ListSubscriptions(ctx, userClaims.TenantId)
			if err != nil {
				return nil, err
			}

			subscriptions := make([]*account.Subscription, len(subscriptionList))
			for i, subscription := range subscriptionList {
				subscriptions[i] = &account.Subscription{
					Id:                 *subscription.SubscriptionID,
					Name:               *subscription.DisplayName,
					TenantId:           *subscription.TenantID,
					UserAccessTenantId: userClaims.TenantId,
				}
			}

			return subscriptions, nil
		},
		DisplayResource: func(subscription *account.Subscription) (string, error) {
			return fmt.Sprintf("%s %s", subscription.Name, color.HiBlackString("(%s)", subscription.Id)), nil
		},
		Selected: func(subscription *account.Subscription) bool {
			return strings.EqualFold(subscription.Id, defaultSubscriptionId)
		},
	})
}

// PromptLocation prompts the user to select an Azure location.
func (ps *PromptService) PromptLocation(
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

	return PromptCustomResource(ctx, CustomResourceOptions[account.Location]{
		SelectorOptions: mergedOptions,
		LoadData: func(ctx context.Context) ([]*account.Location, error) {
			locationList, err := ps.subscriptionService.ListSubscriptionLocations(
				ctx,
				azureContext.Scope.SubscriptionId,
				azureContext.Scope.TenantId,
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
			return fmt.Sprintf("%s %s", location.RegionalDisplayName, color.HiBlackString("(%s)", location.Name)), nil
		},
		Selected: func(resource *account.Location) bool {
			return resource.Name == defaultLocation
		},
	})
}

// PromptResourceGroup prompts the user to select an Azure resource group.
func (ps *PromptService) PromptResourceGroup(
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
		CreatingMessage:    "Creating new resource group...",
	}

	if err := mergo.Merge(mergedSelectorOptions, options.SelectorOptions, mergo.WithoutDereference); err != nil {
		return nil, err
	}

	if err := mergo.Merge(mergedSelectorOptions, defaultSelectorOptions, mergo.WithoutDereference); err != nil {
		return nil, err
	}

	return PromptCustomResource(ctx, CustomResourceOptions[azapi.ResourceGroup]{
		SelectorOptions: mergedSelectorOptions,
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
				color.HiBlackString("(Location: %s)", resourceGroup.Location),
			), nil
		},
		Selected: func(resourceGroup *azapi.ResourceGroup) bool {
			return resourceGroup.Name == azureContext.Scope.ResourceGroup
		},
		CreateResource: func(ctx context.Context) (*azapi.ResourceGroup, error) {
			namePrompt := ux.NewPrompt(&ux.PromptOptions{
				Message: "Enter the name for the resource group",
			})

			resourceGroupName, err := namePrompt.Ask()
			if err != nil {
				return nil, err
			}

			if err := azureContext.EnsureLocation(ctx); err != nil {
				return nil, err
			}

			var resourceGroup *azapi.ResourceGroup

			taskName := fmt.Sprintf("Creating resource group %s", color.CyanString(resourceGroupName))

			err = ux.NewTaskList(nil).
				AddTask(ux.TaskOptions{
					Title: taskName,
					Action: func(setProgress ux.SetProgressFunc) (ux.TaskState, error) {
						newResourceGroup, err := ps.resourceService.CreateOrUpdateResourceGroup(
							ctx,
							azureContext.Scope.SubscriptionId,
							resourceGroupName,
							azureContext.Scope.Location,
							nil,
						)
						if err != nil {
							return ux.Error, err
						}

						resourceGroup = newResourceGroup
						return ux.Success, nil
					},
				}).
				Run()

			if err != nil {
				return nil, err
			}

			return resourceGroup, nil
		},
	})
}

// PromptSubscriptionResource prompts the user to select an Azure subscription resource.
func (ps *PromptService) PromptSubscriptionResource(
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
	if options.ResourceType != nil {
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
		CreatingMessage:    fmt.Sprintf("Creating new %s...", resourceName),
	}

	if err := mergo.Merge(mergedSelectorOptions, options.SelectorOptions, mergo.WithoutDereference); err != nil {
		return nil, err
	}

	if err := mergo.Merge(mergedSelectorOptions, defaultSelectorOptions, mergo.WithoutDereference); err != nil {
		return nil, err
	}

	resource, err := PromptCustomResource(ctx, CustomResourceOptions[azapi.ResourceExtended]{
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

			if len(filteredResources) == 0 {
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
				color.HiBlackString("(%s)", parsedResource.ResourceGroupName),
			), nil
		},
		Selected:       options.Selected,
		CreateResource: options.CreateResource,
	})

	if err != nil {
		return nil, err
	}

	if err := azureContext.Resources.Add(resource.Id); err != nil {
		return nil, err
	}

	return resource, nil
}

// PromptResourceGroupResource prompts the user to select an Azure resource group resource.
func (ps *PromptService) PromptResourceGroupResource(
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
	if options.ResourceType != nil {
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
		CreatingMessage:    fmt.Sprintf("Creating new %s...", resourceName),
	}

	if err := mergo.Merge(mergedSelectorOptions, options.SelectorOptions, mergo.WithoutDereference); err != nil {
		return nil, err
	}

	if err := mergo.Merge(mergedSelectorOptions, defaultSelectorOptions, mergo.WithoutDereference); err != nil {
		return nil, err
	}

	resource, err := PromptCustomResource(ctx, CustomResourceOptions[azapi.ResourceExtended]{
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

			if len(filteredResources) == 0 {
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
		CreateResource: options.CreateResource,
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
		CreatingMessage:    "Creating new resource...",
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

		var choices []string

		if allowNewResource {
			choices = make([]string, len(resources)+1)
			choices[0] = mergedSelectorOptions.NewResourceMessage

			if defaultIndex != nil {
				*defaultIndex++
			}
		} else {
			choices = make([]string, len(resources))
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

			if allowNewResource {
				choices[i+1] = displayValue
			} else {
				choices[i] = displayValue
			}
		}

		resourceSelector := ux.NewSelect(&ux.SelectOptions{
			Message:        mergedSelectorOptions.Message,
			HelpMessage:    mergedSelectorOptions.HelpMessage,
			DisplayCount:   mergedSelectorOptions.DisplayCount,
			DisplayNumbers: mergedSelectorOptions.DisplayNumbers,
			Allowed:        choices,
			SelectedIndex:  defaultIndex,
		})

		userSelectedIndex, err := resourceSelector.Ask()
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
		if options.CreateResource == nil {
			return nil, fmt.Errorf("no create resource function provided")
		}

		createdResource, err := options.CreateResource(ctx)
		if err != nil {
			return nil, err
		}

		selectedResource = createdResource
	} else {
		// If a new resource is allowed, decrement the selected index
		if allowNewResource {
			*selectedIndex--
		}

		selectedResource = resources[*selectedIndex]
	}

	log.Printf("Selected resource: %v", *selectedResource)

	return selectedResource, nil
}
