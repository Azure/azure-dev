// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOptimizeConnectionFlags_Resolve_AllEmpty(t *testing.T) {
	t.Setenv("AZURE_AI_OPTIMIZE_ENDPOINT", "")

	f := &optimizeConnectionFlags{}
	_, err := f.resolve(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "endpoint")
}

func TestOptimizeConnectionFlags_Resolve_FromEnv(t *testing.T) {
	t.Setenv("AZURE_AI_OPTIMIZE_ENDPOINT", "https://example.com")

	f := &optimizeConnectionFlags{}
	endpoint, err := f.resolve(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "https://example.com", endpoint)
}

func TestOptimizeConnectionFlags_Resolve_FlagsOverrideEnv(t *testing.T) {
	t.Setenv("AZURE_AI_OPTIMIZE_ENDPOINT", "https://from-env.com")

	f := &optimizeConnectionFlags{
		endpoint: "https://from-flag.com",
	}
	endpoint, err := f.resolve(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "https://from-flag.com", endpoint)
}

func TestOptimizeConnectionFlags_Resolve_TrimsTrailingSlash(t *testing.T) {
	t.Setenv("AZURE_AI_OPTIMIZE_ENDPOINT", "https://example.com/")

	f := &optimizeConnectionFlags{}
	endpoint, err := f.resolve(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "https://example.com", endpoint)
}

func TestOptimizeConnectionFlags_Resolve_ProjectEndpointFlag(t *testing.T) {
	f := &optimizeConnectionFlags{
		projectEndpoint: "https://my-project.services.ai.azure.com/",
	}
	endpoint, err := f.resolve(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "https://my-project.services.ai.azure.com", endpoint)
}
