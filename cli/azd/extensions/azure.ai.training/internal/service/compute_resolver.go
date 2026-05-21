// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package service

import (
	"context"
	"fmt"
)

// DefaultComputeResolver is a stub implementation of ComputeResolver.
// Replace with actual ARM API call to resolve compute name to ARM resource ID.
type DefaultComputeResolver struct{}

func NewDefaultComputeResolver() *DefaultComputeResolver {
	return &DefaultComputeResolver{}
}

func (r *DefaultComputeResolver) ResolveCompute(ctx context.Context, computeName string) (string, error) {
	return "", fmt.Errorf("compute resolution not implemented: provide a full ARM resource ID for compute '%s'", computeName)
}
