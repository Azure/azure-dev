// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const deletedEvalID = "eval_delete_target"

type evalDeleteService struct {
	mu          sync.Mutex
	status      int
	deleteID    string
	named       []eval_api.OpenAIEval
	requests    []string
	afterDelete func()
}

func evalDeleteFixture(t *testing.T, state map[string]string) (*evalContext, *testEnvServer, *evalDeleteService) {
	t.Helper()
	service := &evalDeleteService{
		status:   http.StatusNoContent,
		deleteID: deletedEvalID,
		named:    []eval_api.OpenAIEval{{ID: deletedEvalID, Name: "quality"}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		service.mu.Lock()
		defer service.mu.Unlock()
		service.requests = append(service.requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/"+service.deleteID):
			if service.afterDelete != nil {
				service.afterDelete()
			}
			w.WriteHeader(service.status)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/evals"):
			assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{"data": service.named, "has_more": false}))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)
	env := &testEnvServer{state: state}
	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	ec := &evalContext{
		evalClient: eval_api.NewEvalClientFromPipeline(server.URL, pipeline),
		azdClient:  newTestAzdClient(t, env),
		envName:    "test",
		rootKnown:  true,
		root:       t.TempDir(),
	}
	return ec, env, service
}

func runEvalDelete(t *testing.T, ec *evalContext, name string) (string, string, error) {
	t.Helper()
	cmd := newEvalDeleteCommand()
	cmd.Flags().String("output", "json", "")
	var out, stderr bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{name, "--force"})
	// Only connection construction is injected; exercise the command's actual
	// argument parsing, deletion, name fallback, cleanup, and output path.
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		force, err := cmd.Flags().GetBool("force")
		require.NoError(t, err)
		action := &evalDeleteAction{cmd: cmd, flags: &evalDeleteFlags{force: force}, evalID: args[0]}
		return action.delete(cmd.Context(), ec)
	}
	err := cmd.ExecuteContext(t.Context())
	return out.String(), stderr.String(), err
}

func evalDeleteStateFixture() (map[string]string, []string, map[string]string) {
	alias := project.FingerprintKey("eval", "alias")
	quality := project.FingerprintKey("eval", "quality")
	digest := digestIDKey(strings.Repeat("a", 64))
	suffix := "_" + project.EvalScopeTag(scopeB)
	removed := map[string]string{
		alias + "_ID":                           deletedEvalID,
		alias:                                   "alias-definition",
		alias + "_ID" + project.EvalScopeSuffix: scopeA,
		alias + project.EvalScopeSuffix:         scopeA,
		quality + "_ID" + suffix:                deletedEvalID,
		quality + suffix:                        "deleted-definition",
		digest + suffix:                         deletedEvalID,
		idKey("evalrun", deletedEvalID):         "run_deleted",
	}
	preserved := map[string]string{
		quality + "_ID": "eval_other",
		quality:         "other-definition",
		quality + "_ID" + project.EvalScopeSuffix:   scopeA,
		quality + project.EvalScopeSuffix:           scopeA,
		digest:                                      "eval_other",
		digest + project.EvalScopeSuffix:            scopeA,
		versionKey("dataset", "shared"):             "1.0",
		versionKey("evaluator", "shared"):           "2",
		project.FingerprintKey("dataset", "shared"): deletedEvalID,
		idKey("evalrun", "eval_other"):              "run_other",
		"unrelated":                                 deletedEvalID,
	}
	state := map[string]string{}
	var keys []string
	for key, value := range removed {
		state[key] = value
		keys = append(keys, key)
	}
	maps.Copy(state, preserved)
	return state, keys, preserved
}

func TestEvalDeleteClearsOnlyConfirmedIDReferences(t *testing.T) {
	for _, name := range []string{deletedEvalID, "quality"} {
		for _, status := range []int{http.StatusOK, http.StatusNoContent} {
			t.Run(name+"/"+http.StatusText(status), func(t *testing.T) {
				state, removed, preserved := evalDeleteStateFixture()
				ec, env, service := evalDeleteFixture(t, state)
				service.status = status
				out, stderr, err := runEvalDelete(t, ec, name)
				require.NoError(t, err)
				assert.Empty(t, stderr)
				require.JSONEq(t, `{"id":"eval_delete_target","status":"deleted"}`, out)
				fresh := reader(t, env)
				for _, key := range removed {
					assert.Empty(t, fresh.privateValue(t.Context(), key), key)
				}
				for key, value := range preserved {
					assert.Equal(t, value, fresh.privateValue(t.Context(), key), key)
				}
				if name == deletedEvalID {
					assert.Equal(t, []string{"DELETE /openai/v1/evals/" + deletedEvalID}, service.requests)
				} else {
					assert.Equal(t, []string{
						"DELETE /openai/v1/evals/quality",
						"GET /openai/v1/evals",
						"DELETE /openai/v1/evals/" + deletedEvalID,
					}, service.requests)
				}
			})
		}
	}
}

func TestEvalDeleteKeepsSurvivingScopedLookupReachable(t *testing.T) {
	base := project.FingerprintKey("eval", "quality")
	digest := digestIDKey(strings.Repeat("b", 64))
	suffix := "_" + project.EvalScopeTag(scopeB)
	state := map[string]string{
		base + "_ID":                           deletedEvalID,
		base:                                   "shared-definition",
		base + "_ID" + project.EvalScopeSuffix: scopeA,
		base + project.EvalScopeSuffix:         scopeA,
		base + "_ID" + suffix:                  "eval_survivor",
		base + suffix:                          "surviving-definition",
		digest:                                 deletedEvalID,
		digest + project.EvalScopeSuffix:       scopeA,
		digest + suffix:                        "eval_survivor",
	}
	ec, env, service := evalDeleteFixture(t, state)
	_, _, err := runEvalDelete(t, ec, deletedEvalID)
	require.NoError(t, err)
	fresh := reader(t, env)
	assert.Empty(t, fresh.privateValue(t.Context(), base+"_ID"))
	assert.Equal(t, "shared-definition", fresh.privateValue(t.Context(), base))
	assert.Empty(t, fresh.privateValue(t.Context(), digest))
	assert.Equal(t, "eval_survivor", fresh.scopedValue(t.Context(), base+"_ID", scopeB))
	assert.Equal(t, "surviving-definition", fresh.scopedValue(t.Context(), base, scopeB))
	assert.Equal(t, "eval_survivor", fresh.scopedValue(t.Context(), digest, scopeB))
	service.deleteID = "eval_survivor"
	_, _, err = runEvalDelete(t, ec, service.deleteID)
	require.NoError(t, err)
	final := reader(t, env)
	assert.Empty(t, final.privateValue(t.Context(), base))
	assert.Empty(t, final.privateValue(t.Context(), base+project.EvalScopeSuffix))
	assert.Empty(t, final.privateValue(t.Context(), base+"_ID"+project.EvalScopeSuffix))
	assert.Empty(t, final.privateValue(t.Context(), digest+project.EvalScopeSuffix))
}

func TestEvalDeleteFailureOrAmbiguousNamePreservesState(t *testing.T) {
	for _, scenario := range []string{"server failure", "not found", "duplicate names"} {
		t.Run(scenario, func(t *testing.T) {
			state, _, _ := evalDeleteStateFixture()
			ec, env, service := evalDeleteFixture(t, state)
			name := deletedEvalID
			switch scenario {
			case "server failure":
				service.status = http.StatusInternalServerError
			case "not found":
				service.status = http.StatusNotFound
				service.named = nil
			case "duplicate names":
				name = "quality"
				service.named = append(service.named, eval_api.OpenAIEval{ID: "eval_other", Name: "quality"})
			}
			_, _, err := runEvalDelete(t, ec, name)
			require.Error(t, err)
			assert.Empty(t, env.config, "failed deletion must not write private state")
			assert.Equal(t, state, reader(t, env).loadPrivateState(t.Context()))
			if scenario == "duplicate names" {
				assert.Equal(t, []string{"DELETE /openai/v1/evals/quality", "GET /openai/v1/evals"}, service.requests)
			}
		})
	}
}

func TestEvalDeleteRereadsStateBeforeRemovingReferences(t *testing.T) {
	base := project.FingerprintKey("eval", "quality")
	ec, env, service := evalDeleteFixture(t, map[string]string{base + "_ID": deletedEvalID, base: "old-definition"})
	ec.loadPrivateState(t.Context())
	other := *ec
	other.state = nil
	service.afterDelete = func() {
		assert.NoError(t, other.setPrivate(t.Context(), base+"_ID", "eval_replacement"))
		assert.NoError(t, other.setPrivate(t.Context(), base, "new-definition"))
	}
	_, _, err := runEvalDelete(t, ec, deletedEvalID)
	require.NoError(t, err)
	fresh := reader(t, env)
	assert.Equal(t, "eval_replacement", fresh.privateValue(t.Context(), base+"_ID"))
	assert.Equal(t, "new-definition", fresh.privateValue(t.Context(), base))
}

func TestEvalDeleteWarnsOnLocalCleanupFailureWithoutCorruptingJSON(t *testing.T) {
	state, _, _ := evalDeleteStateFixture()
	ec, env, _ := evalDeleteFixture(t, state)
	env.failSetConfig = true
	out, stderr, err := runEvalDelete(t, ec, deletedEvalID)
	require.NoError(t, err, "the remote deletion succeeded")
	require.JSONEq(t, `{"id":"eval_delete_target","status":"deleted"}`, out)
	assert.Contains(t, stderr, "warning:")
	assert.Contains(t, stderr, "config store unavailable")
	assert.Empty(t, env.config)
}

func TestEvalDeletePreservesSurvivingScopesDefinitionHistory(t *testing.T) {
	ec, env, service, cfg, dir := newCatalogPinFixture(t)
	group := cfg.Evals[0]
	ensure := func(scope string, threshold int) string {
		t.Helper()
		fresh := *ec
		fresh.state = nil
		fresh.schemas = nil
		group.Evaluators[0].InitializationParameters = map[string]any{"threshold": threshold}
		current := *cfg
		current.Evals = []project.Eval{group}
		r := &evalReconciler{ec: &fresh, scope: scope}
		require.NoError(t, r.Validate(t.Context(), &current, dir))
		r.ReserveDeclared(t.Context(), current.Evals)
		id, _, err := r.EnsureEval(t.Context(), group, "")
		require.NoError(t, err)
		return id
	}
	first := ensure(scopeA, 1)
	second := ensure(scopeB, 2)
	require.NotEqual(t, first, second)
	fingerprint := project.FingerprintKey("eval", group.Name)
	baseline := env.stored(t, fingerprint)
	require.NotEmpty(t, baseline, "production stores one unqualified definition baseline for the name")

	_, _, err := runEvalDelete(t, ec, first)
	require.NoError(t, err)
	assert.Equal(t, baseline, env.stored(t, fingerprint))
	assert.Equal(t, second, reader(t, env).scopedValue(t.Context(), idKey("eval", group.Name), scopeB))

	edited := ensure(scopeB, 3)
	assert.NotEqual(t, second, edited, "a surviving scope's non-pin edit must create new immutable criteria")
	assert.Equal(t, float64(3), service.evals[edited].TestingCriteria[0].InitializationParameters["threshold"])
}
