// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package mockinputs

import (
	"context"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/inputs"
)

// Resolver implements the input coordinator with a test-specific function.
type Resolver func(context.Context, *v1beta.InputDefinition) (*string, error)

// HasInputProviders reports that the configured test provider is available.
func (Resolver) HasInputProviders(context.Context) (bool, error) { return true, nil }

// ResolveInput invokes the configured test function.
func (r Resolver) ResolveInput(ctx context.Context, definition *v1beta.InputDefinition) ([]inputs.Option, error) {
	value, err := r(ctx, definition)
	if err != nil || value == nil {
		return nil, err
	}
	return []inputs.Option{{Value: *value, Recommended: true}}, nil
}

// OptionsResolver implements the coordinator with a test-specific option list.
type OptionsResolver func(context.Context, *v1beta.InputDefinition) ([]inputs.Option, error)

// HasInputProviders reports that the configured test provider is available.
func (OptionsResolver) HasInputProviders(context.Context) (bool, error) { return true, nil }

// ResolveInput invokes the configured test function.
func (r OptionsResolver) ResolveInput(ctx context.Context, definition *v1beta.InputDefinition) ([]inputs.Option, error) {
	return r(ctx, definition)
}
