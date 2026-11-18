// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"context"
	"fmt"

	"azure.resources/internal/resources"
)

func (w *Wizard) configureResource(
	ctx context.Context, resourceType string, pending map[string]string,
) (string, resources.Definition, error) {
	definition := resources.Definition{Type: resourceType}
	var name string
	var err error
	switch resourceType {
	case resources.Mongo, resources.Cosmos:
		name, err = w.resourceName(ctx, "")
	case resources.Redis:
		name = "redis"
	case resources.AISearch:
		name = "search"
	case resources.KeyVault:
		name = "vault"
	case resources.Storage:
		name = "storage"
	case resources.EventHubs:
		name = "event-hubs"
	case resources.ServiceBus:
		name = "service-bus"
	case resources.OpenAIModel, resources.AIProject:
		return w.configureAI(ctx, resourceType, pending)
	default:
		return "", definition, fmt.Errorf("unsupported resource type %q", resourceType)
	}
	if err != nil {
		return "", definition, err
	}
	if err := w.validateResourceName(name); err != nil {
		return "", definition, err
	}
	// These resource types have one shared backing resource per entry.
	for _, other := range w.Resources {
		if other.Type == resourceType && !other.Existing {
			return "", definition, fmt.Errorf("only one %s resource is supported per infrastructure entry", resourceType)
		}
	}
	switch resourceType {
	case resources.Storage:
		var selected []string
		for len(selected) == 0 {
			selected, err = w.multi(ctx, "What type of data do you want to store?", []string{"Blobs"})
			if err != nil {
				return "", definition, err
			}
			if len(selected) == 0 {
				w.message("At least one data type must be selected.")
			}
		}
		var container string
		container, err = w.label(ctx, "Input a blob container name to be created:", "", true)
		if err == nil {
			definition.StorageContainers = []string{container}
		}
	case resources.EventHubs:
		var hub string
		hub, err = w.label(ctx, "Input the event hub name:", "", false)
		if err == nil {
			definition.Hubs = []string{hub}
		}
	case resources.ServiceBus:
		var queue string
		queue, err = w.label(ctx, "Input the queue name:", "", false)
		if err == nil {
			definition.Queues = []string{queue}
		}
	}
	return name, definition, err
}
