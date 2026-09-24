// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import (
	"context"
	"fmt"
	"maps"
	"os"

	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/google/uuid"
)

// EnvironmentState contains detached, raw environment data, without process values
// or provider mappings. Config retains unresolved secret references and vault data.
type EnvironmentState struct {
	Dotenv map[string]string
	Config config.Config
}

// SnapshotState returns a detached copy of raw dotenv and configuration.
func (e *Environment) SnapshotState() (EnvironmentState, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	cfg, err := config.Clone(e.config)
	if err != nil {
		return EnvironmentState{}, fmt.Errorf("snapshotting environment config: %w", err)
	}
	return EnvironmentState{Dotenv: maps.Clone(e.dotenv), Config: cfg}, nil
}

// ReplaceState installs a detached copy of loaded state and resets deletion
// tracking. It clones the config before acquiring mu because state.Config may be
// this environment's synchronized view, whose Clone method takes mu.RLock. The
// environment identity and any retained variable/config views remain intact.
func (e *Environment) ReplaceState(state EnvironmentState) error {
	cfg, err := config.Clone(state.Config)
	if err != nil {
		return fmt.Errorf("loading environment config: %w", err)
	}
	dotenv := make(map[string]string, len(state.Dotenv))
	maps.Copy(dotenv, state.Dotenv)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.dotenv = dotenv
	e.config = cfg
	clear(e.deletedKeys)
	return nil
}

// MergeAndSave overlays current values and pending deletions on storedDotenv,
// writes a detached snapshot, and commits the merged dotenv only on success.
// Concurrent setters wait until completion, so their changes remain pending for
// the next save. The writer must follow [Env.MergeAndSave]'s lock
// restrictions and must not call back into this environment.
func (e *Environment) MergeAndSave(
	ctx context.Context,
	storedDotenv map[string]string,
	write func(ctx context.Context, state EnvironmentState) error,
) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	merged := make(map[string]string, len(storedDotenv)+len(e.dotenv))
	maps.Copy(merged, storedDotenv)
	maps.Copy(merged, e.dotenv)
	for key := range e.deletedKeys {
		delete(merged, key)
	}
	cfg, err := config.Clone(e.config)
	if err != nil {
		return fmt.Errorf("snapshotting environment config: %w", err)
	}
	if err := write(ctx, EnvironmentState{Dotenv: maps.Clone(merged), Config: cfg}); err != nil {
		return err
	}
	e.dotenv = merged
	clear(e.deletedKeys)
	return nil
}

func traceSavedName(name string) {
	tracing.SetUsageAttributes(fields.StringHashed(fields.EnvNameKey, name))
}

func traceLoadedState(name string, state EnvironmentState) {
	traceSavedName(name)
	subscription, found := state.Dotenv[SubscriptionIdEnvVarName]
	if !found {
		subscription = os.Getenv(SubscriptionIdEnvVarName)
	}
	if _, err := uuid.Parse(subscription); err == nil {
		tracing.SetGlobalAttributes(fields.SubscriptionIdKey.String(subscription))
	} else {
		tracing.SetGlobalAttributes(fields.StringHashed(fields.SubscriptionIdKey, subscription))
	}
}
