// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package envkeys

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// Store provides access to azd environment values.
type Store interface {
	Get(ctx context.Context, key string) (value string, present bool, err error)
	Set(ctx context.Context, key, value string) error
}

// EnvServiceStore adapts the azd Environment RPCs to Store.
type EnvServiceStore struct {
	Client  azdext.EnvironmentServiceClient
	EnvName string
}

// Get reports presence on a successful RPC, even for empty values.
// GetValue cannot distinguish missing keys from explicit empties.
func (s EnvServiceStore) Get(ctx context.Context, key string) (string, bool, error) {
	resp, err := s.Client.GetValue(ctx, &azdext.GetEnvRequest{EnvName: s.EnvName, Key: key})
	if err != nil {
		return "", false, fmt.Errorf("failed to get environment variable %s: %w", key, err)
	}
	return resp.GetValue(), true, nil
}

// Set writes a value to the selected azd environment.
func (s EnvServiceStore) Set(ctx context.Context, key, value string) error {
	_, err := s.Client.SetValue(ctx, &azdext.SetEnvRequest{EnvName: s.EnvName, Key: key, Value: value})
	if err != nil {
		return fmt.Errorf("failed to set environment variable %s: %w", key, err)
	}
	return nil
}

// Input is a user-owned key with read-only typed access.
// Init records explicit choices through existing environment helpers.
type Input struct{ name string }

// Output is an extension-owned string value.
type Output struct{ name string }

type acrModeKey struct{ name string }

// Name returns the environment key.
func (k Input) Name() string { return k.name }

// Get reads the input without changing it.
func (k Input) Get(ctx context.Context, s Store) (string, bool, error) {
	return s.Get(ctx, k.name)
}

// Name returns the environment key.
func (k Output) Name() string { return k.name }

// Get reads the output.
func (k Output) Get(ctx context.Context, s Store) (string, bool, error) {
	return s.Get(ctx, k.name)
}

// Set writes the output.
func (k Output) Set(ctx context.Context, s Store, value string) error {
	return s.Set(ctx, k.name, value)
}

// Name returns the environment key.
func (k acrModeKey) Name() string { return k.name }

// Get validates a decision, leaving an empty value unset.
func (k acrModeKey) Get(ctx context.Context, s Store) (AcrMode, bool, error) {
	raw, present, err := s.Get(ctx, k.name)
	if err != nil || !present || raw == "" {
		return "", present, err
	}
	value, err := ParseAcrMode(raw)
	return value, true, err
}

// Set validates and writes a decision.
func (k acrModeKey) Set(ctx context.Context, s Store, value AcrMode) error {
	if value == "" {
		return fmt.Errorf("%s cannot be empty", k.name)
	}
	if _, err := ParseAcrMode(string(value)); err != nil {
		return err
	}
	return s.Set(ctx, k.name, string(value))
}

// AcrMode records how the project obtains its registry.
type AcrMode string

const (
	// AcrCreate provisions an extension-owned registry.
	AcrCreate AcrMode = "create"
	// AcrReuseConnect connects an existing registry to the project.
	AcrReuseConnect AcrMode = "reuse-connect"
	// AcrAlreadyConnected uses an existing project connection.
	AcrAlreadyConnected AcrMode = "already-connected"
	// AcrNone disables registry provisioning.
	AcrNone AcrMode = "none"
)

// ParseAcrMode validates a mode without choosing a default.
// An empty value remains unset for the caller to resolve.
func ParseAcrMode(raw string) (AcrMode, error) {
	switch mode := AcrMode(raw); mode {
	case "", AcrCreate, AcrReuseConnect, AcrAlreadyConnected, AcrNone:
		return mode, nil
	default:
		return "", fmt.Errorf("%s has unsupported value %q", FoundryAcrMode.Name(), raw)
	}
}

var (
	// ContainerRegistryResourceID is the user-selected registry.
	ContainerRegistryResourceID = Input{name: "AZURE_CONTAINER_REGISTRY_RESOURCE_ID"}
	// ContainerRegistryEndpoint holds the registry login server.
	ContainerRegistryEndpoint = Output{name: "AZURE_CONTAINER_REGISTRY_ENDPOINT"}
	// FoundryAcrMode records the provisioning and ownership decision.
	FoundryAcrMode = acrModeKey{name: "AZD_FOUNDRY_ACR_MODE"}
	// ProjectAcrConnectionName is the project's registry connection.
	ProjectAcrConnectionName = Output{name: "AZURE_AI_PROJECT_ACR_CONNECTION_NAME"}
	// FoundryAcrPullAssigned stores pull access as "true" or "false".
	FoundryAcrPullAssigned = Output{name: "AZD_FOUNDRY_ACR_PULL_ASSIGNED"}
	// AgentSkipAcr is the user or CI registry skip flag.
	AgentSkipAcr = Input{name: "AZD_AGENT_SKIP_ACR"}
)
