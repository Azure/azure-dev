// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package service

import (
	"context"
	"fmt"
)

// DefaultInputResolver is a stub implementation of InputResolver.
// Replace with actual datastore upload logic to resolve local input paths to datastore URIs.
type DefaultInputResolver struct{}

func NewDefaultInputResolver() *DefaultInputResolver {
	return &DefaultInputResolver{}
}

func (r *DefaultInputResolver) ResolveInput(ctx context.Context, inputPath string, inputType string) (string, error) {
	return "", fmt.Errorf("input resolution not implemented: provide a remote URI for input path '%s'", inputPath)
}
