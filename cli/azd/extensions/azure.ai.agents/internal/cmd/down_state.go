// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func stateTargetFromDeployedEndpoint(endpoint string) (*stateStoreTarget, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("deployed agent endpoint must be an HTTPS resource URL without credentials")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 5 && (len(parts) != 7 || parts[5] != "versions" || parts[6] == "") {
		return nil, fmt.Errorf("deployed agent endpoint must identify one agent and optional exact version")
	}
	u.Path, u.RawPath = "/"+strings.Join(parts[:5], "/")+"/endpoint/protocols/invocations", ""
	return stateStoreTargetFromEndpoint(u.String())
}

// A successful Project deletion invalidates all remote versions, not just the
// version currently selected by AGENT_*_ENDPOINT. Other delete flows stay scoped
// to their selected version.
func (s *agentStateStore) deleteRemoteVersions(ctx context.Context, agentKey string) error {
	agentKey = normalizeEndpoint(agentKey)
	target, err := stateTargetFromDeployedEndpoint("https://" + strings.TrimSuffix(agentKey, "/remote"))
	if err != nil {
		return fmt.Errorf("identify deleted project's exact agent state: %w", err)
	}
	base := target.agentKey
	entries, err := os.ReadDir(s.agentsDir)
	if err != nil {
		return fmt.Errorf("inventory saved agent versions: %w", err)
	}
	keys := map[string]bool{agentKey: true, base: true, base + "/remote": true}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("saved agent version inventory contains a non-regular state file")
		}
		raw, err := os.ReadFile(filepath.Join(s.agentsDir, entry.Name()))
		if err != nil {
			return fmt.Errorf("read saved agent version inventory: %w", err)
		}
		var record agentStateRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			return fmt.Errorf("decode saved agent version inventory: %w", err)
		}
		remainder, versioned := strings.CutPrefix(record.AgentKey, base+"/versions/")
		matches := record.AgentKey == base || record.AgentKey == base+"/remote"
		if versioned && strings.HasSuffix(remainder, "/remote") {
			version := strings.TrimSuffix(remainder, "/remote")
			matches = version != "" && !strings.ContainsAny(version, "/\\?#")
		}
		if !matches {
			continue
		}
		if entry.Name() != agentStateKeyHash(record.AgentKey)+".json" || record.Version != agentStateSchemaVersion {
			return fmt.Errorf("saved agent version identity or schema does not match its file")
		}
		keys[record.AgentKey] = true
	}
	for key := range keys {
		if err := s.withLock(ctx, key, func(path string) error {
			if _, err := s.load(path, key); err != nil {
				return err
			}
			return s.remove(path, key)
		}); err != nil {
			return fmt.Errorf("remove saved context for the deleted project's agent versions: %w", err)
		}
	}
	return nil
}

func cleanupDeletedProjectAgentState(
	ctx context.Context, client *azdext.AzdClient, envName string, services map[string]*azdext.ServiceConfig,
) (bool, error) {
	value, err := client.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envName, Key: "AZD_FOUNDRY_BYO_AKS_PREPARATION",
	})
	if err != nil {
		return false, fmt.Errorf("read owned Project deletion evidence for agent state cleanup: %w", err)
	}
	if value == nil || value.Value == "" {
		return false, nil
	}
	var journal struct {
		Version         int  `json:"version"`
		CleanupComplete bool `json:"cleanupComplete"`
		Foundry         *struct {
			Spec struct {
				ProjectID string `json:"projectId"`
			} `json:"spec"`
			ProjectEndpoint string `json:"projectEndpoint"`
			ProjectDeleted  bool   `json:"projectDeleted"`
			AccountDeleted  bool   `json:"accountDeleted"`
		} `json:"foundry"`
	}
	if err := json.Unmarshal([]byte(value.Value), &journal); err != nil {
		return true, fmt.Errorf("decode owned Project deletion evidence: %w", err)
	}
	if !journal.CleanupComplete {
		return true, nil
	}
	if journal.Version != 2 || journal.Foundry == nil ||
		!journal.Foundry.ProjectDeleted || !journal.Foundry.AccountDeleted {
		return true, fmt.Errorf("completed owned Project deletion evidence is unverified; saved agent contexts retained")
	}
	foundry := journal.Foundry
	id, err := arm.ParseResourceID(foundry.Spec.ProjectID)
	if err != nil || !strings.EqualFold(id.ResourceType.String(), "Microsoft.CognitiveServices/accounts/projects") {
		return true, fmt.Errorf("deleted Project identity is invalid; saved agent contexts retained")
	}
	for _, service := range services {
		if service.GetHost() != AiAgentHost {
			continue
		}
		current, err := client.Environment().GetValue(ctx, &azdext.GetEnvRequest{
			EnvName: envName, Key: "AGENT_" + toServiceKey(service.Name) + "_ENDPOINT",
		})
		if err != nil {
			return true, fmt.Errorf("read deployed agent endpoint for scoped state cleanup: %w", err)
		}
		if current == nil || current.Value == "" {
			continue
		}
		target, err := stateTargetFromDeployedEndpoint(current.Value)
		if err != nil {
			return true, fmt.Errorf("identify deployed agent for scoped state cleanup: %w", err)
		}
		if foundry.ProjectEndpoint == "" {
			return true, fmt.Errorf("deleted Project API endpoint is unverified; saved agent contexts retained")
		}
		if !strings.EqualFold(normalizeEndpoint(target.ProjectEndpoint), normalizeEndpoint(foundry.ProjectEndpoint)) {
			continue
		}
		store, err := newAgentStateStoreForClient(client)
		if err != nil {
			return true, err
		}
		if err := store.deleteRemoteVersions(ctx, buildRemoteAgentKeyFromEndpoint(current.Value)); err != nil {
			return true, err
		}
	}
	return true, nil
}
