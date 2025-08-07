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
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
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
		{Namespace: "host", Label: "Host service", SelectResource: selectHost},
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

func selectHost(
	console input.Console,
	ctx context.Context,
	p PromptOptions) (*project.ResourceConfig, error) {
	// ACA is the default host type, and should be shown first in the list
	defaultHostType := project.ResourceTypeHostContainerApp
	defaultHostDisplayName := fmt.Sprintf("%s (default)", defaultHostType.String())
	resourceTypesDisplayMap := make(map[string]project.ResourceType)
	resourceTypesDisplayMap[defaultHostDisplayName] = defaultHostType
	resourceTypesDisplay := []string{defaultHostDisplayName}

	otherHostTypes := []string{}
	for _, resourceType := range project.AllResourceTypes() {
		if strings.HasPrefix(string(resourceType), "host.") && resourceType != defaultHostType {
			resourceTypesDisplayMap[resourceType.String()] = resourceType
			otherHostTypes = append(otherHostTypes, resourceType.String())
		}
	}

	slices.Sort(otherHostTypes)
	resourceTypesDisplay = append(resourceTypesDisplay, otherHostTypes...)

	r := &project.ResourceConfig{}
	hostOption, err := console.Select(ctx, input.ConsoleOptions{
		Message:      "Which type of host?",
		Options:      resourceTypesDisplay,
		DefaultValue: defaultHostDisplayName,
	})
	if err != nil {
		return nil, err
	}

	r.Type = resourceTypesDisplayMap[resourceTypesDisplay[hostOption]]
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
			if menu.Namespace == "existing" {
				continue
			}

			if menu.Namespace == "host" || // host resources are not yet supported
				menu.Namespace == "db" { // db resources are not yet supported
				continue
			}

			selectMenu = append(selectMenu, menu)

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
		resourceMeta, ok := scaffold.ResourceMetaFromType(azureResourceType)
		if ok && resourceMeta.ParentForEval != "" {
			azureResourceType = resourceMeta.ParentForEval
		}

		managedResourceIds := make([]string, 0, len(p.PrjConfig.Resources))
		env := a.env.Dotenv()

		for res, resCfg := range p.PrjConfig.Resources {
			if resCfg.Type != r.Type {
				continue
			}

			if resId, ok := env[infra.ResourceIdName(res)]; ok {
				managedResourceIds = append(managedResourceIds, resId)
			}
		}

		resourceId, err := a.promptResource(
			ctx,
			fmt.Sprintf("Which %s resource?", r.Type.String()),
			azureResourceType,
			managedResourceIds)
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
	excludeResourceIds []string,
) (string, error) {
	options := armresources.ClientListOptions{
		Filter: to.Ptr(fmt.Sprintf("resourceType eq '%s'", resourceType)),
	}

	a.console.ShowSpinner(ctx, "Listing resources...", input.Step)
	allResources, err := a.resourceService.ListSubscriptionResources(ctx, a.env.GetSubscriptionId(), &options)
	if err != nil {
		return "", fmt.Errorf("listing resources: %w", err)
	}

	resources := make([]*azapi.ResourceExtended, 0, len(allResources))
	for _, resource := range allResources {
		if slices.Contains(excludeResourceIds, resource.Id) {
			continue
		}

		resources = append(resources, resource)
	}

	if len(resources) == 0 {
		return "", nil
	}
	a.console.StopSpinner(ctx, "", input.StepDone)

	slices.SortFunc(resources, func(a, b *azapi.ResourceExtended) int {
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
