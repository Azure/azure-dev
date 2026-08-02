// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build live

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/pkg/evalcore"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// The command tests need an eval that has already been run, and building one
// through the CLI is not possible: there is no command that creates an eval
// from flags, only `run`, which needs a config file and a deployed target.
// So the fixture is built with the client and every assertion is made against
// the binary. What is under test is the command surface; the eval is scenery.
//
// It is built once for the whole package because two completed runs cost
// minutes, and torn down in TestMain rather than t.Cleanup so that whichever
// test happened to trigger the build does not take the fixture away from the
// rest.

const fixtureAPIVersion = "2025-11-15-preview"

// scoringGrader splits the rows deterministically. A grader that scores every
// row the same way makes --failed-only and a comparison indistinguishable from
// a no-op, so "good" is the difference between a pass and a failure.
const scoringGrader = `def grade(sample, item) -> float:
    response = (item or {}).get("response", "")
    return 1.0 if "good" in response else 0.0
`

// evalFixture is one eval with two completed runs of the same criterion.
type evalFixture struct {
	EvaluatorName string
	EvalID        string

	// Baseline scores worse than Treatment, so a comparison between them has
	// a delta to report rather than zero.
	BaselineRunID  string
	TreatmentRunID string
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

func buildFixture(logf func(string, ...any)) (*evalFixture, error) {
	ctx := context.Background()

	client, err := liveClient()
	if err != nil {
		return nil, fmt.Errorf("acquiring an azd credential: %w", err)
	}

	name := strings.ReplaceAll(uniqueName("azdclifx"), "-", "_")
	script, err := publishScoringEvaluator(ctx, client, name)
	if err != nil {
		return nil, err
	}
	logf("published code evaluator %s version %s", name, script.Version)

	if err := awaitEvaluatorListed(ctx, client, name, script.Version); err != nil {
		return nil, err
	}

	evalID, err := createFixtureEval(ctx, client, name)
	if err != nil {
		return nil, err
	}
	logf("created eval %s", evalID)

	// Different pass rates so the comparison has something to measure.
	baseline, err := startFixtureRun(ctx, client, evalID, "baseline",
		[]string{"a bad answer", "another bad answer", "a good answer"})
	if err != nil {
		return nil, err
	}
	treatment, err := startFixtureRun(ctx, client, evalID, "treatment",
		[]string{"a good answer", "another good answer", "a third good answer"})
	if err != nil {
		return nil, err
	}
	logf("started runs %s and %s", baseline, treatment)

	// Polled together: they are independent, and serialising them doubles the
	// slowest part of the suite for nothing.
	errs := make(chan error, 2)
	for _, runID := range []string{baseline, treatment} {
		go func(id string) { errs <- awaitCompleted(ctx, client, evalID, id, logf) }(runID)
	}
	for range 2 {
		if err := <-errs; err != nil {
			return nil, err
		}
	}

	return &evalFixture{
		EvaluatorName:  name,
		EvalID:         evalID,
		BaselineRunID:  baseline,
		TreatmentRunID: treatment,
	}, nil
}

func publishScoringEvaluator(
	ctx context.Context,
	client *eval_api.EvalClient,
	name string,
) (*eval_api.EvaluatorVersion, error) {
	dir, err := os.MkdirTemp("", "azdcli-grader")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, name+".py")
	if err := os.WriteFile(path, []byte(scoringGrader), 0o600); err != nil {
		return nil, err
	}
	script, err := evalcore.LoadCodeEvaluator(name, path)
	if err != nil {
		return nil, fmt.Errorf("loading the grader: %w", err)
	}

	// Without a data_schema the criteria builder has no mapping to derive, so
	// the schema is what makes the evaluator usable rather than merely
	// publishable.
	opts := eval_api.CodeEvaluatorOptions{
		DataSchema: json.RawMessage(
			`{"type":"object","properties":{"response":{"type":"string"}},"required":["response"]}`),
		Metrics: json.RawMessage(
			`{"result":{"type":"continuous","desirable_direction":"increase","is_primary":true}}`),
	}
	var version *eval_api.EvaluatorVersion
	if err := retryCredentialFlake(func() error {
		var err error
		version, err = client.CreateCodeEvaluatorVersion(ctx, script, opts, fixtureAPIVersion)
		return err
	}); err != nil {
		return nil, fmt.Errorf("publishing the code evaluator: %w", err)
	}
	deferTeardown(func() {
		_ = client.DeleteEvaluatorVersion(
			context.Background(), name, version.Version, fixtureAPIVersion)
	})
	return version, nil
}

// awaitEvaluatorListed waits for the version listing to catch up, which is the
// view eval creation resolves against. The direct read goes consistent first,
// so waiting on that alone still leaves the create failing with "was not
// found".
func awaitEvaluatorListed(
	ctx context.Context,
	client *eval_api.EvalClient,
	name, version string,
) error {
	deadline := time.Now().Add(2 * time.Minute)
	for {
		list, err := client.ListEvaluatorVersions(ctx, name, fixtureAPIVersion)
		if err == nil && list != nil {
			for _, entry := range list.Value {
				if entry.Version == version {
					return nil
				}
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("evaluator %s version %s never appeared in the listing", name, version)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func createFixtureEval(
	ctx context.Context,
	client *eval_api.EvalClient,
	evaluatorName string,
) (string, error) {
	// Hand-written rather than built with buildEvalRequest, which is
	// unexported. That is safe only because the evaluator is one published
	// here whose schema is a single `response` column; a built-in would need
	// the shipping builder, since their input contracts differ per evaluator.
	var group *eval_api.OpenAIEval
	if err := retryCredentialFlake(func() error {
		var err error
		group, err = client.CreateOpenAIEval(ctx, &eval_api.CreateOpenAIEvalRequest{
			Name: uniqueName("azdcli-fixture"),
			DataSourceConfig: &eval_api.DataSourceConfig{
				Type: "custom",
				ItemSchema: map[string]any{
					"type":       "object",
					"properties": map[string]any{"response": map[string]any{"type": "string"}},
				},
			},
			TestingCriteria: []eval_api.TestingCriterion{{
				Type:          "azure_ai_evaluator",
				Name:          evaluatorName,
				EvaluatorName: evaluatorName,
				DataMapping:   map[string]string{"response": "{{item.response}}"},
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
	evalID, label string,
	responses []string,
) (string, error) {
	rows := make([]map[string]any, 0, len(responses))
	for _, r := range responses {
		rows = append(rows, map[string]any{"response": r})
	}

	ds := eval_api.NewDatasetOnlyDataSource()
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
