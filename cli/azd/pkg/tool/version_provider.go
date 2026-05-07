// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"runtime"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
)

// LatestVersionProvider retrieves the latest available version of a tool
// from a remote source (package registry, marketplace, etc.).
type LatestVersionProvider interface {
	// GetLatestVersion returns the latest version string for the given
	// tool, or an error if the version cannot be determined.
	GetLatestVersion(
		ctx context.Context,
		tool *ToolDefinition,
	) (string, error)
}

// ---------------------------------------------------------------------------
// PackageManagerVersionProvider
// ---------------------------------------------------------------------------

// PackageManagerVersionProvider queries a platform package manager
// (npm, winget, brew) for the latest available version of a tool.
type PackageManagerVersionProvider struct {
	commandRunner exec.CommandRunner
}

// NewPackageManagerVersionProvider creates a provider that uses the
// given command runner to invoke package manager queries.
func NewPackageManagerVersionProvider(
	commandRunner exec.CommandRunner,
) *PackageManagerVersionProvider {
	return &PackageManagerVersionProvider{
		commandRunner: commandRunner,
	}
}

// GetLatestVersion queries the package manager configured in the
// tool's install strategy for the current platform.
func (p *PackageManagerVersionProvider) GetLatestVersion(
	ctx context.Context,
	tool *ToolDefinition,
) (string, error) {
	strategy, ok := tool.InstallStrategies[runtime.GOOS]
	if !ok {
		return "", fmt.Errorf(
			"no install strategy for %s on %s",
			tool.Id, runtime.GOOS,
		)
	}

	if strategy.PackageManager == "" || strategy.PackageId == "" {
		return "", fmt.Errorf(
			"no package manager configured for %s", tool.Id,
		)
	}

	switch strategy.PackageManager {
	case "npm":
		return p.queryNpm(ctx, strategy.PackageId)
	case "winget":
		return p.queryWinget(ctx, strategy.PackageId)
	case "brew":
		return p.queryBrew(ctx, strategy.PackageId)
	case "apt":
		return p.queryApt(ctx, strategy.PackageId)
	default:
		return "", fmt.Errorf(
			"unsupported package manager %q for version query",
			strategy.PackageManager,
		)
	}
}

// queryNpm runs `npm view <pkg> version` and returns the trimmed stdout.
func (p *PackageManagerVersionProvider) queryNpm(
	ctx context.Context,
	packageID string,
) (string, error) {
	result, err := p.commandRunner.Run(ctx, exec.RunArgs{
		Cmd:  "npm",
		Args: []string{"view", packageID, "version"},
	})
	if err != nil {
		return "", fmt.Errorf("npm view %s: %w", packageID, err)
	}

	version := strings.TrimSpace(result.Stdout)
	if version == "" {
		return "", fmt.Errorf(
			"npm view returned empty version for %s", packageID,
		)
	}

	return version, nil
}

// queryWinget runs `winget show --id <pkg>` and parses the "Version"
// field from the text output.
func (p *PackageManagerVersionProvider) queryWinget(
	ctx context.Context,
	packageID string,
) (string, error) {
	result, err := p.commandRunner.Run(ctx, exec.RunArgs{
		Cmd: "winget",
		Args: []string{
			"show",
			"--id", packageID,
			"--disable-interactivity",
			"--accept-source-agreements",
		},
	})
	if err != nil {
		return "", fmt.Errorf(
			"winget show %s: %w", packageID, err,
		)
	}

	return parseWingetVersion(result.Stdout)
}

// parseWingetVersion extracts the version from winget show output.
// The output contains lines like "Version: 2.65.0".
func parseWingetVersion(output string) (string, error) {
	for line := range strings.SplitSeq(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(trimmed, "Version:"); ok {
			version := strings.TrimSpace(after)
			if version != "" {
				return version, nil
			}
		}
	}

	return "", fmt.Errorf(
		"could not find Version field in winget output",
	)
}

// brewInfoJSON models the relevant subset of `brew info --json=v2`.
type brewInfoJSON struct {
	Formulae []struct {
		Versions struct {
			Stable string `json:"stable"`
		} `json:"versions"`
	} `json:"formulae"`
}

// queryBrew runs `brew info <pkg> --json=v2` and parses the stable
// version from the JSON response.
func (p *PackageManagerVersionProvider) queryBrew(
	ctx context.Context,
	packageID string,
) (string, error) {
	result, err := p.commandRunner.Run(ctx, exec.RunArgs{
		Cmd:  "brew",
		Args: []string{"info", packageID, "--json=v2"},
	})
	if err != nil {
		return "", fmt.Errorf(
			"brew info %s: %w", packageID, err,
		)
	}

	var info brewInfoJSON
	if err := json.Unmarshal(
		[]byte(result.Stdout), &info,
	); err != nil {
		return "", fmt.Errorf(
			"parsing brew info JSON for %s: %w", packageID, err,
		)
	}

	if len(info.Formulae) == 0 ||
		info.Formulae[0].Versions.Stable == "" {
		return "", fmt.Errorf(
			"no stable version found for %s in brew", packageID,
		)
	}

	return info.Formulae[0].Versions.Stable, nil
}

// queryApt runs `apt-cache policy <pkg>` and parses the Candidate
// version from the output.
func (p *PackageManagerVersionProvider) queryApt(
	ctx context.Context,
	packageID string,
) (string, error) {
	result, err := p.commandRunner.Run(ctx, exec.RunArgs{
		Cmd:  "apt-cache",
		Args: []string{"policy", packageID},
	})
	if err != nil {
		return "", fmt.Errorf(
			"apt-cache policy %s: %w", packageID, err,
		)
	}

	return parseAptCandidate(result.Stdout)
}

// parseAptCandidate extracts the upstream version from apt-cache
// policy output. The output looks like:
//
//	azure-cli:
//	  Installed: 2.65.0-1~noble
//	  Candidate: 2.67.0-1~noble
//	  Version table:
//	    ...
//
// The Debian revision suffix (everything from the first hyphen
// following at least one digit) is stripped so that "2.67.0-1~noble"
// becomes "2.67.0".
func parseAptCandidate(output string) (string, error) {
	for line := range strings.SplitSeq(output, "\n") {
		trimmed := strings.TrimSpace(line)
		after, ok := strings.CutPrefix(trimmed, "Candidate:")
		if !ok {
			continue
		}

		version := strings.TrimSpace(after)
		if version == "" || version == "(none)" {
			return "", fmt.Errorf(
				"no candidate version available in apt",
			)
		}

		// Strip Debian revision suffix (e.g. "-1~noble").
		if idx := strings.Index(version, "-"); idx > 0 {
			version = version[:idx]
		}

		return version, nil
	}

	return "", fmt.Errorf(
		"could not find Candidate field in apt-cache output",
	)
}

// ---------------------------------------------------------------------------
// ExtensionRegistryVersionProvider
// ---------------------------------------------------------------------------

// ExtensionRegistryVersionProvider queries the azd extension registry
// for the latest version of a library-category tool.
type ExtensionRegistryVersionProvider struct {
	cacheManager *extensions.RegistryCacheManager
}

// NewExtensionRegistryVersionProvider creates a provider backed by
// the given registry cache manager.
func NewExtensionRegistryVersionProvider(
	cacheManager *extensions.RegistryCacheManager,
) *ExtensionRegistryVersionProvider {
	return &ExtensionRegistryVersionProvider{
		cacheManager: cacheManager,
	}
}

// GetLatestVersion returns the latest version of the azd extension
// identified by the tool's Id.
func (p *ExtensionRegistryVersionProvider) GetLatestVersion(
	ctx context.Context,
	tool *ToolDefinition,
) (string, error) {
	version, err := p.cacheManager.GetExtensionLatestVersion(
		ctx, extensions.MainRegistryName, tool.Id,
	)
	if err != nil {
		if errors.Is(err, extensions.ErrCacheNotFound) ||
			errors.Is(err, extensions.ErrCacheExpired) {
			return "", fmt.Errorf(
				"extension registry cache is stale or missing for %s;"+
					" run 'azd extension list' to refresh: %w",
				tool.Id, err,
			)
		}
		return "", fmt.Errorf(
			"extension registry lookup for %s: %w", tool.Id, err,
		)
	}

	return version, nil
}

// ---------------------------------------------------------------------------
// MarketplaceVersionProvider
// ---------------------------------------------------------------------------

// vsMarketplaceURL is the VS Code Marketplace extension query API.
const vsMarketplaceURL = "https://marketplace.visualstudio.com/" +
	"_apis/public/gallery/extensionquery"

const (
	// vsMarketplaceFilterByName is the filter type for searching
	// by extension identifier (e.g. "ms-azuretools.vscode-bicep").
	vsMarketplaceFilterByName = 7

	// vsMarketplaceQueryFlags is a bitmask requesting:
	//   0x002 IncludeFiles
	//   0x010 IncludeVersionProperties
	//   0x080 IncludeStatistics
	//   0x100 IncludeVersions
	//   0x200 IncludeLatestVersionOnly
	//   0x080 + 0x200 + 0x100 + 0x010 + 0x002 + 0x004 = 0x392 = 914
	vsMarketplaceQueryFlags = 914
)

// MarketplaceVersionProvider queries the VS Code Marketplace for the
// latest version of a VS Code extension.
type MarketplaceVersionProvider struct {
	httpClient httpDoer
	baseURL    string
}

// NewMarketplaceVersionProvider creates a provider that uses the
// given HTTP client for marketplace API calls.
func NewMarketplaceVersionProvider(
	httpClient httpDoer,
) *MarketplaceVersionProvider {
	return &MarketplaceVersionProvider{
		httpClient: httpClient,
		baseURL:    vsMarketplaceURL,
	}
}

// marketplaceQuery is the request body for the VS Code Marketplace
// extension query API.
type marketplaceQuery struct {
	Filters []marketplaceFilter `json:"filters"`
	Flags   int                 `json:"flags"`
}

type marketplaceFilter struct {
	Criteria []marketplaceCriteria `json:"criteria"`
}

type marketplaceCriteria struct {
	FilterType int    `json:"filterType"`
	Value      string `json:"value"`
}

// vsMarketplaceResponse models the relevant subset of the extension
// query API response.
type vsMarketplaceResponse struct {
	Results []struct {
		Extensions []struct {
			Versions []struct {
				Version string `json:"version"`
			} `json:"versions"`
		} `json:"extensions"`
	} `json:"results"`
}

// GetLatestVersion queries the VS Code Marketplace for the latest
// version of the extension identified by the tool's Id.
func (p *MarketplaceVersionProvider) GetLatestVersion(
	ctx context.Context,
	tool *ToolDefinition,
) (string, error) {
	// The tool Id is the VS Code extension identifier
	// (e.g. "ms-azuretools.vscode-bicep").
	query := marketplaceQuery{
		Filters: []marketplaceFilter{{
			Criteria: []marketplaceCriteria{{
				FilterType: vsMarketplaceFilterByName,
				Value:      tool.Id,
			}},
		}},
		Flags: vsMarketplaceQueryFlags,
	}

	payloadBytes, err := json.Marshal(query)
	if err != nil {
		return "", fmt.Errorf("marshaling marketplace query: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, p.baseURL,
		bytes.NewReader(payloadBytes),
	)
	if err != nil {
		return "", fmt.Errorf("creating marketplace request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept",
		"application/json;api-version=6.0-preview.1")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf(
			"marketplace API call for %s: %w", tool.Id, err,
		)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf(
			"marketplace API returned HTTP %d for %s",
			resp.StatusCode, tool.Id,
		)
	}

	var body vsMarketplaceResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf(
			"decoding marketplace response for %s: %w",
			tool.Id, err,
		)
	}

	if len(body.Results) == 0 ||
		len(body.Results[0].Extensions) == 0 ||
		len(body.Results[0].Extensions[0].Versions) == 0 {
		return "", fmt.Errorf(
			"no versions found in marketplace for %s", tool.Id,
		)
	}

	return body.Results[0].Extensions[0].Versions[0].Version, nil
}

// ---------------------------------------------------------------------------
// Provider selection
// ---------------------------------------------------------------------------

// SelectVersionProvider returns the appropriate LatestVersionProvider
// for the given tool based on its category and configuration. Returns
// nil when no provider applies.
func SelectVersionProvider(
	tool *ToolDefinition,
	commandRunner exec.CommandRunner,
	registryCacheManager *extensions.RegistryCacheManager,
	httpClient httpDoer,
) LatestVersionProvider {
	switch tool.Category {
	case ToolCategoryLibrary:
		if registryCacheManager != nil {
			return NewExtensionRegistryVersionProvider(
				registryCacheManager,
			)
		}
		return nil

	case ToolCategoryExtension:
		if httpClient != nil {
			return NewMarketplaceVersionProvider(httpClient)
		}
		return nil

	default:
		// CLI and Server tools: check if a package manager
		// strategy is available for the current platform.
		if strategy, ok := tool.InstallStrategies[runtime.GOOS]; ok {
			if strategy.PackageManager != "" &&
				strategy.PackageId != "" &&
				isQueryableManager(strategy.PackageManager) {
				return NewPackageManagerVersionProvider(
					commandRunner,
				)
			}
		}
		// Tools installed via InstallCommand only (e.g. az on
		// Linux) have no queryable package manager, so update
		// detection is not supported on this platform.
		log.Printf(
			"version-provider: no queryable provider for %s on %s",
			tool.Id, runtime.GOOS,
		)
		return nil
	}
}

// isQueryableManager reports whether the named package manager
// supports version queries via SelectVersionProvider.
func isQueryableManager(name string) bool {
	switch name {
	case "npm", "winget", "brew", "apt":
		return true
	default:
		return false
	}
}

// SelectVersionProviders builds a map of tool ID to provider for a
// set of tools. Tools that have no applicable provider are omitted.
func SelectVersionProviders(
	tools []*ToolDefinition,
	commandRunner exec.CommandRunner,
	registryCacheManager *extensions.RegistryCacheManager,
	httpClient httpDoer,
) map[string]LatestVersionProvider {
	providers := make(map[string]LatestVersionProvider, len(tools))
	for _, t := range tools {
		p := SelectVersionProvider(
			t, commandRunner, registryCacheManager, httpClient,
		)
		if p != nil {
			providers[t.Id] = p
			log.Printf(
				"version-provider: selected %T for %s",
				p, t.Id,
			)
		}
	}
	return providers
}
