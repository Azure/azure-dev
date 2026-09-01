// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testProjectEndpoint = "https://account.services.ai.azure.com/api/projects/project"

func TestResolveProjectEndpointPrefersFlag(t *testing.T) {
	resolved, err := resolveProjectEndpoint(t.Context(), resolveProjectEndpointOpts{
		FlagValue: testProjectEndpoint,
		ReadAzdHostedSources: func(context.Context) (azdHostedSources, error) {
			return azdHostedSources{
				EnvValue: "https://other.services.ai.azure.com/api/projects/other",
			}, nil
		},
	})

	require.NoError(t, err)
	assert.Equal(t, testProjectEndpoint, resolved.Endpoint)
}

func TestResolveProjectEndpointUsesPersistedProjectContext(t *testing.T) {
	resolved, err := resolveProjectEndpoint(t.Context(), resolveProjectEndpointOpts{
		ReadAzdHostedSources: func(context.Context) (azdHostedSources, error) {
			return azdHostedSources{
				Config: projectContextState{Endpoint: testProjectEndpoint},
			}, nil
		},
	})

	require.NoError(t, err)
	assert.Equal(t, testProjectEndpoint, resolved.Endpoint)
}

func TestValidateProjectEndpointDoesNotDiscloseCredentials(t *testing.T) {
	credential := "user:secret"
	_, err := validateProjectEndpoint(
		"https://" + credential + "@account.services.ai.azure.com/api/projects/project?sig=sensitive",
	)

	require.Error(t, err)
	assert.NotContains(t, err.Error(), credential)
	assert.NotContains(t, err.Error(), "sensitive")
}
