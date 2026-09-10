// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type memoryResponseStore struct {
	record  *savedResponse
	saves   int
	saveErr error
}

func (s *memoryResponseStore) Get(context.Context, string) (*savedResponse, error) {
	return s.record, nil
}

func (s *memoryResponseStore) Save(_ context.Context, _ string, record savedResponse) error {
	s.record = &record
	s.saves++
	return s.saveErr
}

func (s *memoryResponseStore) Delete(context.Context, string) error {
	s.record = nil
	return nil
}

func TestResponseLifecycleCommand(t *testing.T) {
	const endpoint = "https://example.services.ai.azure.com/api/projects/project/agents/agent/" +
		"endpoint/protocols/openai/responses?api-version=v1"
	for _, tt := range []struct {
		name       string
		useCurrent bool
		endpoint   string
		want       string
	}{
		{
			name: "immediate project follow", useCurrent: true,
			want: "azd ai agent invocations follow",
		},
		{
			name: "recovery requires explicit identity",
			want: `azd ai agent invocations follow --id "resp_test" --protocol responses --agent-name "resp-test"`,
		},
		{
			name: "endpoint mode retains targeting", useCurrent: true, endpoint: endpoint,
			want: `azd ai agent invocations follow --id "resp_test" --agent-endpoint "` + endpoint + `"`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			action := &InvokeAction{flags: &invokeFlags{agentEndpoint: tt.endpoint}}
			if tt.endpoint != "" {
				action.endpoint = &parsedAgentEndpoint{}
			}
			rc := &remoteContext{name: "deployed-agent", serviceName: "resp-test"}
			assert.Equal(t, tt.want, action.responseLifecycleCommand(rc, "resp_test", invocationFollow, tt.useCurrent))
			if !tt.useCurrent || tt.endpoint != "" {
				assert.Equal(t, strings.Replace(tt.want, "invocations follow", "invocations show", 1),
					action.responseLifecycleCommand(rc, "resp_test", invocationShow, false))
			}
		})
	}
}

func TestInvokeLongRunningFlags(t *testing.T) {
	cmd := newInvokeCommand(nil)
	assert.NotNil(t, cmd.Flags().Lookup("long-running"))
	assert.NotNil(t, cmd.Flags().Lookup("no-wait"))
	assert.Nil(t, cmd.Flags().Lookup("resumable"))
}

func TestInvokeLongRunningValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "no wait", args: []string{"--no-wait", "hello"}, want: "--no-wait requires --long-running"},
		{name: "local", args: []string{"--long-running", "--local", "hello"}, want: "remote Responses agents"},
		{
			name: "invocations",
			args: []string{"--long-running", "--protocol", "invocations", "hello"},
			want: "--long-running is not supported with the invocations protocol",
		},
		{
			name: "timeout",
			args: []string{"--long-running", "--timeout", "30", "hello"},
			want: "--timeout cannot be used with --long-running",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newInvokeCommand(nil)
			cmd.SetArgs(tt.args)
			err := cmd.Execute()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestBuildResponsesRequestBody(t *testing.T) {
	foreground := buildResponsesRequestBody("hello", "sess_1", "conv_1", false)
	assert.Equal(t, true, foreground["stream"])
	assert.Equal(t, "sess_1", foreground["agent_session_id"])
	assert.Equal(t, map[string]string{"id": "conv_1"}, foreground["conversation"])
	assert.NotContains(t, foreground, "background")
	assert.NotContains(t, foreground, "store")

	background := buildResponsesRequestBody("hello", "", "conv_1", true)
	assert.Equal(t, true, background["background"])
	assert.Equal(t, true, background["store"])
	assert.NotContains(t, background, "agent_session_id")
}

func TestResponseIdentityTrackerPersistsIdentityOnlyOnce(t *testing.T) {
	store := &memoryResponseStore{}
	var output bytes.Buffer
	tracker := &responseIdentityTracker{store: store, agentKey: "agent", writer: &output}

	require.NoError(t, tracker.Apply(t.Context(), "resp_123"))
	require.NoError(t, tracker.Apply(t.Context(), "resp_123"))

	require.NotNil(t, store.record)
	assert.Equal(t, "resp_123", store.record.ResponseID)
	assert.Equal(t, 1, store.saves)
	assert.Equal(t, "Response:     resp_123\n", output.String())
}

func TestResponseIdentityTrackerRecordsSaveFailure(t *testing.T) {
	store := &memoryResponseStore{saveErr: errors.New("write failed")}
	var output bytes.Buffer
	tracker := &responseIdentityTracker{store: store, agentKey: "agent", writer: &output}

	require.NoError(t, tracker.Apply(t.Context(), "resp_123"))
	require.Error(t, tracker.saveErr)
	assert.Contains(t, output.String(), "resp_123")
	assert.Contains(t, output.String(), "was not saved")
}

func TestNoWaitSavesIdentityAndStopsBeforeOutput(t *testing.T) {
	store := &memoryResponseStore{}
	var output bytes.Buffer
	tracker := &responseIdentityTracker{store: store, agentKey: "agent", writer: &output}
	stream := "event: response.created\n" +
		"data: {\"type\":\"response.created\",\"sequence_number\":0," +
		"\"response\":{\"id\":\"resp_123\",\"status\":\"queued\"}}\n\n" +
		"event: response.output_text.delta\n" +
		"data: {\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"delta\":\"hidden\"}\n\n"

	err := readResponsesSSE(t.Context(), bytes.NewBufferString(stream), &output, "agent", responsesSSEOptions{
		requireTerminal: true,
		onResponseID: func(responseID string) error {
			if err := tracker.Apply(t.Context(), responseID); err != nil {
				return err
			}
			return errBackgroundNoWait
		},
	})
	require.ErrorIs(t, err, errBackgroundNoWait)
	require.NotNil(t, store.record)
	assert.Equal(t, "resp_123", store.record.ResponseID)
	assert.NotContains(t, output.String(), "hidden")
}

func TestResponseLifecycleURLs(t *testing.T) {
	assert.Equal(
		t,
		"https://example.test/agents/agent/endpoint/protocols/openai/responses/resp_1?api-version=v1&stream=true",
		buildResponseLifecycleURL("https://example.test", "agent", "resp_1", "v1", true),
	)
	assert.Equal(
		t,
		"https://example.test/agents/agent/endpoint/protocols/openai/responses/resp_1/cancel?api-version=v1",
		buildResponseCancelURL("https://example.test", "agent", "resp_1", "v1"),
	)
}
