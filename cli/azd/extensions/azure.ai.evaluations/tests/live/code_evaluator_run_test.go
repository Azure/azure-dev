// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build live

package live

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/pkg/evalcore"

	"github.com/stretchr/testify/require"
)

// These tests close the gap between publishing a code evaluator and using one.
//
// The publish path was verified on its own first, and passing that proves less
// than it appears to: a script publishes cleanly while carrying no
// data_schema, because only metrics are defaulted. Nothing then tells the
// caller that the evaluator cannot be wired into an eval. The criteria builder
// derives data_mapping from the schema the evaluator publishes, so no schema
// means no mapping, and the service refuses a criterion with none. That
// failure would surface at run time, long after the publish that caused it.
//
// So there are two tests. The first runs a code evaluator end to end and
// requires it to score a sample. The second publishes without a schema and
// records what the service actually does about it, rather than leaving the
// consequence to inference.

// writeCodeEvaluator writes an evaluator that scores the length of a response.
//
// It is one self-contained script. A code evaluator runs as an OpenAI python
// grader, whose contract is a single Source string with one entry point: a
// top-level grade(sample, item). There is no package and no import path, so a
// helper module beside it could not be reached even if it were published.
func writeCodeEvaluator(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name+".py")

	source := `def grade(sample, item) -> float:
    response = (item or {}).get("response", "")
    return float(len(response))
`
	require.NoError(t, os.WriteFile(path, []byte(source), 0o600))
	return path
}

// dataSchemaForResponse is the schema the criteria builder needs to derive a
// data_mapping. Only the caller can supply it: nothing about it is inferable
// from Python source.
func dataSchemaForResponse(t *testing.T) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"type":       "object",
		"properties": map[string]any{"response": map[string]any{"type": "string"}},
		"required":   []string{"response"},
	})
	require.NoError(t, err)
	return raw
}

// publishCodeEvaluator registers the script and returns the version, after
// confirming it is readable.
func publishCodeEvaluator(
	t *testing.T,
	env *liveEnv,
	name string,
	withSchema bool,
) *eval_api.EvaluatorVersion {
	t.Helper()
	ctx := context.Background()

	path := writeCodeEvaluator(t, name)
	script, err := evalcore.LoadCodeEvaluator(name, path)
	require.NoError(t, err, "loading the evaluator script")

	var opts eval_api.CodeEvaluatorOptions
	if withSchema {
		opts.DataSchema = dataSchemaForResponse(t)
		opts.Metrics = json.RawMessage(
			`{"result":{"type":"continuous","desirable_direction":"increase","is_primary":true}}`)
	}

	version, err := env.evalClient.CreateCodeEvaluatorVersion(ctx, script, opts, projectAPIVersion)
	require.NoError(t, err, "publishing the code evaluator")
	t.Cleanup(func() {
		_ = env.evalClient.DeleteEvaluatorVersion(
			context.Background(), name, version.Version, projectAPIVersion)
	})
	t.Logf("published code evaluator %s version %s", name, version.Version)

	awaitEvaluatorResolvable(t, env, name, version.Version)
	return version
}

// awaitEvaluatorResolvable waits for a published version the way the
// reconciler does, and reports how long each of the two views took.
//
// The numbers are the point. The direct read goes consistent almost at once
// while the listing lags it, and the eval-create resolver follows the slower
// one: a publish was observed reading back at 03:06:58 and still failing eval
// creation at 03:06:59. Logging both is what keeps the reconciler's tolerance
// honest instead of guessed, and asserting on the listing here is what proves
// the gate it waits on is the right one.
func awaitEvaluatorResolvable(t *testing.T, env *liveEnv, name, version string) {
	t.Helper()
	ctx := context.Background()

	start := time.Now()
	var readable time.Duration

	for {
		if readable == 0 {
			if _, err := env.evalClient.GetEvaluatorRaw(
				ctx, name, version, projectAPIVersion,
			); err == nil {
				readable = time.Since(start)
				t.Logf("evaluator %s readable after %s",
					name, readable.Round(time.Millisecond))
			}
		}
		if readable != 0 && evaluatorVersionListed(ctx, env, name, version) {
			t.Logf("evaluator %s listed after %s",
				name, time.Since(start).Round(time.Millisecond))
			return
		}
		if time.Since(start) > 2*time.Minute {
			t.Fatalf("evaluator %s never became resolvable", name)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func evaluatorVersionListed(
	ctx context.Context,
	env *liveEnv,
	name, version string,
) bool {
	list, err := env.evalClient.ListEvaluatorVersions(ctx, name, projectAPIVersion)
	if err != nil || list == nil {
		return false
	}
	for _, entry := range list.Value {
		if entry.Version == version {
			return true
		}
	}
	return false
}

// createEvalReferencing creates an eval naming a custom evaluator, tolerating
// the window in which the evaluator is published but not yet resolvable.
//
// The delay is reported so a run that hits it leaves evidence of how long it
// took, which is the number the reconciler's own tolerance has to be built on.
func createEvalReferencing(
	t *testing.T,
	env *liveEnv,
	req *eval_api.CreateOpenAIEvalRequest,
	within time.Duration,
) (*eval_api.OpenAIEval, error) {
	t.Helper()
	ctx := context.Background()

	start := time.Now()
	for {
		group, err := env.evalClient.CreateOpenAIEval(ctx, req)
		if err == nil {
			t.Logf("eval accepted the evaluator after %s", time.Since(start).Round(time.Millisecond))
			return group, nil
		}
		if !strings.Contains(strings.ToLower(err.Error()), "was not found") {
			return nil, err
		}
		if time.Since(start) > within {
			t.Logf("the evaluator was still unresolvable after %s", within)
			return nil, err
		}
		time.Sleep(5 * time.Second)
	}
}

// TestLiveCodeEvaluatorScoresARun is the test the publish tests could not be:
// it requires the evaluator to actually run and return a score.
//
// No agent is involved. A code evaluator reads item fields, so the run uses a
// dataset-only source and the rows are supplied inline. That keeps the test
// about the evaluator rather than about a target being reachable.
func TestLiveCodeEvaluatorScoresARun(t *testing.T) {
	env := setup(t)
	ctx := context.Background()

	name := strings.ReplaceAll(uniqueName("azdcoderun"), "-", "_")
	publishCodeEvaluator(t, env, name, true)

	group, err := createEvalReferencing(t, env, &eval_api.CreateOpenAIEvalRequest{
		Name: uniqueName("azd-code-eval"),
		DataSourceConfig: &eval_api.DataSourceConfig{
			Type: "custom",
			ItemSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"response": map[string]any{"type": "string"}},
			},
		},
		TestingCriteria: []eval_api.TestingCriterion{{
			Type:          "azure_ai_evaluator",
			Name:          name,
			EvaluatorName: name,
			DataMapping:   map[string]string{"response": "{{item.response}}"},
		}},
	}, 3*time.Minute)
	require.NoError(t, err, "creating an eval that references the code evaluator")
	t.Logf("created eval %s", group.ID)

	ds := eval_api.NewDatasetOnlyDataSource()
	ds.SetFileContent([]map[string]any{
		{"response": "a short answer"},
		{"response": "a considerably longer answer than the first one"},
	})

	run, err := env.evalClient.CreateOpenAIEvalRun(ctx, group.ID, &eval_api.CreateOpenAIEvalRunRequest{
		Name:       uniqueName("code-run"),
		DataSource: ds,
	})
	require.NoError(t, err, "starting the run")
	t.Cleanup(func() {
		_, _ = env.evalClient.CancelOpenAIEvalRun(context.Background(), group.ID, run.ID)
	})
	t.Logf("started run %s", run.ID)

	final := awaitRun(t, env, group.ID, run.ID, 10*time.Minute)

	// Reaching a terminal state is not the same as having evaluated anything:
	// a run whose every sample errors still reports completed.
	require.Equal(t, "completed", strings.ToLower(final.Status),
		"the run must complete rather than fail or cancel")
	require.NotNil(t, final.ResultCounts, "a completed run must report counts")
	require.Zero(t, final.ResultCounts.Errored,
		"an errored sample means the code evaluator did not run")
	require.Positive(t, final.ResultCounts.Passed+final.ResultCounts.Failed,
		"the run must score at least one sample; scoring nothing means the rows "+
			"never reached the evaluator")
}

// TestLiveCodeEvaluatorWithoutSchemaIsAccepted pins down what happens to a
// script published with no data_schema, which is the shape most people's
// their first evaluator will produce.
//
// It was expected to be refused. The reasoning was that the criteria builder
// derives data_mapping from the evaluator's schema, so no schema means no
// mapping, and the service rejects a criterion with none. An earlier run
// appeared to confirm it. That was wrong: the refusal was the propagation 404
// in disguise, read as a mapping error because it arrived at the same call.
// With the publish properly gated, a schema-less evaluator is accepted and an
// empty data_mapping is allowed, so this is not the trap it looked like.
//
// The create is deliberately not retried. publishCodeEvaluator has already
// waited on the same condition the reconciler waits on, so a "was not found"
// here would mean that gate is the wrong one — which is worth failing on,
// because a retry would hide it.
func TestLiveCodeEvaluatorWithoutSchemaIsAccepted(t *testing.T) {
	env := setup(t)
	ctx := context.Background()

	name := strings.ReplaceAll(uniqueName("azdcodenoschema"), "-", "_")
	published := publishCodeEvaluator(t, env, name, false)
	require.NotEmpty(t, published.Version,
		"a script with no schema still publishes; the schema is not required to register")

	_, err := env.evalClient.CreateOpenAIEval(ctx, &eval_api.CreateOpenAIEvalRequest{
		Name: uniqueName("azd-code-eval-noschema"),
		DataSourceConfig: &eval_api.DataSourceConfig{
			Type: "custom",
			ItemSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"response": map[string]any{"type": "string"}},
			},
		},
		TestingCriteria: []eval_api.TestingCriterion{{
			Type:          "azure_ai_evaluator",
			Name:          name,
			EvaluatorName: name,
			// Deliberately empty: this is what the criteria builder produces
			// for an evaluator that publishes no data_schema.
			DataMapping: map[string]string{},
		}},
	})

	if err == nil {
		t.Log("an evaluator with no data_schema was accepted with an empty data_mapping; " +
			"publishing without a schema is not by itself a blocker")
		return
	}
	require.NotContains(t, strings.ToLower(err.Error()), "was not found",
		"the evaluator was published and waited for, so a not-found here means the "+
			"propagation gate the reconciler uses does not cover eval creation")
	t.Logf("an evaluator with no data_schema was refused: %v", err)
	require.Contains(t, strings.ToLower(err.Error()), "mapping",
		"the refusal should name the mapping, so the CLI can explain it at publish time")
}

// awaitRun polls until the run reaches a terminal state or the deadline passes.
func awaitRun(
	t *testing.T,
	env *liveEnv,
	evalID string,
	runID string,
	within time.Duration,
) *eval_api.OpenAIEvalRun {
	t.Helper()
	ctx := context.Background()

	terminal := map[string]bool{
		"completed": true, "failed": true, "canceled": true, "cancelled": true, "error": true,
	}
	deadline := time.Now().Add(within)
	for {
		current, err := env.evalClient.GetOpenAIEvalRun(ctx, evalID, runID)
		require.NoError(t, err, "polling the run")
		if terminal[strings.ToLower(current.Status)] {
			if current.ResultCounts != nil {
				t.Logf("run %s reached %s: passed=%d failed=%d errored=%d",
					runID, current.Status,
					current.ResultCounts.Passed,
					current.ResultCounts.Failed,
					current.ResultCounts.Errored)
			}
			body, _ := json.MarshalIndent(current.PerTestingCriteria, "", "  ")
			t.Logf("per-criteria results: %s", string(body))
			return current
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s did not finish within %s (last status %q)",
				runID, within, current.Status)
		}
		time.Sleep(10 * time.Second)
	}
}
