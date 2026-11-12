// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func invalidExactRubrics() []struct{ name, definition, field string } {
	return []struct{ name, definition, field string }{
		{"omitted dimensions", `{"type":"rubric"}`, "dimensions"},
		{"null type", `{"type":null,"dimensions":[]}`, "definition.type"},
		{"null type null dimensions", `{"type":null,"dimensions":null}`, "definition.type"},
		{"empty type", `{"type":"","dimensions":[]}`, "definition.type"},
		{"space type", `{"type":"   ","dimensions":[]}`, "definition.type"},
		{"escaped whitespace type", `{"type":" \t\r\n ","dimensions":[]}`, "definition.type"},
		{"unicode whitespace type", `{"type":"\u00a0\u2003","dimensions":[]}`, "definition.type"},
		{"number type", `{"type":42,"dimensions":[]}`, "definition.type"},
		{"threshold above one", `{"type":"rubric","dimensions":[],"pass_threshold":1.0000000000000001}`, "pass_threshold"},
		{"negative tiny threshold", `{"type":"rubric","dimensions":[],"pass_threshold":-1e-400}`, "pass_threshold"},
		{"rounded weight above one", `{"dimensions":[{"weight":1.0000000000000001}]}`, ".weight"},
		{"rounded weight below one", `{"dimensions":[{"weight":0.99999999999999999}]}`, ".weight"},
		{"rounded weight above ten", `{"dimensions":[{"weight":10.0000000000000001}]}`, ".weight"},
		{"scientific fraction", `{"dimensions":[{"weight":10000000000000001e-16}]}`, ".weight"},
		{"scientific below one", `{"dimensions":[{"weight":99999999999999999e-17}]}`, ".weight"},
		{"quoted numeric weight", `{"dimensions":[{"weight":"1"}]}`, ".weight"},
	}
}

func TestEvaluatorDefinitionKindPreservesNamedTypes(t *testing.T) {
	kind, err := evaluatorDefinitionKind(nil)
	require.NoError(t, err)
	assert.Empty(t, kind, "absence retains compatibility defaulting")
	for _, original := range []string{"rubric", "prompt", "custom_kind", " custom_kind ", " rubric "} {
		t.Run(original, func(t *testing.T) {
			raw, err := json.Marshal(original)
			require.NoError(t, err)
			kind, err := evaluatorDefinitionKind(raw)
			require.NoError(t, err)
			assert.Equal(t, original, kind, "validation must not trim or reclassify a nonblank named kind")
		})
	}
}

func TestRubricExactValidationRejectsInvalidInputBeforeWriting(t *testing.T) {
	t.Setenv("AZD_SERVER", "")
	for _, tc := range invalidExactRubrics() {
		for _, verb := range []string{"create", "update"} {
			for _, fullDocument := range []bool{false, true} {
				name := tc.name + "/" + verb
				raw := tc.definition
				if fullDocument {
					name += "/document"
					raw = `{"definition":` + raw + `}`
				}
				t.Run(name, func(t *testing.T) {
					path := writeEvaluatorFile(t, raw)
					cmd := newEvaluatorWriteCommand(verb, "")
					cmd.SetContext(t.Context())
					var out bytes.Buffer
					cmd.SetOut(&out)
					action := &evaluatorWriteAction{
						cmd: cmd, verb: verb, name: "quality", flags: &evaluatorWriteFlags{fromFile: path},
					}
					require.ErrorContains(t, action.Run(), tc.field,
						"invalid input must fail before attempting the unavailable host connection")
					assert.Empty(t, out.String())
					after, err := os.ReadFile(path)
					require.NoError(t, err)
					assert.Equal(t, raw, string(after))
				})
			}
		}
	}
}

func TestRubricExactValidationPreservesArtifactAndCatalog(t *testing.T) {
	for _, tc := range invalidExactRubrics() {
		for _, caller := range []string{"download", "job show", "job show preserved", "generate"} {
			t.Run(tc.name+"/"+caller, func(t *testing.T) {
				job := &eval_api.GenerationJob{
					ID: "evaluator-job", Status: "succeeded",
					Result: json.RawMessage(`{"name":"quality","version":"3","definition":` + tc.definition + `}`),
				}
				dir := t.TempDir()
				var ec *evalContext
				var plans []generationPlan
				if caller == "generate" {
					ec, plans, dir, _ = generationRecoveryFixture(t, job)
				}
				path := filepath.Join(dir, "evaluators", "quality.json")
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
				original := []byte(`{"type":"rubric","dimensions":[{"id":"keep-local-edit"}]}`)
				require.NoError(t, os.WriteFile(path, original, 0o600))
				configPath := filepath.Join(dir, "azure.eval.yaml")
				catalog := []byte("evaluators:\n  - name: quality\n    source: ./evaluators/quality.json\n")
				require.NoError(t, os.WriteFile(configPath, catalog, 0o600))
				cmd := jsonCmd(t, "json")
				cmd.SetContext(t.Context())
				var out bytes.Buffer
				cmd.SetOut(&out)
				var err error
				switch caller {
				case "download":
					action := &evaluatorDownloadAction{cmd: cmd, name: "quality", version: "3", outFile: path, force: true}
					err = action.download(t.Context(), evaluatorServing(t, []string{"3"}, string(job.Result)))
				case "job show", "job show preserved":
					action := &jobShowAction{cmd: cmd, flags: &jobFlags{path: dir, force: caller == "job show"}}
					ref, collectErr := action.collect(t.Context(), &evalContext{}, evaluatorJobs, job, &out)
					err = collectErr
					assert.Nil(t, ref)
				case "generate":
					plans[1].ReplaceApproved = true
					err = ec.runGenerations(cmd, plans[1:], generateFlags{path: dir})
				}
				require.ErrorContains(t, err, tc.field)
				after, err := os.ReadFile(path)
				require.NoError(t, err)
				assert.Equal(t, original, after)
				after, err = os.ReadFile(configPath)
				require.NoError(t, err)
				assert.Equal(t, catalog, after)
				if caller == "generate" {
					var result map[string]generationResult
					require.NoError(t, json.Unmarshal(out.Bytes(), &result))
					assert.Equal(t, "failed", result["evaluator"].Status)
					assert.Contains(t, result["evaluator"].Error, tc.field)
					assert.Nil(t, result["evaluator"].ArtifactRef)
				} else {
					assert.Empty(t, out.String())
				}
			})
		}
	}
}

func TestRubricExactValidationStopsCreateAndUpBeforeMutation(t *testing.T) {
	for _, tc := range invalidExactRubrics() {
		for _, caller := range []string{"create", "up"} {
			t.Run(tc.name+"/"+caller, func(t *testing.T) {
				ec, env, service, cfg, dir := validationFixture(t)
				service.status = http.StatusForbidden
				cfg.Evaluators = []project.EvaluatorDecl{{Name: "custom", Source: "custom.json"}}
				cfg.Evals[0].Evaluators = evalcore.EvaluatorList{{Evaluator: "custom"}}
				require.NoError(t, os.WriteFile(filepath.Join(dir, "custom.json"), []byte(tc.definition), 0o600))
				require.ErrorContains(t, reconcileArtifactConfig(t, caller, ec, cfg, dir), tc.field)
				assert.Empty(t, service.requests)
				assert.Empty(t, env.config)
				assert.Empty(t, env.values)
			})
		}
	}
}

func TestRubricExactValidationAcceptsEquivalentNumberForms(t *testing.T) {
	for _, tc := range []struct{ weight, threshold string }{
		{"1", "0"}, {"10", "1"}, {"5.000", "-0"}, {"1e1", "1e-400"},
		{"50e-1", "0.99999999999999999"}, {"0.1e1", "1.0000000000000000"},
		{"10000000000000000e-16", "1E+0"},
	} {
		t.Run(tc.weight+"/"+tc.threshold, func(t *testing.T) {
			definition := `{"type":"rubric","dimensions":[{"id":"a","weight":` + tc.weight +
				`}],"pass_threshold":` + tc.threshold + `}`
			validated, err := validateRubricDefinition(json.RawMessage(definition))
			require.NoError(t, err)
			assert.Equal(t, definition, string(validated), "validation must not change authored digest bytes")
			projected, err := editableRubric(json.RawMessage(definition))
			require.NoError(t, err)
			require.NotNil(t, projected)
			assert.Contains(t, string(projected), `"weight": `+tc.weight)
			assert.Contains(t, string(projected), `"pass_threshold": `+tc.threshold)
		})
	}
}

func TestRubricExactValidationCompatibilityAcrossCallers(t *testing.T) {
	for _, tc := range []struct{ name, definition, kind string }{
		{"omitted", `{"dimensions":[{"id":"a","weight":5e0}],"pass_threshold":0.99999999999999999}`, "rubric"},
		{"rubric", `{"type":"rubric","dimensions":[],"pass_threshold":1e-400}`, "rubric"},
		{
			"prompt",
			`{"type":"prompt","dimensions":null,"prompt_text":"Authored prompt","pass_threshold":"separate"}`,
			"prompt",
		},
		{"unknown", `{"type":"custom_kind","dimensions":[],"pass_threshold":2,"future":9007199254740993}`, "custom_kind"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := json.RawMessage(`{"name":"custom","version":"3","definition":` + tc.definition + `}`)
			ec := evaluatorServing(t, []string{"3"}, string(raw))
			dir := t.TempDir()
			path := filepath.Join(dir, "download.json")
			cmd := evaluatorDownloadCmd(t)
			cmd.SetOut(io.Discard)
			require.NoError(t, (&evaluatorDownloadAction{
				cmd: cmd, name: "custom", version: "3", outFile: path,
			}).download(t.Context(), ec))
			downloaded, err := os.ReadFile(path)
			require.NoError(t, err)
			job := &eval_api.GenerationJob{ID: "evaluator-job", Status: "succeeded", Result: raw}
			for _, force := range []bool{false, true} {
				action := &jobShowAction{cmd: cmd, flags: &jobFlags{path: dir, force: force}}
				ref, err := action.collect(t.Context(), &evalContext{}, evaluatorJobs, job, io.Discard)
				require.NoError(t, err)
				require.NotNil(t, ref)
				collected, err := os.ReadFile(project.ResolveSource(dir, ref.Source))
				require.NoError(t, err)
				require.JSONEq(t, string(downloaded), string(collected))
			}
			generationContext, plans, generationDir, _ := generationRecoveryFixture(t, job)
			require.NoError(t, generationContext.runGenerations(cmd, plans[1:], generateFlags{path: generationDir}))
			generated, err := os.ReadFile(filepath.Join(generationDir, "evaluators", "quality.json"))
			require.NoError(t, err)
			require.JSONEq(t, string(downloaded), string(generated))
			for _, verb := range []string{"create", "update"} {
				writeContext, _, service, _, _ := newCatalogPinFixture(t)
				if verb == "create" {
					service.latest = ""
				}
				body, err := normalizeRubricBody("custom", []byte(`{"definition":`+tc.definition+`}`))
				require.NoError(t, err)
				action := &evaluatorWriteAction{cmd: cmd, name: "custom", verb: verb}
				require.NoError(t, action.write(t.Context(), writeContext, body))
				require.Equal(t, 1, service.publishes)
				var written struct {
					Definition map[string]json.RawMessage `json:"definition"`
				}
				require.NoError(t, json.Unmarshal(service.versions[service.latest], &written))
				assert.Equal(t, json.RawMessage(`"`+tc.kind+`"`), written.Definition["type"])
				if tc.name == "omitted" {
					assert.Equal(t, json.RawMessage(`0.99999999999999999`), written.Definition["pass_threshold"])
					assert.Contains(t, string(written.Definition["dimensions"]), "5e0")
				}
			}
			if tc.kind != "rubric" {
				assert.True(t, strings.Contains(string(downloaded), `"definition"`),
					"non-rubric documents keep their separate format")
			}
		})
	}
}
