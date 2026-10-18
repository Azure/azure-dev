// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package envkeys

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

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

// Get returns present=true on every successful RPC, including empty values.
// GetValue cannot distinguish an absent key from an explicitly empty value.
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
// Init may record an explicit user choice through the underlying environment service.
type Input struct{ name string }

// Output is an extension-owned string value.
type Output struct{ name string }

// Enum is an extension-owned decision restricted to registered values.
type Enum[T ~string] struct {
	name    string
	allowed []T
}

// JSON is an extension-owned structured value with strict decoding and optional validation.
type JSON[T any] struct {
	name     string
	validate func(T) error
}

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
func (k Enum[T]) Name() string { return k.name }

// Get reads and validates a decision. An empty value is treated as unset.
func (k Enum[T]) Get(ctx context.Context, s Store) (T, bool, error) {
	raw, present, err := s.Get(ctx, k.name)
	if err != nil || !present || raw == "" {
		return "", present, err
	}
	value := T(raw)
	if err := k.check(value); err != nil {
		return "", true, err
	}
	return value, true, nil
}

// Set validates and writes a decision.
func (k Enum[T]) Set(ctx context.Context, s Store, value T) error {
	if err := k.check(value); err != nil {
		return err
	}
	return s.Set(ctx, k.name, string(value))
}

func (k Enum[T]) check(value T) error {
	if !slices.Contains(k.allowed, value) {
		return fmt.Errorf("%s has unsupported value %q (allowed: %v)", k.name, value, k.allowed)
	}
	return nil
}

// Name returns the environment key.
func (k JSON[T]) Name() string { return k.name }

// Get rejects unknown fields, trailing data, and values rejected by the key's validator.
func (k JSON[T]) Get(ctx context.Context, s Store) (T, bool, error) {
	var zero T
	raw, present, err := s.Get(ctx, k.name)
	if err != nil || !present {
		return zero, present, err
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	var value T
	if err := dec.Decode(&value); err != nil {
		return zero, true, fmt.Errorf("%s is not valid: %w", k.name, err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return zero, true, fmt.Errorf("%s has trailing data: %w", k.name, err)
		}
		return zero, true, fmt.Errorf("%s has trailing data", k.name)
	}
	if k.validate != nil {
		if err := k.validate(value); err != nil {
			return zero, true, fmt.Errorf("%s is not valid: %w", k.name, err)
		}
	}
	return value, true, nil
}

// Set validates and encodes a structured value before writing it.
func (k JSON[T]) Set(ctx context.Context, s Store, value T) error {
	if k.validate != nil {
		if err := k.validate(value); err != nil {
			return fmt.Errorf("%s is not valid: %w", k.name, err)
		}
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode %s: %w", k.name, err)
	}
	return s.Set(ctx, k.name, string(data))
}

// AcrMode records how a Foundry project obtains its container registry.
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

var (
	// ContainerRegistryResourceID identifies the registry selected by the user.
	ContainerRegistryResourceID = Input{name: "AZURE_CONTAINER_REGISTRY_RESOURCE_ID"}
	// ContainerRegistryEndpoint holds the provisioned or selected registry's login server.
	ContainerRegistryEndpoint = Output{name: "AZURE_CONTAINER_REGISTRY_ENDPOINT"}
	// FoundryAcrMode records the registry provisioning and ownership decision.
	FoundryAcrMode = Enum[AcrMode]{
		name:    "AZD_FOUNDRY_ACR_MODE",
		allowed: []AcrMode{AcrCreate, AcrReuseConnect, AcrAlreadyConnected, AcrNone},
	}
	// ProjectAcrConnectionName holds the registry connection name in the Foundry project.
	ProjectAcrConnectionName = Output{name: "AZURE_AI_PROJECT_ACR_CONNECTION_NAME"}
	// FoundryAcrPullAssigned records whether pull access was assigned, as "true" or "false".
	FoundryAcrPullAssigned = Output{name: "AZD_FOUNDRY_ACR_PULL_ASSIGNED"}
	// AgentSkipAcr is the user or CI flag that skips registry provisioning.
	AgentSkipAcr = Input{name: "AZD_AGENT_SKIP_ACR"}
)

// AllKeys returns the registered keys for documentation and inventory.
func AllKeys() []interface{ Name() string } {
	return []interface{ Name() string }{
		ContainerRegistryResourceID,
		ContainerRegistryEndpoint,
		FoundryAcrMode,
		ProjectAcrConnectionName,
		FoundryAcrPullAssigned,
		AgentSkipAcr,
	}
}
