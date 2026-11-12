// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"context"
	"fmt"
	"strings"

	"azure.resources/internal/resources"
	"azure.resources/internal/resources/scaffold"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

var azureResourceTypes = map[string]string{
	resources.Redis:       "Microsoft.Cache/redis",
	resources.Mongo:       "Microsoft.DocumentDB/databaseAccounts/mongodbDatabases",
	resources.Cosmos:      "Microsoft.DocumentDB/databaseAccounts/sqlDatabases",
	resources.OpenAIModel: "Microsoft.CognitiveServices/accounts/deployments",
	resources.AIProject:   "Microsoft.CognitiveServices/accounts/projects",
	resources.AISearch:    "Microsoft.Search/searchServices",
	resources.EventHubs:   "Microsoft.EventHub/namespaces",
	resources.ServiceBus:  "Microsoft.ServiceBus/namespaces",
	resources.Storage:     "Microsoft.Storage/storageAccounts",
	resources.KeyVault:    "Microsoft.KeyVault/vaults",
}

func (w *Wizard) configureExisting(
	ctx context.Context, pending map[string]string,
) (string, resources.Definition, error) {
	definition := resources.Definition{Existing: true}
	category, err := w.choose(ctx, "Which type of existing resource?", []string{
		"AI", "Database", "Key Vault", "Messaging", "Storage account",
	})
	if err != nil {
		return "", definition, err
	}
	resourceType, err := w.selectResourceType(ctx, category)
	if err != nil {
		return "", definition, err
	}
	definition.Type = resourceType
	azureType, supported := azureResourceTypes[resourceType]
	if !supported {
		return "", definition, fmt.Errorf("existing resource type %q is not supported", resourceType)
	}
	meta, ok := scaffold.ResourceMetaFromType(azureType)
	if ok && meta.ParentResourceType != "" {
		azureType = meta.ParentResourceType
	}
	azureContext, values, err := w.azureScope(ctx, pending, false)
	if err != nil {
		return "", definition, err
	}
	options := &azdext.PromptResourceOptions{
		ResourceType: azureType,
		SelectOptions: &azdext.PromptResourceSelectOptions{
			Message: "Select an existing Azure resource", AllowNewResource: new(false),
		},
	}
	if resourceType == resources.OpenAIModel {
		options.Kinds = []string{"OpenAI"}
	}
	response, err := w.Client.Prompt().PromptSubscriptionResource(ctx,
		&azdext.PromptSubscriptionResourceRequest{AzureContext: azureContext, Options: options})
	if err != nil {
		return "", definition, fmt.Errorf("selecting existing resource: %w", err)
	}
	resource := response.GetResource()
	if resource.GetId() == "" {
		return "", definition, fmt.Errorf("no existing resources of type %s were found", azureType)
	}
	id, err := arm.ParseResourceID(resource.Id)
	if err != nil {
		return "", definition, fmt.Errorf("the selected resource has an invalid Azure resource ID")
	}
	if !strings.EqualFold(id.ResourceType.String(), azureType) {
		return "", definition, fmt.Errorf("the selected resource does not match the requested resource type")
	}
	if !strings.EqualFold(id.SubscriptionID, azureContext.Scope.SubscriptionId) {
		return "", definition, fmt.Errorf("the selected resource does not belong to the selected subscription")
	}
	for name := range w.Resources {
		if strings.EqualFold(values[resourceIDKey(w.EntryName, name)], resource.Id) {
			return "", definition, fmt.Errorf("the selected resource is already managed as %s in this entry", name)
		}
	}
	name, err := w.resourceName(ctx, w.uniqueName(id.Name))
	if err != nil {
		return "", definition, err
	}
	pending[resourceIDKey(w.EntryName, name)] = resource.Id
	if azureContext.Scope.Location == "" && resource.Location != "" {
		pending["AZURE_LOCATION"] = resource.Location
	}
	return name, definition, nil
}
