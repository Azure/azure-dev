// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
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
	"gopkg.in/yaml.v3"
)

func TestExplicitLocalComposedDefaultMappings(t *testing.T) {
	for _, tc := range []struct {
		name     string
		local    bool
		required string
		explicit map[string]string
		wantErr  string
	}{
		{name: "local optional defaults", local: true},
		{name: "nonlocal defaults unchanged"},
		{name: "required absent default rejected", local: true, required: "response", wantErr: "requires mapped inputs"},
		{name: "explicit absent default rejected", local: true,
			explicit: map[string]string{"response": "{{item.response}}"}, wantErr: "response"},
		{name: "explicit alias retained", local: true, explicit: map[string]string{"response": "{{item.query}}"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			group := project.Eval{Name: "composed", Evaluators: evalcore.EvaluatorList{{
				Evaluator: "builtin.valid", DataMapping: tc.explicit,
			}}}
			if tc.local {
				group.Source = &project.SourceDecl{Type: project.SourceTypeLocal, File: "rows.jsonl"}
			}
			body := `{"definition":{"data_schema":{"properties":{"query":{"type":"string"}}}}}`
			if tc.required != "" {
				body = `{"definition":{"data_schema":{"required":["` + tc.required + `"]}}}`
			}
			schema, err := evaluatorContract([]byte(body))
			require.NoError(t, err)
			request, err := buildEvalRequest(&group,
				map[string]*eval_api.EvaluatorSummary{"builtin.valid": schema}, map[string]bool{"query": true})
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			mapping := request.TestingCriteria[0].DataMapping
			assert.Equal(t, "{{item.query}}", mapping["query"])
			if !tc.local {
				assert.Equal(t, defaultCriterionMapping(project.EvaluationLevelTurn), mapping)
			} else if tc.explicit != nil {
				assert.Equal(t, "{{item.query}}", mapping["response"])
				assert.NotContains(t, mapping, "tool_calls")
			} else {
				assert.Equal(t, map[string]string{"query": "{{item.query}}"}, mapping)
			}
		})
	}
}

func TestExplicitLocalComposedPreparedColumnsAndPins(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid", true: "changed after validation"}[changed], func(t *testing.T) {
			ec, _, service, cfg, dir := validationFixture(t)
			cfg.Datasets = nil
			group := &cfg.Evals[0]
			group.Dataset = ""
			group.Source = &project.SourceDecl{Type: project.SourceTypeLocal, File: "rows.jsonl"}
			group.Evaluators[0].Version = "7"
			service.definition = strings.Replace(service.definition, `"version":"1"`, `"version":"7"`, 1)
			path := filepath.Join(dir, "rows.jsonl")
			require.NoError(t, os.WriteFile(path, []byte("{\"query\":\"local\"}\n"), 0o600))
			r := &evalReconciler{ec: ec}
			require.NoError(t, r.Validate(t.Context(), cfg, dir))
			assert.Equal(t, map[string]bool{"query": true}, r.prepared[group.Name].columns)
			assert.Equal(t, "7", r.prepared[group.Name].request.TestingCriteria[0].EvaluatorVersion)
			if changed {
				require.NoError(t, os.WriteFile(path, []byte("{\"other\":\"changed\"}\n"), 0o600))
			}

			_, _, err := r.EnsureEval(t.Context(), *group, path)
			if changed {
				require.Error(t, err)
				assert.Zero(t, service.createCount)
			} else {
				require.NoError(t, err)
				require.Len(t, service.createdRequests, 1)
				assert.Equal(t, "7", service.createdRequests[0].TestingCriteria[0].EvaluatorVersion)
			}
			assert.Contains(t, service.requests, "GET /evaluators/builtin.valid/versions/7")
			for _, request := range service.requests {
				assert.NotContains(t, request, "/datasets/")
			}
		})
	}
}

func TestExplicitLocalSelectedTypedContractOverridesLatest(t *testing.T) {
	ec, _, service, cfg, dir := validationFixture(t)
	cfg.Datasets = nil
	group := &cfg.Evals[0]
	group.Dataset = ""
	group.Source = &project.SourceDecl{Type: project.SourceTypeLocal, File: "rows.jsonl"}
	group.Evaluators[0].Version = "7"
	group.Evaluators[0].DataMapping = map[string]string{"query": "{{item.query}}", "count": "{{item.count}}"}
	latest, err := evaluatorContract([]byte(service.definition))
	require.NoError(t, err)
	ec.schemas = map[string]*eval_api.EvaluatorSummary{"builtin.valid": latest}
	service.definition = `{"definition":{"data_schema":` +
		`{"properties":{"query":{"type":"string"},"count":{"type":"integer"}},"required":["query","count"]}}}`
	path := filepath.Join(dir, "rows.jsonl")
	require.NoError(t, os.WriteFile(path, []byte("{\"query\":\"local\",\"count\":9007199254740993}\n"), 0o600))
	r := &evalReconciler{ec: ec}
	require.NoError(t, r.PreflightLocalEval(t.Context(), *group, path))
	require.NoError(t, r.Validate(t.Context(), cfg, dir))
	_, _, err = r.EnsureEval(t.Context(), *group, path)
	require.NoError(t, err)
	require.Len(t, service.createdRequests, 1)
	properties, ok := service.createdRequests[0].DataSourceConfig.ItemSchema["properties"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[string]any{"type": "integer"}, properties["count"])
	assert.Equal(t, "7", service.createdRequests[0].TestingCriteria[0].EvaluatorVersion)
	assert.Same(t, latest, ec.schemas["builtin.valid"], "pin resolution must not rewrite the latest catalog cache")
}

func TestExplicitLocalPublicationInvalidatesCatalog(t *testing.T) {
	dir, _ := localPublicationConfig(t, false, false)
	cfg, err := project.OpenEvalConfig(dir)
	require.NoError(t, err)
	ec, _, _ := localPublicationContext(t, false)
	r := &evalReconciler{ec: ec}
	require.NoError(t, r.Validate(t.Context(), cfg, dir))
	assert.Nil(t, ec.schemas, "preflight must not retain a pre-publication catalog")
	ec.schemas = map[string]*eval_api.EvaluatorSummary{"stale": {Name: "stale"}}
	_, changed, err := r.EnsureEvaluator(t.Context(), cfg.Evaluators[0], "")
	require.NoError(t, err)
	require.True(t, changed)
	assert.Nil(t, ec.schemas, "successful publication must invalidate earlier catalog reads")
}

func TestExplicitLocalRunInheritsCatalogPin(t *testing.T) {
	dir := localSourceConfig(t, "{\"query\":\"local\",\"count\":9007199254740993}\n", 0)
	cfg, err := project.OpenEvalConfig(dir)
	require.NoError(t, err)
	cfg.Evals[0].Evaluators[0].Evaluator = "custom.valid"
	cfg.Evals[0].Evaluators[0].DataMapping = map[string]string{"query": "{{item.query}}", "count": "{{item.count}}"}
	cfg.Evaluators = []project.EvaluatorDecl{{Name: "custom.valid", Version: "7"}}
	body, err := yaml.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "azure.eval.yaml"), body, 0o600))
	latest, err := evaluatorContract([]byte(`{"definition":{"data_schema":` +
		`{"properties":{"query":{"type":"string"},"count":{"type":"string"}},"required":["query","count"]}}}`))
	require.NoError(t, err)
	selected, err := evaluatorContract([]byte(`{"definition":{"data_schema":` +
		`{"properties":{"query":{"type":"string"},"count":{"type":"integer"}},"required":["query","count"]}}}`))
	require.NoError(t, err)
	ec, requests := localSourceContext(t, func(definition map[string]any) {
		definition["data_source_config"] = map[string]any{"type": "custom", "item_schema": map[string]any{
			"type": "object", "properties": map[string]any{
				"query": map[string]any{"type": "string"}, "count": map[string]any{"type": "integer"},
			},
		}}
	})
	ec.schemas = map[string]*eval_api.EvaluatorSummary{
		"custom.valid": latest, evaluatorSchemaKey("custom.valid", "7"): selected,
	}
	_, err = startLocalSource(t, ec, dir, "local-quality", nil)
	require.NoError(t, err)
	recorded := recordedIdentityRequests(requests)
	require.Len(t, recorded, 4)
	assert.Equal(t, "/evaluators/custom.valid/versions/7", recorded[0].path)
	assert.Equal(t, http.MethodPost, recorded[3].method)
	assert.Contains(t, string(recorded[3].body), `"count":9007199254740993`)
}

func TestExplicitLocalCatalogPinPreflightUsesSelectedContract(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		t.Run(caller, func(t *testing.T) {
			ec, _, service, cfg, dir := validationFixture(t)
			cfg.Datasets = nil
			group := &cfg.Evals[0]
			group.Dataset = ""
			group.Source = &project.SourceDecl{Type: project.SourceTypeLocal, File: "rows.jsonl"}
			group.Evaluators[0].Evaluator = "custom.valid"
			group.Evaluators[0].DataMapping = map[string]string{"query": "{{item.query}}", "count": "{{item.count}}"}
			cfg.Evaluators = []project.EvaluatorDecl{{Name: "custom.valid", Version: "7"}}
			latest, err := evaluatorContract([]byte(service.definition))
			require.NoError(t, err)
			ec.schemas = map[string]*eval_api.EvaluatorSummary{"custom.valid": latest}
			service.definition = `{"definition":{"data_schema":` +
				`{"properties":{"query":{"type":"string"},"count":{"type":"integer"}},"required":["query","count"]}}}`
			require.NoError(t, os.WriteFile(filepath.Join(dir, "rows.jsonl"),
				[]byte("{\"query\":\"local\",\"count\":42}\n"), 0o600))
			if caller == "create" {
				body, err := yaml.Marshal(cfg)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(dir, "azure.eval.yaml"), body, 0o600))
				require.NoError(t, runLocalCreate(t, ec, dir, group.Name))
			} else {
				_, err := deployValidationFixture(t, t.Context(), ec, cfg, dir)
				require.NoError(t, err)
			}
			require.Len(t, service.createdRequests, 1)
			assert.Equal(t, "7", service.createdRequests[0].TestingCriteria[0].EvaluatorVersion)
		})
	}
}
