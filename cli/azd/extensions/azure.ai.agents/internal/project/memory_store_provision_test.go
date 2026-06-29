// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"errors"
	"testing"

	"azureaiagent/internal/exterrors"

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
