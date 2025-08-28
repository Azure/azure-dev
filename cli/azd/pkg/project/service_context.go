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

// WithPublishing sets a flag on the context to indicate that the caller is only interested in building and
// publishing artifacts, not deploying them. This is used by `azd publish`.
func WithPublishOnly(ctx context.Context, publishOnly bool) context.Context {
	return context.WithValue(ctx, publishOnlyContextKey, publishOnly)
}

// IsPublishing returns true when the caller is only interested in building and publishing artifacts, not deploying
// them. This is used by `azd publish`.
func IsPublishOnly(ctx context.Context) bool {
	if val := ctx.Value(publishOnlyContextKey); val != nil {
		if publishOnly, ok := val.(bool); ok {
			return publishOnly
		}
	}
	return false
}

// WithImageName sets a custom image name override (from --image flag).
func WithImageName(ctx context.Context, imageName string) context.Context {
	return context.WithValue(ctx, imageNameContextKey, imageName)
}

// GetImageName returns the custom image name override, or empty string if none.
func GetImageName(ctx context.Context) string {
	if val := ctx.Value(imageNameContextKey); val != nil {
		if imageName, ok := val.(string); ok {
			return imageName
		}
	}
	return ""
}

// WithImageTag sets a custom image tag override (from --image-tag flag).
func WithImageTag(ctx context.Context, imageTag string) context.Context {
	return context.WithValue(ctx, imageTagContextKey, imageTag)
}

// GetImageTag returns the custom image tag override, or empty string if none.
func GetImageTag(ctx context.Context) string {
	if val := ctx.Value(imageTagContextKey); val != nil {
		if imageTag, ok := val.(string); ok {
			return imageTag
		}
	}
	return ""
}
