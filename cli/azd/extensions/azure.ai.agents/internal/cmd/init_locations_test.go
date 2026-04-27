// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"

	"azureaiagent/internal/exterrors"
)

func TestFetchHostedAgentRegionsFromURL_Success(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"regions": ["eastus2", "westus3", "swedencentral"]}`))
	}))
	t.Cleanup(server.Close)

	regions, err := fetchHostedAgentRegionsFromURL(t.Context(), http.DefaultClient, server.URL)
	require.NoError(t, err)
	require.Equal(t, []string{"eastus2", "westus3", "swedencentral"}, regions)
}

func TestFetchHostedAgentRegionsFromURL_NormalizesEntries(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"regions": ["  EastUS2 ", "westus3", "", "  "]}`))
	}))
	t.Cleanup(server.Close)

	regions, err := fetchHostedAgentRegionsFromURL(t.Context(), http.DefaultClient, server.URL)
	require.NoError(t, err)
	require.Equal(t, []string{"eastus2", "westus3"}, regions)
}

func TestFetchHostedAgentRegionsFromURL_HTTPError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	_, err := fetchHostedAgentRegionsFromURL(t.Context(), http.DefaultClient, server.URL)
	require.Error(t, err)
}

func TestFetchHostedAgentRegionsFromURL_MalformedJSON(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{not json`))
	}))
	t.Cleanup(server.Close)

	_, err := fetchHostedAgentRegionsFromURL(t.Context(), http.DefaultClient, server.URL)
	require.Error(t, err)
}

func TestFetchHostedAgentRegionsFromURL_EmptyManifest(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"regions": []}`))
	}))
	t.Cleanup(server.Close)

	_, err := fetchHostedAgentRegionsFromURL(t.Context(), http.DefaultClient, server.URL)
	require.Error(t, err)
}

func TestFetchHostedAgentRegionsFromURL_RespectsTimeout(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(hostedAgentRegionsFetchTimeout + 2*time.Second)
	}))
	t.Cleanup(server.Close)

	start := time.Now()
	_, err := fetchHostedAgentRegionsFromURL(t.Context(), http.DefaultClient, server.URL)
	elapsed := time.Since(start)

	require.Error(t, err)
	require.Less(t, elapsed, hostedAgentRegionsFetchTimeout+1*time.Second)
}

func TestSupportedModelLocations(t *testing.T) {
	resetRegionsCache(t, []string{"eastus2", "westus3"})

	tests := []struct {
		name           string
		modelLocations []string
		want           []string
		wantErr        bool
	}{
		{"AllSupported", []string{"eastus2", "westus3"}, []string{"eastus2", "westus3"}, false},
		{"SomeUnsupported", []string{"eastus2", "unsupported"}, []string{"eastus2"}, false},
		{"NoneSupported", []string{"unsupported1", "unsupported2"}, nil, true},
		{"EmptyInput", []string{}, nil, true},
		{"NilInput", nil, nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := supportedModelLocations(t.Context(), tt.modelLocations)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.ElementsMatch(t, tt.want, result)
		})
	}
}

func TestSupportedModelLocations_EmptyIntersectionReturnsStructuredError(t *testing.T) {
	resetRegionsCache(t, []string{"eastus2"})

	_, err := supportedModelLocations(t.Context(), []string{"unsupported"})
	require.Error(t, err)
	localErr, ok := err.(*azdext.LocalError)
	require.True(t, ok, "expected *azdext.LocalError, got %T", err)
	require.Equal(t, exterrors.CodeNoSupportedModelLocations, localErr.Code)
}

func TestSupportedModelLocations_DoesNotMutateInput(t *testing.T) {
	resetRegionsCache(t, []string{"eastus2", "westus3"})

	input := []string{"eastus2", "unsupported", "westus3"}
	original := slices.Clone(input)

	_, err := supportedModelLocations(t.Context(), input)
	require.NoError(t, err)
	require.Equal(t, original, input)
}

func TestSupportedRegionsForInit_FetchesOnceAndCaches(t *testing.T) {
	resetRegionsCache(t, nil)

	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"regions": ["eastus2"]}`))
	}))
	t.Cleanup(server.Close)

	prev := hostedAgentRegionsURL
	hostedAgentRegionsURL = server.URL
	t.Cleanup(func() { hostedAgentRegionsURL = prev })

	for range 3 {
		got, err := supportedRegionsForInit(t.Context())
		require.NoError(t, err)
		require.Equal(t, []string{"eastus2"}, got)
	}
	require.Equal(t, 1, hits)
}

func resetRegionsCache(t *testing.T, regions []string) {
	t.Helper()

	regionsCache.mu.Lock()
	prev := regionsCache.regions
	regionsCache.regions = regions
	regionsCache.mu.Unlock()

	t.Cleanup(func() {
		regionsCache.mu.Lock()
		regionsCache.regions = prev
		regionsCache.mu.Unlock()
	})
}
