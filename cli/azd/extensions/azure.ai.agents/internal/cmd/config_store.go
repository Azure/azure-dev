// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const (
	// configPathPrefix is the UserConfig namespace for agent context state.
	configPathPrefix = "extensions.ai-agents"
)

// configPath builds the full UserConfig path for a given store field.
func configPath(storeField string) string {
	return configPathPrefix + "." + storeField
}

// getAgentSpecificContextValue retrieves a single value from the named store field map in UserConfig.
// Returns ("", nil) when the key is not found.
// The JSON path for the property to retrieve is constructed like "extensions.ai-agents.<storeField>.<agentKey>",
// where <agentKey> is a structured key representing the agent (e.g. "<project endpoint>/agents/<agent name>/version/<agent version>/{local|remote}").
func getAgentSpecificContextValue(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	storeField string,
	agentKey string,
) (string, error) {
	ch, err := azdext.NewConfigHelper(azdClient)
	if err != nil {
		return "", fmt.Errorf("getAgentSpecificContextValue: %w", err)
	}

	var store map[string]string
	found, err := ch.GetUserJSON(ctx, configPath(storeField), &store)
	if err != nil {
		return "", fmt.Errorf("getAgentSpecificContextValue: failed to read %s: %w", storeField, err)
	}

	if !found || store == nil {
		return "", nil
	}

	return store[agentKey], nil
}

// getContextValueWithFallback tries the primary key first, then falls back to legacy keys.
// If a value is found under a legacy key, it is rewritten to the primary key for future lookups.
func getContextValueWithFallback(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	storeField string,
	primaryKey string,
	legacyKeys []string,
) (string, error) {
	// Try primary key first.
	val, err := getAgentSpecificContextValue(ctx, azdClient, storeField, primaryKey)
	if err != nil {
		return "", err
	}
	if val != "" {
		return val, nil
	}

	// Try legacy keys.
	for _, legacyKey := range legacyKeys {
		if legacyKey == "" || legacyKey == primaryKey {
			continue
		}
		val, err = getAgentSpecificContextValue(ctx, azdClient, storeField, legacyKey)
		if err != nil {
			continue
		}
		if val != "" {
			// Rewrite under primary key for future lookups.
			_ = setAgentSpecificContextValue(ctx, azdClient, storeField, primaryKey, val)
			return val, nil
		}
	}

	return "", nil
}

// setAgentSpecificContextValue persists a value into the named store field map in UserConfig.
// It performs a read-modify-write on the map.
// The JSON path for the property to store is constructed like "extensions.ai-agents.<storeField>.<agentKey>",
// where <agentKey> is a structured key representing the agent (e.g. "<project endpoint>/agents/<agent name>/version/<agent version>/{local|remote}").
func setAgentSpecificContextValue(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	storeField string,
	agentKey string,
	value string,
) error {
	ch, err := azdext.NewConfigHelper(azdClient)
	if err != nil {
		return fmt.Errorf("setAgentSpecificContextValue: %w", err)
	}

	var store map[string]string
	found, err := ch.GetUserJSON(ctx, configPath(storeField), &store)
	if err != nil {
		return fmt.Errorf("setAgentSpecificContextValue: failed to read %s: %w", storeField, err)
	}

	if !found || store == nil {
		store = make(map[string]string)
	}

	store[agentKey] = value

	if err := ch.SetUserJSON(ctx, configPath(storeField), store); err != nil {
		return fmt.Errorf("setAgentSpecificContextValue: failed to write %s: %w", storeField, err)
	}

	return nil
}

// deleteContextValue removes a key from the named store field map in UserConfig.
func deleteContextValue(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	storeField string,
	agentKey string,
) error {
	ch, err := azdext.NewConfigHelper(azdClient)
	if err != nil {
		return fmt.Errorf("deleteContextValue: %w", err)
	}

	var store map[string]string
	found, err := ch.GetUserJSON(ctx, configPath(storeField), &store)
	if err != nil {
		return fmt.Errorf("deleteContextValue: failed to read %s: %w", storeField, err)
	}

	if !found || store == nil {
		return nil
	}

	delete(store, agentKey)

	if err := ch.SetUserJSON(ctx, configPath(storeField), store); err != nil {
		return fmt.Errorf("deleteContextValue: failed to write %s: %w", storeField, err)
	}

	return nil
}

// --- Agent Key Construction ---

// normalizeEndpoint canonicalizes an endpoint string for use in agent keys.
// Removes trailing slashes and lowercases the host portion.
func normalizeEndpoint(endpoint string) string {
	endpoint = strings.TrimRight(endpoint, "/")
	// Lowercase the scheme+host portion (before the first path segment).
	if idx := strings.Index(endpoint, "://"); idx != -1 {
		hostEnd := strings.Index(endpoint[idx+3:], "/")
		if hostEnd == -1 {
			endpoint = strings.ToLower(endpoint)
		} else {
			hostEnd += idx + 3
			endpoint = strings.ToLower(endpoint[:hostEnd]) + endpoint[hostEnd:]
		}
	}
	return endpoint
}

// buildAgentKey constructs the structured agent key used as a map key in the config store.
// Format: <endpoint>/agents/<name>/version/<version>/{local|remote}
func buildAgentKey(endpoint, agentName, version string, local bool) string {
	if version == "" {
		version = "latest"
	}

	mode := "remote"
	if local {
		mode = "local"
	}

	return fmt.Sprintf("%s/agents/%s/version/%s/%s", normalizeEndpoint(endpoint), agentName, version, mode)
}

// buildLocalAgentKey constructs a key for local mode.
// projectPath is used to disambiguate across different projects using the same port.
func buildLocalAgentKey(port int, agentName, version, projectPath string) string {
	endpoint := fmt.Sprintf("localhost:%d/%s", port, projectHash(projectPath))
	return buildAgentKey(endpoint, agentName, version, true)
}

// buildRemoteAgentKey is a convenience for remote mode keys.
func buildRemoteAgentKey(projectEndpoint, agentName, version string) string {
	return buildAgentKey(projectEndpoint, agentName, version, false)
}

// projectHash returns a short hash of the project path for key isolation.
func projectHash(projectPath string) string {
	if projectPath == "" {
		return "unknown"
	}
	h := sha256.Sum256([]byte(projectPath))
	return hex.EncodeToString(h[:8])
}

// legacyLocalKey returns the old-style key used before this migration.
// Format was "{serviceName}-local" or "local".
func legacyLocalKey(serviceName string) string {
	if serviceName == "" {
		return "local"
	}
	return serviceName + "-local"
}

// setContextValueSafe wraps setContextValue with error logging (non-fatal).
// Use this for fire-and-forget persistence where failure should not block the caller.
func setContextValueSafe(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	storeField string,
	agentKey string,
	value string,
) {
	if value == "" {
		return
	}
	if err := setAgentSpecificContextValue(ctx, azdClient, storeField, agentKey, value); err != nil {
		log.Printf("setContextValueSafe: failed to persist %s for key %q: %v", storeField, agentKey, err)
	}
}
