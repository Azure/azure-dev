// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build live

// This file proves the request buildEvalRequest produces is accepted by
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
	"sync"
	"testing"
	"time"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/stretchr/testify/require"
)

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
		sharedCred, sharedCredErr = azidentity.NewAzureDeveloperCLICredential(
			&azidentity.AzureDeveloperCLICredentialOptions{})
	})
	return sharedCred, sharedCredErr
}

// credentialFlake is what a token refresh that overran its budget looks like
// by the time it reaches a test.
const credentialFlake = "AzureDeveloperCLICredential: exit status 1"

// retryingCredential retries a token request that failed for that reason.
//
// The refresh shells out to azd, and the SDK gives that subprocess ten
// seconds. On a machine already running the rest of this suite it sometimes
// does not finish in ten, and the failure lands on whichever test asked for a
// token at the wrong moment — reproducibly at 10.1s, and never when that test
// is run on its own. Retrying is right because nothing about the request was
// wrong: the same call succeeds moments later.
type retryingCredential struct {
	inner azcore.TokenCredential
}

func (c retryingCredential) GetToken(
	ctx context.Context,
	opts policy.TokenRequestOptions,
) (azcore.AccessToken, error) {
	var token azcore.AccessToken
	var err error
	for attempt := range 4 {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return azcore.AccessToken{}, ctx.Err()
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}
		token, err = c.inner.GetToken(ctx, opts)
		if err == nil || !strings.Contains(err.Error(), credentialFlake) {
			return token, err
		}
	}
	return token, err
}

func liveEvalClient(t *testing.T) (*eval_api.EvalClient, string) {
	t.Helper()
	if os.Getenv("AZURE_AI_EVAL_E2E_LIVE") != "1" {
		t.Skip("set AZURE_AI_EVAL_E2E_LIVE=1 to run live tests")
	}
	endpoint := strings.TrimSuffix(os.Getenv("FOUNDRY_PROJECT_ENDPOINT"), "/")
	if endpoint == "" {
		t.Fatal("FOUNDRY_PROJECT_ENDPOINT is required")
	}
	cred, err := liveCredential()
	require.NoError(t, err)

	judge := os.Getenv("AZURE_AI_EVAL_MODEL")
	if judge == "" {
		judge = "gpt-4.1-nano"
	}
	return eval_api.NewEvalClient(endpoint, retryingCredential{inner: cred}), judge
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

	// Deliberately the production lookup rather than the listing above. Taking
	// the schemas straight from a filtered list is what let this test pass
	// while the shipping path resolved none of them: it built the input the
	// product was failing to build.
	ec := &evalContext{evalClient: client}
	schemas := ec.evaluatorSchemas(ctx)
	require.NotEmpty(t, schemas)

	for _, summary := range listed.Value {
		summary := summary
		t.Run(summary.Name, func(t *testing.T) {
			require.NotNil(t, schemas[summary.Name],
				"the shipping lookup did not resolve %s", summary.Name)
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

			group := &project.Eval{
				Name:    fmt.Sprintf("azd-live-%d", time.Now().UTC().UnixNano()),
				Dataset: "inline",
				Target:  &project.Target{Type: "agent", Name: "probe-agent"},
				Evaluators: []evalcore.EvaluatorRef{{
					Evaluator:                summary.Name,
					InitializationParameters: map[string]any{"deployment_name": judge},
				}},
				EvaluationLevel: level,
			}

			req, err := buildEvalRequest(group, schemas, columns)
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

	group := &project.Eval{
		Name:    "azd-live-negative",
		Dataset: "inline",
		Target:  &project.Target{Type: "agent", Name: "probe-agent"},
		Evaluators: []evalcore.EvaluatorRef{{
			Name:                     "builtin.ifeval",
			InitializationParameters: map[string]any{"deployment_name": judge},
		}},
	}

	// A dataset with only `query` cannot satisfy ifeval.
	_, err = buildEvalRequest(group, schemas, map[string]bool{"query": true})
	require.Error(t, err)
	require.Contains(t, err.Error(), "instruction_id_list")
	t.Logf("pre-flight error: %v", err)
}
