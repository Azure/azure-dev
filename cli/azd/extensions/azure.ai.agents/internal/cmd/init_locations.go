// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sync"
	"time"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// hostedAgentRegionsURL points at the supported-regions manifest.
// It is a var so tests can override it.
var hostedAgentRegionsURL = "https://aka.ms/azd-ai-agents/regions"

const (
	hostedAgentRegionsFetchTimeout = 5 * time.Second
	// hostedAgentRegionsManifestMaxBytes caps the manifest body to guard against
	// unexpectedly large responses from the source URL.
	hostedAgentRegionsManifestMaxBytes = 1 << 20 // 1 MiB
)

type hostedAgentRegionsManifest struct {
	Regions []string `json:"regions"`
}

var regionsCache struct {
	mu       sync.Mutex
	regions  []string
	inflight *regionsFetch
}

// regionsFetch coordinates concurrent callers waiting on the same in-flight fetch
// so the package-level mutex can be released while the network call is running.
type regionsFetch struct {
	done    chan struct{}
	regions []string
	err     error
}

// supportedRegionsForInit returns the list of Azure regions supported for hosted agents.
// The result is cached for the process after the first successful fetch.
//
// The fetch itself is performed without holding regionsCache.mu so callers whose
// context is canceled can return promptly even if another goroutine is mid-fetch.
func supportedRegionsForInit(ctx context.Context) ([]string, error) {
	regionsCache.mu.Lock()
	if regionsCache.regions != nil {
		regions := slices.Clone(regionsCache.regions)
		regionsCache.mu.Unlock()
		return regions, nil
	}

	fetch := regionsCache.inflight
	if fetch == nil {
		fetch = &regionsFetch{done: make(chan struct{})}
		regionsCache.inflight = fetch
		go runRegionsFetch(fetch)
	}
	regionsCache.mu.Unlock()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-fetch.done:
		if fetch.err != nil {
			return nil, fetch.err
		}
		return slices.Clone(fetch.regions), nil
	}
}

// runRegionsFetch performs the network fetch, populates the cache on success, and
// signals all waiters via fetch.done. A failed fetch clears the inflight slot so
// subsequent callers retry instead of latching the error forever.
func runRegionsFetch(fetch *regionsFetch) {
	// The fetch uses its own timeout (hostedAgentRegionsFetchTimeout) and is
	// independent of any single caller's context, since the result is shared.
	regions, err := fetchHostedAgentRegionsFromURL(
		context.Background(), http.DefaultClient, hostedAgentRegionsURL,
	)

	regionsCache.mu.Lock()
	if err == nil {
		regionsCache.regions = regions
	}
	regionsCache.inflight = nil
	regionsCache.mu.Unlock()

	fetch.regions = regions
	fetch.err = err
	close(fetch.done)
}

// supportedModelLocations returns the intersection of a model's available locations with
// the supported hosted-agent regions. Returns an error when the intersection is empty
// because passing an empty allowlist downstream disables filtering, which would let users
// pick regions that are not supported for hosted agents.
func supportedModelLocations(ctx context.Context, modelLocations []string) ([]string, error) {
	supported, err := supportedRegionsForInit(ctx)
	if err != nil {
		return nil, err
	}

	result := slices.DeleteFunc(slices.Clone(modelLocations), func(loc string) bool {
		return !locationAllowed(loc, supported)
	})

	if len(result) == 0 {
		return nil, exterrors.Dependency(
			exterrors.CodeNoSupportedModelLocations,
			"the selected model is not available in any region supported for hosted agents",
			"select a different model.",
		)
	}

	return result, nil
}

func fetchHostedAgentRegionsFromURL(ctx context.Context, httpClient *http.Client, url string) ([]string, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, hostedAgentRegionsFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, regionsFetchError(err)
	}

	//nolint:gosec // URL is the hardcoded hostedAgentRegionsURL constant or test override
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, regionsFetchError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, regionsFetchError(fmt.Errorf("unexpected HTTP status %d", resp.StatusCode))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, hostedAgentRegionsManifestMaxBytes+1))
	if err != nil {
		return nil, regionsFetchError(err)
	}
	if len(body) > hostedAgentRegionsManifestMaxBytes {
		return nil, regionsFetchError(fmt.Errorf(
			"manifest exceeds %d byte limit", hostedAgentRegionsManifestMaxBytes,
		))
	}

	var manifest hostedAgentRegionsManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, regionsFetchError(err)
	}

	regions := make([]string, 0, len(manifest.Regions))
	for _, r := range manifest.Regions {
		if normalized := normalizeLocationName(r); normalized != "" {
			regions = append(regions, normalized)
		}
	}

	if len(regions) == 0 {
		return nil, regionsFetchError(fmt.Errorf("manifest contained no valid regions"))
	}

	return regions, nil
}

func regionsFetchError(err error) error {
	return exterrors.Dependency(
		exterrors.CodeRegionsFetchFailed,
		fmt.Sprintf("could not retrieve the list of supported Azure regions: %v", err),
		"check your network connection and try again. "+
			"If the issue persists, file an issue at https://github.com/Azure/azure-dev/issues",
	)
}

// isNoSupportedLocationsError reports whether err is the structured error returned by
// [supportedModelLocations] when no region in the model's location list is supported
// for hosted agents.
func isNoSupportedLocationsError(err error) bool {
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	return ok && localErr.Code == exterrors.CodeNoSupportedModelLocations
}
