// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import "context"

type contextKey string

const (
	publishOnlyContextKey contextKey = "publishOnly"
)

// WithPublishOnly adds the publish-only flag to the context
func WithPublishOnly(ctx context.Context, publishOnly bool) context.Context {
	return context.WithValue(ctx, publishOnlyContextKey, publishOnly)
}

// IsPublishOnly retrieves the publish-only flag from the context
func IsPublishOnly(ctx context.Context) bool {
	if val := ctx.Value(publishOnlyContextKey); val != nil {
		if publishOnly, ok := val.(bool); ok {
			return publishOnly
		}
	}
	return false
}
