// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"

	"azureaiagent/internal/exterrors"
)

func TestPromptHarnessRegionsForInit(t *testing.T) {
	previousURL := harnessAgentRegionsURL
	t.Cleanup(func() { harnessAgentRegionsURL = previousURL })
	// Preserve any cached result populated by other init tests.
	resetAgentRegionsCache(t, &harnessRegionsCache, nil)
	resetRegionsCache(t, []string{"hosted-only"})
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"regions": ["  NewRegion "]}`))
	}))
	t.Cleanup(server.Close)
	harnessAgentRegionsURL = server.URL

	for _, harness := range []string{"", "other_harness"} {
		regions, err := promptHarnessRegionsForInit(t.Context(), harness)
		require.NoError(t, err)
		require.Nil(t, regions)
	}
	require.Zero(t, hits.Load(), "unrestricted prompt agents must not fetch a manifest")

	for _, harness := range []string{"microsoft_copilot_preview", "github_copilot_preview"} {
		regions, err := promptHarnessRegionsForInit(t.Context(), harness)
		require.NoError(t, err)
		require.Equal(t, []string{"newregion"}, regions, "remote data must override the embedded policy")
		require.NoError(t, validateInitLocation("newregion", regions))
		require.Error(t, validateInitLocation("japaneast", regions))
		regions[0] = "mutated"
	}
	require.EqualValues(t, 1, hits.Load(), "both harness names share a cache")
	hosted, err := supportedRegionsForInit(t.Context())
	require.NoError(t, err)
	require.Equal(t, []string{"hosted-only"}, hosted, "hosted and harness policies must remain independent")
}

func TestPromptHarnessRegionsForInit_Fallback(t *testing.T) {
	for _, body := range []string{"unavailable", `{"regions":[]}`, `{"regions":["  "]}`} {
		t.Run(body, func(t *testing.T) {
			resetAgentRegionsCache(t, &harnessRegionsCache, nil)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if body == "unavailable" {
					w.WriteHeader(http.StatusServiceUnavailable)
				}
				_, _ = w.Write([]byte(body))
			}))
			t.Cleanup(server.Close)
			previousURL := harnessAgentRegionsURL
			harnessAgentRegionsURL = server.URL
			t.Cleanup(func() { harnessAgentRegionsURL = previousURL })
			regions, err := promptHarnessRegionsForInit(t.Context(), "microsoft_copilot_preview")
			require.NoError(t, err)
			want, err := parseAgentRegionsManifest(embeddedHarnessAgentRegionsJSON)
			require.NoError(t, err)
			require.NotEmpty(t, want)
			require.Equal(t, want, regions)
		})
	}
}

func TestAgentRegionsCache_CancelledWaiter(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		_, _ = w.Write([]byte(`{"regions":["westus"]}`))
	}))
	t.Cleanup(server.Close)
	t.Cleanup(unblock)
	var cache agentRegionsCache
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		_, err := cache.load(ctx, server.URL, embeddedHarnessAgentRegionsJSON)
		done <- err
	}()
	<-started
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	// The shared fetch survives a waiter's cancellation.
	unblock()
	regions, err := cache.load(t.Context(), server.URL, embeddedHarnessAgentRegionsJSON)
	require.NoError(t, err)
	require.Equal(t, []string{"westus"}, regions)
}

func TestFetchHostedAgentRegionsFromURL_Success(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"regions": ["eastus2", "westus3", "swedencentral"]}`))
	}))
	t.Cleanup(server.Close)

	regions, err := fetchAgentRegionsFromURL(t.Context(), http.DefaultClient, server.URL)
	require.NoError(t, err)
	require.Equal(t, []string{"eastus2", "westus3", "swedencentral"}, regions)
}

func TestFetchHostedAgentRegionsFromURL_NormalizesEntries(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"regions": ["  EastUS2 ", "westus3", "", "  "]}`))
	}))
	t.Cleanup(server.Close)

	regions, err := fetchAgentRegionsFromURL(t.Context(), http.DefaultClient, server.URL)
	require.NoError(t, err)
	require.Equal(t, []string{"eastus2", "westus3"}, regions)
}

func TestFetchHostedAgentRegionsFromURL_HTTPError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	_, err := fetchAgentRegionsFromURL(t.Context(), http.DefaultClient, server.URL)
	require.Error(t, err)
}

func TestFetchHostedAgentRegionsFromURL_MalformedJSON(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{not json`))
	}))
	t.Cleanup(server.Close)

	_, err := fetchAgentRegionsFromURL(t.Context(), http.DefaultClient, server.URL)
	require.Error(t, err)
}

func TestFetchHostedAgentRegionsFromURL_EmptyManifest(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"regions": []}`))
	}))
	t.Cleanup(server.Close)

	_, err := fetchAgentRegionsFromURL(t.Context(), http.DefaultClient, server.URL)
	require.Error(t, err)
}

func TestFetchHostedAgentRegionsFromURL_RespectsTimeout(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(hostedAgentRegionsFetchTimeout + 2*time.Second)
	}))
	t.Cleanup(server.Close)

	start := time.Now()
	_, err := fetchAgentRegionsFromURL(t.Context(), http.DefaultClient, server.URL)
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
	localErr, ok := errors.AsType[*azdext.LocalError](err)
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

func TestFetchHostedAgentRegionsFromURL_RejectsOversizedManifest(t *testing.T) {
	t.Parallel()

	huge := strings.Repeat("a", hostedAgentRegionsManifestMaxBytes+1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"regions":["` + huge + `"]}`))
	}))
	t.Cleanup(server.Close)

	_, err := fetchAgentRegionsFromURL(t.Context(), http.DefaultClient, server.URL)
	require.Error(t, err)
	require.Contains(t, err.Error(), "byte limit")
}

func TestSupportedRegionsForInit_ConcurrentCallersFetchOnce(t *testing.T) {
	resetRegionsCache(t, nil)

	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		// Brief delay so concurrent callers genuinely overlap on the in-flight fetch.
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write([]byte(`{"regions": ["eastus2"]}`))
	}))
	t.Cleanup(server.Close)

	prev := hostedAgentRegionsURL
	hostedAgentRegionsURL = server.URL
	t.Cleanup(func() { hostedAgentRegionsURL = prev })

	const callers = 8
	var wg sync.WaitGroup
	wg.Add(callers)
	for range callers {
		go func() {
			defer wg.Done()
			got, err := supportedRegionsForInit(t.Context())
			require.NoError(t, err)
			require.Equal(t, []string{"eastus2"}, got)
		}()
	}
	wg.Wait()

	require.Equal(t, int32(1), hits.Load())
}

func TestSupportedRegionsForInit_FallsBackToEmbeddedOnFetchError(t *testing.T) {
	resetRegionsCache(t, nil)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	prev := hostedAgentRegionsURL
	hostedAgentRegionsURL = server.URL
	t.Cleanup(func() { hostedAgentRegionsURL = prev })

	got, err := supportedRegionsForInit(t.Context())
	require.NoError(t, err)

	want, err := parseEmbeddedHostedAgentRegions()
	require.NoError(t, err)
	require.NotEmpty(t, want)
	require.Equal(t, want, got)
}

func TestParseEmbeddedHostedAgentRegions_NotEmpty(t *testing.T) {
	t.Parallel()

	regions, err := parseEmbeddedHostedAgentRegions()
	require.NoError(t, err)
	require.NotEmpty(t, regions, "embedded fallback manifest must contain at least one region")
}

func resetRegionsCache(t *testing.T, regions []string) {
	resetAgentRegionsCache(t, &regionsCache, regions)
}

func resetAgentRegionsCache(t *testing.T, cache *agentRegionsCache, regions []string) {
	t.Helper()

	cache.mu.Lock()
	prev := cache.regions
	prevInflight := cache.inflight
	cache.regions = regions
	cache.inflight = nil
	cache.mu.Unlock()

	t.Cleanup(func() {
		cache.mu.Lock()
		cache.regions = prev
		cache.inflight = prevInflight
		cache.mu.Unlock()
	})
}
