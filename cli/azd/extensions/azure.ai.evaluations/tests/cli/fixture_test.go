// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build live

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"azureaieval/internal/pkg/eval_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// The command tests need an eval that has already been run, and building one
// through the CLI is not possible: there is no command that creates an eval
// from flags, only `run start`, which needs a config file and a deployed
// target. So the fixture is built with the client and every assertion is made
// against the binary. What is under test is the command surface; the eval is
// scenery.
//
// It is built once for the whole package because two completed runs cost
// minutes, and torn down in TestMain rather than t.Cleanup so that whichever
// test happened to trigger the build does not take the fixture away from the
// rest.
//
// It evaluates an agent with a built-in evaluator, because that is all M1 can
// run: a deterministic code grader over a target-less dataset would score the
// rows predictably, but code evaluators and no-target runs are both M2. The
// cost is that pass and fail are decided by a judge, so no test may assert how
// many rows failed — only that filtering by verdict is self-consistent.

const fixtureAPIVersion = "2025-11-15-preview"

const defaultFixtureModel = "gpt-4o-mini"

// fixtureQueries are answered by the agent under evaluation. They are ordinary
// support questions: the fixture proves the command surface, not the agent.
var fixtureQueries = []string{
	"How do I reset my password?",
	"How do I change my billing address?",
	"What are your support hours?",
}

// evalFixture is one eval with two completed runs.
type evalFixture struct {
	EvaluatorName string
	EvalID        string

	// The agent the runs evaluate, so that a test needing a further run does
	// not have to resolve one again.
	AgentName string

	// Two runs of the same eval, so that listing, limiting and defaulting to
	// the most recent all have something to distinguish.
	FirstRunID  string
	SecondRunID string
}

var (
	fixtureOnce sync.Once
	fixture     *evalFixture
	fixtureErr  error

	// teardown runs after the last test, in reverse order.
	teardownMu sync.Mutex
	teardown   []func()
)

func deferTeardown(fn func()) {
	teardownMu.Lock()
	defer teardownMu.Unlock()
	teardown = append(teardown, fn)
}

func runTeardown() {
	teardownMu.Lock()
	defer teardownMu.Unlock()
	for i := len(teardown) - 1; i >= 0; i-- {
		teardown[i]()
	}
	teardown = nil
}

// runQuietly invokes the binary without a *testing.T.
//
// Teardown runs after the last test has reported, and logging or asserting
// against a finished test panics, so nothing here may touch one.
func runQuietly(args ...string) {
	full := append(append([]string{}, args...), "--project-endpoint", endpoint)
	_ = exec.Command(binaryPath, full...).Run()
}

var (
	credOnce sync.Once
	cred     *azidentity.AzureDeveloperCLICredential
	credErr  error
)

// liveClient builds the client the fixture is assembled with. One credential
// for the package, because azidentity caches tokens per instance and a fresh
// one per call makes every call shell out to azd again.
//
// The first token is fetched here rather than lazily on the first request:
// that call is the one that flakes, and paying for it up front means the rest
// of the fixture runs against a cached token.
func liveClient() (*eval_api.EvalClient, error) {
	credOnce.Do(func() {
		cred, credErr = azidentity.NewAzureDeveloperCLICredential(
			&azidentity.AzureDeveloperCLICredentialOptions{})
		if credErr != nil {
			return
		}
		credErr = retryCredentialFlake(func() error {
			_, err := cred.GetToken(context.Background(), policy.TokenRequestOptions{
				Scopes: []string{"https://ai.azure.com/.default"},
			})
			return err
		})
	})
	if credErr != nil {
		return nil, credErr
	}
	return eval_api.NewEvalClient(endpoint, cred), nil
}

// retryCredentialFlake reruns a request that failed only because azd's token
// helper exited non-zero.
//
// It is the same failure the harness retries around the binary, for the same
// reason: nothing was sent, and the alternative is a suite that fails on a
// different test each run for a reason unrelated to the code. Any other error
// is returned immediately.
func retryCredentialFlake(fn func() error) error {
	var err error
	for attempt := range 4 {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 2 * time.Second)
		}
		if err = fn(); err == nil || !strings.Contains(err.Error(), credentialFlake) {
			return err
		}
	}
	return err
}

// sharedEval returns the fixture, building it on first use.
//
// A failure here fails the calling test rather than skipping it: every test
// that asks for the fixture is testing something that cannot be exercised
// without one, and a suite that goes green because its subject was missing is
// worse than one that goes red.
func sharedEval(t *testing.T) *evalFixture {
	t.Helper()
	fixtureOnce.Do(func() {
		start := time.Now()
		fixture, fixtureErr = buildFixture(t.Logf)
		t.Logf("fixture ready in %s", time.Since(start).Round(time.Second))
	})
	if fixtureErr != nil {
		t.Fatalf("building the shared eval the command tests run against: %v", fixtureErr)
	}
	return fixture
}

func fixtureModel() string {
	if model := os.Getenv("AZURE_AI_EVAL_MODEL"); model != "" {
		return model
	}
	return defaultFixtureModel
}

// resolveFixtureAgent names the agent the fixture evaluates.
//
// It reads /agents, not /assistants: they are different collections, and an
// eval target resolves against the former. Naming an assistant is accepted by
// the create and then fails the run with "resources not found".
func resolveFixtureAgent(ctx context.Context) (string, error) {
	if name := os.Getenv("AZURE_AI_EVAL_AGENT"); name != "" {
		return name, nil
	}

	// Builds the shared credential if it does not exist yet; the token below
	// comes from it.
	if _, err := liveClient(); err != nil {
		return "", err
	}

	token, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://ai.azure.com/.default"},
	})
	if err != nil {
		return "", fmt.Errorf("acquiring a token to list agents: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx, http.MethodGet, endpoint+"/agents?api-version="+fixtureAPIVersion, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token.Token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("listing the project's agents: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf(
			"listing the project's agents returned %d; set AZURE_AI_EVAL_AGENT to name one",
			resp.StatusCode)
	}

	var listing struct {
		Data []struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listing); err != nil {
		return "", err
	}
	for _, a := range listing.Data {
		if a.Name != "" {
			return a.Name, nil
		}
	}
	return "", fmt.Errorf(
		"this project has no agent in /agents, so an agent-target run cannot be built; " +
			"deploy an agent or set AZURE_AI_EVAL_AGENT")
}

func buildFixture(logf func(string, ...any)) (*evalFixture, error) {
	ctx := context.Background()

	client, err := liveClient()
	if err != nil {
		return nil, fmt.Errorf("acquiring an azd credential: %w", err)
	}

	agent, err := resolveFixtureAgent(ctx)
	if err != nil {
		return nil, err
	}
	logf("evaluating agent %q", agent)

	evaluatorName := "builtin.task_adherence"
	evalID, err := createFixtureEval(ctx, client, evaluatorName)
	if err != nil {
		return nil, err
	}
	logf("created eval %s", evalID)

	first, err := startFixtureRun(ctx, client, evalID, agent, "first")
	if err != nil {
		return nil, err
	}
	second, err := startFixtureRun(ctx, client, evalID, agent, "second")
	if err != nil {
		return nil, err
	}
	logf("started runs %s and %s", first, second)

	// Polled together: they are independent, and serializing them doubles the
	// slowest part of the suite for nothing.
	errs := make(chan error, 2)
	for _, runID := range []string{first, second} {
		go func(id string) { errs <- awaitCompleted(ctx, client, evalID, id, logf) }(runID)
	}
	for range 2 {
		if err := <-errs; err != nil {
			return nil, err
		}
	}

	return &evalFixture{
		// The criterion is named without the builtin. prefix, and that is the
		// name results are reported under.
		EvaluatorName: strings.TrimPrefix(evaluatorName, "builtin."),
		EvalID:        evalID,
		AgentName:     agent,
		FirstRunID:    first,
		SecondRunID:   second,
	}, nil
}

func createFixtureEval(
	ctx context.Context,
	client *eval_api.EvalClient,
	evaluatorName string,
) (string, error) {
	criterionName := strings.TrimPrefix(evaluatorName, "builtin.")

	var group *eval_api.OpenAIEval
	if err := retryCredentialFlake(func() error {
		var err error
		group, err = client.CreateOpenAIEval(ctx, &eval_api.CreateOpenAIEvalRequest{
			Name: uniqueName("azdcli-fixture"),
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
				Name:          criterionName,
				EvaluatorName: evaluatorName,
				DataMapping: map[string]string{
					"query":            "{{item.query}}",
					"response":         "{{sample.output_items}}",
					"tool_calls":       "{{sample.tool_calls}}",
					"tool_definitions": "{{sample.tool_definitions}}",
				},
				InitializationParameters: map[string]any{
					"model":           fixtureModel(),
					"deployment_name": fixtureModel(),
				},
			}},
		})
		return err
	}); err != nil {
		return "", fmt.Errorf("creating the fixture eval: %w", err)
	}
	deferTeardown(func() {
		_ = client.DeleteOpenAIEval(context.Background(), group.ID)
	})
	return group.ID, nil
}

func startFixtureRun(
	ctx context.Context,
	client *eval_api.EvalClient,
	evalID, agentName, label string,
) (string, error) {
	rows := make([]map[string]any, 0, len(fixtureQueries))
	for _, q := range fixtureQueries {
		rows = append(rows, map[string]any{"query": q})
	}

	ds := eval_api.NewAgentTargetDataSource(agentName, nil)
	ds.SetFileContent(rows)

	var run *eval_api.OpenAIEvalRun
	if err := retryCredentialFlake(func() error {
		var err error
		run, err = client.CreateOpenAIEvalRun(ctx, evalID, &eval_api.CreateOpenAIEvalRunRequest{
			Name:       uniqueName("azdcli-" + label),
			DataSource: ds,
		})
		return err
	}); err != nil {
		return "", fmt.Errorf("starting the %s run: %w", label, err)
	}
	return run.ID, nil
}

var terminalRunStatus = map[string]bool{
	"completed": true, "failed": true, "canceled": true, "cancelled": true, "error": true,
}

// awaitCompleted requires the run to have scored something.
//
// A run whose every sample errors still reports completed, so the status alone
// would let the whole suite run against an eval that measured nothing.
func awaitCompleted(
	ctx context.Context,
	client *eval_api.EvalClient,
	evalID, runID string,
	logf func(string, ...any),
) error {
	deadline := time.Now().Add(15 * time.Minute)
	for {
		var run *eval_api.OpenAIEvalRun
		if err := retryCredentialFlake(func() error {
			var err error
			run, err = client.GetOpenAIEvalRun(ctx, evalID, runID)
			return err
		}); err != nil {
			return fmt.Errorf("polling run %s: %w", runID, err)
		}
		if terminalRunStatus[strings.ToLower(run.Status)] {
			if strings.ToLower(run.Status) != "completed" {
				return fmt.Errorf("run %s finished as %q: %s", runID, run.Status, run.Failure())
			}
			if run.ResultCounts == nil {
				return fmt.Errorf("run %s completed without reporting counts", runID)
			}
			if run.ResultCounts.Passed+run.ResultCounts.Failed == 0 {
				return fmt.Errorf(
					"run %s completed without scoring any row (errored=%d); the fixture "+
						"would prove nothing", runID, run.ResultCounts.Errored)
			}
			logf("run %s completed: passed=%d failed=%d errored=%d",
				runID, run.ResultCounts.Passed, run.ResultCounts.Failed, run.ResultCounts.Errored)
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("run %s did not finish in time (last status %q)", runID, run.Status)
		}
		time.Sleep(10 * time.Second)
	}
}
