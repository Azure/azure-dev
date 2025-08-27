// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import "context"

type contextKey string

const (
	publishOnlyContextKey contextKey = "publishOnly"
	imageNameContextKey   contextKey = "imageName"
	imageTagContextKey    contextKey = "imageTag"
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

// WithImageName adds a custom image name to the context
func WithImageName(ctx context.Context, imageName string) context.Context {
	return context.WithValue(ctx, imageNameContextKey, imageName)
}

// GetImageName retrieves image name from the context
func GetImageName(ctx context.Context) string {
	if val := ctx.Value(imageNameContextKey); val != nil {
		if imageName, ok := val.(string); ok {
			return imageName
		}
	}
	return ""
}

// WithImageTag adds a custom image tag to the context
func WithImageTag(ctx context.Context, imageTag string) context.Context {
	return context.WithValue(ctx, imageTagContextKey, imageTag)
}

// GetImageTag retrieves image tag from the context
func GetImageTag(ctx context.Context) string {
	if val := ctx.Value(imageTagContextKey); val != nil {
		if imageTag, ok := val.(string); ok {
			return imageTag
		}
	}
	return ""
}
