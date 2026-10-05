// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaieval/internal/pkg/dataset_api"
	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluatorDriftFailsBeforeDatasetPublication(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		t.Run(caller, func(t *testing.T) {
			ec, env, service, cfg, dir := validationFixture(t)
			cfg.Evaluators = []project.EvaluatorDecl{{Name: "custom", Source: "custom.json"}}
			cfg.Evals[0].Evaluators = evalcore.EvaluatorList{{Evaluator: "custom"}}
			require.NoError(t, os.WriteFile(filepath.Join(dir, "custom.json"),
				[]byte(`{"type":"rubric","dimensions":[{"id":"a","weight":7}]}`), 0o600))
			env.state[project.FingerprintKey("evaluator", "custom")] = "prior-authored-digest"
			env.state[versionKey("evaluator", "custom")] = "1"
			before := maps.Clone(env.state)
			service.evaluatorVersion = "2"
			service.definition = `{"name":"custom","version":"2","definition":` +
				`{"type":"rubric","dimensions":[{"id":"a","weight":5}],"data_schema":{"properties":{}}}}`

			require.ErrorContains(t, reconcileArtifactConfig(t, caller, ec, cfg, dir), "is at version 2")
			for _, request := range service.requests {
				assert.True(t, strings.HasPrefix(request, "GET "), "unexpected mutation: %s", request)
			}
			assert.False(t, service.dataset)
			assert.Zero(t, service.createCount)
			assert.Empty(t, env.config)
			assert.Equal(t, before, env.state)
		})
	}
}

func TestRepinnedLocalDatasetValidatesSelectedRegisteredRows(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		for _, tc := range []struct {
			name             string
			rows             string
			metadataStatus   int
			credentialStatus int
			contentStatus    int
		}{
			{name: "missing required column", rows: `{"response":"answer"}`},
			{name: "missing later column", rows: "{\"query\":\"first\",\"response\":\"answer\"}\n{\"response\":\"second\"}"},
			{name: "malformed selected content", rows: "not JSON"},
			{name: "metadata denied", metadataStatus: http.StatusForbidden},
			{name: "credentials denied", credentialStatus: http.StatusForbidden},
			{name: "content denied", contentStatus: http.StatusForbidden},
		} {
			t.Run(caller+"/"+tc.name, func(t *testing.T) {
				ec, env, service, cfg, dir := validationFixture(t)
				digest, err := project.Fingerprint(filepath.Join(dir, "rows.jsonl"))
				require.NoError(t, err)
				env.state[project.FingerprintKey("dataset", "turn-tests")] = digest
				env.state[versionKey("dataset", "turn-tests")] = "1.0"
				before := maps.Clone(env.state)
				cfg.Datasets[0].Version = "2.0"
				service.dataset = true
				service.registeredRows = tc.rows
				service.datasetReadStatus = tc.metadataStatus
				service.credentialStatus, service.contentStatus = tc.credentialStatus, tc.contentStatus

				require.Error(t, reconcileArtifactConfig(t, caller, ec, cfg, dir))
				for _, request := range service.requests {
					assert.True(t, strings.HasPrefix(request, "GET ") ||
						request == "POST /datasets/turn-tests/versions/2.0/credentials",
						"only the selected content may be read, without publication: %s", request)
				}
				assert.Zero(t, service.createCount)
				assert.Empty(t, env.config)
				assert.Empty(t, env.values)
				assert.Equal(t, before, env.state)
			})
		}
	}
}

func TestRepinnedLocalDatasetPreservesPublicationBaseline(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		t.Run(caller, func(t *testing.T) {
			ec, env, service, cfg, dir := validationFixture(t)
			path := filepath.Join(dir, "rows.jsonl")
			digest, err := project.Fingerprint(path)
			require.NoError(t, err)
			env.state[project.FingerprintKey("dataset", "turn-tests")] = digest
			env.state[versionKey("dataset", "turn-tests")] = "1.0"
			cfg.Datasets[0].Version = "2.0"
			service.dataset = true
			service.registeredRows = `{"query":"selected remote row","response":"selected remote answer"}`
			for range 2 {
				ec.state = nil
				require.NoError(t, reconcileArtifactConfig(t, caller, ec, cfg, dir))
			}
			assert.Contains(t, service.requests, "POST /datasets/turn-tests/versions/2.0/credentials")
			assert.Equal(t, "1.0", env.stored(t, versionKey("dataset", "turn-tests")))
			assert.Equal(t, digest, env.stored(t, project.FingerprintKey("dataset", "turn-tests")))
			assert.Equal(t, 1, service.createCount)
			for _, request := range service.requests {
				assert.NotContains(t, request, "startPendingUpload")
			}
		})
	}
}

func TestSimulationExplicitGeneratedToolDefinitionsAreNotSeedColumns(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		t.Run(caller, func(t *testing.T) {
			ec, env, service, cfg, dir := validationFixture(t)
			group := runnableSimulation()
			group.Name, group.Dataset = cfg.Evals[0].Name, cfg.Datasets[0].Name
			group.Evaluators = evalcore.EvaluatorList{{
				Evaluator: "builtin.valid",
				DataMapping: map[string]string{
					"messages": "{{item.messages}}", "tool_definitions": "{{item.tool_definitions}}",
				},
			}}
			cfg.Evals[0] = *group
			service.definition = `{"definition":{"data_schema":{"properties":{"messages":{},"tool_definitions":{}}}}}`
			require.NoError(t, os.WriteFile(filepath.Join(dir, "rows.jsonl"),
				[]byte(`{"test_case_description":"A support question."}`), 0o600))
			require.NoError(t, reconcileArtifactConfig(t, caller, ec, cfg, dir))
			assert.Equal(t, 1, service.createCount)
			require.Len(t, service.createdRequests, 1)
			require.Len(t, service.createdRequests[0].TestingCriteria, 1)
			assert.Equal(t, group.Evaluators[0].DataMapping, service.createdRequests[0].TestingCriteria[0].DataMapping)
			assert.Equal(t, "1.0", env.stored(t, versionKey("dataset", "turn-tests")))
		})
	}

}

func TestSimulationDefaultInferenceRemainsMessagesOnly(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		t.Run(caller, func(t *testing.T) {
			ec, _, service, cfg, dir := validationFixture(t)
			group := runnableSimulation()
			group.Name, group.Dataset = cfg.Evals[0].Name, cfg.Datasets[0].Name
			group.Evaluators = evalcore.EvaluatorList{{Evaluator: "builtin.valid"}}
			cfg.Evals[0] = *group
			service.definition = `{"definition":{"data_schema":{"properties":{"messages":{},"tool_definitions":{}}}}}`
			require.NoError(t, os.WriteFile(filepath.Join(dir, "rows.jsonl"),
				[]byte(`{"test_case_description":"A support question."}`), 0o600))
			require.NoError(t, reconcileArtifactConfig(t, caller, ec, cfg, dir))
			require.Len(t, service.createdRequests, 1)
			request := service.createdRequests[0]
			require.Len(t, request.TestingCriteria, 1)
			assert.Equal(t, map[string]string{"messages": "{{item.messages}}"}, request.TestingCriteria[0].DataMapping)
			properties, ok := request.DataSourceConfig.ItemSchema["properties"].(map[string]any)
			require.True(t, ok)
			assert.NotContains(t, properties, "tool_definitions",
				"explicit-binding validation must not expand default schema")
			assert.False(t, request.DataSourceConfig.IncludeSampleSchema)
		})
	}
}

func TestOptionalDatasetToolColumnsAreRequiredOnlyWhenExplicit(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		for _, field := range []string{"tool_calls", "tool_definitions", "context", "ground_truth"} {
			for _, explicit := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/explicit=%t", caller, field, explicit), func(t *testing.T) {
					ec, env, service, cfg, dir := validationFixture(t)
					service.definition = `{"definition":{"data_schema":{"properties":{"query":{},` +
						fmt.Sprintf("%q", field) + `:{}}}}}`
					rows := fmt.Sprintf(
						"{\"query\":\"first\",\"response\":\"answer\",%q:[]}\n"+
							"{\"query\":\"second\",\"response\":\"answer\"}\n",
						field)
					require.NoError(t, os.WriteFile(filepath.Join(dir, "rows.jsonl"), []byte(rows), 0o600))
					if explicit {
						cfg.Evals[0].Evaluators[0].DataMapping = map[string]string{field: "{{item." + field + "}}"}
					}
					err := reconcileArtifactConfig(t, caller, ec, cfg, dir)
					if explicit {
						require.ErrorContains(t, err, field)
						assert.Zero(t, service.createCount)
						assert.False(t, service.dataset)
						assert.Empty(t, env.config)
						return
					}
					require.NoError(t, err)
					require.Len(t, service.createdRequests, 1)
					require.Len(t, service.createdRequests[0].TestingCriteria, 1)
					assert.Equal(t, map[string]string{
						"query": "{{item.query}}", "response": "{{item.response}}",
						"tool_calls": "{{item.tool_calls}}", "tool_definitions": "{{item.tool_definitions}}",
					},
						service.createdRequests[0].TestingCriteria[0].DataMapping,
						"optional defaults remain mapped without becoming required row inputs")
				})
			}
		}
	}
}

func TestValidatedRepinnedDatasetCannotBecomeAnUpload(t *testing.T) {
	ec, env, service, cfg, dir := validationFixture(t)
	path := filepath.Join(dir, cfg.Datasets[0].File)
	digest, err := project.Fingerprint(path)
	require.NoError(t, err)
	env.state[project.FingerprintKey("dataset", "turn-tests")] = digest
	env.state[versionKey("dataset", "turn-tests")] = "1.0"
	cfg.Datasets[0].Version = "2.0"
	service.dataset = true
	service.registeredRows = `{"query":"selected rows","response":"selected answer"}`
	r := &evalReconciler{ec: ec}
	require.NoError(t, r.Validate(t.Context(), cfg, dir))
	service.dataset = false
	_, changed, err := r.EnsureDataset(t.Context(), cfg.Datasets[0], path)
	require.Error(t, err)
	assert.False(t, changed)
	assert.Empty(t, env.config)
	for _, request := range service.requests {
		assert.NotContains(t, request, "startPendingUpload")
	}
}

func TestNewDatasetPinUsesLocalUploadInsteadOfRemoteContent(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		t.Run(caller, func(t *testing.T) {
			ec, env, service, cfg, dir := validationFixture(t)
			path := filepath.Join(dir, cfg.Datasets[0].File)
			digest, err := project.Fingerprint(path)
			require.NoError(t, err)
			env.state[project.FingerprintKey("dataset", "turn-tests")] = digest
			env.state[versionKey("dataset", "turn-tests")] = "1.0"
			cfg.Datasets[0].Version = "2.0"
			service.registeredRows = `invalid and must not be read`
			require.NoError(t, reconcileArtifactConfig(t, caller, ec, cfg, dir))
			assert.Contains(t, service.requests, "POST /datasets/turn-tests/versions/2.0/startPendingUpload")
			assert.NotContains(t, service.requests, "POST /datasets/turn-tests/versions/2.0/credentials")
			assert.Equal(t, "2.0", env.stored(t, versionKey("dataset", "turn-tests")))
			assert.Equal(t, digest, env.stored(t, project.FingerprintKey("dataset", "turn-tests")))
		})
	}
}

func TestDatasetFileChangedAfterValidationRequiresRetry(t *testing.T) {
	ec, env, service, cfg, dir := validationFixture(t)
	r := &evalReconciler{ec: ec}
	require.NoError(t, r.Validate(t.Context(), cfg, dir))
	path := filepath.Join(dir, cfg.Datasets[0].File)
	require.NoError(t, os.WriteFile(path, []byte(`{"response":"different content"}`), 0o600))
	_, changed, err := r.EnsureDataset(t.Context(), cfg.Datasets[0], path)
	require.ErrorContains(t, err, "changed after validation")
	assert.False(t, changed)
	assert.False(t, service.dataset)
	assert.Empty(t, env.config)
}

func TestEvaluatorDriftPreflightPreservesReuseAndUnrecordedPublication(t *testing.T) {
	for _, reuse := range []bool{false, true} {
		t.Run(fmt.Sprintf("reuse=%t", reuse), func(t *testing.T) {
			ec, env, service, cfg, dir := validationFixture(t)
			body := `{"type":"rubric","dimensions":[{"id":"a","weight":5}]}`
			cfg.Evaluators = []project.EvaluatorDecl{{Name: "custom", Source: "custom.json"}}
			cfg.Evals[0].Evaluators = evalcore.EvaluatorList{{Evaluator: "custom"}}
			path := filepath.Join(dir, "custom.json")
			require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
			service.evaluatorVersion = "2"
			service.definition = `{"name":"custom","version":"2","definition":` + body + `}`
			if reuse {
				digest, err := project.Fingerprint(path)
				require.NoError(t, err)
				env.state[project.FingerprintKey("evaluator", "custom")] = digest
				env.state[versionKey("evaluator", "custom")] = "1"
			} else {
				service.definition = `{"name":"custom","version":"2","definition":` +
					`{"type":"rubric","dimensions":[{"id":"a","weight":7}]}}`
			}
			require.NoError(t, (&evalReconciler{ec: ec}).Validate(t.Context(), cfg, dir))
			assert.Empty(t, env.config)
		})
	}
}

func TestUnpinnedLocalDatasetRequiresSuccessfulFallbackRead(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		for _, status := range []int{http.StatusForbidden, http.StatusGatewayTimeout, http.StatusNotFound} {
			t.Run(caller+"/"+http.StatusText(status), func(t *testing.T) {
				ec, env, service, cfg, dir := validationFixture(t)
				digest, err := project.Fingerprint(filepath.Join(dir, "rows.jsonl"))
				require.NoError(t, err)
				env.state[project.FingerprintKey("dataset", "turn-tests")] = digest
				env.state[versionKey("dataset", "turn-tests")] = "1.0"
				before := maps.Clone(env.state)
				service.dataset = true
				service.emptyDatasetListing = true
				service.datasetReadStatus = status

				err = reconcileArtifactConfig(t, caller, ec, cfg, dir)
				require.Error(t, err)
				if status == http.StatusNotFound {
					assert.Contains(t, err.Error(), `no dataset "turn-tests" at version "1.0"`)
				} else {
					assert.Contains(t, err.Error(), fmt.Sprint(status))
				}
				assert.Equal(t, []string{
					"GET /datasets/turn-tests/versions",
					"GET /datasets/turn-tests/versions/1.0",
				}, service.requests, "fallback failures must stop before uploads, tags, or evaluator publication")
				assert.Zero(t, service.createCount)
				assert.Empty(t, env.config)
				assert.Empty(t, env.values)
				assert.Equal(t, before, env.state)
			})
		}
	}
}

func TestUnpinnedLocalDatasetFallbackTimeoutStopsBeforeMutation(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		t.Run(caller, func(t *testing.T) {
			ec, env, service, cfg, dir := validationFixture(t)
			digest, err := project.Fingerprint(filepath.Join(dir, "rows.jsonl"))
			require.NoError(t, err)
			env.state[project.FingerprintKey("dataset", "turn-tests")] = digest
			env.state[versionKey("dataset", "turn-tests")] = "1.0"
			before := maps.Clone(env.state)
			transport := &datasetFallbackTimeoutTransport{}
			pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, &policy.ClientOptions{
				Transport: transport, Retry: policy.RetryOptions{MaxRetries: -1},
			})
			ec.datasetClient = dataset_api.NewDatasetClientFromPipeline("https://example.test", pipeline)

			require.ErrorIs(t, reconcileArtifactConfig(t, caller, ec, cfg, dir), context.DeadlineExceeded)
			assert.Equal(t, []string{
				"GET /datasets/turn-tests/versions",
				"GET /datasets/turn-tests/versions/1.0",
			}, transport.requests)
			assert.Empty(t, service.requests, "no evaluator or eval work follows a failed dataset fallback read")
			assert.Empty(t, env.config)
			assert.Empty(t, env.values)
			assert.Equal(t, before, env.state)
		})
	}
}

func TestUnpinnedLocalDatasetToleratesListingDelayAfterSuccessfulRead(t *testing.T) {
	for _, caller := range []string{"create", "up"} {
		t.Run(caller, func(t *testing.T) {
			ec, env, service, cfg, dir := validationFixture(t)
			digest, err := project.Fingerprint(filepath.Join(dir, "rows.jsonl"))
			require.NoError(t, err)
			env.state[project.FingerprintKey("dataset", "turn-tests")] = digest
			env.state[versionKey("dataset", "turn-tests")] = "1.0"
			service.dataset = true
			service.emptyDatasetListing = true

			require.NoError(t, reconcileArtifactConfig(t, caller, ec, cfg, dir))
			assert.Contains(t, service.requests, "GET /datasets/turn-tests/versions/1.0")
			for _, request := range service.requests {
				assert.NotContains(t, request, "startPendingUpload", "the unchanged dataset must not be uploaded again")
			}
			assert.Equal(t, 1, service.createCount)
			assert.Equal(t, "1.0", env.stored(t, versionKey("dataset", "turn-tests")))
			assert.Equal(t, digest, env.stored(t, project.FingerprintKey("dataset", "turn-tests")))
		})
	}
}

type datasetFallbackTimeoutTransport struct {
	requests []string
}

func (s *datasetFallbackTimeoutTransport) Do(request *http.Request) (*http.Response, error) {
	s.requests = append(s.requests, request.Method+" "+request.URL.Path)
	if strings.HasSuffix(request.URL.Path, "/versions") {
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{"value":[]}`)), Request: request,
		}, nil
	}
	return nil, context.DeadlineExceeded
}
