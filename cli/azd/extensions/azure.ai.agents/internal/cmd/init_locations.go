// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// promptHarnessRegionsForInit loads the Copilot harness region policy shared by
// generated and adopted agents. Other prompt agents remain unrestricted.
func promptHarnessRegionsForInit(ctx context.Context, harness string) ([]string, error) {
	if !agent_api.IsCopilotHarness(harness) {
		return nil, nil
	}
	return harnessRegionsCache.load(ctx, harnessAgentRegionsURL, embeddedHarnessAgentRegionsJSON)
}

type harnessRegionPolicyKey struct{}

func withHarnessRegionPolicy(ctx context.Context, harness string) context.Context {
	return context.WithValue(ctx, harnessRegionPolicyKey{}, agent_api.IsCopilotHarness(harness))
}

func initLocationAllowed(ctx context.Context, location string, allowedLocations []string) bool {
	harness, _ := ctx.Value(harnessRegionPolicyKey{}).(bool)
	if locationAllowed(location, allowedLocations) {
		return true
	}
	if !harness || strings.TrimSpace(location) == "" {
		return false
	}
	// Accept configured locations and existing projects in internal regions.
	// Never merge them into the public allowlist used by region pickers and guidance.
	internal, err := internalHarnessRegionsCache.load(ctx, internalHarnessAgentRegionsURL, embeddedInternalHarnessRegionsJSON)
	return err == nil && len(internal) > 0 && locationAllowed(location, internal)
}

func validateInitLocation(ctx context.Context, location string, allowedLocations []string) error {
	if initLocationAllowed(ctx, location, allowedLocations) {
		return nil
	}
	return exterrors.Validation(
		exterrors.CodeLocationMismatch,
		fmt.Sprintf("location %q is not supported for this agent setup", location),
		"select a Foundry project or create one in a supported region: "+strings.Join(allowedLocations, ", "),
	)
}

// hostedAgentRegionsURL points at the supported-regions manifest.
// It is a var so tests can override it.
var hostedAgentRegionsURL = "https://aka.ms/azd-ai-agents/regions"

// harnessAgentRegionsURL points at the live public harness regions manifest.
// Updating the manifest does not require an extension release.
var harnessAgentRegionsURL = "https://aka.ms/azd-ai-agents/harness-region"

// internalHarnessAgentRegionsURL points at the independently updated internal policy.
var internalHarnessAgentRegionsURL = "https://aka.ms/azd-ai-agents/harness-internal-region"

// embeddedHostedAgentRegionsJSON is the build-time fallback used when the live
// manifest fetch fails (e.g. transient network issues, restrictive proxies).
//
//go:embed hosted-agent-regions.json
var embeddedHostedAgentRegionsJSON []byte

//go:embed harness-agent-regions.json
var embeddedHarnessAgentRegionsJSON []byte

//go:embed harness-agent-internal-regions.json
var embeddedInternalHarnessRegionsJSON []byte

const (
	hostedAgentRegionsFetchTimeout = 5 * time.Second
	// hostedAgentRegionsManifestMaxBytes caps the manifest body to guard against
	// unexpectedly large responses from the source URL.
	hostedAgentRegionsManifestMaxBytes = 1 << 20 // 1 MiB
)

type agentRegionsManifest struct {
	Regions []string `json:"regions"`
}

var regionsCache agentRegionsCache
var harnessRegionsCache agentRegionsCache
var internalHarnessRegionsCache = agentRegionsCache{allowEmpty: true}

type agentRegionsCache struct {
	// An empty internal manifest disables internal regions; public policies must be nonempty.
	allowEmpty bool
	mu         sync.Mutex
	regions    []string
	inflight   *regionsFetch
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
	return regionsCache.load(ctx, hostedAgentRegionsURL, embeddedHostedAgentRegionsJSON)
}

// load shares one fetch across callers and caches the remote or embedded result for the process.
func (cache *agentRegionsCache) load(ctx context.Context, url string, embedded []byte) ([]string, error) {
	cache.mu.Lock()
	if cache.regions != nil {
		regions := slices.Clone(cache.regions)
		cache.mu.Unlock()
		return regions, nil
	}

	fetch := cache.inflight
	if fetch == nil {
		fetch = &regionsFetch{done: make(chan struct{})}
		cache.inflight = fetch
		// context.WithoutCancel keeps any context values but drops cancellation,
		// because the fetch result is shared across all waiters and must not be
		// aborted by a single caller's cancellation.
		go cache.runRegionsFetch(context.WithoutCancel(ctx), fetch, url, embedded)
	}
	cache.mu.Unlock()

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
// signals all waiters via fetch.done. If the fetch fails, the embedded build-time
// manifest is used as a fallback so a transient network issue doesn't halt init.
//
// ctx must not carry a cancellation that any single caller can trigger, since the
// fetch result is shared. Callers pass context.WithoutCancel(callerCtx).
func (cache *agentRegionsCache) runRegionsFetch(ctx context.Context, fetch *regionsFetch, url string, embedded []byte) {
	// The fetch applies its own timeout (hostedAgentRegionsFetchTimeout).
	regions, err := fetchAgentRegionsManifest(ctx, http.DefaultClient, url, cache.allowEmpty)

	if err != nil {
		if fallback, fbErr := parseAgentRegions(embedded, cache.allowEmpty); fbErr == nil {
			regions = fallback
			err = nil
		}
	}

	cache.mu.Lock()
	if err == nil {
		cache.regions = regions
	}
	cache.inflight = nil
	cache.mu.Unlock()

	fetch.regions = regions
	fetch.err = err
	close(fetch.done)
}

// parseEmbeddedHostedAgentRegions decodes the embedded build-time manifest used
// as a fallback when the live fetch fails.
func parseEmbeddedHostedAgentRegions() ([]string, error) {
	return parseAgentRegionsManifest(embeddedHostedAgentRegionsJSON)
}

func parseAgentRegionsManifest(data []byte) ([]string, error) {
	return parseAgentRegions(data, false)
}

func parseAgentRegions(data []byte, allowEmpty bool) ([]string, error) {
	var manifest agentRegionsManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	regions := make([]string, 0, len(manifest.Regions))
	for _, r := range manifest.Regions {
		if normalized := normalizeLocationName(r); normalized != "" {
			regions = append(regions, normalized)
		}
	}
	if len(regions) == 0 && (!allowEmpty || manifest.Regions == nil || len(manifest.Regions) > 0) {
		return nil, fmt.Errorf("manifest contained no valid regions")
	}
	return regions, nil
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

func fetchAgentRegionsFromURL(ctx context.Context, httpClient *http.Client, url string) ([]string, error) {
	return fetchAgentRegionsManifest(ctx, httpClient, url, false)
}

func fetchAgentRegionsManifest(ctx context.Context, httpClient *http.Client, url string, allowEmpty bool) ([]string, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, hostedAgentRegionsFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, regionsFetchError(err)
	}

	//nolint:gosec // URL is a hardcoded agent regions manifest URL or test override
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

	regions, err := parseAgentRegions(body, allowEmpty)
	if err != nil {
		return nil, regionsFetchError(err)
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
