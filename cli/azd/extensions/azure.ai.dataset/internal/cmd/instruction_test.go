// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"azureaidataset/internal/pkg/gen_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// contextServingAgent builds a datasetContext whose generation client answers
// the agent read with status and body.
func contextServingAgent(t *testing.T, status int, body string) *datasetContext {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if body != "" {
			_, _ = w.Write([]byte(body))
		}
	}))
	t.Cleanup(srv.Close)

	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	return &datasetContext{genClient: gen_api.NewClientFromPipeline(srv.URL, pipeline)}
}

// agentBody is a catalog agent as the service returns one: versions inlined,
// with only `latest` populated on a plain read.
func agentBody(t *testing.T, instructions string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"name": "support-bot",
		"versions": map[string]any{
			"latest": map[string]any{
				"version":    "1",
				"definition": map[string]any{"instructions": instructions},
			},
		},
	})
	require.NoError(t, err)
	return string(raw)
}

// What the caller passed wins: --instruction is the explicit answer to the
// question the agent is only a fallback for.
func TestResolveGenerationInstructionPrefersTheExplicitValue(t *testing.T) {
	dc := contextServingAgent(t, http.StatusOK, agentBody(t, "from the agent"))
	var out bytes.Buffer

	got, err := dc.resolveGenerationInstruction(
		context.Background(), "from the flag", "support-bot", &out, false)

	require.NoError(t, err)
	assert.Equal(t, "from the flag", got)
	assert.Empty(t, out.String(), "nothing was looked up, so nothing is announced")
}

// With no agent to read there is nothing to fall back to, and an empty
// instruction is a valid request rather than an error.
func TestResolveGenerationInstructionWithoutAnAgent(t *testing.T) {
	dc := contextServingAgent(t, http.StatusOK, agentBody(t, "unused"))
	var out bytes.Buffer

	got, err := dc.resolveGenerationInstruction(context.Background(), "", "", &out, false)

	require.NoError(t, err)
	assert.Empty(t, got)
}

// The service accepts an agent source meant to pull these instructions, but it
// fails for every agent, so they are read here instead. Losing this read makes
// generation silently ignore the agent it was pointed at.
func TestResolveGenerationInstructionReadsTheAgent(t *testing.T) {
	dc := contextServingAgent(t, http.StatusOK, agentBody(t, "Answer support questions politely."))
	var out bytes.Buffer

	got, err := dc.resolveGenerationInstruction(
		context.Background(), "", "support-bot", &out, false)

	require.NoError(t, err)
	assert.Equal(t, "Answer support questions politely.", got)
	assert.Contains(t, out.String(), "support-bot",
		"the user is told what the generation was seeded from")
}

// Generation can still proceed from the agent source alone, so a failure to
// read the agent is reported and stepped over rather than ending the command.
func TestResolveGenerationInstructionWarnsButContinues(t *testing.T) {
	dc := contextServingAgent(t, http.StatusNotFound, `{"error":{"code":"NotFound"}}`)
	var out bytes.Buffer

	got, err := dc.resolveGenerationInstruction(
		context.Background(), "", "missing-bot", &out, false)

	require.NoError(t, err, "an unreadable agent must not fail the generation")
	assert.Empty(t, got)
	assert.Contains(t, out.String(), "warning")
	assert.Contains(t, out.String(), "missing-bot")
}

// --quiet is for scripts, where the progress lines are noise on stdout that a
// caller may be parsing.
func TestResolveGenerationInstructionStaysQuiet(t *testing.T) {
	var out bytes.Buffer

	found := contextServingAgent(t, http.StatusOK, agentBody(t, "Answer politely."))
	got, err := found.resolveGenerationInstruction(
		context.Background(), "", "support-bot", &out, true)
	require.NoError(t, err)
	assert.Equal(t, "Answer politely.", got, "quiet changes what is printed, not what is resolved")
	assert.Empty(t, out.String())

	missing := contextServingAgent(t, http.StatusNotFound, `{}`)
	_, err = missing.resolveGenerationInstruction(
		context.Background(), "", "missing-bot", &out, true)
	require.NoError(t, err)
	assert.Empty(t, out.String(), "even the warning is suppressed")
}
