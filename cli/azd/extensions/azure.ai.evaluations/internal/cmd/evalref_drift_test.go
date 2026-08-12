// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"azureaieval/internal/pkg/dataset_api"
	"azureaieval/internal/pkg/eval_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reconcilerListingVersions builds a reconciler whose dataset client answers a
// version listing with these versions.
func reconcilerListingVersions(t *testing.T, versions ...string) *evalReconciler {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		values := make([]map[string]any, 0, len(versions))
		for _, v := range versions {
			values = append(values, map[string]any{"name": "golden", "version": v})
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"value": values}))
	}))
	t.Cleanup(srv.Close)

	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	return &evalReconciler{ec: &evalContext{
		datasetClient: dataset_api.NewDatasetClientFromPipeline(srv.URL, pipeline),
	}}
}

// A version published outside the repo would otherwise be overwritten by the
// next deploy. The evaluator side of this is guarded; the dataset side is the
// same risk against content nobody has a copy of.
func TestCheckDatasetDriftRefusesANewerPublishedVersion(t *testing.T) {
	r := reconcilerListingVersions(t, "1.0", "2.0", "3.0")

	err := r.checkDatasetDrift(context.Background(), "golden", "2.0")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "3.0", "the version that is actually there")
	assert.Contains(t, err.Error(), "2.0", "and the one this repo last deployed")
	assert.Contains(t, err.Error(), "outside this repo")
	assert.Contains(t, err.Error(), "version: 3.0", "the fix is a pin the user can paste")
}

// Matching versions are the ordinary case and must stay silent, or every
// deploy would report drift.
func TestCheckDatasetDriftAcceptsAMatch(t *testing.T) {
	r := reconcilerListingVersions(t, "1.0", "2.0")
	require.NoError(t, r.checkDatasetDrift(context.Background(), "golden", "2.0"))
}

// The listing is eventually consistent and answers with nothing for a second
// or two after a publish. Reading that as "the project is behind" would report
// drift on a dataset this repo had just deployed.
func TestCheckDatasetDriftIgnoresAnEmptyListing(t *testing.T) {
	r := reconcilerListingVersions(t)
	require.NoError(t, r.checkDatasetDrift(context.Background(), "golden", "2.0"))
	assert.Empty(t, r.latestDatasetVersion(context.Background(), "golden"))
}

// Only a newer version is someone else's work. An older one means this repo is
// ahead, which the deploy is about to fix anyway.
func TestCheckDatasetDriftIgnoresAnOlderVersion(t *testing.T) {
	r := reconcilerListingVersions(t, "1.0")
	require.NoError(t, r.checkDatasetDrift(context.Background(), "golden", "2.0"),
		"a project behind this repo is not drift")
}

// An unreachable project must not fail the deploy on drift it could not check.
func TestLatestDatasetVersionToleratesAFailedListing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	r := &evalReconciler{ec: &evalContext{
		datasetClient: dataset_api.NewDatasetClientFromPipeline(srv.URL, pipeline),
	}}

	assert.Empty(t, r.latestDatasetVersion(context.Background(), "golden"))
	require.NoError(t, r.checkDatasetDrift(context.Background(), "golden", "2.0"))
}

// writeEvalYAML puts a configuration in a temp dir and returns the dir.
func writeEvalYAML(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	evals := filepath.Join(dir, "evals")
	require.NoError(t, os.MkdirAll(evals, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(evals, "eval.yaml"), []byte(body), 0o600))
	return evals
}

// evalContextListingEvals builds a context whose service lists exactly these
// evals. resolveEvalRef asks the service by name when no id was recorded, so a
// context without a client cannot exercise it.
func evalContextListingEvals(t *testing.T, envName, body string) *evalContext {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	return &evalContext{
		envName:    envName,
		evalClient: eval_api.NewEvalClientFromPipeline(srv.URL, pipeline),
	}
}

// evalContextRefusingToListEvals builds a context whose service will not answer
// the listing at all, which is a different thing from listing nothing.
func evalContextRefusingToListEvals(t *testing.T, envName string, status int) *evalContext {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":{"code":"TooManyRequests"}}`))
	}))
	t.Cleanup(srv.Close)

	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	return &evalContext{
		envName:    envName,
		evalClient: eval_api.NewEvalClientFromPipeline(srv.URL, pipeline),
	}
}

// A listing that failed is not a listing that came back empty. Both leave no
// id, and only one of them means the eval was never deployed.
//
// This is not hypothetical: four concurrent `run start` calls against a live
// project produced exactly one of these, and the reader was told a deployed
// eval did not exist and to run `azd up` -- which would publish a second copy
// of something already there.
func TestResolveEvalRefReportsARefusedListingRatherThanCallingItUndeployed(t *testing.T) {
	dir := writeEvalYAML(t, `
datasets:
  - name: golden
evals:
  - name: support-quality
    dataset: golden
    evaluators:
      - evaluator: builtin.relevance
`)
	ec := evalContextRefusingToListEvals(t, "dev", http.StatusTooManyRequests)

	_, err := ec.resolveEvalRef(context.Background(), dir, "support-quality")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing evals",
		"the failure that actually happened is the one reported")
	assert.NotContains(t, err.Error(), "azd up",
		"deploying again does not fix a listing the service refused")
}

// A declared eval that was never deployed has no id to address, and the
// service would answer 404 for a name it never saw. Naming the command that
// deploys is the difference between a dead end and a next step.
func TestResolveEvalRefFailsFastOnAnUndeployedDeclaration(t *testing.T) {
	dir := writeEvalYAML(t, `
datasets:
  - name: golden
evals:
  - name: support-quality
    dataset: golden
    evaluators:
      - evaluator: builtin.relevance
`)
	// An environment exists; the id was simply never recorded in it. Without
	// this the case under test is "nowhere to record", which is a different
	// answer. The service lists nothing, which is what never deployed looks
	// like from the outside.
	ec := evalContextListingEvals(t, "dev", `{"data":[]}`)

	_, err := ec.resolveEvalRef(context.Background(), dir, "support-quality")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "support-quality")
	// No azd project stands behind this context, so there is no infrastructure
	// to provision and `azd up` would fail compiling a missing template before
	// it deployed anything.
	assert.Contains(t, err.Error(), "azd deploy", "the error has to say what would fix it")
}

// The id lives in the azd environment, so `--project-endpoint` against a
// directory that never had one has a published eval and no note of it. Failing
// there would make the declaration unusable outside a project, though the
// service can be asked for the same name.
func TestResolveEvalRefFindsAPublishedEvalByName(t *testing.T) {
	dir := writeEvalYAML(t, `
datasets:
  - name: golden
evals:
  - name: support-quality
    dataset: golden
    evaluators:
      - evaluator: builtin.relevance
`)
	ec := evalContextListingEvals(t, "",
		`{"data":[{"id":"eval_published","name":"support-quality"}]}`)

	ref, err := ec.resolveEvalRef(context.Background(), dir, "support-quality")

	require.NoError(t, err)
	assert.Equal(t, "eval_published", ref.ID,
		"the service knows the id this environment never recorded")
	assert.True(t, ref.Declared(), "and it is still the declaration that was matched")
}

// With no azd environment at all there is nowhere the id could have been
// recorded, so `create` may well have published this eval and had nowhere to
// note it. Sending the reader to `azd up` lands them in the same place.
func TestResolveEvalRefNamesTheMissingEnvironment(t *testing.T) {
	dir := writeEvalYAML(t, `
datasets:
  - name: golden
evals:
  - name: support-quality
    dataset: golden
    evaluators:
      - evaluator: builtin.relevance
`)
	ec := evalContextListingEvals(t, "", `{"data":[]}`)

	_, err := ec.resolveEvalRef(context.Background(), dir, "support-quality")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "azd env new",
		"the fix is an environment, not another deploy")
	assert.NotContains(t, err.Error(), "azd up",
		"deploying again would record the id in the same nowhere")
}

// An eval made by `azd ai eval create` has no evals: entry, so anything that
// is not a declared name is sent on as an id rather than refused.
func TestResolveEvalRefTreatsAnUnknownNameAsAnID(t *testing.T) {
	dir := writeEvalYAML(t, `
datasets:
  - name: golden
evals:
  - name: support-quality
    dataset: golden
    evaluators:
      - evaluator: builtin.relevance
`)
	ec := &evalContext{}

	ref, err := ec.resolveEvalRef(context.Background(), dir, "eval_68a1f2c3")

	require.NoError(t, err)
	assert.Equal(t, "eval_68a1f2c3", ref.ID)
	assert.False(t, ref.Declared(), "an id carries no declaration to run from")
	assert.Nil(t, ref.Eval)
}

// With no configuration and no name there is nothing to resolve, and the
// message has to say where a declaration would have been looked for.
func TestResolveEvalRefWithoutAConfigurationOrAName(t *testing.T) {
	ec := &evalContext{}

	_, err := ec.resolveEvalRef(context.Background(), t.TempDir(), "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--eval")
	assert.Contains(t, err.Error(), "eval.yaml")
}

// Outside a project an id is still enough to address every route under it.
func TestResolveEvalRefAcceptsAnIDWithoutAConfiguration(t *testing.T) {
	ec := &evalContext{}

	ref, err := ec.resolveEvalRef(context.Background(), t.TempDir(), "eval_68a1f2c3")

	require.NoError(t, err)
	assert.Equal(t, "eval_68a1f2c3", ref.ID)
	assert.False(t, ref.Declared())
}

// Naming nothing where several evals are declared is ambiguous, and the
// configuration's own complaint is the useful one.
func TestResolveEvalRefReportsAmbiguity(t *testing.T) {
	dir := writeEvalYAML(t, `
datasets:
  - name: golden
evals:
  - name: first
    dataset: golden
    evaluators:
      - evaluator: builtin.relevance
  - name: second
    dataset: golden
    evaluators:
      - evaluator: builtin.coherence
`)
	ec := &evalContext{}

	_, err := ec.resolveEvalRef(context.Background(), dir, "")
	require.Error(t, err)
}
