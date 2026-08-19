// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// evalContextListingOneRun builds a context whose service has exactly one run,
// so the run-scoped commands have something to fall back to.
func evalContextListingOneRun(t *testing.T, runID string) *evalContext {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(
			`{"data":[{"id":"` + runID + `","status":"completed"}]}`))
	}))
	t.Cleanup(srv.Close)

	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	return &evalContext{evalClient: eval_api.NewEvalClientFromPipeline(srv.URL, pipeline)}
}

// commandWritingTo returns a command whose two streams can be read apart.
func commandWritingTo(t *testing.T, out, errOut *bytes.Buffer, jsonOutput bool) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "show"}
	cmd.SetContext(context.Background())
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.Flags().StringP("output", "o", "", "")
	if jsonOutput {
		require.NoError(t, cmd.Flags().Set("output", "json"))
	}
	return cmd
}

// Falling back to the last run is what makes these commands usable without an
// id, and it is also how a reader ends up looking at a different run than they
// think. The id it settled on is named.
func TestTheRunAFallbackSettledOnIsNamed(t *testing.T) {
	ec := evalContextListingOneRun(t, "evalrun_last")

	var out, errOut bytes.Buffer
	run, err := ec.latestOrNamedRun(commandWritingTo(t, &out, &errOut, false), "eval_1", "", false)

	require.NoError(t, err)
	assert.Equal(t, "evalrun_last", run.ID)
	assert.Contains(t, errOut.String(), "Using last run: evalrun_last")
	assert.Empty(t, out.String(),
		"the line is context, so it must not land in a redirected listing")
}

// A caller who named the run already knows which one it is, and a parser is
// not reading prose.
func TestTheRunIsNotNamedBackToWhoeverNamedIt(t *testing.T) {
	t.Run("named explicitly", func(t *testing.T) {
		ec := evalContextListingOneRun(t, "evalrun_asked")

		var out, errOut bytes.Buffer
		_, err := ec.latestOrNamedRun(
			commandWritingTo(t, &out, &errOut, false), "eval_1", "evalrun_asked", true)

		require.NoError(t, err)
		assert.Empty(t, errOut.String())
	})

	t.Run("output is json", func(t *testing.T) {
		ec := evalContextListingOneRun(t, "evalrun_last")

		var out, errOut bytes.Buffer
		_, err := ec.latestOrNamedRun(commandWritingTo(t, &out, &errOut, true), "eval_1", "", false)

		require.NoError(t, err)
		assert.Empty(t, errOut.String())
	})
}
