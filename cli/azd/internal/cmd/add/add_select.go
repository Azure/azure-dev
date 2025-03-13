// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

// resourceSelection prompts the user to select a given resource type, returning the resulting resource configuration.
type resourceSelection func(console input.Console, ctx context.Context, p PromptOptions) (*project.ResourceConfig, error)

// A menu to be displayed.
type Menu struct {
	// Namespace of the resource type.
	Namespace string
	// Label displayed in the menu.
	Label string

	// SelectResource is the continuation that returns the resource with type filled in.
	SelectResource resourceSelection
}

func (a *AddAction) selectMenu() []Menu {
	return []Menu{
		{Namespace: "db", Label: "Database", SelectResource: selectDatabase},
		{Namespace: "host", Label: "Host service"},
		{Namespace: "ai", Label: "AI", SelectResource: a.selectAiType},
		{Namespace: "messaging", Label: "Messaging", SelectResource: selectMessaging},
		{Namespace: "storage", Label: "Storage account", SelectResource: selectStorage},
		{Namespace: "keyvault", Label: "Key Vault", SelectResource: selectKeyVault},
		{Namespace: "existing", Label: "~Existing resource", SelectResource: a.selectExistingResource},
	}
}

func (a *AddAction) selectAiType(
	console input.Console, ctx context.Context, p PromptOptions) (*project.ResourceConfig, error) {
	openAiOption := "Azure OpenAI model"
	otherAiModels := "Azure AI services model"
	aiSearch := "Azure AI Search"
	options := []string{
		openAiOption,
		otherAiModels,
		aiSearch,
	}
	aiOptionIndex, err := console.Select(ctx, input.ConsoleOptions{
		Message:      "Which type of AI resource?",
		DefaultValue: openAiOption,
		Options:      options,
	})
	if err != nil {
		return nil, err
	}
	selectedOption := options[aiOptionIndex]
	switch selectedOption {
	case openAiOption:
		return a.selectOpenAi(console, ctx, p)
	case otherAiModels:
		return a.selectAiModel(console, ctx, p)
	case aiSearch:
		return a.selectSearch(console, ctx, p)
	default:
		return nil, fmt.Errorf("invalid option %q", selectedOption)
	}
}

func selectDatabase(
	console input.Console,
	ctx context.Context,
	p PromptOptions) (*project.ResourceConfig, error) {
	resourceTypesDisplayMap := make(map[string]project.ResourceType)
	for _, resourceType := range project.AllResourceTypes() {
		if strings.HasPrefix(string(resourceType), "db.") {
			resourceTypesDisplayMap[resourceType.String()] = resourceType
		}
	}

	r := &project.ResourceConfig{}
	resourceTypesDisplay := slices.Sorted(maps.Keys(resourceTypesDisplayMap))
	dbOption, err := console.Select(ctx, input.ConsoleOptions{
		Message: "Which type of database?",
		Options: resourceTypesDisplay,
	})
	if err != nil {
		return nil, err
	}

	r.Type = resourceTypesDisplayMap[resourceTypesDisplay[dbOption]]
	return r, nil
}

func selectMessaging(
	console input.Console,
	ctx context.Context,
	p PromptOptions) (*project.ResourceConfig, error) {
	resourceTypesDisplayMap := make(map[string]project.ResourceType)
	for _, resourceType := range project.AllResourceTypes() {
		if strings.HasPrefix(string(resourceType), "messaging.") {
			resourceTypesDisplayMap[resourceType.String()] = resourceType
		}
	}

	r := &project.ResourceConfig{}
	resourceTypesDisplay := slices.Sorted(maps.Keys(resourceTypesDisplayMap))
	dbOption, err := console.Select(ctx, input.ConsoleOptions{
		Message: "Which type of messaging service?",
		Options: resourceTypesDisplay,
	})
	if err != nil {
		return nil, err
	}

	r.Type = resourceTypesDisplayMap[resourceTypesDisplay[dbOption]]
	return r, nil
}

func selectStorage(
	console input.Console,
	ctx context.Context,
	p PromptOptions) (*project.ResourceConfig, error) {
	r := &project.ResourceConfig{}
	r.Type = project.ResourceTypeStorage
	r.Props = project.StorageProps{}
	return r, nil
}

func selectKeyVault(console input.Console, ctx context.Context, p PromptOptions) (*project.ResourceConfig, error) {
	r := &project.ResourceConfig{}
	r.Type = project.ResourceTypeKeyVault
	return r, nil
}

func (a *AddAction) selectExistingResource(
	console input.Console,
	ctx context.Context,
	p PromptOptions) (*project.ResourceConfig, error) {
	res := &project.ResourceConfig{}
	res.Existing = true

	if p.ExistingId == "" {
		all := a.selectMenu()
		selectMenu := make([]Menu, 0, len(all))
		for _, menu := range all {
			if menu.Namespace != "existing" {
				selectMenu = append(selectMenu, menu)
			}
		}
		slices.SortFunc(selectMenu, func(a, b Menu) int {
			return strings.Compare(a.Label, b.Label)
		})

		selections := make([]string, 0, len(selectMenu))
		for _, menu := range selectMenu {
			selections = append(selections, menu.Label)
		}
		idx, err := a.console.Select(ctx, input.ConsoleOptions{
			Message: "Which type of existing resource?",
			Options: selections,
		})
		if err != nil {
			return nil, err
		}

		selected := selectMenu[idx]

		r, err := selected.SelectResource(a.console, ctx, p)
		if err != nil {
			return nil, err
		}

		azureResourceType := r.Type.AzureResourceType()
		resourceId, err := a.promptResource(ctx, "Which resource?", azureResourceType)
		if err != nil {
			return nil, fmt.Errorf("prompting for resource: %w", err)
		}

		if resourceId == "" {
			return nil, fmt.Errorf("no resources of type '%s' were found", azureResourceType)
		}

		res.Type = r.Type
		res.ResourceId = resourceId
	} else {
		resourceId, err := arm.ParseResourceID(p.ExistingId)
		if err != nil {
			return nil, err
		}

		azureResourceType := resourceId.ResourceType.String()
		resourceType := resourceType(azureResourceType)
		if resourceType == "" {
			return nil, fmt.Errorf("resource type '%s' is not currently supported", azureResourceType)
		}

		res.Type = resourceType
		res.ResourceId = resourceId.String()
	}

	return res, nil
}

func (a *AddAction) promptResource(
	ctx context.Context,
	msg string,
	resourceType string,
) (string, error) {
	options := azapi.ListResourcesOptions{
		ResourceType: resourceType,
	}

	a.console.ShowSpinner(ctx, "Listing resources...", input.Step)
	resources, err := a.resourceService.ListResources(ctx, a.env.GetSubscriptionId(), &options)
	if err != nil {
		return "", fmt.Errorf("listing resources: %w", err)
	}
	if len(resources) == 0 {
		return "", nil
	}
	a.console.StopSpinner(ctx, "", input.StepDone)

	slices.SortFunc(resources, func(a, b *azapi.Resource) int {
		return strings.Compare(a.Name, b.Name)
	})

	choices := make([]string, len(resources))
	for idx, resource := range resources {
		choices[idx] = fmt.Sprintf("%d. %s (%s)", idx+1, resource.Name, resource.Location)
	}

	choice, err := a.console.Select(ctx, input.ConsoleOptions{
		Message: msg,
		Options: choices,
	})
	if err != nil {
		return "", fmt.Errorf("selecting resource: %w", err)
	}

	return resources[choice].Id, nil
}
