// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package telemetry

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOperationClassification(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name       string
		properties map[string]any
		want       OperationClass
	}{
		{"empty", nil, OperationClass{"unknown", "unknown"}},
		{"unrecognized", map[string]any{"kind": "customer-secret"}, OperationClass{"unknown", "unknown"}},
		{"hosted", map[string]any{"kind": "hosted", "name": "customer-secret"}, OperationClass{"hosted", "none"}},
		{"websocket", map[string]any{"kind": "hosted", "protocols": []any{
			map[string]any{"protocol": "responses"}, map[string]any{"protocol": "invocations_ws"},
		}}, OperationClass{"hosted_invocations_ws", "none"}},
		{"prompt", map[string]any{"kind": "prompt"}, OperationClass{"prompt", "none"}},
		{"workflow", map[string]any{"kind": "workflow"}, OperationClass{"workflow", "none"}},
		{"voice-default", map[string]any{"kind": "voice"}, OperationClass{"voice_managed", "none"}},
		{"voice-alias", map[string]any{"kind": "prompt-voice", "modelType": "managed"},
			OperationClass{"voice_managed", "none"}},
		{"byom", map[string]any{"kind": "voice", "modelType": "self_deployed"}, OperationClass{"voice_byom", "none"}},
		{"byom-legacy", map[string]any{"kind": "prompt-voice", "model_type": "self_deployed"},
			OperationClass{"voice_byom", "none"}},
		{"wrapper", map[string]any{"kind": "voice", "conversationEngine": map[string]any{"type": "hosted_agent"}},
			OperationClass{"voice_hosted_wrapper", "none"}},
		{"wrapper-normalized", map[string]any{"kind": "voice",
			"conversationEngine": map[string]any{"type": " Hosted_Agent "}}, OperationClass{"voice_hosted_wrapper", "none"}},
		{"wrapper-legacy", map[string]any{"kind": "prompt-voice",
			"conversation_engine": map[string]any{"type": "hosted_agent"}}, OperationClass{"voice_hosted_wrapper", "none"}},
		{"unknown-engine", map[string]any{"kind": "voice", "conversationEngine": nil},
			OperationClass{"unknown", "unknown"}},
		{"unknown-mode", map[string]any{"kind": "voice", "modelType": "customer-secret"},
			OperationClass{"unknown", "unknown"}},
		{"malformed-mode", map[string]any{"kind": "voice", "modelType": 42},
			OperationClass{"unknown", "unknown"}},
		{"unresolved-ref", map[string]any{"kind": "voice", "$ref": "customer-secret"},
			OperationClass{"unknown", "unknown"}},
		{"phone", map[string]any{"kind": "voice", "telephony": map[string]any{"bindings": []any{
			map[string]any{"identifier": "customer-secret", "connection": "customer-secret"},
		}}}, OperationClass{"voice_managed", "enabled"}},
		{"phone-empty", map[string]any{"kind": "voice", "telephony": map[string]any{"bindings": []any{}}},
			OperationClass{"voice_managed", "none"}},
		{"phone-malformed", map[string]any{"kind": "voice", "telephony": map[string]any{"bindings": "secret"}},
			OperationClass{"voice_managed", "unknown"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before, err := json.Marshal(tt.properties)
			require.NoError(t, err)
			class := ClassifyOperation(tt.properties)
			require.Equal(t, tt.want, class)
			after, err := json.Marshal(tt.properties)
			require.NoError(t, err)
			require.Equal(t, before, after)
			event, ok := OperationClassified("deploy", class)
			require.True(t, ok)
			require.Empty(t, event.Attributes, "no new telemetry attributes")
			require.NotContains(t, event.Name, "customer-secret")
			require.LessOrEqual(t, len(event.Name), 128)
		})
	}
}

func TestOperationEventVocabulary(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"init", "provision", "deploy"} {
		event, ok := OperationClassified(operation, OperationClass{"unexpected-user-value", "secret"})
		require.True(t, ok)
		require.Equal(t, "agent.operation.v1."+operation+".unknown.unknown", event.Name)
		require.Nil(t, event.Attributes)
	}
	for _, operation := range []string{"", "invoke", "down", "customer-secret"} {
		_, ok := OperationClassified(operation, OperationClass{})
		require.False(t, ok)
	}
}
