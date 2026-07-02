// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"errors"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/azure"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestProvisionMemoryStores_NoStoresIsNoOp(t *testing.T) {
	p := &AgentServiceTargetProvider{}

	require.NoError(t, p.provisionMemoryStores(t.Context(), nil, "", nil))
	require.NoError(t, p.provisionMemoryStores(
		t.Context(), &ServiceTargetAgentConfig{}, "https://proj", nil,
	))
}

func TestProvisionMemoryStores_RequiresProjectEndpoint(t *testing.T) {
	p := &AgentServiceTargetProvider{}

	cfg := &ServiceTargetAgentConfig{
		MemoryStores: []MemoryStore{
			{Name: "m", ChatModel: "chat", EmbeddingModel: "embed"},
		},
	}

	err := p.provisionMemoryStores(t.Context(), cfg, "", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "project endpoint")
}

func TestProvisionMemoryStores_ValidatesRequiredFields(t *testing.T) {
	p := &AgentServiceTargetProvider{}

	tests := []struct {
		name  string
		store MemoryStore
	}{
		{
			name:  "missing name",
			store: MemoryStore{ChatModel: "chat", EmbeddingModel: "embed"},
		},
		{
			name:  "missing chat model",
			store: MemoryStore{Name: "m", EmbeddingModel: "embed"},
		},
		{
			name:  "missing embedding model",
			store: MemoryStore{Name: "m", ChatModel: "chat"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &ServiceTargetAgentConfig{MemoryStores: []MemoryStore{tt.store}}
			err := p.provisionMemoryStores(t.Context(), cfg, "https://proj", nil)
			require.Error(t, err)
		})
	}
}

func TestMapMemoryStoreOptions(t *testing.T) {
	require.Nil(t, mapMemoryStoreOptions(nil))

	// An options struct with no fields set should map to nil so the service applies
	// its own defaults rather than receiving an empty options object.
	require.Nil(t, mapMemoryStoreOptions(&MemoryStoreOptions{}))

	opts := mapMemoryStoreOptions(&MemoryStoreOptions{
		ChatSummaryEnabled:      new(true),
		UserProfileEnabled:      new(false),
		ProceduralMemoryEnabled: new(true),
		DefaultTtlSeconds:       new(100),
		UserProfileDetails:      "details",
	})
	require.NotNil(t, opts)
	require.True(t, *opts.ChatSummaryEnabled)
	require.False(t, *opts.UserProfileEnabled)
	require.True(t, *opts.ProceduralMemoryEnabled)
	require.Equal(t, 100, *opts.DefaultTTLSeconds)
	require.Equal(t, "details", opts.UserProfileDetails)
}

// ensure the validation error uses the dedicated memory store error code
func TestProvisionMemoryStores_UsesMemoryStoreErrorCode(t *testing.T) {
	p := &AgentServiceTargetProvider{}
	cfg := &ServiceTargetAgentConfig{
		MemoryStores: []MemoryStore{{Name: "m", ChatModel: "chat"}},
	}

	err := p.provisionMemoryStores(t.Context(), cfg, "https://proj", nil)
	require.Error(t, err)

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "expected a *azdext.LocalError")
	require.Equal(t, exterrors.CodeInvalidMemoryStore, localErr.Code)
}

// A bad entry anywhere in the list must fail before the endpoint check / any network call,
// so an invalid store cannot half-provision the valid stores that precede it.
func TestProvisionMemoryStores_ValidatesAllStoresUpFront(t *testing.T) {
	p := &AgentServiceTargetProvider{}
	cfg := &ServiceTargetAgentConfig{
		MemoryStores: []MemoryStore{
			{Name: "good", ChatModel: "chat", EmbeddingModel: "embed"},
			{Name: "bad", ChatModel: "chat"}, // missing embeddingModel
		},
	}

	// Endpoint is empty; validation must still fire first (fail fast before the endpoint check).
	err := p.provisionMemoryStores(t.Context(), cfg, "", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "bad")
}

func TestMemoryStoreDefinitionDrift(t *testing.T) {
	base := azure.MemoryStoreDefinition{
		Kind:           azure.MemoryStoreKindDefault,
		ChatModel:      "chat",
		EmbeddingModel: "embed",
	}

	t.Run("no drift when identical and no options declared", func(t *testing.T) {
		require.Empty(t, memoryStoreDefinitionDrift(base, base))
	})

	t.Run("chat and embedding model drift", func(t *testing.T) {
		declared := base
		live := azure.MemoryStoreDefinition{ChatModel: "chat-2", EmbeddingModel: "embed-2"}
		drift := memoryStoreDefinitionDrift(declared, live)
		require.Len(t, drift, 2)
		require.Contains(t, drift[0], "chatModel")
		require.Contains(t, drift[1], "embeddingModel")
	})

	t.Run("unset declared options never report drift", func(t *testing.T) {
		declared := base // Options nil
		live := base
		live.Options = &azure.MemoryStoreOptions{
			UserProfileEnabled: new(true),
			DefaultTTLSeconds:  new(100),
		}
		require.Empty(t, memoryStoreDefinitionDrift(declared, live))
	})

	t.Run("declared option differs from live", func(t *testing.T) {
		declared := base
		declared.Options = &azure.MemoryStoreOptions{UserProfileEnabled: new(true)}
		live := base
		live.Options = &azure.MemoryStoreOptions{UserProfileEnabled: new(false)}
		drift := memoryStoreDefinitionDrift(declared, live)
		require.Len(t, drift, 1)
		require.Contains(t, drift[0], "userProfileEnabled")
	})

	t.Run("declared option missing on live reports drift", func(t *testing.T) {
		declared := base
		declared.Options = &azure.MemoryStoreOptions{
			DefaultTTLSeconds:  new(100),
			UserProfileDetails: "avoid sensitive data",
		}
		live := base // Options nil
		drift := memoryStoreDefinitionDrift(declared, live)
		require.Len(t, drift, 2)
	})

	t.Run("matching declared option reports no drift", func(t *testing.T) {
		declared := base
		declared.Options = &azure.MemoryStoreOptions{UserProfileEnabled: new(true)}
		live := base
		live.Options = &azure.MemoryStoreOptions{
			UserProfileEnabled: new(true),
			ChatSummaryEnabled: new(true), // live-only field is ignored
		}
		require.Empty(t, memoryStoreDefinitionDrift(declared, live))
	})
}

func TestBoolPtrDiffers(t *testing.T) {
	require.False(t, boolPtrDiffers(nil, new(true)), "nil declared never differs")
	require.True(t, boolPtrDiffers(new(true), nil), "set declared vs nil live differs")
	require.True(t, boolPtrDiffers(new(true), new(false)), "different values differ")
	require.False(t, boolPtrDiffers(new(true), new(true)), "equal values do not differ")
}
