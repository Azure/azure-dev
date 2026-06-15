// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
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
