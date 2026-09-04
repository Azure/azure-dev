// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildConversationContinuationRequestUsesSameShapeForAnyStatus(t *testing.T) {
	t.Parallel()

	for _, status := range []string{"queued", "in_progress", "completed", "failed", "incomplete", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			request, err := buildConversationContinuationRequest("revised input", &savedBackgroundResponse{
				ResponseID:     "resp_123",
				Status:         status,
				SessionID:      "sess_123",
				ConversationID: "conv_123",
			})

			require.NoError(t, err)
			assert.Equal(t, "revised input", request["input"])
			assert.Equal(t, true, request["stream"])
			assert.Equal(t, true, request["store"])
			assert.Equal(t, true, request["background"])
			assert.Equal(t, "sess_123", request["agent_session_id"])
			assert.Equal(t, map[string]string{"id": "conv_123"}, request["conversation"])
			assert.NotContains(t, request, "previous_response_id")
		})
	}
}

func TestBuildConversationContinuationRequestOmitsEmptySession(t *testing.T) {
	t.Parallel()

	request, err := buildConversationContinuationRequest("next", &savedBackgroundResponse{
		ResponseID:     "resp_123",
		ConversationID: "conv_123",
	})

	require.NoError(t, err)
	assert.NotContains(t, request, "agent_session_id")
	assert.Equal(t, map[string]string{"id": "conv_123"}, request["conversation"])
}

func TestBuildConversationContinuationRequestRequiresConversation(t *testing.T) {
	t.Parallel()

	request, err := buildConversationContinuationRequest("next", &savedBackgroundResponse{
		ResponseID: "resp_123",
		SessionID:  "sess_123",
	})

	require.EqualError(t, err, "saved background Response has no conversation ID for --steer")
	assert.Nil(t, request)
}
