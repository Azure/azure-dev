// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package llm

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
)

// ModelFactory creates model containers using registered model providers
type ModelFactory struct {
	serviceLocator ioc.ServiceLocator
}

// NewModelFactory creates a new model factory with the given service locator
func NewModelFactory(serviceLocator ioc.ServiceLocator) *ModelFactory {
	return &ModelFactory{
		serviceLocator: serviceLocator,
	}
}

// CreateModelContainer creates a model container for the specified model type.
// It resolves the appropriate model provider and delegates container creation to it.
// Returns an error with suggestions if the model type is not supported.
func (f *ModelFactory) CreateModelContainer(
	ctx context.Context, modelType LlmType, opts ...ModelOption) (*ModelContainer, error) {
	var modelProvider ModelProvider
	if err := f.serviceLocator.ResolveNamed(string(modelType), &modelProvider); err != nil {
		return nil, &internal.ErrorWithSuggestion{
			Err: fmt.Errorf("The model type '%s' is not supported. Support types include: azure, ollama", modelType),
			//nolint:lll
			Suggestion: "Use `azd config set` to set the model type and any model specific options, such as the model name or version.",
		}
	}

	return modelProvider.CreateModelContainer(ctx, opts...)
}

// ModelProvider defines the interface for creating model containers
type ModelProvider interface {
	CreateModelContainer(ctx context.Context, opts ...ModelOption) (*ModelContainer, error)
}
