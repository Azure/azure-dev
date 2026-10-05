// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_api

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStateStoreSerializedValueLimit(t *testing.T) {
	require.Equal(t, 1_048_576, MaxStateStoreValueBytes)
	for _, size := range []int{MaxStateStoreValueBytes - 1, MaxStateStoreValueBytes, MaxStateStoreValueBytes + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			value := json.RawMessage(`{"text":"` + strings.Repeat("a", size-len(`{"text":""}`)) + `"}`)
			client, transport := newCaptureClient(200, `{}`)
			_, err := client.SetStateStoreItem(t.Context(), "agent", "store", "key",
				SetStateStoreItemRequest{Value: value, Tags: map[string]string{"kind": "test"}}, "")
			if size > MaxStateStoreValueBytes {
				require.ErrorIs(t, err, ErrStateStoreValueTooLarge)
				require.Empty(t, transport.requests)
				return
			}
			require.NoError(t, err)
			require.Len(t, transport.requests, 1)
			body, err := io.ReadAll(transport.requests[0].Body)
			require.NoError(t, err)
			var envelope SetStateStoreItemRequest
			require.NoError(t, json.Unmarshal(body, &envelope))
			require.Len(t, envelope.Value, size)
			require.Greater(t, len(body), size, "the request envelope and tags are not part of the value limit")
		})
	}
}

func TestStateStoreSerializedLimitIncludesEscaping(t *testing.T) {
	for _, tt := range []struct {
		name, text string
	}{
		{"HTML", strings.Repeat("<>&", MaxStateStoreValueBytes/18+1)},
		{"Unicode line separator", strings.Repeat("\u2028", MaxStateStoreValueBytes/6+1)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			value := json.RawMessage(`{"text":"` + tt.text + `"}`)
			require.Less(t, len(value), MaxStateStoreValueBytes, "raw size alone must not decide acceptance")
			client, transport := newCaptureClient(200, `{}`)
			_, err := client.SetStateStoreItem(t.Context(), "agent", "store", "key",
				SetStateStoreItemRequest{Value: value}, "")
			require.ErrorIs(t, err, ErrStateStoreValueTooLarge)
			require.Empty(t, transport.requests)
		})
	}
}

func TestStateStoreSerializedLimitExcludesDiscardedWhitespace(t *testing.T) {
	// The API validator measures the value sent on the wire. The CLI separately rejects
	// oversized raw input to bound buffering and tells callers to compact it themselves.
	value := json.RawMessage(strings.Repeat(" ", MaxStateStoreValueBytes) + "{\n  \"value\": 1\n}")
	client, transport := newCaptureClient(200, `{}`)
	_, err := client.SetStateStoreItem(t.Context(), "agent", "store", "key", SetStateStoreItemRequest{Value: value}, "")
	require.NoError(t, err)
	require.Len(t, transport.requests, 1)
	body, err := io.ReadAll(transport.requests[0].Body)
	require.NoError(t, err)
	require.JSONEq(t, `{"value":{"value":1},"tags":null}`, string(body))
}
