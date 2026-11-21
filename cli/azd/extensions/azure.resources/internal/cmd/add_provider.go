// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"io"
	"log"
	"slices"

	"azure.resources/internal/add"
	"azure.resources/internal/resources"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
)

const (
	invalidAddItemCode    = "invalid_add_item"
	addRequiresPromptCode = "add_requires_prompt"
)

func addProviderItems() []*v1beta.AddItem {
	return []*v1beta.AddItem{
		{Id: resources.AppService, DisplayName: "App Service"},
		{Id: resources.AISearch, DisplayName: "AI Search"},
		{Id: resources.ContainerApp, DisplayName: "Container App"},
		{Id: resources.Mongo, DisplayName: "Cosmos DB for MongoDB"},
		{Id: resources.Cosmos, DisplayName: "Cosmos DB for NoSQL"},
		{Id: resources.EventHubs, DisplayName: "Event Hubs"},
		{Id: add.ExistingResource, DisplayName: "Existing resource"},
		{Id: resources.AIProject, DisplayName: "Foundry model"},
		{Id: resources.KeyVault, DisplayName: "Key Vault"},
		{Id: resources.OpenAIModel, DisplayName: "OpenAI model"},
		{Id: resources.Redis, DisplayName: "Redis"},
		{Id: resources.ServiceBus, DisplayName: "Service Bus"},
		{Id: resources.Storage, DisplayName: "Storage account"},
	}
}

func projectAddProviderItems(ctx context.Context, client azdext.ProjectServiceClient) []*v1beta.AddItem {
	items := addProviderItems()
	current, err := loadProject(ctx, client)
	if err != nil {
		log.Print("could not read project configuration for add item availability")
		return items
	}
	project, err := parseProject(current, "")
	if err != nil {
		log.Print("could not resolve resources layer for add item availability")
		return items
	}
	for _, item := range items {
		switch item.Id {
		case resources.ContainerApp, resources.AppService, resources.OpenAIModel, resources.AIProject, add.ExistingResource:
			continue
		}
		for _, resource := range project.config {
			if resource.Type == item.Id && !resource.Existing {
				item.DisabledReason = "Only allowed one per layer"
				break
			}
		}
	}
	return items
}

func newAddProviderHandler(
	client *azdext.AzdClient, extCtx *azdext.ExtensionContext, writer io.Writer,
) func(context.Context, string) error {
	items := addProviderItems()
	return func(ctx context.Context, itemID string) error {
		if !slices.ContainsFunc(items, func(item *v1beta.AddItem) bool { return item.GetId() == itemID }) {
			return &azdext.LocalError{
				Category: azdext.LocalErrorCategoryValidation, Code: invalidAddItemCode,
				Message:    "unrecognized Azure Resources add item",
				Suggestion: "Run azd add and select an item from the Azure Resources extension.",
			}
		}
		if extCtx.NoPrompt {
			return &azdext.LocalError{
				Category: azdext.LocalErrorCategoryValidation, Code: addRequiresPromptCode,
				Message:    "adding an Azure resource or service requires interactive input",
				Suggestion: "Run azd add without --no-prompt.",
			}
		}
		projectDir, err := azdext.GetProjectDir()
		if err != nil {
			return err
		}
		return runAddWizard(ctx, client, writer, projectDir, extCtx.Environment, "", itemID)
	}
}
