// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package service

import (
	"context"
	"fmt"

	"azure.ai.customtraining/pkg/client"
)

// DefaultComputeResolver resolves a compute name to a full ARM resource ID
// by calling the ARM control plane API via the Client.
//
// When compute GET moves to the data plane, this resolver can be swapped out
// for a DataPlaneComputeResolver without changing any other code.
type DefaultComputeResolver struct {
	client *client.Client
}

// NewDefaultComputeResolver creates a compute resolver that calls the ARM API
// via the given Client. The client must have ARM context set via SetARMContext.
func NewDefaultComputeResolver(apiClient *client.Client) *DefaultComputeResolver {
	return &DefaultComputeResolver{
		client: apiClient,
	}
}

// ResolveCompute calls the ARM API to resolve a compute name to its full ARM resource ID.
// Returns a helpful error message if the user lacks permissions (401/403).
func (r *DefaultComputeResolver) ResolveCompute(ctx context.Context, computeName string) (string, error) {
	result, err := r.client.GetCompute(ctx, computeName)
	if err != nil {
		return "", err
	}

	fmt.Printf("  ✓ Compute resolved: %s\n", computeName)
	return result.ID, nil
}
