// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build live

// This file proves the request buildEvalGroupRequest produces is accepted by
// the real service. It lives in the cmd package on purpose: the tests under
// tests/live can only hand-roll a request, which validates the API but not the
// code that ships.
//
//	go test -tags live -v ./internal/cmd/ -run TestLiveBuild
//
// Required: AZURE_AI_EVAL_E2E_LIVE=1 and FOUNDRY_PROJECT_ENDPOINT.

package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/stretchr/testify/require"
)

func liveEvalClient(t *testing.T) (*eval_api.EvalClient, string) {
	t.Helper()
	if os.Getenv("AZURE_AI_EVAL_E2E_LIVE") != "1" {
		t.Skip("set AZURE_AI_EVAL_E2E_LIVE=1 to run live tests")
	}
	endpoint := strings.TrimSuffix(os.Getenv("FOUNDRY_PROJECT_ENDPOINT"), "/")
	if endpoint == "" {
		t.Fatal("FOUNDRY_PROJECT_ENDPOINT is required")
	}
	cred, err := azidentity.NewAzureDeveloperCLICredential(
		&azidentity.AzureDeveloperCLICredentialOptions{})
	require.NoError(t, err)

	judge := os.Getenv("AZURE_AI_EVAL_MODEL")
	if judge == "" {
		judge = "gpt-4.1-nano"
	}
	return eval_api.NewEvalClient(endpoint, cred), judge
}

// TestLiveBuildAcceptedForEveryBuiltin walks every built-in the project
// exposes, builds a group with the shipping builder, and posts it.
//
// Each evaluator declares a different input contract, so this is the test that
// would have caught the fixed data mapping: it previously produced a
// MissingRequiredDataMapping rejection for builtin.ifeval and would do so
// again for any evaluator whose contract the builder stops honouring.
func TestLiveBuildAcceptedForEveryBuiltin(t *testing.T) {
	client, judge := liveEvalClient(t)
	ctx := context.Background()

	listed, err := client.ListEvaluators(ctx, eval_api.EvaluatorTypeBuiltin, ProjectEndpointAPIVersion)
	require.NoError(t, err)
	require.NotEmpty(t, listed.Value)
	schemas := listed.ByName()

	for _, summary := range listed.Value {
		summary := summary
		t.Run(summary.Name, func(t *testing.T) {
			// Give the builder a dataset carrying every column the evaluator
			// accepts, so a rejection means the request shape is wrong rather
			// than the data being genuinely absent.
			columns := map[string]bool{"query": true}
			if ds := summary.DataSchema(); ds != nil {
				for _, name := range ds.PropertyNames() {
					columns[name] = true
				}
			}

			level := ""
			if len(summary.SupportedEvaluationLevels) > 0 {
				level = summary.SupportedEvaluationLevels[0]
			}

			group := &project.EvalGroup{
				Name:       fmt.Sprintf("azd-live-%d", time.Now().UTC().UnixNano()),
				Dataset:    "inline",
				Target:     &project.Target{Type: "agent", Name: "probe-agent"},
				Evaluators: []evalcore.EvaluatorRef{{Name: summary.Name}},
				Options:    &project.Options{EvalModel: judge, EvaluationLevel: level},
			}

			req, err := buildEvalGroupRequest(group, schemas, columns)
			require.NoError(t, err, "the builder must satisfy every published contract")

			created, err := client.CreateOpenAIEval(ctx, req)
			require.NoError(t, err,
				"the service rejected the request this extension builds for %s", summary.Name)
			require.NotEmpty(t, created.ID)
			t.Cleanup(func() {
				_ = client.DeleteOpenAIEval(context.Background(), created.ID)
			})
			t.Logf("%s accepted as %s", summary.Name, created.ID)
		})
	}
}

// TestLiveBuildRejectsMissingColumnsLocally proves the pre-flight check fires
// before the network call, so a user sees which column is missing instead of a
// service error naming an internal field path.
func TestLiveBuildRejectsMissingColumnsLocally(t *testing.T) {
	client, judge := liveEvalClient(t)
	ctx := context.Background()

	listed, err := client.ListEvaluators(ctx, eval_api.EvaluatorTypeBuiltin, ProjectEndpointAPIVersion)
	require.NoError(t, err)
	schemas := listed.ByName()

	target, ok := schemas["builtin.ifeval"]
	if !ok {
		t.Skip("builtin.ifeval is not available in this project")
	}
	require.NotNil(t, target.DataSchema())
	require.NotEmpty(t, target.DataSchema().Required,
		"this test relies on ifeval declaring required inputs")

	group := &project.EvalGroup{
		Name:       "azd-live-negative",
		Dataset:    "inline",
		Target:     &project.Target{Type: "agent", Name: "probe-agent"},
		Evaluators: []evalcore.EvaluatorRef{{Name: "builtin.ifeval"}},
		Options:    &project.Options{EvalModel: judge},
	}

	// A dataset with only `query` cannot satisfy ifeval.
	_, err = buildEvalGroupRequest(group, schemas, map[string]bool{"query": true})
	require.Error(t, err)
	require.Contains(t, err.Error(), "instruction_id_list")
	t.Logf("pre-flight error: %v", err)
}
