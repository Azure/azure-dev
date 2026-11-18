// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"azure.resources/internal/resources"
	"azure.resources/internal/resources/scaffold"
)

// Run builds a proposed entry-local configuration without persisting any changes.
func (w *Wizard) Run(ctx context.Context) (*Result, error) {
	if w.Client == nil {
		return nil, fmt.Errorf("an azd host connection is required")
	}
	// Work on a separate wizard so cancellation cannot alter the caller's maps or slices.
	copy := *w
	data, err := json.Marshal(w.Resources)
	if err != nil {
		return nil, fmt.Errorf("cloning resources: %w", err)
	}
	copy.Resources = nil
	if err := json.Unmarshal(data, &copy.Resources); err != nil {
		return nil, fmt.Errorf("cloning resources: %w", err)
	}
	if copy.Resources == nil {
		copy.Resources = resources.Config{}
	}
	copy.Services = make(map[string]map[string]any, len(w.Services))
	for name, service := range w.Services {
		copy.Services[name] = cloneServiceValue(service).(map[string]any)
	}
	return copy.run(ctx)
}

func cloneServiceValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(value))
		for key, item := range value {
			result[key] = cloneServiceValue(item)
		}
		return result
	case []any:
		result := make([]any, len(value))
		for i, item := range value {
			result[i] = cloneServiceValue(item)
		}
		return result
	case []string:
		return slices.Clone(value)
	default:
		return value
	}
}

func (w *Wizard) run(ctx context.Context) (*Result, error) {
	if len(w.Resources) > 0 {
		if err := w.Resources.Validate(); err != nil {
			return nil, fmt.Errorf("invalid resource configuration: %w", err)
		}
	}
	category, err := w.choose(ctx, "What would you like to add?", []string{
		"Database", "Host service", "AI", "Messaging", "Storage account", "Key Vault", "Existing resource",
	})
	if err != nil {
		return nil, err
	}
	result := &Result{Resources: w.Resources, Services: w.Services, Environment: map[string]string{}}
	var name string
	var definition resources.Definition
	if category == "Existing resource" {
		name, definition, err = w.configureExisting(ctx, result.Environment)
	} else {
		var resourceType string
		resourceType, err = w.selectResourceType(ctx, category)
		if err == nil {
			if isHost(resourceType) {
				var service map[string]any
				name, definition, service, err = w.configureHost(ctx, resourceType)
				if err == nil {
					w.Services[name] = service
					result.HasService = true
					definition.Uses, err = w.selectUses(ctx, name, resourceType)
				}
			} else {
				name, definition, err = w.configureResource(ctx, resourceType, result.Environment)
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if err := w.addDependencies(definition); err != nil {
		return nil, err
	}
	w.Resources[name] = definition
	if err := w.linkHosts(ctx, name, definition); err != nil {
		return nil, err
	}
	if err := w.Resources.Validate(); err != nil {
		return nil, fmt.Errorf("invalid proposed configuration: %w", err)
	}
	result.Name = name
	return result, nil
}

func (w *Wizard) selectResourceType(ctx context.Context, category string) (string, error) {
	var labels, types []string
	message := ""
	switch category {
	case "Database":
		message = "Which type of database?"
		labels = []string{"CosmosDB", "MongoDB", "Redis"}
		types = []string{resources.Cosmos, resources.Mongo, resources.Redis}
	case "Host service":
		message = "Which type of host?"
		labels = []string{"Container App (default)", "App Service"}
		types = []string{resources.ContainerApp, resources.AppService}
	case "AI":
		message = "Which type of AI resource?"
		labels = []string{"Azure OpenAI model", "Microsoft Foundry model", "Azure AI Search"}
		types = []string{resources.OpenAIModel, resources.AIProject, resources.AISearch}
	case "Messaging":
		message = "Which type of messaging service?"
		labels = []string{"Event Hubs", "Service Bus"}
		types = []string{resources.EventHubs, resources.ServiceBus}
	case "Storage account":
		return resources.Storage, nil
	case "Key Vault":
		return resources.KeyVault, nil
	default:
		return "", fmt.Errorf("unsupported resource category %q", category)
	}
	selected, err := w.choose(ctx, message, labels)
	if err != nil {
		return "", err
	}
	return types[slices.Index(labels, selected)], nil
}

func isHost(resourceType string) bool {
	return resourceType == resources.ContainerApp || resourceType == resources.AppService
}

func canLink(hostType string, definition resources.Definition) bool {
	if !isHost(hostType) || (isHost(definition.Type) && definition.Type != hostType) {
		return false
	}
	// Existing Redis/Mongo credentials cannot be synthesized by the provider.
	if definition.Existing {
		switch definition.Type {
		case resources.Mongo, resources.Redis:
			return false
		}
	}
	return true
}

func (w *Wizard) selectUses(ctx context.Context, name, resourceType string) ([]string, error) {
	var choices []string
	for _, other := range slices.Sorted(maps.Keys(w.Resources)) {
		if other != name && canLink(resourceType, w.Resources[other]) {
			choices = append(choices, other)
		}
	}
	if len(choices) == 0 {
		return nil, nil
	}
	return w.multi(ctx, fmt.Sprintf("Select the resources that %s uses", name), choices)
}

func (w *Wizard) linkHosts(ctx context.Context, name string, definition resources.Definition) error {
	var choices []string
	for _, other := range slices.Sorted(maps.Keys(w.Resources)) {
		host := w.Resources[other]
		if other != name && canLink(host.Type, definition) && !slices.Contains(host.Uses, name) {
			choices = append(choices, other)
		}
	}
	if len(choices) == 0 {
		return nil
	}
	message := "Select the service(s) that uses this resource"
	if isHost(definition.Type) {
		message = "Select the front-end service(s) that uses this service (if applicable)"
	}
	selected, err := w.multi(ctx, message, choices)
	if err != nil {
		return err
	}
	for _, other := range selected {
		host := w.Resources[other]
		host.Uses = append(host.Uses, name)
		w.Resources[other] = host
	}
	return nil
}

func (w *Wizard) addDependencies(definition resources.Definition) error {
	if definition.Existing {
		return nil
	}
	switch definition.Type {
	case resources.Mongo, resources.Redis:
		for _, other := range w.Resources {
			if other.Type == resources.KeyVault && !other.Existing {
				return nil
			}
		}
		if _, exists := w.Resources["vault"]; exists {
			return fmt.Errorf("the database requires a deployable Key Vault named vault; that name is already in use")
		}
		if _, exists := w.Services["vault"]; exists {
			return fmt.Errorf("the required Key Vault name vault is already used by a service")
		}
		for _, other := range w.Resources {
			if other.Type == resources.KeyVault && other.Existing {
				return fmt.Errorf("the database requires a deployable Key Vault, not an existing Key Vault")
			}
		}
		w.Resources["vault"] = resources.Definition{Type: resources.KeyVault}
	}
	return nil
}

func resourceIDKey(entry, name string) string {
	return scaffold.EntryEnvKey(entry, "AZURE_RESOURCE_"+resources.ServiceKey(name)+"_ID")
}
