// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/gofrs/flock"
)

const (
	agentStateSchemaVersion = 1
	agentStateLockTimeout   = 30 * time.Second
)

type agentStateRecord struct {
	Version        int              `json:"version"`
	AgentKey       string           `json:"agentKey"`
	SessionID      string           `json:"sessionId,omitempty"`
	ConversationID string           `json:"conversationId,omitempty"`
	Response       *savedResponse   `json:"response,omitempty"`
	Invocation     *savedInvocation `json:"invocation,omitempty"`
	StateStore     string           `json:"stateStore,omitempty"`
}

func (r *agentStateRecord) empty() bool {
	return r.SessionID == "" &&
		r.ConversationID == "" &&
		r.Response == nil &&
		r.Invocation == nil &&
		r.StateStore == ""
}

type agentStateStore struct {
	agentsDir string
	locksDir  string
	client    *azdext.AzdClient
}

var agentStateStoreRootOverrides sync.Map

func newAgentStateStore() (*agentStateStore, error) {
	configDir, err := config.GetUserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("resolve azd config directory: %w", err)
	}
	return newAgentStateStoreAt(configDir)
}

func newAgentStateStoreForClient(client *azdext.AzdClient) (*agentStateStore, error) {
	var store *agentStateStore
	var err error
	if configDir, ok := agentStateStoreRootOverrides.Load(client); ok {
		store, err = newAgentStateStoreAt(configDir.(string))
	} else {
		store, err = newAgentStateStore()
	}
	if err != nil {
		return nil, err
	}
	store.client = client
	return store, nil
}

func newAgentStateStoreAt(configDir string) (*agentStateStore, error) {
	rootDir := filepath.Join(configDir, "state", "extensions", "azure.ai.agents")
	store := &agentStateStore{
		agentsDir: filepath.Join(rootDir, "agents"),
		locksDir:  filepath.Join(rootDir, "locks"),
	}
	for _, dir := range []string{store.agentsDir, store.locksDir} {
		//nolint:gosec // G703: directories are rooted in the configured azd user-state directory.
		if err := os.MkdirAll(dir, osutil.PermissionDirectoryOwnerOnly); err != nil {
			return nil, fmt.Errorf("create agent state directory %q: %w", dir, err)
		}
	}
	return store, nil
}

func agentStateKeyHash(agentKey string) string {
	sum := sha256.Sum256([]byte(normalizeEndpoint(agentKey)))
	return hex.EncodeToString(sum[:])
}

func (s *agentStateStore) statePath(agentKey string) string {
	return filepath.Join(s.agentsDir, agentStateKeyHash(agentKey)+".json")
}

func (s *agentStateStore) lockPath(agentKey string) string {
	return filepath.Join(s.locksDir, agentStateKeyHash(agentKey)+".lock")
}

func (s *agentStateStore) withLock(
	ctx context.Context,
	agentKey string,
	action func(string) error,
) error {
	agentKey = normalizeEndpoint(agentKey)
	if agentKey == "" {
		return errors.New("agent state key must not be empty")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	lockPath := s.lockPath(agentKey)
	if err := validateAgentStateLockFile(lockPath); err != nil {
		return err
	}
	lock := flock.New(lockPath, flock.SetPermissions(osutil.PermissionFileOwnerOnly))
	waitCtx, cancel := context.WithTimeout(ctx, agentStateLockTimeout)
	defer cancel()

	locked, err := lock.TryLockContext(waitCtx, 50*time.Millisecond)
	if err != nil || !locked {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err == nil {
			err = waitCtx.Err()
			if err == nil {
				err = errors.New("lock unavailable")
			}
		}
		return fmt.Errorf("acquire agent state lock for %q: %w", agentKey, err)
	}
	defer func() {
		_ = lock.Unlock()
	}()

	return action(s.statePath(agentKey))
}

func validateAgentStateLockFile(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect agent state lock %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("agent state lock %q is not a regular file", path)
	}
	return nil
}

func (s *agentStateStore) load(path, agentKey string) (*agentStateRecord, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect agent state %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("agent state %q is not a regular file", path)
	}

	data, err := os.ReadFile(path) //nolint:gosec // path is derived from AZD_CONFIG_DIR and a SHA-256 hash.
	if err != nil {
		return nil, fmt.Errorf("read agent state %q: %w", path, err)
	}
	var record agentStateRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("parse agent state %q: %w", path, err)
	}
	if record.Version != agentStateSchemaVersion {
		return nil, fmt.Errorf("unsupported agent state version %d in %q", record.Version, path)
	}
	if record.AgentKey != agentKey {
		return nil, fmt.Errorf("agent state key mismatch in %q", path)
	}
	return &record, nil
}

func (s *agentStateStore) get(ctx context.Context, agentKey string) (*agentStateRecord, error) {
	agentKey = normalizeEndpoint(agentKey)
	var record *agentStateRecord
	err := s.withLock(ctx, agentKey, func(path string) error {
		var err error
		record, err = s.loadOrMigrate(ctx, path, agentKey)
		return err
	})
	if err != nil {
		return nil, err
	}
	if record.empty() {
		return nil, nil
	}
	return record, nil
}

func (s *agentStateStore) mutate(
	ctx context.Context,
	agentKey string,
	mutation func(*agentStateRecord),
) error {
	agentKey = normalizeEndpoint(agentKey)
	return s.withLock(ctx, agentKey, func(path string) error {
		record, err := s.loadOrMigrate(ctx, path, agentKey)
		if err != nil {
			return err
		}
		mutation(record)
		record.AgentKey = agentKey
		if record.empty() {
			return s.remove(path, agentKey)
		}
		return writeAgentState(path, record)
	})
}

func (s *agentStateStore) delete(ctx context.Context, agentKey string) error {
	agentKey = normalizeEndpoint(agentKey)
	return s.withLock(ctx, agentKey, func(path string) error {
		return s.remove(path, agentKey)
	})
}

func (s *agentStateStore) remove(path, agentKey string) error {
	if err := s.markLegacyChecked(agentKey); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove agent state %q: %w", path, err)
	}
	return nil
}

func writeAgentState(path string, record *agentStateRecord) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("serialize agent state: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(path), ".agent-state-*")
	if err != nil {
		return fmt.Errorf("create temporary agent state: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temporary agent state: %w", err)
	}
	if err := tmp.Chmod(osutil.PermissionFileOwnerOnly); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set agent state permissions: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("flush temporary agent state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary agent state: %w", err)
	}
	if err := replaceAgentStateFile(tmpPath, path); err != nil {
		return fmt.Errorf("replace agent state %q: %w", path, err)
	}
	return nil
}
