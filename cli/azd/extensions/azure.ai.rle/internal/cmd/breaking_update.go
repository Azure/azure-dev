// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const (
	rleRegistryURLEnvVar  = "AZD_AI_RLE_REGISTRY_URL"
	defaultRleRegistryURL = "https://raw.githubusercontent.com/sujit-kamireddy/azure-dev/main/" +
		"cli/azd/extensions/registry.rle-dev.json"
	maxRegistryResponseBytes = 4 * 1024 * 1024
)

type extensionUpdate struct {
	LatestVersion string
	IsBreaking    bool
}

type extensionUpdateChecker interface {
	Check(ctx context.Context, currentVersion string) (*extensionUpdate, error)
}

type registryExtensionUpdateChecker struct {
	client      *http.Client
	registryURL string
}

type rleRegistry struct {
	Extensions []struct {
		ID       string `json:"id"`
		Versions []struct {
			Version         string `json:"version"`
			BreakingChanges bool   `json:"breakingChanges"`
		} `json:"versions"`
	} `json:"extensions"`
}

func newRegistryExtensionUpdateChecker() *registryExtensionUpdateChecker {
	registryURL := os.Getenv(rleRegistryURLEnvVar)
	if registryURL == "" {
		registryURL = defaultRleRegistryURL
	}
	return &registryExtensionUpdateChecker{
		client:      &http.Client{Timeout: 5 * time.Second},
		registryURL: registryURL,
	}
}

func (c *registryExtensionUpdateChecker) Check(
	ctx context.Context,
	currentVersion string,
) (*extensionUpdate, error) {
	if currentVersion == "dev" {
		return nil, nil
	}

	installedVersion, err := semver.NewVersion(currentVersion)
	if err != nil {
		return nil, fmt.Errorf("parse installed RLE version %q: %w", currentVersion, err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.registryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create RLE registry request: %w", err)
	}
	response, err := c.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("download RLE registry: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download RLE registry: HTTP %d", response.StatusCode)
	}

	var registry rleRegistry
	reader := io.LimitReader(response.Body, maxRegistryResponseBytes+1)
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read RLE registry: %w", err)
	}
	if len(data) > maxRegistryResponseBytes {
		return nil, fmt.Errorf("read RLE registry: response exceeds %d bytes", maxRegistryResponseBytes)
	}
	if err := json.Unmarshal(data, &registry); err != nil {
		return nil, fmt.Errorf("parse RLE registry: %w", err)
	}

	var latestVersion *semver.Version
	hasBreakingUpdate := false
	foundExtension := false
	for _, extension := range registry.Extensions {
		if extension.ID != "azure.ai.rle" {
			continue
		}
		foundExtension = true
		for _, candidate := range extension.Versions {
			candidateVersion, err := semver.NewVersion(candidate.Version)
			if err != nil {
				return nil, fmt.Errorf("parse registry RLE version %q: %w", candidate.Version, err)
			}
			if !candidateVersion.GreaterThan(installedVersion) {
				continue
			}
			if latestVersion == nil || candidateVersion.GreaterThan(latestVersion) {
				latestVersion = candidateVersion
			}
			hasBreakingUpdate = hasBreakingUpdate || candidate.BreakingChanges
		}
		break
	}

	if !foundExtension {
		return nil, fmt.Errorf("parse RLE registry: extension azure.ai.rle was not found")
	}
	if latestVersion == nil {
		return nil, nil
	}
	return &extensionUpdate{
		LatestVersion: latestVersion.Original(),
		IsBreaking:    hasBreakingUpdate,
	}, nil
}

func breakingUpdateError(currentVersion string, update *extensionUpdate) error {
	return &azdext.LocalError{
		Message: fmt.Sprintf(
			"RLE %s cannot run because updating to version %s crosses a breaking release.",
			currentVersion,
			update.LatestVersion,
		),
		Code:       "rle_breaking_update_required",
		Category:   azdext.LocalErrorCategoryUser,
		Suggestion: "Update the extension before continuing: azd extension update azure.ai.rle",
	}
}
