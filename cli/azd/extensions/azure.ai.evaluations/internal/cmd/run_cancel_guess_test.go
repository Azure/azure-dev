// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// evalContextRememberingRun serves a run lookup that fails with the given
// status, a listing that holds someone else's run, and an environment that
// remembers a run of this project's own.
func evalContextRememberingRun(
	t *testing.T, remembered, otherRun string, getStatus int,
) *evalContext {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/runs") {
			_, _ = w.Write([]byte(`{"data":[{"id":"` + otherRun + `","status":"in_progress"}]}`))
			return
		}
		w.WriteHeader(getStatus)
		_, _ = w.Write([]byte(`{"error":{"message":"nope"}}`))
	}))
	t.Cleanup(srv.Close)

	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})

	env := &testEnvServer{values: map[string]string{idKey("evalrun", "eval_1"): remembered}}
	return &evalContext{
		evalClient: eval_api.NewEvalClientFromPipeline(srv.URL, pipeline),
		azdClient:  newTestAzdClient(t, env),
		envName:    "bugbash",
	}
}

// A remembered run that is merely unreachable has not been replaced by a
// different one.
//
// Any failure used to fall through to the newest run the service lists, so a
// 403 or a 500 quietly moved the command onto another run. On a shared project
// that is somebody else's, and `run cancel` reaches this same lookup.
func TestAnUnreachableRememberedRunIsReportedNotSwapped(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusInternalServerError} {
		ec := evalContextRememberingRun(t, "evalrun_mine", "evalrun_theirs", status)

		var out, errOut bytes.Buffer
		_, err := ec.latestOrNamedRun(
			commandWritingTo(t, &out, &errOut, false), "eval_1", "", true)

		require.Error(t, err, "status %d", status)
		assert.Contains(t, err.Error(), "evalrun_mine",
			"the run it could not read is the one worth naming")
		assert.NotContains(t, err.Error(), "evalrun_theirs")
	}
}

// A remembered run the service no longer has is still worth falling through on:
// it is gone, not unreachable.
func TestARememberedRunThatIsGoneFallsThrough(t *testing.T) {
	ec := evalContextRememberingRun(t, "evalrun_deleted", "evalrun_newest", http.StatusNotFound)

	var out, errOut bytes.Buffer
	run, err := ec.latestOrNamedRun(
		commandWritingTo(t, &out, &errOut, false), "eval_1", "", true)

	require.NoError(t, err)
	assert.Equal(t, "evalrun_newest", run.ID)
}

// A command that changes a run never picks one for itself.
//
// The listing is the one source that can name a run this environment never
// started -- ListOpenAIEvalRuns sends no order parameter, and on a shared
// project the newest row may be someone else's. Reading the wrong run is a
// confusing answer; cancelling it is somebody's lost work.
func TestAMutatingCommandWillNotGuessARun(t *testing.T) {
	ec := evalContextListingOneRun(t, "evalrun_theirs")

	var out, errOut bytes.Buffer
	_, err := ec.latestOrNamedRun(
		commandWritingTo(t, &out, &errOut, false), "eval_1", "", false)

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "evalrun_theirs",
		"it must not even name the run it declined to act on")
	assert.Contains(t, err.Error(), "name the run")
}

// The same command still acts on a run this environment started, which is the
// case that makes it usable without an id.
func TestAMutatingCommandStillUsesTheRunThisEnvironmentStarted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"evalrun_mine","status":"in_progress"}`))
	}))
	t.Cleanup(srv.Close)

	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	env := &testEnvServer{values: map[string]string{idKey("evalrun", "eval_1"): "evalrun_mine"}}
	ec := &evalContext{
		evalClient: eval_api.NewEvalClientFromPipeline(srv.URL, pipeline),
		azdClient:  newTestAzdClient(t, env),
		envName:    "bugbash",
	}

	var out, errOut bytes.Buffer
	run, err := ec.latestOrNamedRun(
		commandWritingTo(t, &out, &errOut, false), "eval_1", "", false)

	require.NoError(t, err)
	assert.Equal(t, "evalrun_mine", run.ID)
}
