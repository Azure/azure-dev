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
	"net/url"
	"os"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const maxRegistryResponseBytes = 4 * 1024 * 1024

type extensionUpdate struct {
	LatestVersion string
	IsBreaking    bool
	Source        string
}

type extensionUpdateChecker interface {
	Check(ctx context.Context, currentVersion string) (*extensionUpdate, error)
}

type registryExtensionUpdateChecker struct {
	client        *http.Client
	resolveSource func() (extensionUpdateSource, error)
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
	return &registryExtensionUpdateChecker{
		client:        &http.Client{Timeout: 5 * time.Second},
		resolveSource: resolveInstalledUpdateSource,
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

	source, err := c.resolveSource()
	if err != nil {
		return nil, err
	}
	reader, err := c.openRegistry(ctx, source)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	var registry rleRegistry
	data, err := io.ReadAll(io.LimitReader(reader, maxRegistryResponseBytes+1))
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
		Source:        source.Name,
	}, nil
}

func (c *registryExtensionUpdateChecker) openRegistry(
	ctx context.Context, source extensionUpdateSource,
) (io.ReadCloser, error) {
	if source.Type == "file" {
		file, err := os.Open(source.Location)
		if err != nil {
			return nil, fmt.Errorf("open RLE registry file: %w", err)
		}
		return file, nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source.Location, nil)
	if err != nil {
		return nil, fmt.Errorf("create RLE registry request: %w", err)
	}
	response, err := c.client.Do(request)
	if err != nil {
		if requestError, ok := errors.AsType[*url.Error](err); ok {
			err = requestError.Err
		}
		return nil, fmt.Errorf("download RLE registry: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		if err := response.Body.Close(); err != nil {
			return nil, fmt.Errorf("download RLE registry: HTTP %d; close response: %w", response.StatusCode, err)
		}
		return nil, fmt.Errorf("download RLE registry: HTTP %d", response.StatusCode)
	}
	return response.Body, nil
}

func breakingUpdateError(update *extensionUpdate) error {
	return &azdext.LocalError{
		Message:    fmt.Sprintf("Update the RLE extension to %s before continuing.", update.LatestVersion),
		Code:       "rle_breaking_update_required",
		Category:   azdext.LocalErrorCategoryUser,
		Suggestion: "Run: " + extensionUpdateCommand(update.Source),
	}
}

func extensionUpdateCommand(source string) string {
	return fmt.Sprintf("azd extension update azure.ai.rle --source %s", source)
}
