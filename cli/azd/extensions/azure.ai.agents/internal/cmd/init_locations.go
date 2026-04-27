// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sync"
	"time"

	"azureaiagent/internal/exterrors"
)

// hostedAgentRegionsURL points at the supported-regions manifest.
// TODO: switch to an aka.ms link once provisioned.
const hostedAgentRegionsURL = "https://raw.githubusercontent.com/Azure/azure-dev/main/" +
	"cli/azd/extensions/azure.ai.agents/hosted-agent-regions.json"

const hostedAgentRegionsFetchTimeout = 5 * time.Second

type hostedAgentRegionsManifest struct {
	Regions []string `json:"regions"`
}

var regionsCache struct {
	mu      sync.Mutex
	regions []string
}

// supportedRegionsForInit returns the list of Azure regions supported for hosted agents.
// The result is cached for the process after the first successful fetch.
func supportedRegionsForInit(ctx context.Context) ([]string, error) {
	regionsCache.mu.Lock()
	defer regionsCache.mu.Unlock()

	if regionsCache.regions != nil {
		return slices.Clone(regionsCache.regions), nil
	}

	regions, err := fetchHostedAgentRegionsFromURL(ctx, http.DefaultClient, hostedAgentRegionsURL)
	if err != nil {
		return nil, err
	}

	regionsCache.regions = regions
	return slices.Clone(regions), nil
}

// supportedModelLocations returns the intersection of a model's available locations with
// the supported hosted-agent regions.
func supportedModelLocations(ctx context.Context, modelLocations []string) ([]string, error) {
	supported, err := supportedRegionsForInit(ctx)
	if err != nil {
		return nil, err
	}

	return slices.DeleteFunc(slices.Clone(modelLocations), func(loc string) bool {
		return !locationAllowed(loc, supported)
	}), nil
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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, regionsFetchError(err)
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
