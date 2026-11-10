// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type legacyAgentState struct {
	Sessions      map[string]string          `json:"sessions"`
	Conversations map[string]string          `json:"conversations"`
	Responses     map[string]savedResponse   `json:"responses"`
	Invocations   map[string]savedInvocation `json:"invocations"`
	StateStores   map[string]string          `json:"stateStores"`
}

type legacyProjectState struct {
	Sessions      map[string]string `json:"sessions"`
	Conversations map[string]string `json:"conversations"`
	Invocations   map[string]string `json:"invocations"`
}

func (s *agentStateStore) legacyMarkerPath(agentKey string) string {
	return filepath.Join(s.locksDir, agentStateKeyHash(agentKey)+".migrated")
}

func (s *agentStateStore) markLegacyChecked(agentKey string) error {
	path := s.legacyMarkerPath(agentKey)
	if err := validateAgentStateLockFile(path); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect legacy state marker: %w", err)
	}
	if err := os.WriteFile(path, nil, osutil.PermissionFileOwnerOnly); err != nil {
		return fmt.Errorf("mark legacy state checked: %w", err)
	}
	return nil
}

// loadOrMigrate runs under the agent lock. Legacy sources remain untouched; the marker
// prevents a later delete from resurrecting their stale values.
func (s *agentStateStore) loadOrMigrate(
	ctx context.Context, path, agentKey string,
) (*agentStateRecord, error) {
	record, err := s.load(path, agentKey)
	if err != nil {
		return nil, err
	}
	if record != nil {
		if err := s.markLegacyChecked(agentKey); err != nil {
			return nil, err
		}
		return record, nil
	}
	record = &agentStateRecord{Version: agentStateSchemaVersion, AgentKey: agentKey}
	if s.client == nil {
		return record, nil
	}

	markerPath := s.legacyMarkerPath(agentKey)
	if err := validateAgentStateLockFile(markerPath); err != nil {
		return nil, err
	}
	if _, err := os.Stat(markerPath); err == nil {
		return record, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect legacy state marker: %w", err)
	}

	helper, err := azdext.NewConfigHelper(s.client)
	if err != nil {
		return nil, err
	}
	var legacy legacyAgentState
	if _, err := helper.GetUserJSON(ctx, "extensions.ai-agents", &legacy); err != nil {
		if status.Code(err) != codes.Unimplemented {
			return nil, fmt.Errorf("read legacy agent state: %w", err)
		}
		log.Printf("legacy agent state: UserConfig service is unavailable; checking project-local state")
	}
	legacy.apply(record)
	// UserConfig was introduced after the project-local file and takes precedence.
	if err := s.readLegacyProjectState(ctx, record); err != nil {
		return nil, err
	}
	if !record.empty() {
		if err := writeAgentState(path, record); err != nil {
			return nil, err
		}
	}
	if err := s.markLegacyChecked(agentKey); err != nil {
		return nil, err
	}
	return record, nil
}

func (s *agentStateStore) readLegacyProjectState(ctx context.Context, record *agentStateRecord) error {
	legacy, err := s.loadLegacyProjectState(ctx)
	if err != nil {
		return err
	}
	if legacy != nil {
		state := legacyAgentState{Sessions: legacy.Sessions, Conversations: legacy.Conversations}
		state.apply(record)
		if record.Invocation == nil {
			if id, found := legacyAgentValue(legacy.Invocations, record.AgentKey); found && id != "" {
				record.Invocation = &savedInvocation{InvocationID: id}
			}
		}
	}
	return nil
}

func (s *agentStateStore) loadLegacyProjectState(ctx context.Context) (*legacyProjectState, error) {
	project, err := s.client.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		if isMissingAzdProjectError(err) || status.Code(err) == codes.Unimplemented {
			return nil, nil
		}
		return nil, fmt.Errorf("locate legacy agent project: %w", err)
	}
	if project.GetProject() == nil || project.Project.Path == "" {
		return nil, nil
	}
	env, err := s.client.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		if isDefaultEnvironmentMissing(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("locate legacy agent environment: %w", err)
	}
	if env.GetEnvironment() == nil || env.Environment.Name == "" {
		return nil, nil
	}
	path := filepath.Join(project.Project.Path, ".azure", env.Environment.Name, ".foundry-agent.json")
	data, err := os.ReadFile(path) //nolint:gosec // path comes from the host's project and environment.
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read legacy project state: %w", err)
	}
	var legacy legacyProjectState
	if err := json.Unmarshal(data, &legacy); err != nil {
		log.Printf("legacy agent state: ignoring invalid project state %q: %v", path, err)
		return nil, nil
	}
	return &legacy, nil
}

// legacySessionKeys includes not-yet-migrated caches so deletion cannot revive a session later.
func (s *agentStateStore) legacySessionKeys(ctx context.Context, prefix, sessionID string) ([]string, error) {
	if s.client == nil {
		return nil, nil
	}
	helper, err := azdext.NewConfigHelper(s.client)
	if err != nil {
		return nil, err
	}
	var legacy legacyAgentState
	if _, err := helper.GetUserJSON(ctx, "extensions.ai-agents", &legacy); err != nil &&
		status.Code(err) != codes.Unimplemented {
		return nil, fmt.Errorf("read legacy agent sessions: %w", err)
	}
	project, err := s.loadLegacyProjectState(ctx)
	if err != nil {
		return nil, err
	}
	sessions := []map[string]string{legacy.Sessions}
	if project != nil {
		sessions = append(sessions, project.Sessions)
	}
	_, name, _ := strings.Cut(strings.TrimSuffix(prefix, "/versions/"), "/agents/")
	var keys []string
	for _, state := range sessions {
		for key, id := range state {
			if id != sessionID {
				continue
			}
			if key == name {
				keys = append(keys, prefix+"latest/remote")
				continue
			}
			key = normalizeLegacyAgentKey(key)
			if strings.HasPrefix(key, prefix) && strings.HasSuffix(key, "/remote") {
				keys = append(keys, key)
			}
		}
	}
	return keys, nil
}

func (legacy legacyAgentState) apply(record *agentStateRecord) {
	if record.SessionID == "" {
		record.SessionID, _ = legacyAgentValue(legacy.Sessions, record.AgentKey)
	}
	if record.ConversationID == "" {
		record.ConversationID, _ = legacyAgentValue(legacy.Conversations, record.AgentKey)
	}
	if record.StateStore == "" {
		record.StateStore, _ = legacyAgentValue(legacy.StateStores, record.AgentKey)
	}
	if record.Response == nil {
		if response, found := legacyAgentValue(legacy.Responses, record.AgentKey); found {
			record.Response = &response
		}
	}
	if record.Invocation == nil {
		if invocation, found := legacyAgentValue(legacy.Invocations, record.AgentKey); found {
			record.Invocation = &invocation
		}
	}
}

func normalizeLegacyAgentKey(key string) string {
	for _, mode := range []string{"/remote", "/local"} {
		if base, found := strings.CutSuffix(key, mode); found {
			return normalizeEndpoint(base) + mode
		}
	}
	return normalizeEndpoint(key)
}

func legacyAgentValue[T any](values map[string]T, agentKey string) (T, bool) {
	if value, found := values[agentKey]; found {
		return value, true
	}
	for _, key := range slices.Sorted(maps.Keys(values)) {
		if normalizeLegacyAgentKey(key) == agentKey {
			return values[key], true
		}
	}
	_, agent, found := strings.Cut(agentKey, "/agents/")
	if found {
		name, suffix, _ := strings.Cut(agent, "/")
		var fallback []string
		switch suffix {
		case "versions/latest/remote":
			fallback = []string{name}
		case "versions/latest/local":
			fallback = []string{name + "-local", "local"}
		}
		for _, key := range fallback {
			if value, found := values[key]; found {
				return value, true
			}
		}
	}
	var zero T
	return zero, false
}
