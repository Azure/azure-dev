// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

type catalogPinService struct {
	mu      sync.Mutex
	latest  string
	created []eval_api.CreateOpenAIEvalRequest
	evals   map[string]*eval_api.OpenAIEval
}

func (s *catalogPinService) serve(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/evaluators/"):
			if strings.HasSuffix(r.URL.Path, "/versions") {
				assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
					"value": []map[string]string{{"name": "custom", "version": s.latest}},
				}))
			} else {
				version := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
				assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
					"name": "custom", "version": version,
					"definition": map[string]any{"data_schema": map[string]any{"properties": map[string]any{}}},
				}))
			}
		case r.Method == http.MethodPost && r.URL.Path == "/openai/v1/evals":
			var request eval_api.CreateOpenAIEvalRequest
			if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&request)) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			s.created = append(s.created, request)
			id := fmt.Sprintf("eval_%d", len(s.created))
			eval := &eval_api.OpenAIEval{
				ID: id, Name: request.Name, Metadata: request.Metadata, TestingCriteria: request.TestingCriteria,
			}
			s.evals[id] = eval
			assert.NoError(t, json.NewEncoder(w).Encode(eval))
		case strings.HasPrefix(r.URL.Path, "/openai/v1/evals/"):
			id := strings.TrimPrefix(r.URL.Path, "/openai/v1/evals/")
			eval, ok := s.evals[id]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if r.Method == http.MethodPost {
				var update eval_api.UpdateOpenAIEvalRequest
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&update))
				eval.Name, eval.Metadata = update.Name, update.Metadata
			} else {
				assert.Equal(t, http.MethodGet, r.Method)
			}
			assert.NoError(t, json.NewEncoder(w).Encode(eval))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	}
}

func newCatalogPinFixture(t *testing.T) (*evalContext, *testEnvServer, *catalogPinService, *project.EvalConfig, string) {
	t.Helper()
	dir := t.TempDir()
	service := &catalogPinService{latest: "2", evals: map[string]*eval_api.OpenAIEval{}}
	server := httptest.NewServer(service.serve(t))
	t.Cleanup(server.Close)
	env := &testEnvServer{}
	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	ec := &evalContext{
		azdClient: newTestAzdClient(t, env), envName: "test", rootKnown: true, root: dir,
		evalClient: eval_api.NewEvalClientFromPipeline(server.URL, pipeline),
	}
	cfg := &project.EvalConfig{
		Evaluators: []project.EvaluatorDecl{{Name: "custom", Version: "1"}},
		Evals: []project.Eval{{
			Name: "quality", Source: &project.SourceDecl{Type: "traces", AgentName: "agent"},
			Evaluators: evalcore.EvaluatorList{{Evaluator: "custom"}},
		}},
	}
	return ec, env, service, cfg, dir
}

func reconcileCatalogPin(
	t *testing.T, caller string, ec *evalContext, cfg *project.EvalConfig, dir string,
) string {
	t.Helper()
	// Each invocation starts with a new command context but the same persisted environment.
	fresh := *ec
	fresh.state = nil
	fresh.schemas = nil
	if caller == "up" {
		_, err := deployValidationFixture(t, t.Context(), &fresh, cfg, dir)
		require.NoError(t, err)
		return fresh.privateValue(t.Context(), idKey("eval", cfg.Evals[0].Name))
	}
	var out bytes.Buffer
	cmd := jsonCmd(t, "json")
	cmd.SetContext(t.Context())
	cmd.SetOut(&out)
	err := (&evalCreateAction{cmd: cmd}).create(&fresh, cfg, &cfg.Evals[0], filepath.Join(dir, "azure.eval.yaml"))
	require.NoError(t, err)
	var result map[string]string
	require.NoError(t, json.Unmarshal(out.Bytes(), &result))
	return result["id"]
}

func TestCatalogEvaluatorPinChangesRecreateTheStoredCriteria(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		for _, included := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/included=%t", caller, included), func(t *testing.T) {
				ec, env, service, cfg, dir := newCatalogPinFixture(t)
				var ids []string
				for _, pin := range []string{"1", "2", ""} {
					cfg.Evaluators[0].Version = pin
					current := cfg
					if included {
						current = catalogPinFromInclude(t, cfg, dir)
					}
					id := reconcileCatalogPin(t, caller, ec, current, dir)
					ids = append(ids, id)
					assert.Equal(t, id, reconcileCatalogPin(t, caller, ec, current, dir), "unchanged retry must reuse")
					require.Len(t, service.evals[id].TestingCriteria, 1)
					assert.Equal(t, pin, service.evals[id].TestingCriteria[0].EvaluatorVersion,
						"the actual immutable criterion must follow the authored catalog pin")
					assert.Empty(t, current.Evals[0].Evaluators[0].Version, "resolving must not mutate authored refs")
				}
				assert.NotEqual(t, ids[0], ids[1], "changing catalog pin must recreate")
				assert.NotEqual(t, ids[1], ids[2], "removing catalog pin must recreate")
				require.Len(t, service.created, 3)
				assert.Equal(t, []string{"1", "2", ""}, []string{
					service.created[0].TestingCriteria[0].EvaluatorVersion,
					service.created[1].TestingCriteria[0].EvaluatorVersion,
					service.created[2].TestingCriteria[0].EvaluatorVersion,
				})
				assert.NotEmpty(t, env.stored(t, project.FingerprintKey("eval", "quality")))
			})
		}
	}
}

func catalogPinFromInclude(t *testing.T, cfg *project.EvalConfig, dir string) *project.EvalConfig {
	t.Helper()
	nested := filepath.Join(dir, "shared", "catalog")
	require.NoError(t, os.MkdirAll(nested, 0o700))
	catalog, err := yaml.Marshal(cfg.Evaluators[0])
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(nested, "custom.yaml"), catalog, 0o600))
	raw, err := yaml.Marshal(map[string]any{
		"evaluators": []map[string]string{{"$ref": "./shared/catalog/custom.yaml"}},
		"evals":      cfg.Evals,
	})
	require.NoError(t, err)
	path := filepath.Join(dir, "azure.eval.yaml")
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	loaded, err := project.LoadEvalConfig(path)
	require.NoError(t, err)
	return loaded
}

func TestExplicitEvaluatorPinWinsAndUnpinnedLatestDoesNotRecreate(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		t.Run(caller, func(t *testing.T) {
			ec, _, service, cfg, dir := newCatalogPinFixture(t)
			cfg.Evals[0].Evaluators[0].Version = "1"
			first := reconcileCatalogPin(t, caller, ec, cfg, dir)
			cfg.Evaluators[0].Version = "2"
			assert.Equal(t, first, reconcileCatalogPin(t, caller, ec, cfg, dir))
			cfg.Evaluators[0].Version = ""
			assert.Equal(t, first, reconcileCatalogPin(t, caller, ec, cfg, dir))
			assert.Equal(t, "1", service.evals[first].TestingCriteria[0].EvaluatorVersion)

			cfg.Evals[0].Evaluators[0].Version = ""
			unpinned := reconcileCatalogPin(t, caller, ec, cfg, dir)
			assert.NotEqual(t, first, unpinned)
			service.latest = "3"
			assert.Equal(t, unpinned, reconcileCatalogPin(t, caller, ec, cfg, dir))
			assert.Empty(t, service.evals[unpinned].TestingCriteria[0].EvaluatorVersion)
			assert.Len(t, service.created, 2, "a new remote latest version must not split eval history")
		})
	}
}

func TestTargetedCreateReservesSiblingUsingItsCatalogPin(t *testing.T) {
	ec, _, service, cfg, dir := newCatalogPinFixture(t)
	ownerID := reconcileCatalogPin(t, "create", ec, cfg, dir)
	owner := cfg.Evals[0]
	newcomer := owner
	newcomer.Name = "second"
	newcomer.Evaluators = evalcore.EvaluatorList{{Evaluator: "custom", Version: "1"}}
	cfg.Evals = []project.Eval{newcomer, owner}
	newID := reconcileCatalogPin(t, "create", ec, cfg, dir)
	assert.NotEqual(t, ownerID, newID, "a sibling's effective pin must not be mistaken for an abandoned eval")
	assert.Equal(t, "quality", service.evals[ownerID].Name)
	assert.Equal(t, newID, reconcileCatalogPin(t, "create", ec, cfg, dir))
	assert.Len(t, service.created, 2)
}

func TestCatalogPinsRepairLegacyUnpinnedFingerprint(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		for _, pin := range []string{"1", "2", ""} {
			t.Run(caller+"/pin="+pin, func(t *testing.T) {
				ec, env, service, cfg, dir := newCatalogPinFixture(t)
				first := reconcileCatalogPin(t, caller, ec, cfg, dir)
				legacyDigest, err := project.FingerprintGroup(cfg.Evals[0])
				require.NoError(t, err)
				legacyDefinition, err := project.FingerprintDefinition(cfg.Evals[0])
				require.NoError(t, err)
				// Earlier lifecycle builds sent pin 1 but fingerprinted an empty reference.
				env.config[privateStatePath], err = json.Marshal(map[string]string{
					project.FingerprintKey("eval", "quality"): fingerprintEra + legacyDefinition,
					idKey("eval", "quality"):                  first,
					digestIDKey(legacyDigest):                 first,
				})
				require.NoError(t, err)
				cfg.Evaluators[0].Version = pin
				id := reconcileCatalogPin(t, caller, ec, cfg, dir)
				if pin == "1" {
					assert.Equal(t, first, id, "unchanged inherited pins must not fork history during migration")
				} else {
					assert.NotEqual(t, first, id)
				}
				assert.Equal(t, pin, service.evals[id].TestingCriteria[0].EvaluatorVersion)
				assert.Equal(t, id, reconcileCatalogPin(t, caller, ec, cfg, dir))
			})
		}
	}
}

func TestCatalogPinDecisionAndReservationUseThePreparedPolicy(t *testing.T) {
	ec, _, _, cfg, dir := newCatalogPinFixture(t)
	first := reconcileCatalogPin(t, "create", ec, cfg, dir)
	cfg.Evaluators[0].Version = "2"
	reconciler := &evalReconciler{ec: ec}
	require.NoError(t, reconciler.Validate(t.Context(), cfg, dir))
	reconciler.ReserveDeclared(t.Context(), cfg.Evals)
	assert.Empty(t, reconciler.claimedBy[first], "the eval being recreated must release its old reservation")
	decision, err := reconciler.decide(t.Context(), cfg.Evals[0])
	require.NoError(t, err)
	require.True(t, decision.recreate)
	effective := cfg.Evals[0]
	effective.Evaluators = evalcore.EvaluatorList{{Evaluator: "custom", Version: "2"}}
	digest, err := project.FingerprintGroup(effective)
	require.NoError(t, err)
	assert.Equal(t, digest, decision.digest)
	assert.Equal(t, "2", reconciler.prepared["quality"].request.TestingCriteria[0].EvaluatorVersion)
}

func TestCatalogPinRenameKeepsTheSameEffectivePolicy(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		t.Run(caller, func(t *testing.T) {
			ec, _, service, cfg, dir := newCatalogPinFixture(t)
			first := reconcileCatalogPin(t, caller, ec, cfg, dir)
			cfg.Evals[0].Name = "renamed"
			// Moving a pin from catalog to reference does not change what it requests.
			cfg.Evals[0].Evaluators[0].Version = "1"
			cfg.Evaluators[0].Version = ""
			assert.Equal(t, first, reconcileCatalogPin(t, caller, ec, cfg, dir))
			assert.Equal(t, "renamed", service.evals[first].Name)
			assert.Len(t, service.created, 1)
		})
	}
}

func TestStoredLatestPolicyIsNotAResolvedVersionChange(t *testing.T) {
	ec, _, service, cfg, dir := newCatalogPinFixture(t)
	cfg.Evaluators[0].Version = ""
	first := reconcileCatalogPin(t, "create", ec, cfg, dir)
	service.evals[first].TestingCriteria[0].EvaluatorVersion = "latest"
	service.latest = "9"
	assert.Equal(t, first, reconcileCatalogPin(t, "create", ec, cfg, dir))
	assert.Len(t, service.created, 1)
}

func seedLegacyCatalogPinState(t *testing.T, env *testEnvServer, group project.Eval, id string) {
	t.Helper()
	digest, err := project.FingerprintGroup(group)
	require.NoError(t, err)
	definition, err := project.FingerprintDefinition(group)
	require.NoError(t, err)
	env.config[privateStatePath], err = json.Marshal(map[string]string{
		project.FingerprintKey("eval", group.Name): fingerprintEra + definition,
		idKey("eval", group.Name):                  id,
		digestIDKey(digest):                        id,
	})
	require.NoError(t, err)
}

func TestLegacyCatalogPinRenameBeforeMigration(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		for _, pin := range []string{"1", "2", ""} {
			t.Run(caller+"/pin="+pin, func(t *testing.T) {
				ec, env, service, cfg, dir := newCatalogPinFixture(t)
				first := reconcileCatalogPin(t, caller, ec, cfg, dir)
				seedLegacyCatalogPinState(t, env, cfg.Evals[0], first)
				cfg.Evals[0].Name = "renamed-before-migration"
				cfg.Evaluators[0].Version = pin
				id := reconcileCatalogPin(t, caller, ec, cfg, dir)
				if pin == "1" {
					assert.Equal(t, first, id, "a rename with the same inherited pin must keep its pre-fix history")
					assert.Len(t, service.created, 1)
				} else {
					assert.NotEqual(t, first, id, "a legacy digest does not prove the stored pin is still requested")
					assert.Len(t, service.created, 2)
					assert.Equal(t, "quality", service.evals[first].Name, "do not rename a rejected legacy candidate")
				}
				assert.Equal(t, pin, service.evals[id].TestingCriteria[0].EvaluatorVersion)
				assert.Equal(t, id, reconcileCatalogPin(t, caller, ec, cfg, dir), "the new digest must be idempotent")
			})
		}
	}
}

func TestLegacyCatalogPinFallbackDoesNotStealSibling(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		t.Run(caller, func(t *testing.T) {
			ec, env, service, cfg, dir := newCatalogPinFixture(t)
			first := reconcileCatalogPin(t, caller, ec, cfg, dir)
			owner := cfg.Evals[0]
			// An explicit pin spelling makes the authored declarations distinct,
			// but both requested criterion pin 1 before the fingerprint fix.
			cfg.Evals[0].Evaluators = evalcore.EvaluatorList{{Evaluator: "custom", Version: "1"}}
			newcomer := owner
			newcomer.Name = "second"
			cfg.Evals = append([]project.Eval{newcomer}, cfg.Evals...)
			seedLegacyCatalogPinState(t, env, cfg.Evals[1], first)
			legacyDigest, err := project.FingerprintGroup(owner)
			require.NoError(t, err)
			var state map[string]string
			require.NoError(t, json.Unmarshal(env.config[privateStatePath], &state))
			state[digestIDKey(legacyDigest)] = first
			env.config[privateStatePath], err = json.Marshal(state)
			require.NoError(t, err)
			id := reconcileCatalogPin(t, caller, ec, cfg, dir)
			assert.NotEqual(t, first, id)
			assert.Equal(t, "quality", service.evals[first].Name)
			assert.Equal(t, "1", service.evals[first].TestingCriteria[0].EvaluatorVersion)
			assert.Equal(t, id, reconcileCatalogPin(t, caller, ec, cfg, dir))
			assert.Len(t, service.created, 2, "the existing owner must keep its own history")
		})
	}
}

func TestLegacyCatalogPinFallbackDoesNotStealUnselectedInheritedSibling(t *testing.T) {
	ec, env, service, cfg, dir := newCatalogPinFixture(t)
	first := reconcileCatalogPin(t, "create", ec, cfg, dir)
	owner := cfg.Evals[0]
	seedLegacyCatalogPinState(t, env, owner, first)
	newcomer := owner
	newcomer.Name = "second"
	cfg.Evals = []project.Eval{newcomer, owner}
	id := reconcileCatalogPin(t, "create", ec, cfg, dir)
	assert.NotEqual(t, first, id)
	assert.Equal(t, "quality", service.evals[first].Name)
	assert.Len(t, service.created, 2)
}

func TestLegacyCatalogPinFallbackRequiresCompleteStoredPinEvidence(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		for _, unavailable := range []string{"criteria", "evaluator", "version"} {
			t.Run(caller+"/"+unavailable, func(t *testing.T) {
				ec, env, service, cfg, dir := newCatalogPinFixture(t)
				first := reconcileCatalogPin(t, caller, ec, cfg, dir)
				seedLegacyCatalogPinState(t, env, cfg.Evals[0], first)
				switch unavailable {
				case "criteria":
					service.evals[first].TestingCriteria = nil
				case "evaluator":
					service.evals[first].TestingCriteria[0].EvaluatorName = "different"
				case "version":
					service.evals[first].TestingCriteria[0].EvaluatorVersion = ""
				}
				cfg.Evals[0].Name = "renamed-before-migration"
				id := reconcileCatalogPin(t, caller, ec, cfg, dir)
				assert.NotEqual(t, first, id, "an incomplete old definition cannot prove that its pin matches")
				assert.Equal(t, "quality", service.evals[first].Name, "rejected candidates must not be renamed")
				assert.Equal(t, "1", service.evals[id].TestingCriteria[0].EvaluatorVersion)
			})
		}
	}
}

func TestLegacyCatalogPinFallbackOnlyWhenEffectiveIndexIsMissing(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		t.Run(caller, func(t *testing.T) {
			ec, env, service, cfg, dir := newCatalogPinFixture(t)
			first := reconcileCatalogPin(t, caller, ec, cfg, dir)
			seedLegacyCatalogPinState(t, env, cfg.Evals[0], first)
			cfg.Evals[0].Name = "renamed-before-migration"
			effective, err := project.FingerprintGroup(withCatalogEvaluatorPins(cfg.Evals[0], cfg))
			require.NoError(t, err)
			var state map[string]string
			require.NoError(t, json.Unmarshal(env.config[privateStatePath], &state))
			// A newer index whose resource disappeared must not resurrect a
			// different history through the still-live older index.
			state[digestIDKey(effective)] = "eval_deleted"
			env.config[privateStatePath], err = json.Marshal(state)
			require.NoError(t, err)
			id := reconcileCatalogPin(t, caller, ec, cfg, dir)
			assert.NotEqual(t, first, id)
			assert.Equal(t, "quality", service.evals[first].Name)
		})
	}
}
