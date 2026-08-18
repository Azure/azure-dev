// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build live

package live

import (
	"context"
	"strings"
	"testing"
	"time"

	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/require"
)

// TestLiveRunCancel covers the one route whose meaning depends on the request
// body. POST on the run cancels it when the body is empty and updates its
// status and counters when it is not, so a stray body here would silently
// overwrite a run instead of stopping it. Only a live call can tell the two
// apart: both are the same method on the same path, and both return 200.
func TestLiveRunCancel(t *testing.T) {
	env := setup(t)
	agentName := resolveAgent(t, env)
	ctx := context.Background()

	builtins, err := env.evalClient.ListEvaluators(
		ctx, eval_api.EvaluatorTypeBuiltin, projectAPIVersion)
	require.NoError(t, err)
	require.NotEmpty(t, builtins.Value)
	evaluatorName := pickQualityEvaluator(t, builtins.Value)

	group, err := env.evalClient.CreateOpenAIEval(ctx, &eval_api.CreateOpenAIEvalRequest{
		Name: uniqueName("azd-eval-e2e-cancel"),
		DataSourceConfig: &eval_api.DataSourceConfig{
			Type:                "custom",
			IncludeSampleSchema: true,
			ItemSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"query": map[string]any{"type": "string"}},
			},
		},
		TestingCriteria: []eval_api.TestingCriterion{{
			Type:          "azure_ai_evaluator",
			Name:          strings.TrimPrefix(evaluatorName, "builtin."),
			EvaluatorName: evaluatorName,
			DataMapping: map[string]string{
				"query":            "{{item.query}}",
				"response":         "{{sample.output_items}}",
				"tool_calls":       "{{sample.tool_calls}}",
				"tool_definitions": "{{sample.tool_definitions}}",
			},
			InitializationParameters: map[string]any{
				"model":           env.judgeModel,
				"deployment_name": env.judgeModel,
			},
		}},
	})
	require.NoError(t, err, "creating the eval to cancel a run from")

	t.Cleanup(func() {
		_ = env.evalClient.DeleteOpenAIEval(context.Background(), group.ID)
	})

	ds := eval_api.NewAgentTargetDataSource(agentName, nil)
	ds.SetFileContent([]map[string]any{
		{"query": "How do I reset my password?"},
	})

	run, err := env.evalClient.CreateOpenAIEvalRun(ctx, group.ID, &eval_api.CreateOpenAIEvalRunRequest{
		Name:       uniqueName("cancel"),
		DataSource: ds,
	})
	require.NoError(t, err, "starting the run to cancel")
	require.NotEmpty(t, run.ID)
	t.Logf("started run %s (status %s)", run.ID, run.Status)

	canceled, err := env.evalClient.CancelOpenAIEvalRun(ctx, group.ID, run.ID)
	if err != nil {
		// The service refuses to cancel a run that already left the cancellable
		// window. That is a race this test starts but does not control, and it
		// is the behaviour a separate case already pins, so there is nothing
		// left here to observe.
		current, getErr := env.evalClient.GetOpenAIEvalRun(ctx, group.ID, run.ID)
		require.NoError(t, getErr, "reading the run whose cancel was refused")
		t.Skipf("cancel was refused with the run already at %q: %v", current.Status, err)
	}
	require.NotNil(t, canceled)
	t.Logf("cancel returned status %s", canceled.Status)

	// A sample takes roughly 40 seconds, so a run cancelled immediately after
	// it starts should never reach completed.
	deadline := time.Now().Add(5 * time.Minute)
	var status string
	for {
		current, err := env.evalClient.GetOpenAIEvalRun(ctx, group.ID, run.ID)
		require.NoError(t, err, "polling the cancelled run")
		status = strings.ToLower(current.Status)
		if status == "canceled" || status == "cancelled" {
			break
		}
		require.NotEqual(t, "completed", status,
			"the run completed instead of cancelling, so the empty-body POST did not cancel it")
		require.False(t, time.Now().After(deadline),
			"the run never reached a cancelled state; last status was %s", status)
		time.Sleep(5 * time.Second)
	}

	t.Logf("run reached %s", status)
}
