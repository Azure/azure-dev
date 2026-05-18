// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveProjectEndpoint_FlagWins(t *testing.T) {
	ep, src, err := resolveProjectEndpoint(context.Background(), "https://flag.example.com")
	require.NoError(t, err)
	require.Equal(t, "https://flag.example.com", ep)
	require.Equal(t, sourceFlag, src)
}

func TestResolveProjectEndpoint_HostEnvVar(t *testing.T) {
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://host.example.com")

	ep, src, err := resolveProjectEndpoint(context.Background(), "")
	require.NoError(t, err)
	require.Equal(t, "https://host.example.com", ep)
	require.Equal(t, sourceFoundryEnv, src)
}

func TestResolveProjectEndpoint_InvalidScheme(t *testing.T) {
	cases := []struct {
		name     string
		endpoint string
		wantMsg  string
	}{
		{"http scheme", "http://example.com", "must use https scheme"},
		{"no scheme", "example.com/foo", "must use https scheme"},
		{"empty host", "https:///path", "missing host"},
		{"ftp scheme", "ftp://example.com", "must use https scheme"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := resolveProjectEndpoint(context.Background(), tc.endpoint)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantMsg)
		})
	}
}

func TestResolveProjectEndpoint_MissingAll(t *testing.T) {
	t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "")

	_, _, err := resolveProjectEndpoint(context.Background(), "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no Foundry project endpoint resolved")
}
