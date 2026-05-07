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

// validStoreFields is the allow-list of store fields that can be used as config map names.
var validStoreFields = map[string]bool{
	"sessions":      true,
	"conversations": true,
}

// validateStoreField returns an error if the field is not in the allow-list.
func validateStoreField(field string) error {
	if !validStoreFields[field] {
		return fmt.Errorf("invalid store field %q: must be one of sessions, conversations", field)
	}
	return nil
}

// validateKeySegment returns an error if the segment contains path separators
// that could produce malformed config keys.
func validateKeySegment(name, value string) error {
	if strings.ContainsAny(value, "/\\") {
		return fmt.Errorf("invalid %s %q: must not contain path separators", name, value)
	}
	return nil
}

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
	if err := validateStoreField(storeField); err != nil {
		return "", err
	}

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
	if primaryKey == "" {
		return "", nil
	}

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
			if err := setAgentSpecificContextValue(ctx, azdClient, storeField, primaryKey, val); err != nil {
				log.Printf("getContextValueWithFallback: failed to rewrite %q under primary key: %v", legacyKey, err)
			}
			return val, nil
		}
	}

	return "", nil
}

// setAgentSpecificContextValue persists a value into the named store field map in UserConfig.
// It performs a read-modify-write on the map. This is not safe for concurrent updates to
// different keys (last writer wins), which is acceptable for CLI use where only one command
// runs at a time.
func setAgentSpecificContextValue(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	storeField string,
	agentKey string,
	value string,
) error {
	if err := validateStoreField(storeField); err != nil {
		return err
	}

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
	if err := validateStoreField(storeField); err != nil {
		return err
	}

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
	// Strip the scheme (https://, http://) — it's unnecessary noise in the key.
	if idx := strings.Index(endpoint, "://"); idx != -1 {
		endpoint = endpoint[idx+3:]
	}
	// Lowercase the host portion (before the first path segment).
	if hostEnd := strings.Index(endpoint, "/"); hostEnd == -1 {
		endpoint = strings.ToLower(endpoint)
	} else {
		endpoint = strings.ToLower(endpoint[:hostEnd]) + endpoint[hostEnd:]
	}
	return endpoint
}

// buildAgentKey constructs the structured agent key used as a map key in the config store.
// Format: <endpoint>/agents/<name>/versions/<version>/{local|remote}
func buildAgentKey(endpoint, agentName, version string, local bool) string {
	if err := validateKeySegment("agentName", agentName); err != nil {
		log.Printf("buildAgentKey: %v", err)
	}
	if err := validateKeySegment("version", version); err != nil {
		log.Printf("buildAgentKey: %v", err)
	}

	if version == "" {
		version = "latest"
	}

	mode := "remote"
	if local {
		mode = "local"
	}

	return fmt.Sprintf("%s/agents/%s/versions/%s/%s", normalizeEndpoint(endpoint), agentName, version, mode)
}

// buildRemoteAgentKeyFromEndpoint constructs a remote agent key directly from
// an AGENT_{SVC}_ENDPOINT value (format: <projectEndpoint>/agents/<name>/versions/<ver>).
// It simply appends "/remote" to the normalized endpoint.
func buildRemoteAgentKeyFromEndpoint(agentEndpoint string) string {
	return normalizeEndpoint(agentEndpoint) + "/remote"
}

// buildLocalAgentKey constructs a key for local mode.
// projectPath is used to disambiguate across different projects using the same port.
func buildLocalAgentKey(port int, agentName, version, projectPath string) string {
	endpoint := fmt.Sprintf("localhost:%d/%s", port, projectHash(projectPath))
	return buildAgentKey(endpoint, agentName, version, true)
}

// projectHash returns a short hash of the project path for key isolation.
func projectHash(projectPath string) string {
	if projectPath == "" {
		return "unknown"
	}
	h := sha256.Sum256([]byte(projectPath))
	return hex.EncodeToString(h[:8])
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
