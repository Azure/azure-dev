// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
)

const (
	metadataFileName    = "metadata.json"
	metadataCommandName = "metadata"
	metadataTimeout     = 10 * time.Second
)

// MetadataManager handles extension metadata fetching and caching
type MetadataManager struct {
	configManager config.UserConfigManager
}

// NewMetadataManager creates a new metadata manager
func NewMetadataManager(configManager config.UserConfigManager) *MetadataManager {
	return &MetadataManager{
		configManager: configManager,
	}
}

// FetchAndCache fetches metadata from an extension and caches it to disk
// Returns nil error if metadata was successfully fetched and cached, or if extension doesn't support metadata
// Returns warning-level error if metadata fetch failed (installation should still succeed)
func (m *MetadataManager) FetchAndCache(
	ctx context.Context,
	extension *Extension,
) error {
	// Check if extension has metadata capability
	if !extension.HasCapability(MetadataCapability) {
		return nil // Extension doesn't support metadata - this is fine
	}

	userConfigDir, err := config.GetUserConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get user config directory: %w", err)
	}

	extensionDir := filepath.Join(userConfigDir, "extensions", extension.Id)
	metadataPath := filepath.Join(extensionDir, metadataFileName)

	// Check if metadata.json already exists (pre-packaged)
	if _, err := os.Stat(metadataPath); err == nil {
		log.Printf("Extension '%s' has pre-packaged metadata.json, skipping metadata command", extension.Id)
		return nil
	}

	// Get extension binary path
	extensionPath := filepath.Join(userConfigDir, extension.Path)
	if _, err := os.Stat(extensionPath); err != nil {
		return fmt.Errorf("extension binary not found at %s: %w", extensionPath, err)
	}

	// Execute metadata command with timeout
	cmdCtx, cancel := context.WithTimeout(ctx, metadataTimeout)
	defer cancel()

	// #nosec G204 - extensionPath is validated from trusted extension installation directory
	cmd := exec.CommandContext(cmdCtx, extensionPath, metadataCommandName)
	output, err := cmd.Output()
	if err != nil {
		if cmdCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("metadata command timed out after %v", metadataTimeout)
		}
		return fmt.Errorf("metadata command failed: %w", err)
	}

	// Parse metadata JSON
	var metadata ExtensionCommandMetadata
	if err := json.Unmarshal(output, &metadata); err != nil {
		return fmt.Errorf("failed to parse metadata JSON: %w", err)
	}

	// Validate metadata
	if metadata.ID != extension.Id {
		return fmt.Errorf(
			"metadata ID '%s' does not match extension ID '%s'",
			metadata.ID,
			extension.Id,
		)
	}

	if metadata.Version != extension.Version {
		log.Printf(
			"Warning: metadata version '%s' does not match extension version '%s'",
			metadata.Version,
			extension.Version,
		)
	}

	// Write metadata to cache
	metadataJSON, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	if err := os.WriteFile(metadataPath, metadataJSON, 0600); err != nil {
		return fmt.Errorf("failed to write metadata file: %w", err)
	}

	log.Printf("Extension '%s' metadata cached successfully", extension.Id)
	return nil
}

// Load loads cached metadata for an extension
func (m *MetadataManager) Load(extensionId string) (*ExtensionCommandMetadata, error) {
	userConfigDir, err := config.GetUserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get user config directory: %w", err)
	}

	extensionDir := filepath.Join(userConfigDir, "extensions", extensionId)
	metadataPath := filepath.Join(extensionDir, metadataFileName)

	data, err := os.ReadFile(metadataPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("metadata not found for extension '%s'", extensionId)
		}
		return nil, fmt.Errorf("failed to read metadata file: %w", err)
	}

	var metadata ExtensionCommandMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("failed to parse metadata JSON: %w", err)
	}

	return &metadata, nil
}

// Delete removes cached metadata for an extension
func (m *MetadataManager) Delete(extensionId string) error {
	userConfigDir, err := config.GetUserConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get user config directory: %w", err)
	}

	extensionDir := filepath.Join(userConfigDir, "extensions", extensionId)
	metadataPath := filepath.Join(extensionDir, metadataFileName)

	if err := os.Remove(metadataPath); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove metadata file: %w", err)
		}
		// File doesn't exist, which is fine
	}

	return nil
}

// Exists checks if cached metadata exists for an extension
func (m *MetadataManager) Exists(extensionId string) bool {
	userConfigDir, err := config.GetUserConfigDir()
	if err != nil {
		return false
	}

	extensionDir := filepath.Join(userConfigDir, "extensions", extensionId)
	metadataPath := filepath.Join(extensionDir, metadataFileName)

	_, err = os.Stat(metadataPath)
	return err == nil
}
