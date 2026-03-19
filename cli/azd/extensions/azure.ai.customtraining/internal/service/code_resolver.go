// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package service

import (
	"context"
	"fmt"
)

// DefaultCodeResolver is a stub implementation of CodeResolver.
// Replace with actual datastore upload logic to resolve local code path to asset ID.
type DefaultCodeResolver struct{}

func NewDefaultCodeResolver() *DefaultCodeResolver {
	return &DefaultCodeResolver{}
}

func (r *DefaultCodeResolver) ResolveCode(ctx context.Context, codePath string) (string, error) {
	return "", fmt.Errorf("code resolution not implemented: provide a remote URI for code path '%s'", codePath)
}
