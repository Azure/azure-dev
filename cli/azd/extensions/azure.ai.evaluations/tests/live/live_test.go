// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build live

// Package live holds integration tests that talk to a real Foundry project.
// They are excluded from the default build by the `live` tag and additionally
// gated on AZURE_AI_EVAL_E2E_LIVE so an accidental run cannot create resources.
//
//	go test -tags live -v ./tests/live/...
//
// Required:
//
//	AZURE_AI_EVAL_E2E_LIVE=1
//	FOUNDRY_PROJECT_ENDPOINT=https://<account>.services.ai.azure.com/api/projects/<project>
//
// Optional:
//
//	AZURE_AI_EVAL_MODEL=<judge model deployment>   (default gpt-4.1-nano)
//	AZURE_AI_EVAL_AGENT=<deployed agent name>      (enables the run phase)
package live

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"azureaieval/internal/pkg/dataset_api"
	"azureaieval/internal/pkg/eval_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/stretchr/testify/require"
)

const (
	projectAPIVersion    = "2025-11-15-preview"
	defaultJudgeModel    = "gpt-4.1-nano"
	sampleDatasetContent = `{"query":"How do I reset my password?"}
{"query":"What is the refund window?"}
{"query":"Can I change my shipping address after ordering?"}
`
)

type liveEnv struct {
	endpoint      string
	judgeModel    string
	agentName     string
	evalClient    *eval_api.EvalClient
	datasetClient *dataset_api.DatasetClient
}

// One credential for the whole package, because azidentity caches tokens per
// instance. Building one per test made every test shell out to azd again, and
// a refresh that overruns the SDK's ten-second budget for that subprocess
// surfaces as "AzureDeveloperCLICredential: exit status 1" — which reads like
// a broken login rather than a timeout, and lands on whichever test happened
// to run after a slow one.
var (
	sharedCredOnce sync.Once
	sharedCred     *azidentity.AzureDeveloperCLICredential
	sharedCredErr  error
)

func liveCredential() (*azidentity.AzureDeveloperCLICredential, error) {
	sharedCredOnce.Do(func() {
		// Works non-interactively when azd already holds a refresh token,
		// which is what makes an unattended run possible.
		sharedCred, sharedCredErr = azidentity.NewAzureDeveloperCLICredential(
			&azidentity.AzureDeveloperCLICredentialOptions{},
		)
	})
	return sharedCred, sharedCredErr
}

func setup(t *testing.T) *liveEnv {
	t.Helper()

	if os.Getenv("AZURE_AI_EVAL_E2E_LIVE") != "1" {
		t.Skip("set AZURE_AI_EVAL_E2E_LIVE=1 to run live tests")
	}
	endpoint := strings.TrimSuffix(os.Getenv("FOUNDRY_PROJECT_ENDPOINT"), "/")
	if endpoint == "" {
		t.Fatal("FOUNDRY_PROJECT_ENDPOINT is required")
	}

	cred, err := liveCredential()
	require.NoError(t, err, "acquiring an azd credential")

	judge := os.Getenv("AZURE_AI_EVAL_MODEL")
	if judge == "" {
		judge = defaultJudgeModel
	}

	return &liveEnv{
		endpoint:      endpoint,
		judgeModel:    judge,
		agentName:     os.Getenv("AZURE_AI_EVAL_AGENT"),
		evalClient:    eval_api.NewEvalClient(endpoint, cred),
		datasetClient: dataset_api.NewDatasetClient(endpoint, cred),
	}
}

func uniqueName(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UTC().Unix())
}

// pickQualityEvaluator selects a built-in whose required inputs match the
// agent-target data mapping this extension sends.
//
// Built-ins do not share one input contract: builtin.ifeval, for example,
// requires an `instruction_id_list` field, and creating a group with it under
// the agent-target mapping fails with MissingRequiredDataMapping. The
// agent-target mapping supplies query, response, tool_calls and
// tool_definitions, so the evaluators below are the compatible set.
func pickQualityEvaluator(t *testing.T, available []eval_api.EvaluatorSummary) string {
	t.Helper()

	preferred := []string{
		"builtin.task_adherence",
		"builtin.task_completion",
		"builtin.tool_call_accuracy",
	}
	present := map[string]bool{}
	for _, e := range available {
		present[e.Name] = true
	}
	for _, name := range preferred {
		if present[name] {
			return name
		}
	}

	names := make([]string, 0, len(available))
	for _, e := range available {
		names = append(names, e.Name)
	}
	t.Skipf("no agent-target compatible evaluator found; available: %s", strings.Join(names, ", "))
	return ""
}

// TestLiveBuiltinEvaluators is the cheapest reachability check: it proves the
// endpoint, credential, api-version, and auth scope are all correct without
// creating anything.
func TestLiveBuiltinEvaluators(t *testing.T) {
	env := setup(t)
	ctx := context.Background()

	list, err := env.evalClient.ListEvaluators(
		ctx, eval_api.EvaluatorTypeBuiltin, projectAPIVersion,
	)
	require.NoError(t, err, "listing built-in evaluators")
	require.NotEmpty(t, list.Value, "the project should expose built-in evaluators")

	t.Logf("found %d built-in evaluators; first: %s", len(list.Value), list.Value[0].Name)
}

// TestLiveDatasetLifecycle exercises the full pending-upload flow and asserts
// that re-registering the same name yields the next version rather than an error.
func TestLiveDatasetLifecycle(t *testing.T) {
	env := setup(t)
	ctx := context.Background()

	dir := t.TempDir()
	require.NoError(t,
		os.WriteFile(filepath.Join(dir, "golden.jsonl"), []byte(sampleDatasetContent), 0o600))

	name := uniqueName("azd-eval-e2e")

	first, err := env.datasetClient.UploadNewVersion(ctx, name, "", dir, projectAPIVersion)
	require.NoError(t, err, "registering the first dataset version")
	require.Equal(t, name, first.Name)
	require.NotEmpty(t, first.Version)
	t.Logf("registered %s version %s", first.Name, first.Version)

	t.Cleanup(func() {
		// Best effort: leave nothing behind even if the test fails midway.
		_ = env.datasetClient.DeleteDatasetVersion(
			context.Background(), name, first.Version, projectAPIVersion)
	})

	fetched, err := env.datasetClient.GetDataset(ctx, name, first.Version, projectAPIVersion)
	require.NoError(t, err, "reading the dataset back")
	t.Logf("dataset uri: %q (empty means a credential call is required)", fetched.ResolvedBlobURI())

	// The version listing is eventually consistent: it returns nothing for a
	// second or two after a version is created, even though the version itself
	// reads back fine. Poll rather than asserting on the first response.
	var versions *dataset_api.DatasetList
	require.Eventually(t, func() bool {
		var err error
		versions, err = env.datasetClient.ListDatasetVersions(ctx, name, projectAPIVersion)
		return err == nil && versions != nil && len(versions.Value) > 0
	}, 30*time.Second, 2*time.Second, "the version listing never caught up")
	require.Equal(t, first.Version, dataset_api.LatestVersion(versions.Value))

	// A second upload must advance the version, not conflict.
	second, err := env.datasetClient.UploadNewVersion(
		ctx, name, first.Version, dir, projectAPIVersion)
	require.NoError(t, err, "registering a second dataset version")
	require.NotEqual(t, first.Version, second.Version,
		"re-registering the same name must produce the next version")
	t.Cleanup(func() {
		_ = env.datasetClient.DeleteDatasetVersion(
			context.Background(), name, second.Version, projectAPIVersion)
	})
}

// TestLiveEvalLifecycle proves the create request this extension builds is
// accepted, which is the single most important contract to get right.
func TestLiveEvalLifecycle(t *testing.T) {
	env := setup(t)
	ctx := context.Background()

	builtins, err := env.evalClient.ListEvaluators(
		ctx, eval_api.EvaluatorTypeBuiltin, projectAPIVersion)
	require.NoError(t, err)
	require.NotEmpty(t, builtins.Value, "need at least one built-in evaluator")
	evaluatorName := pickQualityEvaluator(t, builtins.Value)

	threshold := 3.0
	req := &eval_api.CreateOpenAIEvalRequest{
		Name:     uniqueName("azd-eval-e2e-group"),
		Metadata: map[string]string{"azd_source": "e2e"},
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
				"threshold":       threshold,
			},
		}},
	}

	group, err := env.evalClient.CreateOpenAIEval(ctx, req)
	require.NoError(t, err, "creating the eval")
	require.NotEmpty(t, group.ID, "the service assigns the id; name is not unique")
	t.Logf("created eval %s (name %q)", group.ID, group.Name)

	fetched, err := env.evalClient.GetOpenAIEval(ctx, group.ID)
	require.NoError(t, err, "reading the eval back")
	require.Equal(t, group.ID, fetched.ID)
}

// resolveAgent names the agent the run phase evaluates.
//
// AZURE_AI_EVAL_AGENT wins when set. Otherwise one is discovered, and failing
// to find one is a failure rather than a skip: skipping by default is how the
// agent-target path went unverified for weeks while the suite reported green.
//
// The listing is /agents, not /assistants. They are different collections and
// a project can have plenty of the latter and none of the former — an eval
// target resolves against /agents, so an assistant name is accepted by the
// request and then fails the run with "resources not found".
func resolveAgent(t *testing.T, env *liveEnv) string {
	t.Helper()

	if env.agentName != "" {
		return env.agentName
	}

	cred, err := liveCredential()
	require.NoError(t, err)
	token, err := cred.GetToken(context.Background(), policy.TokenRequestOptions{
		Scopes: []string{"https://ai.azure.com/.default"},
	})
	require.NoError(t, err, "acquiring a token to list agents")

	req, err := http.NewRequest(http.MethodGet, env.endpoint+"/agents?api-version="+projectAPIVersion, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token.Token)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err, "listing the project's agents")
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode,
		"could not list agents; set AZURE_AI_EVAL_AGENT to name one directly")

	var listing struct {
		Data []struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&listing))

	for _, a := range listing.Data {
		if a.Name != "" {
			t.Logf("no AZURE_AI_EVAL_AGENT set; evaluating %q", a.Name)
			return a.Name
		}
	}

	t.Fatal("this project has no agent in /agents, so the agent-target run path " +
		"cannot be verified here. Assistants do not count: an eval target " +
		"resolves against /agents, and naming an assistant fails the run with " +
		"\"resources not found\". Deploy an agent, or set AZURE_AI_EVAL_AGENT " +
		"to one in another project")
	return ""
}

// TestLiveRun invokes a real agent, which is the only cover the agent-target
// run path has.
func TestLiveRun(t *testing.T) {
	env := setup(t)
	agentName := resolveAgent(t, env)
	ctx := context.Background()

	builtins, err := env.evalClient.ListEvaluators(
		ctx, eval_api.EvaluatorTypeBuiltin, projectAPIVersion)
	require.NoError(t, err)
	require.NotEmpty(t, builtins.Value)
	evaluatorName := pickQualityEvaluator(t, builtins.Value)

	group, err := env.evalClient.CreateOpenAIEval(ctx, &eval_api.CreateOpenAIEvalRequest{
		Name: uniqueName("azd-eval-e2e-run"),
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
	require.NoError(t, err, "creating the eval for the run")

	ds := eval_api.NewAgentTargetDataSource(agentName, nil)
	ds.SetFileContent([]map[string]any{
		{"query": "How do I reset my password?"},
	})

	run, err := env.evalClient.CreateOpenAIEvalRun(ctx, group.ID, &eval_api.CreateOpenAIEvalRunRequest{
		Name:       uniqueName("run"),
		DataSource: ds,
	})
	require.NoError(t, err, "starting the run")
	require.NotEmpty(t, run.ID)
	t.Logf("started run %s (status %s)", run.ID, run.Status)

	t.Cleanup(func() {
		_, _ = env.evalClient.CancelOpenAIEvalRun(context.Background(), group.ID, run.ID)
	})

	// A single sample is roughly 40 seconds; allow generous headroom.
	deadline := time.Now().Add(10 * time.Minute)
	terminal := map[string]bool{
		"completed": true, "failed": true, "canceled": true, "cancelled": true, "error": true,
	}
	for {
		current, err := env.evalClient.GetOpenAIEvalRun(ctx, group.ID, run.ID)
		require.NoError(t, err, "polling the run")
		if terminal[strings.ToLower(current.Status)] {
			t.Logf("run reached %s", current.Status)
			if current.ResultCounts != nil {
				t.Logf("counts: passed=%d failed=%d errored=%d",
					current.ResultCounts.Passed,
					current.ResultCounts.Failed,
					current.ResultCounts.Errored)
			}
			body, _ := json.MarshalIndent(current.PerTestingCriteria, "", "  ")
			t.Logf("per-criteria results: %s", string(body))

			// Reaching a terminal state is not the same as having evaluated
			// anything. A run whose every sample errors still reports
			// "completed", so asserting only on the status would let the target
			// or the evaluator break without the test noticing.
			require.Equal(t, "completed", strings.ToLower(current.Status),
				"the run must complete rather than fail or cancel")
			require.NotNil(t, current.ResultCounts, "a completed run must report counts")
			require.Zero(t, current.ResultCounts.Errored,
				"an errored sample means the target or the evaluator did not run")
			require.Positive(t,
				current.ResultCounts.Passed+current.ResultCounts.Failed,
				"the run must score at least one sample; a pass or a fail are both fine, "+
					"but scoring nothing means the data never reached the evaluator")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s did not finish within the deadline (last status %q)",
				run.ID, current.Status)
		}
		time.Sleep(10 * time.Second)
	}
}
