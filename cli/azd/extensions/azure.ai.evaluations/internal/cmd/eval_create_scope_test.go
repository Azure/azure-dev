// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// twoConfigFixture lays out two configurations, in different directories but
// one shared environment, that both declare a local-source eval named
// "quality". Two agents each scaffolding their own evals is an ordinary
// layout, not a reason to collide.
func twoConfigFixture(t *testing.T) (
	ec *evalContext, env *testEnvServer, service *catalogPinService,
	cfgA, cfgB *project.EvalConfig, pathA, pathB string,
) {
	t.Helper()
	root := t.TempDir()
	dirA := filepath.Join(root, "agent-a", "evals")
	dirB := filepath.Join(root, "agent-b", "evals")
	require.NoError(t, os.MkdirAll(dirA, 0o700))
	require.NoError(t, os.MkdirAll(dirB, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dirA, "rows.jsonl"), []byte("{\"query\":\"from a\"}\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dirB, "rows.jsonl"), []byte("{\"query\":\"from b\"}\n"), 0o600))
	pathA = filepath.Join(dirA, "azure.eval.yaml")
	pathB = filepath.Join(dirB, "azure.eval.yaml")

	newConfig := func() *project.EvalConfig {
		return &project.EvalConfig{
			Evaluators: []project.EvaluatorDecl{{Name: "custom", Version: "1"}},
			Evals: []project.Eval{{
				Name:       "quality",
				Source:     &project.SourceDecl{Type: project.SourceTypeLocal, File: "rows.jsonl"},
				Evaluators: evalcore.EvaluatorList{{Evaluator: "custom"}},
			}},
		}
	}
	cfgA, cfgB = newConfig(), newConfig()

	service = &catalogPinService{latest: "1", versions: map[string]json.RawMessage{
		"1": json.RawMessage(`{"name":"custom","version":"1","definition":{"data_schema":` +
			`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}}}`),
	}, evals: map[string]*eval_api.OpenAIEval{}}
	server := httptest.NewServer(service.serve(t))
	t.Cleanup(server.Close)
	env = &testEnvServer{}
	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	ec = &evalContext{
		azdClient: newTestAzdClient(t, env), envName: "test", rootKnown: true, root: root,
		evalClient: eval_api.NewEvalClientFromPipeline(server.URL, pipeline),
	}
	return ec, env, service, cfgA, cfgB, pathA, pathB
}

// createLocal runs `eval create` for one configuration with a fresh in-memory
// evalContext over the shared environment, the way every invocation of the
// command does, and reports the id the command settled on.
func createLocal(t *testing.T, ec *evalContext, env *testEnvServer, cfg *project.EvalConfig, path string) string {
	t.Helper()
	fresh := *ec
	fresh.azdClient = newTestAzdClient(t, env)
	fresh.state, fresh.schemas = nil, nil
	cmd := jsonCmd(t, "json")
	cmd.SetContext(t.Context())
	var out bytes.Buffer
	cmd.SetOut(&out)
	err := (&evalCreateAction{cmd: cmd}).create(&fresh, cfg, &cfg.Evals[0], path)
	require.NoError(t, err)
	var result map[string]string
	require.NoError(t, json.Unmarshal(out.Bytes(), &result))
	return result["id"]
}

// Two configurations naming their eval "quality" from different local files
// must settle on two different evals: a direct local-source `eval create`
// used to build the reconciler with an empty scope, so the second
// configuration's create overwrote the first's recorded id and local-request
// baseline outright. ADO root comment 4186518098.
func TestEvalCreateScopesLocalSourceByConfigurationPath(t *testing.T) {
	ec, env, service, cfgA, cfgB, pathA, pathB := twoConfigFixture(t)

	idA := createLocal(t, ec, env, cfgA, pathA)
	idB := createLocal(t, ec, env, cfgB, pathB)

	require.NotEmpty(t, idA)
	require.NotEmpty(t, idB)
	assert.NotEqual(t, idA, idB, "two configurations must not collapse onto one eval")
	assert.Equal(t, 2, len(service.created), "each configuration's local content creates its own eval")

	// The unqualified key an unscoped project has always used still belongs to
	// whichever configuration got there first, and the second configuration's
	// id is recorded beside it under a key of its own.
	base := idKey("eval", "quality")
	assert.Equal(t, idA, env.stored(t, base),
		"the first configuration keeps the unqualified key")
	scopeB := project.EvalScope(ec.root, pathB)
	assert.Equal(t, idB, env.stored(t, base+"_"+project.EvalScopeTag(scopeB)),
		"the second configuration's id is recorded under its own scoped key, not overwriting the first's")
}

// Recreating either configuration afterward must be a no-op against its own
// history, and must not be disturbed by the other configuration's deploys in
// between -- the "immutable histories" half of ADO root comment 4186518098.
func TestEvalCreateHistoryIsImmutableAcrossConfigurations(t *testing.T) {
	ec, env, service, cfgA, cfgB, pathA, pathB := twoConfigFixture(t)

	idA := createLocal(t, ec, env, cfgA, pathA)
	idB := createLocal(t, ec, env, cfgB, pathB)
	require.Equal(t, 2, len(service.created))

	// Recreate A, then B, then A again: neither disturbs the other's id or
	// forces an unrelated recreation.
	for range 3 {
		assert.Equal(t, idA, createLocal(t, ec, env, cfgA, pathA),
			"configuration A's id must survive configuration B's deploys")
		assert.Equal(t, idB, createLocal(t, ec, env, cfgB, pathB),
			"configuration B's id must survive configuration A's deploys")
	}
	assert.Equal(t, 2, len(service.created), "no configuration ever forces a recreation of the other's eval")
}

// Two configurations that declare the same eval name but differ in an
// ordinary immutable field (here, evaluation level, not the local file
// content the two tests above exercise) must each keep their own identity
// digest baseline. Scoping only the id and local-request fingerprint left the
// definition baseline itself keyed unscoped: the second configuration's
// create overwrote the first's recorded baseline, so redeploying the first
// afterward read the second's baseline back, saw its own declaration as
// "changed", and recreated an eval that never actually changed. ADO root
// comment 4188353666.
func TestEvalCreateDefinitionBaselineIsScopedAcrossConfigurations(t *testing.T) {
	ec, env, service, cfgA, cfgB, pathA, pathB := twoConfigFixture(t)
	// An ordinary immutable field, not excluded from the digest like name or
	// description, so the two configurations' declarations hash differently.
	cfgB.Evals[0].EvaluationLevel = project.EvaluationLevelTurn

	idA := createLocal(t, ec, env, cfgA, pathA)
	idB := createLocal(t, ec, env, cfgB, pathB)
	require.NotEmpty(t, idA)
	require.NotEmpty(t, idB)
	assert.NotEqual(t, idA, idB)
	require.Equal(t, 2, len(service.created))

	// Redeploying either configuration afterward, alternating with the other,
	// must stay a no-op against its own history: the other configuration's
	// deploy must not have overwritten the definition baseline this one's
	// `decide` compares its own declaration against.
	for range 3 {
		assert.Equal(t, idA, createLocal(t, ec, env, cfgA, pathA),
			"configuration A's id and history must survive configuration B's deploys despite differing evaluation levels")
		assert.Equal(t, idB, createLocal(t, ec, env, cfgB, pathB),
			"configuration B's id and history must survive configuration A's deploys despite differing evaluation levels")
	}
	assert.Equal(t, 2, len(service.created),
		"neither configuration's definition baseline is overwritten by the other's, so neither is ever recreated")
}
