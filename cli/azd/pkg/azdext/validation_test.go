// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidationEnvelope_GetSetRequestId(t *testing.T) {
	env := NewValidationEnvelope()
	msg := &ValidationMessage{RequestId: "req-123"}

	require.Equal(t, "req-123", env.GetRequestId(t.Context(), msg))

	env.SetRequestId(t.Context(), msg, "req-456")
	require.Equal(t, "req-456", msg.RequestId)
}

func TestValidationEnvelope_GetSetError(t *testing.T) {
	env := NewValidationEnvelope()

	t.Run("NilError", func(t *testing.T) {
		msg := &ValidationMessage{}
		require.NoError(t, env.GetError(msg))
	})

	t.Run("WithError", func(t *testing.T) {
		msg := &ValidationMessage{}
		env.SetError(msg, fmt.Errorf("validation failed"))
		require.NotNil(t, msg.Error)
		unwrapped := env.GetError(msg)
		require.Error(t, unwrapped)
		require.Contains(t, unwrapped.Error(), "validation failed")
	})
}

func TestValidationEnvelope_GetInnerMessage(t *testing.T) {
	env := NewValidationEnvelope()

	tests := []struct {
		name string
		msg  *ValidationMessage
	}{
		{
			name: "RegisterRequest",
			msg: &ValidationMessage{
				MessageType: &ValidationMessage_RegisterValidationCheckRequest{
					RegisterValidationCheckRequest: &RegisterValidationCheckRequest{
						CheckType: "local-preflight",
						RuleId:    "test_rule",
					},
				},
			},
		},
		{
			name: "RegisterResponse",
			msg: &ValidationMessage{
				MessageType: &ValidationMessage_RegisterValidationCheckResponse{
					RegisterValidationCheckResponse: &RegisterValidationCheckResponse{},
				},
			},
		},
		{
			name: "CheckRequest",
			msg: &ValidationMessage{
				MessageType: &ValidationMessage_ValidationCheckRequest{
					ValidationCheckRequest: &ValidationCheckRequest{
						CheckType: "local-preflight",
						RuleId:    "test_rule",
						ContextId: "ctx-123",
					},
				},
			},
		},
		{
			name: "CheckResponse",
			msg: &ValidationMessage{
				MessageType: &ValidationMessage_ValidationCheckResponse{
					ValidationCheckResponse: &ValidationCheckResponse{
						Results: []*ValidationCheckResult{
							{
								Severity:     ValidationCheckSeverity_VALIDATION_CHECK_SEVERITY_WARNING,
								DiagnosticId: "test_diag",
								Message:      "test message",
							},
						},
					},
				},
			},
		},
		{
			name: "PrepareContextChunk",
			msg: &ValidationMessage{
				MessageType: &ValidationMessage_PrepareValidationContextChunk{
					PrepareValidationContextChunk: &PrepareValidationContextChunk{
						ContextId: "ctx-1",
						CheckType: "local-preflight",
						Key:       "arm_template",
						Data:      []byte("data"),
					},
				},
			},
		},
		{
			name: "PrepareContextResponse",
			msg: &ValidationMessage{
				MessageType: &ValidationMessage_PrepareValidationContextResponse{
					PrepareValidationContextResponse: &PrepareValidationContextResponse{},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inner := env.GetInnerMessage(tt.msg)
			require.NotNil(t, inner, "GetInnerMessage should return non-nil for %s", tt.name)
		})
	}

	t.Run("NilMessageType", func(t *testing.T) {
		msg := &ValidationMessage{}
		require.Nil(t, env.GetInnerMessage(msg))
	})
}

func TestValidationEnvelope_ProgressNotSupported(t *testing.T) {
	env := NewValidationEnvelope()
	msg := &ValidationMessage{RequestId: "req-1"}

	require.False(t, env.IsProgressMessage(msg))
	require.Empty(t, env.GetProgressMessage(msg))

	progress := env.CreateProgressMessage("req-1", "progress")
	require.NotNil(t, progress)
	require.Equal(t, "req-1", progress.RequestId)
}

func TestValidationContext_Helpers(t *testing.T) {
	valCtx := &ValidationContext{
		ContextID: "ctx-1",
		CheckType: "local-preflight",
		Data: map[string][]byte{
			ValidationContextResourcesSnapshot: []byte(`{"predictedResources":[]}`),
			ValidationContextARMTemplate:       []byte(`{"resources":[]}`),
			ValidationContextARMParameters:     []byte(`{}`),
			ValidationContextEnvLocation:       []byte("eastus2"),
		},
	}

	snapshot, ok := valCtx.ResourcesSnapshot()
	require.True(t, ok)
	require.Contains(t, string(snapshot), "predictedResources")

	template, ok := valCtx.ARMTemplate()
	require.True(t, ok)
	require.Contains(t, string(template), "resources")

	params, ok := valCtx.ARMParameters()
	require.True(t, ok)
	require.Equal(t, "{}", string(params))

	loc, ok := valCtx.EnvLocation()
	require.True(t, ok)
	require.Equal(t, "eastus2", loc)

	// Missing keys
	emptyCtx := &ValidationContext{
		Data: map[string][]byte{},
	}
	_, ok = emptyCtx.ResourcesSnapshot()
	require.False(t, ok)
	_, ok = emptyCtx.EnvLocation()
	require.False(t, ok)
}

func TestContextAssembler(t *testing.T) {
	a := &contextAssembler{}

	// First chunk for "arm_template" — not last key
	complete, _ := a.addChunk(&PrepareValidationContextChunk{
		Key:         "arm_template",
		Data:        []byte("chunk1"),
		ChunkIndex:  0,
		IsLastChunk: false,
		IsLastKey:   false,
		TotalKeys:   2,
	})
	require.False(t, complete)

	// Second chunk for "arm_template" — last chunk for this key, not last key
	complete, _ = a.addChunk(&PrepareValidationContextChunk{
		Key:         "arm_template",
		Data:        []byte("chunk2"),
		ChunkIndex:  1,
		IsLastChunk: true,
		IsLastKey:   false,
		TotalKeys:   2,
	})
	require.False(t, complete)

	// Single chunk for "env_location" — last key
	complete, result := a.addChunk(&PrepareValidationContextChunk{
		Key:         "env_location",
		Data:        []byte("eastus"),
		ChunkIndex:  0,
		IsLastChunk: true,
		IsLastKey:   true,
		TotalKeys:   2,
	})
	require.True(t, complete)
	require.Equal(t, []byte("chunk1chunk2"), result["arm_template"])
	require.Equal(t, []byte("eastus"), result["env_location"])
}

func TestContextAssembler_OutOfOrder(t *testing.T) {
	a := &contextAssembler{}

	// Simulate out-of-order delivery: last-key arrives before
	// an earlier chunk for a different key.

	// env_location (last key) arrives first — but totalKeys=2,
	// so assembler knows it needs to wait for another key.
	complete, _ := a.addChunk(&PrepareValidationContextChunk{
		Key:         "env_location",
		Data:        []byte("eastus"),
		ChunkIndex:  0,
		IsLastChunk: true,
		IsLastKey:   true,
		TotalKeys:   2,
	})
	require.False(t, complete)

	// chunk 1 of arm_template (out of order: index 1 before 0)
	complete, _ = a.addChunk(&PrepareValidationContextChunk{
		Key:         "arm_template",
		Data:        []byte("BBB"),
		ChunkIndex:  1,
		IsLastChunk: true,
		IsLastKey:   false,
		TotalKeys:   2,
	})
	require.False(t, complete)

	// chunk 0 of arm_template
	complete, result := a.addChunk(&PrepareValidationContextChunk{
		Key:         "arm_template",
		Data:        []byte("AAA"),
		ChunkIndex:  0,
		IsLastChunk: false,
		IsLastKey:   false,
		TotalKeys:   2,
	})
	require.True(t, complete)
	// Chunks should be reassembled in index order
	require.Equal(t, []byte("AAABBB"), result["arm_template"])
	require.Equal(t, []byte("eastus"), result["env_location"])
}

func TestValidationContext_PredictedResources(t *testing.T) {
	t.Run("present", func(t *testing.T) {
		valCtx := &ValidationContext{
			Data: map[string][]byte{
				ValidationContextPredictedResources: []byte(`[{"type":"Microsoft.Storage/storageAccounts"}]`),
			},
		}
		raw, ok := valCtx.PredictedResources()
		require.True(t, ok)
		require.Contains(t, string(raw), "Microsoft.Storage")
	})

	t.Run("missing", func(t *testing.T) {
		valCtx := &ValidationContext{Data: map[string][]byte{}}
		_, ok := valCtx.PredictedResources()
		require.False(t, ok)
	})
}

func TestValidationContext_ParsePredictedResources(t *testing.T) {
	t.Run("valid_json", func(t *testing.T) {
		jsonData := `[
			{"type":"Microsoft.Storage/storageAccounts","apiVersion":"2023-01-01","name":"mystorage","location":"eastus"},
			{"type":"Microsoft.Web/sites","apiVersion":"2022-09-01","name":"myapp","kind":"app,linux"}
		]`
		valCtx := &ValidationContext{
			Data: map[string][]byte{
				ValidationContextPredictedResources: []byte(jsonData),
			},
		}
		resources, err := valCtx.ParsePredictedResources()
		require.NoError(t, err)
		require.Len(t, resources, 2)
		require.Equal(t, "Microsoft.Storage/storageAccounts", resources[0].Type)
		require.Equal(t, "mystorage", resources[0].Name)
		require.Equal(t, "eastus", resources[0].Location)
		require.Equal(t, "app,linux", resources[1].Kind)
	})

	t.Run("key_missing_returns_nil", func(t *testing.T) {
		valCtx := &ValidationContext{Data: map[string][]byte{}}
		resources, err := valCtx.ParsePredictedResources()
		require.NoError(t, err)
		require.Nil(t, resources)
	})

	t.Run("invalid_json_returns_error", func(t *testing.T) {
		valCtx := &ValidationContext{
			Data: map[string][]byte{
				ValidationContextPredictedResources: []byte(`not valid json`),
			},
		}
		_, err := valCtx.ParsePredictedResources()
		require.Error(t, err)
	})
}

func TestValidationManager_GetOrCreateProvider(t *testing.T) {
	mgr := &ValidationManager{
		factories: make(map[validationCheckKey]ValidationCheckProviderFactory),
		instances: make(map[validationCheckKey]ValidationCheckProvider),
	}

	key := validationCheckKey{CheckType: "local-preflight", RuleID: "test_rule"}

	// No factory registered — should error
	_, err := mgr.getOrCreateProvider(key)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no factory")

	// Register factory
	callCount := 0
	mgr.factories[key] = func() ValidationCheckProvider {
		callCount++
		return &mockProvider{}
	}

	// First call creates
	p1, err := mgr.getOrCreateProvider(key)
	require.NoError(t, err)
	require.NotNil(t, p1)
	require.Equal(t, 1, callCount)

	// Second call returns cached
	p2, err := mgr.getOrCreateProvider(key)
	require.NoError(t, err)
	require.Same(t, p1, p2)
	require.Equal(t, 1, callCount, "factory should not be called again")
}

func TestValidationManager_OnPrepareContextChunk(t *testing.T) {
	mgr := &ValidationManager{
		cachedContexts:   make(map[string]*ValidationContext),
		contextRefCounts: make(map[string]int),
		assemblers:       make(map[string]*contextAssembler),
	}

	// Send incomplete chunk
	resp, err := mgr.onPrepareContextChunk(t.Context(), &PrepareValidationContextChunk{
		ContextId:   "ctx-1",
		CheckType:   "local-preflight",
		Key:         "arm_template",
		Data:        []byte("hello"),
		ChunkIndex:  0,
		IsLastChunk: true,
		IsLastKey:   false,
		TotalKeys:   2,
	})
	require.NoError(t, err)
	require.Nil(t, resp, "intermediate chunk should return nil response")

	// Context not yet cached
	_, exists := mgr.cachedContexts["ctx-1"]
	require.False(t, exists)

	// Send final chunk
	resp, err = mgr.onPrepareContextChunk(t.Context(), &PrepareValidationContextChunk{
		ContextId:   "ctx-1",
		CheckType:   "local-preflight",
		Key:         "env_location",
		Data:        []byte("eastus"),
		ChunkIndex:  0,
		IsLastChunk: true,
		IsLastKey:   true,
		TotalKeys:   2,
	})
	require.NoError(t, err)
	require.NotNil(t, resp, "final chunk should return ack response")
	require.NotNil(t, resp.GetPrepareValidationContextResponse())

	// Context should now be cached
	cached, exists := mgr.cachedContexts["ctx-1"]
	require.True(t, exists)
	require.Equal(t, []byte("hello"), cached.Data["arm_template"])
	require.Equal(t, []byte("eastus"), cached.Data["env_location"])
}

func TestValidationManager_OnValidationCheck(t *testing.T) {
	mgr := &ValidationManager{
		factories:        make(map[validationCheckKey]ValidationCheckProviderFactory),
		instances:        make(map[validationCheckKey]ValidationCheckProvider),
		cachedContexts:   make(map[string]*ValidationContext),
		contextRefCounts: make(map[string]int),
		assemblers:       make(map[string]*contextAssembler),
	}

	key := validationCheckKey{CheckType: "local-preflight", RuleID: "test_rule"}
	mgr.factories[key] = func() ValidationCheckProvider {
		return &mockProvider{
			results: []*ValidationCheckResult{
				{
					Severity:     ValidationCheckSeverity_VALIDATION_CHECK_SEVERITY_WARNING,
					DiagnosticId: "mock_diag",
					Message:      "mock warning",
				},
			},
		}
	}

	// Cache a context
	mgr.cachedContexts["ctx-abc"] = &ValidationContext{
		ContextID: "ctx-abc",
		CheckType: "local-preflight",
		Data:      map[string][]byte{"env_location": []byte("westus2")},
	}
	mgr.contextRefCounts["ctx-abc"] = 0

	resp, err := mgr.onValidationCheck(t.Context(), &ValidationCheckRequest{
		CheckType: "local-preflight",
		RuleId:    "test_rule",
		ContextId: "ctx-abc",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	checkResp := resp.GetValidationCheckResponse()
	require.NotNil(t, checkResp)
	require.Len(t, checkResp.Results, 1)
	require.Equal(t, "mock_diag", checkResp.Results[0].DiagnosticId)

	// Context should be evicted (ref count was 0, decremented to -1 → cleaned up)
	_, exists := mgr.cachedContexts["ctx-abc"]
	require.False(t, exists, "context should be evicted after use")
}

func TestValidationManager_OnValidationCheck_NilResponse(t *testing.T) {
	mgr := &ValidationManager{
		factories:        make(map[validationCheckKey]ValidationCheckProviderFactory),
		instances:        make(map[validationCheckKey]ValidationCheckProvider),
		cachedContexts:   make(map[string]*ValidationContext),
		contextRefCounts: make(map[string]int),
		assemblers:       make(map[string]*contextAssembler),
	}

	key := validationCheckKey{CheckType: "local-preflight", RuleID: "nil_rule"}
	mgr.factories[key] = func() ValidationCheckProvider {
		return &mockProvider{results: nil}
	}

	resp, err := mgr.onValidationCheck(t.Context(), &ValidationCheckRequest{
		CheckType: "local-preflight",
		RuleId:    "nil_rule",
		ContextId: "no-such-ctx",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	// Should normalize nil to empty response
	checkResp := resp.GetValidationCheckResponse()
	require.NotNil(t, checkResp)
	require.Empty(t, checkResp.Results)
}

func TestValidationManager_Close(t *testing.T) {
	mgr := &ValidationManager{
		factories:        map[validationCheckKey]ValidationCheckProviderFactory{{"a", "b"}: nil},
		instances:        map[validationCheckKey]ValidationCheckProvider{{"a", "b"}: &mockProvider{}},
		cachedContexts:   map[string]*ValidationContext{"x": {}},
		contextRefCounts: map[string]int{"x": 1},
		assemblers:       map[string]*contextAssembler{"y": {}},
	}

	err := mgr.Close()
	require.NoError(t, err)
	require.Empty(t, mgr.factories)
	require.Empty(t, mgr.instances)
	require.Empty(t, mgr.cachedContexts)
	require.Empty(t, mgr.contextRefCounts)
	require.Empty(t, mgr.assemblers)
}

// mockProvider is a test implementation of ValidationCheckProvider.
type mockProvider struct {
	results []*ValidationCheckResult
}

func (p *mockProvider) Validate(
	_ context.Context,
	_ *ValidationContext,
	_ *ValidationCheckRequest,
) (*ValidationCheckResponse, error) {
	if p.results == nil {
		return nil, nil
	}
	return &ValidationCheckResponse{Results: p.results}, nil
}
