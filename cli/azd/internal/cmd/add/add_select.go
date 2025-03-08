// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

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
	case aiSearch:
		return a.selectSearch(console, ctx, p)
	case otherAiModels: // otherAiModels
		return a.selectAiModel(console, ctx, p)
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
