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

// validStoreFields is the allow-list of string fields in agent state.
var validStoreFields = map[string]bool{
	"sessions":      true,
	"conversations": true,
	"stateStores":   true,
}

// validateStoreField returns an error if the field is not in the allow-list.
func validateStoreField(field string) error {
	if !validStoreFields[field] {
		return fmt.Errorf("invalid store field %q: must be one of sessions, conversations, stateStores", field)
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

// getAgentSpecificContextValue retrieves a single value from the extension-owned state store.
// Returns ("", nil) when the key is not found.
func getAgentSpecificContextValue(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	storeField string,
	agentKey string,
) (string, error) {
	if err := validateStoreField(storeField); err != nil {
		return "", err
	}

	store, err := newAgentStateStoreForClient(azdClient)
	if err != nil {
		return "", fmt.Errorf("open agent state store: %w", err)
	}
	record, err := store.get(ctx, agentKey)
	if err != nil {
		return "", fmt.Errorf("read %s agent state: %w", storeField, err)
	}
	if record == nil {
		return "", nil
	}
	switch storeField {
	case "sessions":
		return record.SessionID, nil
	case "conversations":
		return record.ConversationID, nil
	case "stateStores":
		return record.StateStore, nil
	default:
		return "", fmt.Errorf("unsupported agent state field %q", storeField)
	}
}

// setAgentSpecificContextValue persists a value into the extension-owned state store.
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

	store, err := newAgentStateStoreForClient(azdClient)
	if err != nil {
		return fmt.Errorf("open agent state store: %w", err)
	}
	return store.mutate(ctx, agentKey, func(record *agentStateRecord) {
		switch storeField {
		case "sessions":
			record.SessionID = value
		case "conversations":
			record.ConversationID = value
		case "stateStores":
			record.StateStore = value
		}
	})
}

// deleteContextValue removes a field from an agent's extension-owned state.
func deleteContextValue(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	storeField string,
	agentKey string,
) error {
	if err := validateStoreField(storeField); err != nil {
		return err
	}

	store, err := newAgentStateStoreForClient(azdClient)
	if err != nil {
		return fmt.Errorf("open agent state store: %w", err)
	}
	return store.mutate(ctx, agentKey, func(record *agentStateRecord) {
		switch storeField {
		case "sessions":
			record.SessionID = ""
		case "conversations":
			record.ConversationID = ""
		case "stateStores":
			record.StateStore = ""
		}
	})
}

// --- Agent Key Construction ---

// normalizeEndpoint canonicalizes an endpoint string for use in agent keys.
// Removes URL credentials, query, fragment, and trailing slashes, and lowercases the host.
func normalizeEndpoint(endpoint string) string {
	endpoint, _, _ = strings.Cut(endpoint, "?")
	endpoint, _, _ = strings.Cut(endpoint, "#")
	// Strip the scheme (https://, http://) — it's unnecessary noise in the key.
	if idx := strings.Index(endpoint, "://"); idx != -1 {
		endpoint = endpoint[idx+3:]
	}
	host, path, hasPath := strings.Cut(endpoint, "/")
	if userEnd := strings.LastIndex(host, "@"); userEnd != -1 {
		host = host[userEnd+1:]
	}
	host = strings.ToLower(host)
	if !hasPath {
		return host
	}
	return strings.TrimRight(host+"/"+path, "/")
}

// buildAgentKey constructs the structured agent key used in extension-owned state.
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
		log.Printf("setContextValueSafe: failed to persist %s for key %q: %v",
			storeField, normalizeEndpoint(agentKey), err)
	}
}
