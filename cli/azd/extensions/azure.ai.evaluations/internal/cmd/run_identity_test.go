// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"azureaieval/internal/exterrors"
	"azureaieval/internal/pkg/dataset_api"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func unregisteredRunContext(t *testing.T) *evalContext {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	return evalContextFor(srv)
}

type identityRequest struct {
	method string
	path   string
	body   []byte
}

type identityService struct {
	versions     []dataset_api.Dataset
	listStatus   int
	listBody     *string
	getStatus    int
	getStatuses  map[string]int
	id           string
	version      string
	wantVersion  string
	rows         string
	blobStatus   int
	previous     []*eval_api.OpenAIEvalRun
	runMetadata  json.RawMessage
	responseEval bool
	evalConfig   map[string]any
	evalStatus   int
}

func identityRunContext(t *testing.T, service identityService) (*evalContext, <-chan identityRequest) {
	t.Helper()
	requests := make(chan identityRequest, 50)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		requests <- identityRequest{r.Method, r.URL.Path, body}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/runs"):
			if r.Method == http.MethodGet {
				assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{"data": service.previous}))
			} else {
				assert.Equal(t, http.MethodPost, r.Method)
				result := map[string]any{"id": "evalrun_new", "status": "queued"}
				if service.runMetadata != nil {
					result["metadata"] = service.runMetadata
				}
				assert.NoError(t, json.NewEncoder(w).Encode(result))
			}
		case strings.HasSuffix(r.URL.Path, "/eval_1"):
			assert.Equal(t, http.MethodGet, r.Method)
			if service.evalStatus != 0 {
				w.WriteHeader(service.evalStatus)
				return
			}
			config := service.evalConfig
			if service.responseEval {
				config = map[string]any{"type": "azure_ai_source", "scenario": "responses"}
			}
			assert.NoError(t, json.NewEncoder(w).Encode(eval_api.OpenAIEval{ID: "eval_1", DataSourceConfig: config}))
		case strings.HasSuffix(r.URL.Path, "/versions"):
			if service.listStatus != 0 {
				w.WriteHeader(service.listStatus)
				return
			}
			if service.listBody != nil {
				_, _ = io.WriteString(w, *service.listBody)
				return
			}
			versions := service.versions
			if versions == nil {
				versions = []dataset_api.Dataset{}
			}
			assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{"value": versions}))
		case strings.HasSuffix(r.URL.Path, "/credentials"):
			if service.wantVersion != "" {
				assert.True(t, strings.HasSuffix(r.URL.Path, "/versions/"+service.wantVersion+"/credentials"))
			}
			assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{"sas_uri": srv.URL + "/rows.jsonl"}))
		case r.URL.Path == "/rows.jsonl":
			if service.blobStatus != 0 {
				w.WriteHeader(service.blobStatus)
				return
			}
			_, _ = io.WriteString(w, service.rows)
		case strings.Contains(r.URL.Path, "/versions/"):
			if service.wantVersion != "" {
				assert.True(t, strings.HasSuffix(r.URL.Path, "/versions/"+service.wantVersion))
			}
			getStatus := service.getStatus
			if status, ok := service.getStatuses[filepath.Base(r.URL.Path)]; ok {
				getStatus = status
			}
			if getStatus != 0 && getStatus != http.StatusOK {
				w.WriteHeader(getStatus)
				return
			}
			version := service.version
			if version == "" {
				version = filepath.Base(r.URL.Path)
			}
			assert.NoError(t, json.NewEncoder(w).Encode(dataset_api.Dataset{
				Name: "golden", Version: version, ID: service.id,
			}))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return evalContextFor(srv), requests
}

func recordedIdentityRequests(requests <-chan identityRequest) []identityRequest {
	var recorded []identityRequest
	for {
		select {
		case request := <-requests:
			recorded = append(recorded, request)
		default:
			return recorded
		}
	}
}

func submitIdentitySource(
	t *testing.T, ec *evalContext, source *eval_api.EvalRunDataSource, version, level string,
) {
	t.Helper()
	metadata := map[string]string{}
	if version != "" {
		metadata[metaDatasetVersion] = version
	}
	_, err := ec.evalClient.CreateOpenAIEvalRun(t.Context(), "eval_1", &eval_api.CreateOpenAIEvalRunRequest{
		Name: "identity", DataSource: source, EvaluationLevel: level, Metadata: metadata,
	})
	require.NoError(t, err)
}

func identityPostedSource(t *testing.T, requests <-chan identityRequest) map[string]any {
	t.Helper()
	for _, request := range recordedIdentityRequests(requests) {
		if request.method == http.MethodPost && strings.HasSuffix(request.path, "/runs") {
			var body map[string]any
			require.NoError(t, json.Unmarshal(request.body, &body))
			source, ok := body["data_source"].(map[string]any)
			require.True(t, ok)
			return source
		}
	}
	t.Fatal("no run was submitted")
	return nil
}

func TestRegisteredRunIdentityAndVersion(t *testing.T) {
	for _, target := range []string{"static", "agent", "model", "simulation"} {
		for _, tc := range []struct {
			name     string
			file     string
			pin      string
			recorded string
			want     string
		}{
			{name: "latest", want: "3"},
			{name: "recorded", recorded: "2", want: "2"},
			{name: "pinned", pin: "1", recorded: "2", want: "1"},
			{name: "published local file", file: "golden.jsonl", recorded: "2", want: "2"},
			{name: "local file with pin", file: "golden.jsonl", pin: "1", recorded: "2", want: "1"},
			{name: "local file published elsewhere", file: "golden.jsonl", want: "3"},
		} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				const issuedID = "opaque-service-issued-version-id"
				rows := oneRow + oneRow
				if target == "static" {
					rows = `{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"hello"}]}` + "\n"
				}
				group := &project.Eval{Name: "quality", Dataset: "golden"}
				if target == "simulation" {
					group = runnableSimulation()
					group.Dataset = "golden"
					rows = seedRows
				} else if target != "static" {
					group.Target = &project.Target{Type: target, Name: "target"}
				}
				ec, requests := identityRunContext(t, identityService{
					versions:    []dataset_api.Dataset{{Version: "1"}, {Version: "3"}, {Version: "2"}},
					id:          issuedID,
					rows:        rows,
					wantVersion: tc.want,
				})
				ec.state = map[string]string{versionKey("dataset", "golden"): tc.recorded}
				config := writeCatalog(t, tc.file, tc.pin)
				if tc.file != "" {
					require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(config), tc.file), []byte("not JSON"), 0o600))
				}
				ds, version, err := ec.buildRunDataSource(t.Context(), group, config, 0)
				require.NoError(t, err, "published rows, not the retained local file, must be read")
				assert.Equal(t, tc.want, version)
				submitIdentitySource(t, ec, ds, version, group.EvaluationLevel)
				var body map[string]any
				listReads, versionReads := 0, 0
				for _, req := range recordedIdentityRequests(requests) {
					if strings.HasSuffix(req.path, "/runs") {
						require.NoError(t, json.Unmarshal(req.body, &body))
					}
					if strings.HasSuffix(req.path, "/versions") {
						listReads++
					}
					if strings.HasSuffix(req.path, "/versions/"+tc.want) {
						versionReads++
					}
				}
				require.NotNil(t, body)
				assert.Equal(t, map[string]any{metaDatasetVersion: tc.want}, body["metadata"])
				assert.Equal(t, 1, versionReads)
				wantLists := 0
				if tc.pin == "" && tc.recorded == "" {
					wantLists = 1
				}
				assert.Equal(t, wantLists, listReads, "no independent metadata resolution")
				posted, ok := body["data_source"].(map[string]any)
				require.True(t, ok)
				source, ok := posted["source"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, map[string]any{"type": "file_id", "id": issuedID}, source)
				switch target {
				case "simulation":
					assert.Equal(t, string(eval_api.EvalRunDataSourceTypeUserConversationSimulation), posted["type"])
					assert.Equal(t, map[string]any{
						"test_case_description":    "test_case_description",
						"simulation_configuration": "simulation_configuration",
					}, posted["data_mapping"])
				case "static":
					assert.Equal(t, "jsonl", posted["type"])
					assert.NotContains(t, posted, "target")
					assert.NotContains(t, posted, "input_messages")
				default:
					assert.Equal(t, "azure_ai_target_completions", posted["type"])
					assert.Contains(t, posted, "target")
				}
				if target != "simulation" {
					assert.NotContains(t, posted, "data_mapping")
				}
			})
		}
	}
}

func TestRegisteredRunRejectsEffectiveCaps(t *testing.T) {
	for _, file := range []string{"", "golden.jsonl"} {
		for _, flag := range []bool{false, true} {
			t.Run(file+map[bool]string{false: "/config", true: "/flag"}[flag], func(t *testing.T) {
				ec, requests := identityRunContext(t, identityService{id: "issued", rows: oneRow})
				group := &project.Eval{Name: "quality", Dataset: "golden", MaxSamples: 2}
				cmd := buildRunCommand("start", "")
				if flag {
					require.NoError(t, cmd.Flags().Set("max-samples", "1"))
				}
				flagValue, err := cmd.Flags().GetInt("max-samples")
				require.NoError(t, err)
				cap, err := runMaxSamples(cmd, flagValue, group)
				require.NoError(t, err)
				ds, _, err := ec.buildRunDataSource(t.Context(), group, writeCatalog(t, file, "1"), cap)
				require.Error(t, err)
				assert.Nil(t, ds)
				local, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				assert.Equal(t, exterrors.CodeConflictingArguments, local.Code)
				assert.Contains(t, local.Message, "maxSamples")
				assert.Contains(t, local.Suggestion, "publish a smaller dataset")
				assert.Empty(t, recordedIdentityRequests(requests), "cap refusal must not read rows or create anything")
			})
		}
	}
}

func TestExplicitZeroDisablesConfiguredRunCap(t *testing.T) {
	cmd := buildRunCommand("start", "")
	require.NoError(t, cmd.Flags().Set("max-samples", "0"))
	group := &project.Eval{Name: "quality", Dataset: "golden", MaxSamples: 10}
	cap, err := runMaxSamples(cmd, 0, group)
	require.NoError(t, err)
	assert.Zero(t, cap)
	ec, requests := identityRunContext(t, identityService{id: "issued", rows: oneRow})
	ds, version, err := ec.buildRunDataSource(t.Context(), group, writeCatalog(t, "golden.jsonl", "1"), cap)
	require.NoError(t, err)
	submitIdentitySource(t, ec, ds, version, "")
	source := identityPostedSource(t, requests)
	assert.Equal(t, map[string]any{"type": "file_id", "id": "issued"}, source["source"])
}

func TestRegisteredRunIdentityFailuresNeverFallBack(t *testing.T) {
	for _, tc := range []struct {
		name    string
		service identityService
		want    string
		status  int
	}{
		{name: "missing id", service: identityService{rows: oneRow}, want: "registered id"},
		{name: "blank id", service: identityService{id: "  ", rows: oneRow}, want: "registered id"},
		{name: "wrong version", service: identityService{id: "issued", version: "9"}, want: "instead"},
		{name: "deleted version", service: identityService{getStatus: 404}, want: "version 1", status: 404},
		{name: "forbidden version", service: identityService{getStatus: 403}, want: "version 1", status: 403},
		{name: "service failure", service: identityService{getStatus: 503}, want: "version 1", status: 503},
		{name: "unreadable rows", service: identityService{id: "issued", blobStatus: 403}, want: "version 1"},
		{name: "invalid rows", service: identityService{id: "issued", rows: "not JSON"}, want: "version 1"},
		{name: "empty rows", service: identityService{id: "issued"}, want: "no rows"},
	} {
		for _, file := range []string{"", "golden.jsonl"} {
			t.Run(tc.name+"/"+file, func(t *testing.T) {
				ec, requests := identityRunContext(t, tc.service)
				ds, _, err := ec.buildRunDataSource(t.Context(), &project.Eval{Name: "quality", Dataset: "golden"},
					writeCatalog(t, file, "1"), 0)
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.want)
				assert.Nil(t, ds)
				if tc.status == http.StatusForbidden {
					authErr, ok := errors.AsType[*azdext.LocalError](err)
					require.True(t, ok)
					assert.Equal(t, exterrors.CodeAuthFailed, authErr.Code)
					assert.Contains(t, authErr.Message, "HTTP 403")
				} else if tc.status != 0 {
					serviceErr, ok := errors.AsType[*azcore.ResponseError](err)
					require.True(t, ok, "lookup error must remain in the chain")
					assert.Equal(t, tc.status, serviceErr.StatusCode)
				}
				for _, request := range recordedIdentityRequests(requests) {
					assert.True(t, request.method == http.MethodGet || strings.HasSuffix(request.path, "/credentials"),
						"identity failure must never submit or publish: %s %s", request.method, request.path)
				}
			})
		}
	}
}

func TestRunRegistryLookupMustEstablishAbsence(t *testing.T) {
	for _, tc := range []struct {
		name        string
		listStatus  int
		getStatus   int
		wantErr     bool
		wantVersion string
	}{
		{name: "confirmed absent", listStatus: 404, getStatus: 404},
		{name: "successful empty listing with absent probes", getStatus: 404, wantErr: true},
		{name: "listing forbidden", listStatus: 403, getStatus: 404, wantErr: true},
		{name: "probe forbidden", listStatus: 404, getStatus: 403, wantErr: true},
		{name: "publication ahead of listing", listStatus: 404, wantVersion: "1.0"},
		{name: "publication ahead of successful empty listing", wantVersion: "1.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ec, requests := identityRunContext(t, identityService{
				listStatus: tc.listStatus, getStatus: tc.getStatus, id: "issued", rows: oneRow,
			})
			ds, version, err := ec.buildRunDataSource(t.Context(), &project.Eval{Name: "quality", Dataset: "golden"},
				writeCatalog(t, "golden.jsonl", ""), 0)
			if tc.wantErr {
				require.Error(t, err)
				assert.Nil(t, ds)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantVersion, version)
			submitIdentitySource(t, ec, ds, version, "")
			posted := identityPostedSource(t, requests)
			source, ok := posted["source"].(map[string]any)
			require.True(t, ok)
			if tc.wantVersion == "" {
				assert.Equal(t, "file_content", source["type"])
				assert.NotContains(t, source, "id")
			} else {
				assert.Equal(t, map[string]any{"type": "file_id", "id": "issued"}, source)
			}
		})
	}
}

func TestRunRejectsUnreadablePublicationState(t *testing.T) {
	ec, requests := identityRunContext(t, identityService{id: "issued", rows: oneRow})
	stateErr := errors.New("publication state unavailable")
	ec.state = map[string]string{}
	ec.stateErr = stateErr
	ds, _, err := ec.buildRunDataSource(t.Context(), &project.Eval{Name: "quality", Dataset: "golden"},
		writeCatalog(t, "golden.jsonl", ""), 0)
	require.ErrorIs(t, err, stateErr)
	assert.Nil(t, ds)
	assert.Empty(t, recordedIdentityRequests(requests), "failed state lookup must not turn into latest or local data")
}

func TestRunWithoutAnEnvironmentStillResolvesRegisteredVersion(t *testing.T) {
	ec, requests := identityRunContext(t, identityService{
		versions: []dataset_api.Dataset{{Version: "3"}}, id: "issued", rows: oneRow,
	})
	ec.state = map[string]string{}
	ec.stateErr = status.Error(codes.Unknown, "no project exists; to create a new project, run `azd init`")
	require.True(t, isNoDefaultEnvironmentError(ec.stateErr), "use the host's actual absence sentinel")
	ds, version, err := ec.buildRunDataSource(t.Context(), &project.Eval{Name: "quality", Dataset: "golden"},
		writeCatalog(t, "", ""), 0)
	require.NoError(t, err)
	assert.Equal(t, "3", version)
	submitIdentitySource(t, ec, ds, version, "")
	assert.Equal(t, map[string]any{"type": "file_id", "id": "issued"},
		identityPostedSource(t, requests)["source"])
}

func TestLocalUnregisteredCapOnTheWire(t *testing.T) {
	for _, limit := range []int{0, 2} {
		ec, requests := identityRunContext(t, identityService{listStatus: 404, getStatus: 404})
		config := writeCatalog(t, "golden.jsonl", "")
		require.NoError(t, os.WriteFile(
			filepath.Join(filepath.Dir(config), "golden.jsonl"), []byte(oneRow+oneRow+oneRow), 0o600))
		ds, version, err := ec.buildRunDataSource(t.Context(),
			&project.Eval{Name: "quality", Dataset: "golden"}, config, limit)
		require.NoError(t, err)
		assert.Empty(t, version)
		submitIdentitySource(t, ec, ds, version, "")
		source, ok := identityPostedSource(t, requests)["source"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "file_content", source["type"])
		want := limit
		if want == 0 {
			want = 3
		}
		assert.Len(t, source["content"], want)
		assert.NotContains(t, source, "id")
	}
}

func TestRunDatasetConfigurationErrorsDoNotDropPins(t *testing.T) {
	ec, requests := identityRunContext(t, identityService{id: "issued", rows: oneRow})
	config := writeCatalog(t, "", "1")
	require.NoError(t, os.WriteFile(config, []byte("datasets: ["), 0o600))
	ds, _, err := ec.buildRunDataSource(t.Context(), &project.Eval{Name: "quality", Dataset: "golden"}, config, 0)
	require.Error(t, err)
	assert.Nil(t, ds)
	assert.Empty(t, recordedIdentityRequests(requests))
}

func TestRegisteredAgentRunValidatesPublishedRowsInsteadOfLocalFile(t *testing.T) {
	ec, requests := identityRunContext(t, identityService{id: "issued", rows: seedRows})
	ds, _, err := ec.buildRunDataSource(t.Context(), &project.Eval{
		Name: "quality", Dataset: "golden", Target: &project.Target{Type: project.TargetTypeAgent, Name: "agent"},
	}, writeCatalog(t, "golden.jsonl", "1"), 0)
	require.ErrorContains(t, err, `"query"`)
	assert.Nil(t, ds)
	for _, request := range recordedIdentityRequests(requests) {
		assert.False(t, strings.HasSuffix(request.path, "/runs"))
	}
}

func TestRunPinnedVersionDoesNotRequireLocalFileToMatch(t *testing.T) {
	config := writeCatalog(t, "golden.jsonl", "1")
	cfg, err := project.LoadEvalConfig(config)
	require.NoError(t, err)
	ec := &evalContext{state: map[string]string{
		project.FingerprintKey("dataset", "golden"): "different published file",
		versionKey("dataset", "golden"):             "2",
	}}
	require.NoError(t, ec.checkDatasetRegistered(t.Context(), cfg,
		&project.Eval{Name: "quality", Dataset: "golden"}, config))
}

func TestRunReportsUnreadableLocalDriftCheck(t *testing.T) {
	config := writeCatalog(t, "golden.jsonl", "")
	cfg, err := project.LoadEvalConfig(config)
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(filepath.Dir(config), "golden.jsonl")))
	ec := &evalContext{state: map[string]string{
		project.FingerprintKey("dataset", "golden"): "published file digest",
		versionKey("dataset", "golden"):             "2",
	}}
	err = ec.checkDatasetRegistered(t.Context(), cfg,
		&project.Eval{Name: "quality", Dataset: "golden"}, config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "golden.jsonl")
}

func TestRunRerunPreservesRegisteredIdentity(t *testing.T) {
	for _, target := range []bool{false, true} {
		t.Run(map[bool]string{false: "static", true: "agent"}[target], func(t *testing.T) {
			ds := eval_api.NewDatasetOnlyDataSource()
			if target {
				ds = eval_api.NewAgentTargetDataSource("agent", new("2"))
			}
			ds.SetFileID("previous-service-issued-id")
			ec, requests := identityRunContext(t, identityService{previous: []*eval_api.OpenAIEvalRun{
				{
					ID: "evalrun_old", DataSource: ds, EvaluationLevel: "conversation",
					Metadata: map[string]string{metaDataset: "golden", metaDatasetVersion: "1"},
				},
			}})
			reused, metadata, err := ec.reuseDataSourceFromLastRun(t.Context(), "eval_1")
			require.NoError(t, err)
			assert.Equal(t, map[string]string{
				metaDataset: "golden", metaDatasetVersion: "1", metaEvaluationLevel: "conversation",
			}, metadata)
			submitIdentitySource(t, ec, reused, metadata[metaDatasetVersion], metadata[metaEvaluationLevel])
			source := identityPostedSource(t, requests)
			assert.Equal(t, map[string]any{"type": "file_id", "id": "previous-service-issued-id"}, source["source"])
			if target {
				assert.Equal(t, ds.Target.Name, reused.Target.Name)
				assert.Equal(t, ds.Target.Version, reused.Target.Version)
				assert.Equal(t, ds.Target.Type, reused.Target.Type)
			} else {
				assert.Nil(t, reused.Target)
			}
		})
	}
}

func TestRunStartHandoffKeepsSubmittedDatasetAttribution(t *testing.T) {
	for _, mode := range []string{
		"declared registered", "declared local", "registered rerun", "local rerun", "anonymous rerun",
	} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			service := identityService{
				listStatus: http.StatusNotFound, getStatus: http.StatusNotFound,
			}
			registered := strings.Contains(mode, "registered")
			declared := strings.HasPrefix(mode, "declared")
			want := map[string]any{
				"run_id": "evalrun_new", "eval_id": "eval_1", "status": "queued",
			}
			if mode != "anonymous rerun" {
				want["dataset"] = "golden"
			}
			version := ""
			if registered {
				version = "1"
				want["dataset_version"] = version
			}
			chosen := "eval_1"
			if declared {
				chosen = "quality"
				want["eval_name"] = chosen
				body, err := json.Marshal(map[string]any{
					"datasets": []project.DatasetDecl{{Name: "golden", File: "golden.jsonl", Version: version}},
					"evals":    []project.Eval{{Name: chosen, Dataset: "golden"}},
				})
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(dir, "azure.eval.yaml"), body, 0o600))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "golden.jsonl"), []byte(oneRow), 0o600))
				if registered {
					service = identityService{id: "issued", rows: oneRow, wantVersion: version}
				}
			} else {
				ds := eval_api.NewDatasetOnlyDataSource()
				ds.SetFileContent([]map[string]any{{"query": "local row"}})
				metadata := map[string]string{}
				if mode != "anonymous rerun" {
					metadata[metaDataset] = "golden"
				}
				if registered {
					ds.SetFileID("previous-service-issued-id")
					metadata[metaDatasetVersion] = version
				}
				service.previous = []*eval_api.OpenAIEvalRun{{
					ID: "previous", DataSource: ds, Metadata: metadata,
				}}
			}
			ec, requests := identityRunContext(t, service)
			ec.state = map[string]string{idKey("eval", "quality"): "eval_1"}
			cmd := buildRunCommand("start", "")
			cmd.Flags().String("output", "json", "")
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetErr(io.Discard)
			action := &runStartAction{cmd: cmd, flags: &runStartFlags{
				groupName: chosen, evalPath: dir, wait: false,
			}}
			require.NoError(t, action.start(t.Context(), ec, gate{}))

			var got map[string]any
			require.NoError(t, json.Unmarshal(output.Bytes(), &got))
			assert.Equal(t, want, got, "the create response omits metadata; use the submitted attribution")
			submissions := 0
			for _, request := range recordedIdentityRequests(requests) {
				if mode == "registered rerun" {
					assert.NotContains(t, request.path, "/datasets/", "a pinned rerun must not resolve the dataset again")
				}
				if request.method != http.MethodPost || !strings.HasSuffix(request.path, "/runs") {
					continue
				}
				submissions++
				var submitted eval_api.CreateOpenAIEvalRunRequest
				require.NoError(t, json.Unmarshal(request.body, &submitted))
				assert.Equal(t, version, submitted.Metadata[metaDatasetVersion])
				if mode != "anonymous rerun" {
					assert.Equal(t, "golden", submitted.Metadata[metaDataset])
				} else {
					assert.NotContains(t, submitted.Metadata, metaDataset)
				}
			}
			assert.Equal(t, 1, submissions)
		})
	}
}

func TestRunStartHandoffDatasetPairProvenance(t *testing.T) {
	for _, mode := range []string{"declared registered", "registered rerun", "anonymous rerun", "orphan rerun"} {
		for _, tc := range []struct {
			name             string
			echo             string
			pairedDataset    string
			pairedVersion    string
			anonymousDataset string
			anonymousVersion string
			malformed        bool
		}{
			{name: "orphan version", echo: `{"azd_dataset_version":"3"}`, pairedDataset: "golden", pairedVersion: "2"},
			{name: "complete pair", echo: `{"azd_dataset":"other","azd_dataset_version":"3"}`,
				pairedDataset: "other", pairedVersion: "3", anonymousDataset: "other", anonymousVersion: "3"},
			{name: "same name only", echo: `{"azd_dataset":"golden"}`,
				pairedDataset: "golden", pairedVersion: "2", anonymousDataset: "golden"},
			{name: "other name only", echo: `{"azd_dataset":"other"}`,
				pairedDataset: "other", anonymousDataset: "other"},
			{name: "empty metadata", echo: `{}`, pairedDataset: "golden", pairedVersion: "2"},
			{name: "null metadata", echo: `null`, pairedDataset: "golden", pairedVersion: "2"},
			{name: "empty name", echo: `{"azd_dataset":"","azd_dataset_version":"3"}`,
				pairedDataset: "golden", pairedVersion: "2"},
			{name: "null name", echo: `{"azd_dataset":null,"azd_dataset_version":"3"}`,
				pairedDataset: "golden", pairedVersion: "2"},
			{name: "null version", echo: `{"azd_dataset":"golden","azd_dataset_version":null}`,
				pairedDataset: "golden", pairedVersion: "2", anonymousDataset: "golden"},
			{name: "malformed version", echo: `{"azd_dataset_version":3}`, malformed: true},
			{name: "malformed name", echo: `{"azd_dataset":{},"azd_dataset_version":"3"}`, malformed: true},
			{name: "malformed metadata", echo: `[]`, malformed: true},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				dir := t.TempDir()
				registered := strings.Contains(mode, "registered")
				declared := mode == "declared registered"
				submitted := map[string]string{}
				if registered {
					submitted[metaDataset], submitted[metaDatasetVersion] = "golden", "2"
				} else if mode == "orphan rerun" {
					submitted[metaDatasetVersion] = "4"
				}
				service := identityService{runMetadata: json.RawMessage(tc.echo)}
				chosen := "eval_1"
				if declared {
					chosen = "quality"
					submitted[metaEvalName] = chosen
					cfg := &project.EvalConfig{
						Datasets: []project.DatasetDecl{{Name: "golden", Version: "2"}},
						Evals:    []project.Eval{{Name: chosen, Dataset: "golden"}},
					}
					writeLocalContractConfig(t, dir, cfg)
					service.id, service.wantVersion, service.rows = "issued", "2", oneRow
				} else {
					source := eval_api.NewDatasetOnlyDataSource()
					if registered {
						source.SetFileID("issued")
					} else {
						source.SetFileContent([]map[string]any{{"query": "inline snapshot"}})
					}
					service.previous = []*eval_api.OpenAIEvalRun{{
						ID: "previous", DataSource: source, Metadata: submitted,
					}}
				}
				ec, requests := identityRunContext(t, service)
				ec.state = map[string]string{idKey("eval", "quality"): "eval_1"}
				env := &testEnvServer{state: ec.state}
				ec.azdClient = newTestAzdClient(t, env)
				ec.envName, ec.rootKnown = "test", true
				cmd := buildRunCommand("start", "")
				cmd.Flags().String("output", "json", "")
				var out bytes.Buffer
				cmd.SetOut(&out)
				cmd.SetErr(io.Discard)
				action := &runStartAction{cmd: cmd, flags: &runStartFlags{
					groupName: chosen, evalPath: dir, wait: false,
				}}
				err := action.start(t.Context(), ec, gate{})
				if tc.malformed {
					require.Error(t, err)
					assert.Empty(t, out.String(), "invalid service metadata must not produce a successful handoff")
					assert.Empty(t, env.stored(t, idKey("evalrun", "eval_1")))
				} else {
					require.NoError(t, err)
					dataset, version := tc.anonymousDataset, tc.anonymousVersion
					if registered {
						dataset, version = tc.pairedDataset, tc.pairedVersion
					}
					want := map[string]any{"run_id": "evalrun_new", "eval_id": "eval_1", "status": "queued"}
					if declared {
						want["eval_name"] = chosen
					}
					if dataset != "" {
						want["dataset"] = dataset
					}
					if version != "" {
						want["dataset_version"] = version
					}
					var handoff map[string]any
					require.NoError(t, json.Unmarshal(out.Bytes(), &handoff))
					assert.Equal(t, want, handoff)
					assert.Equal(t, "evalrun_new", env.stored(t, idKey("evalrun", "eval_1")))
				}
				posts := 0
				for _, request := range recordedIdentityRequests(requests) {
					if !declared {
						assert.NotContains(t, request.path, "/datasets/", "rerun attribution is not a registry lookup")
					}
					if request.method != http.MethodPost {
						continue
					}
					if declared && request.path == "/datasets/golden/versions/2/credentials" {
						continue
					}
					posts++
					assert.Equal(t, "/openai/v1/evals/eval_1/runs", request.path)
					var body eval_api.CreateOpenAIEvalRunRequest
					require.NoError(t, json.Unmarshal(request.body, &body))
					if len(submitted) == 0 {
						assert.Empty(t, body.Metadata)
					} else {
						assert.Equal(t, submitted, body.Metadata, "rendered fallback must not change the submitted metadata")
					}
					require.NotNil(t, body.DataSource.Source)
					if registered {
						assert.Equal(t, "issued", body.DataSource.Source.ID)
						assert.Empty(t, body.DataSource.Source.Content)
					} else {
						assert.Empty(t, body.DataSource.Source.ID)
						assert.Equal(t, []map[string]any{{"query": "inline snapshot"}}, body.DataSource.Source.Content)
					}
				}
				assert.Equal(t, 1, posts, "only the requested run is submitted")
			})
		}
	}
}

type runHandoffTransport func(*http.Request) (*http.Response, error)

func (f runHandoffTransport) Do(req *http.Request) (*http.Response, error) { return f(req) }

func TestRunStartWaitBudgetHandoffKeepsSubmittedDatasetAttribution(t *testing.T) {
	for _, mode := range []string{"registered", "local", "registered orphan echo", "anonymous orphan echo"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				const endpoint = "https://example.test"
				const runsPath = "/openai/v1/evals/eval_1/runs"
				source := eval_api.NewDatasetOnlyDataSource()
				source.SetFileContent([]map[string]any{{"query": "local row"}})
				metadata := map[string]string{metaDataset: "golden"}
				want := map[string]any{
					"run_id": "evalrun_new", "eval_id": "eval_1", "status": "queued", "dataset": "golden",
				}
				if strings.HasPrefix(mode, "registered") {
					source.SetFileID("previous-service-issued-id")
					metadata[metaDatasetVersion] = "2"
					want["dataset_version"] = "2"
				} else if mode == "anonymous orphan echo" {
					metadata = nil
					delete(want, "dataset")
				}
				var recorded []identityRequest
				polls := 0
				transport := runHandoffTransport(func(req *http.Request) (*http.Response, error) {
					var body []byte
					if req.Body != nil {
						var err error
						body, err = io.ReadAll(req.Body)
						require.NoError(t, err)
					}
					recorded = append(recorded, identityRequest{req.Method, req.URL.Path, body})
					response := httptest.NewRecorder()
					response.Header().Set("Content-Type", "application/json")
					switch {
					case req.Method == http.MethodGet && req.URL.Path == runsPath:
						require.NoError(t, json.NewEncoder(response).Encode(map[string]any{
							"data": []eval_api.OpenAIEvalRun{{ID: "previous", DataSource: source, Metadata: metadata}},
						}))
					case req.Method == http.MethodGet && req.URL.Path == strings.TrimSuffix(runsPath, "/runs"):
						// A bare-ID rerun reconciles the remote eval's own schema
						// before submitting; this eval carries no responses/traces
						// contract, so the GET is compatible with every mode here.
						require.NoError(t, json.NewEncoder(response).Encode(eval_api.OpenAIEval{ID: "eval_1"}))
					case req.Method == http.MethodPost && req.URL.Path == runsPath:
						body := `{"id":"evalrun_new","status":"queued"}`
						if strings.HasSuffix(mode, "orphan echo") {
							body = `{"id":"evalrun_new","status":"queued","metadata":{"azd_dataset_version":"3"}}`
						}
						_, err := io.WriteString(response, body)
						require.NoError(t, err)
					case req.Method == http.MethodGet && req.URL.Path == runsPath+"/evalrun_new":
						polls++
						// Block only on the poll's own deadline, with no real network or elapsed-time sleep.
						<-req.Context().Done()
						require.ErrorIs(t, req.Context().Err(), context.DeadlineExceeded)
						return nil, req.Context().Err()
					case req.Method == http.MethodGet && req.URL.Path == "/datasets/golden/versions":
						response.WriteHeader(http.StatusNotFound)
					case req.Method == http.MethodGet &&
						(req.URL.Path == "/datasets/golden/versions/1.0" || req.URL.Path == "/datasets/golden/versions/1"):
						response.WriteHeader(http.StatusNotFound)
					default:
						t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
						response.WriteHeader(http.StatusBadRequest)
					}
					return response.Result(), nil
				})
				pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, &policy.ClientOptions{
					Transport: transport, Retry: policy.RetryOptions{MaxRetries: -1},
				})
				ec := &evalContext{
					evalClient:    eval_api.NewEvalClientFromPipeline(endpoint, pipeline),
					datasetClient: dataset_api.NewDatasetClientFromPipeline(endpoint, pipeline),
					state:         map[string]string{},
				}
				cmd := buildRunCommand("start", "")
				cmd.Flags().String("output", "json", "")
				var out bytes.Buffer
				cmd.SetOut(&out)
				cmd.SetErr(io.Discard)
				action := &runStartAction{cmd: cmd, flags: &runStartFlags{
					groupName: "eval_1", evalPath: t.TempDir(), wait: true,
				}}
				started := time.Now()
				require.NoError(t, action.start(t.Context(), ec, gate{}))
				assert.Equal(t, waitBudget, time.Since(started), "the real poll budget expires under the fake clock")
				require.NoError(t, t.Context().Err(), "the parent was not cancelled")
				assert.Equal(t, 1, polls)
				var handoff map[string]any
				require.NoError(t, json.Unmarshal(out.Bytes(), &handoff))
				assert.Equal(t, want, handoff)
				submissions := 0
				for _, request := range recorded {
					if strings.HasPrefix(mode, "registered") || mode == "anonymous orphan echo" {
						assert.NotContains(t, request.path, "/datasets/")
					}
					if request.method != http.MethodPost {
						continue
					}
					submissions++
					assert.Equal(t, runsPath, request.path, "no dataset publication")
					var submitted eval_api.CreateOpenAIEvalRunRequest
					require.NoError(t, json.Unmarshal(request.body, &submitted))
					assert.Equal(t, metadata, submitted.Metadata)
					assert.Equal(t, source, submitted.DataSource)
				}
				assert.Equal(t, 1, submissions)
			})
		})
	}
}

func TestRunRerunRefusesLegacyRegisteredInlineRows(t *testing.T) {
	ds := eval_api.NewDatasetOnlyDataSource()
	ds.SetFileContent([]map[string]any{{"query": "possibly a subset"}})
	ec, requests := identityRunContext(t, identityService{previous: []*eval_api.OpenAIEvalRun{
		{ID: "old", DataSource: ds, Metadata: map[string]string{metaDataset: "golden", metaDatasetVersion: "1"}},
	}})
	source, _, err := ec.reuseDataSourceFromLastRun(t.Context(), "eval_1")
	require.Error(t, err)
	assert.Nil(t, source)
	assert.Contains(t, err.Error(), "inline data")
	assert.Contains(t, err.Error(), `"eval_1"`)
	assert.Contains(t, err.Error(), `"golden"`)
	local, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Contains(t, local.Suggestion, "--max-samples 0")
	assert.Contains(t, local.Suggestion, "If that eval declares maxSamples")
	assert.Contains(t, local.Suggestion, "ordinary dataset eval")
	assert.Contains(t, local.Suggestion, "starting it by name")
	for _, request := range recordedIdentityRequests(requests) {
		assert.True(t, request.method == http.MethodGet || strings.HasSuffix(request.path, "/credentials"))
	}
}

func TestRunRerunAttributionSurvivesUntilDatasetPublication(t *testing.T) {
	ds := eval_api.NewDatasetOnlyDataSource()
	ds.SetFileContent([]map[string]any{{"query": "a local row"}})
	firstContext, requests := identityRunContext(t, identityService{
		listStatus: http.StatusNotFound, getStatus: http.StatusNotFound,
		previous: []*eval_api.OpenAIEvalRun{{
			ID: "declared-local-run", DataSource: ds, EvaluationLevel: "conversation",
			Metadata: map[string]string{metaDataset: "golden", metaEvaluationLevel: "turn"},
		}},
	})
	start := func(ec *evalContext) error {
		cmd := buildRunCommand("start", "")
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		action := &runStartAction{cmd: cmd, flags: &runStartFlags{
			groupName: "eval_1", evalPath: t.TempDir(), wait: false,
		}}
		return action.start(t.Context(), ec, gate{})
	}

	require.NoError(t, start(firstContext))
	var submitted *eval_api.CreateOpenAIEvalRunRequest
	for _, req := range recordedIdentityRequests(requests) {
		if req.method == http.MethodPost && strings.HasSuffix(req.path, "/runs") {
			require.NoError(t, json.Unmarshal(req.body, &submitted))
		}
	}
	require.NotNil(t, submitted, "the first bare-ID rerun must be submitted")
	assert.Equal(t, map[string]string{metaDataset: "golden", metaEvaluationLevel: "conversation"}, submitted.Metadata)
	assert.Equal(t, "conversation", submitted.EvaluationLevel)
	assert.Equal(t, ds, submitted.DataSource)
	assert.NotContains(t, submitted.Metadata, metaDatasetVersion, "unregistered rows have no version to invent")

	secondContext, nextRequests := identityRunContext(t, identityService{
		versions: []dataset_api.Dataset{{Version: "1"}},
		previous: []*eval_api.OpenAIEvalRun{{
			ID: "first-id-rerun", DataSource: submitted.DataSource,
			EvaluationLevel: submitted.EvaluationLevel, Metadata: submitted.Metadata,
		}},
	})
	require.ErrorContains(t, start(secondContext), "inline data attributed to a registered dataset")
	for _, req := range recordedIdentityRequests(nextRequests) {
		assert.Equal(t, http.MethodGet, req.method, "publication must block the second ID rerun before submission")
	}
}

func TestRunRerunChecksInlineDatasetWithoutVersionMetadata(t *testing.T) {
	for _, registered := range []bool{false, true} {
		t.Run(map[bool]string{false: "unregistered", true: "registered"}[registered], func(t *testing.T) {
			ds := eval_api.NewDatasetOnlyDataSource()
			ds.SetFileContent([]map[string]any{{"query": "q"}})
			service := identityService{
				previous: []*eval_api.OpenAIEvalRun{
					{ID: "old", DataSource: ds, Metadata: map[string]string{metaDataset: "golden"}},
				},
				listStatus: 404, getStatus: 404,
			}
			if registered {
				service.listStatus = 0
				service.versions = []dataset_api.Dataset{{Version: "1"}}
			}
			ec, requests := identityRunContext(t, service)
			reused, _, err := ec.reuseDataSourceFromLastRun(t.Context(), "eval_1")
			if registered {
				require.ErrorContains(t, err, "inline data")
				assert.Nil(t, reused)
				return
			}
			require.NoError(t, err)
			submitIdentitySource(t, ec, reused, "", "")
			source := identityPostedSource(t, requests)
			assert.Contains(t, source, "source")
			assert.Equal(t, ds.Source, reused.Source)
		})
	}
}

func TestRunRejectsIgnoredCapFlags(t *testing.T) {
	for _, group := range []*project.Eval{
		nil, {Source: &project.SourceDecl{Type: project.SourceTypeTraces}},
		{Source: &project.SourceDecl{Type: project.SourceTypeResponses}},
	} {
		for _, cap := range []string{"0", "1"} {
			cmd := buildRunCommand("start", "")
			require.NoError(t, cmd.Flags().Set("max-samples", cap))
			value, err := cmd.Flags().GetInt("max-samples")
			require.NoError(t, err)
			_, err = runMaxSamples(cmd, value, group)
			require.Error(t, err)
			local, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			assert.Equal(t, exterrors.CodeConflictingArguments, local.Code)
			if group != nil {
				assert.Contains(t, local.Message, "--max-samples is not supported")
				assert.Contains(t, local.Message, "including an explicit value of 0")
				assert.Contains(t, local.Suggestion, "Omit --max-samples.")
				assert.NotContains(t, local.Suggestion, "Remove the dataset cap")
				assert.Contains(t, local.Suggestion, "source.maxTraces")
				assert.Contains(t, local.Suggestion, "source.responseIds")
			}
		}
	}
}

func TestRunStartRejectsExplicitIDRerunCapsBeforeRequests(t *testing.T) {
	for _, cap := range []string{"0", "1"} {
		t.Run(cap, func(t *testing.T) {
			ec, requests := identityRunContext(t, identityService{})
			cmd := buildRunCommand("start", "")
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			require.NoError(t, cmd.Flags().Set("max-samples", cap))
			value, err := cmd.Flags().GetInt("max-samples")
			require.NoError(t, err)
			action := &runStartAction{cmd: cmd, flags: &runStartFlags{
				groupName: "eval_1", evalPath: t.TempDir(), maxSamples: value,
			}}
			err = action.start(t.Context(), ec, gate{})
			local, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok, "explicit rerun cap must be a structured conflict: %v", err)
			assert.Equal(t, exterrors.CodeConflictingArguments, local.Code)
			assert.Contains(t, local.Message, "--max-samples")
			assert.Empty(t, recordedIdentityRequests(requests),
				"reject before listing runs, looking up datasets, or submitting")
		})
	}
}

func TestRunRerunRejectsUnreadableDatasetAttributionState(t *testing.T) {
	ds := eval_api.NewDatasetOnlyDataSource()
	ds.SetFileContent([]map[string]any{{"query": "local"}})
	ec, requests := identityRunContext(t, identityService{previous: []*eval_api.OpenAIEvalRun{{
		ID: "local-run", DataSource: ds, Metadata: map[string]string{metaDataset: "golden"},
	}}})
	ec.state = map[string]string{}
	ec.stateErr = errors.New("cannot read recorded dataset version")
	source, _, err := ec.reuseDataSourceFromLastRun(t.Context(), "eval_1")
	require.ErrorIs(t, err, ec.stateErr)
	assert.Nil(t, source)
	recorded := recordedIdentityRequests(requests)
	require.Len(t, recorded, 1, "an unreadable binding must not fall back to latest, local rows, or submission")
	assert.Equal(t, http.MethodGet, recorded[0].method)
	assert.True(t, strings.HasSuffix(recorded[0].path, "/runs"))
}

func TestSimulationConfiguredCapCannotBeOverriddenByZero(t *testing.T) {
	cmd := buildRunCommand("start", "")
	require.NoError(t, cmd.Flags().Set("max-samples", "0"))
	group := runnableSimulation()
	group.Dataset = "golden"
	group.MaxSamples = 5
	cap, err := runMaxSamples(cmd, 0, group)
	require.NoError(t, err)
	assert.Zero(t, cap)
	ec, requests := identityRunContext(t, identityService{id: "issued", rows: seedRows})
	ds, _, err := ec.buildRunDataSource(t.Context(), group, writeCatalog(t, "", "1"), cap)
	require.ErrorContains(t, err, "maxSamples")
	assert.Nil(t, ds)
	assert.Empty(t, recordedIdentityRequests(requests), "invalid simulation declarations fail before service calls")
}

func TestRunRejectsConfiguredSourceCaps(t *testing.T) {
	for _, source := range []*project.SourceDecl{
		{Type: project.SourceTypeTraces, AgentName: "agent"},
		{Type: project.SourceTypeResponses, ResponseIDs: []string{"response"}},
	} {
		ec := &evalContext{}
		group := &project.Eval{Name: "source", Source: source, MaxSamples: 1}
		ds, _, err := ec.buildRunDataSource(t.Context(), group, "", resolveMaxSamples(0, group))
		require.ErrorContains(t, err, "maxSamples")
		assert.Nil(t, ds)
		local, ok := errors.AsType[*azdext.LocalError](err)
		require.True(t, ok)
		assert.Contains(t, local.Suggestion, "Remove the positive maxSamples value.")
		assert.NotContains(t, local.Message, "--max-samples")
		assert.NotContains(t, local.Suggestion, "Omit --max-samples")
	}
}

func TestSimulationRegisteredIdentityRemainsStrict(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   string
		cap  int
	}{
		{name: "uncapped", id: "simulation-issued-id"},
		{name: "capped", id: "simulation-issued-id", cap: 1},
		{name: "unresolved"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ec, requests := identityRunContext(t, identityService{id: tc.id, rows: seedRows})
			group := runnableSimulation()
			group.Dataset = "golden"
			ds, version, err := ec.buildRunDataSource(t.Context(), group, writeCatalog(t, "golden.jsonl", "1"), tc.cap)
			if tc.cap > 0 || tc.id == "" {
				require.Error(t, err)
				assert.Nil(t, ds)
				for _, request := range recordedIdentityRequests(requests) {
					assert.True(t, request.method == http.MethodGet || strings.HasSuffix(request.path, "/credentials"))
				}
				return
			}
			require.NoError(t, err)
			submitIdentitySource(t, ec, ds, version, group.EvaluationLevel)
			posted := identityPostedSource(t, requests)
			assert.Equal(t, string(eval_api.EvalRunDataSourceTypeUserConversationSimulation), posted["type"])
			assert.Equal(t, map[string]any{"type": "file_id", "id": tc.id}, posted["source"])
			assert.NotContains(t, posted, "input_messages")
			assert.Contains(t, posted, "model_configuration")
		})
	}
}

func TestRunTraceRerunRejectsExplicitDatasetCaps(t *testing.T) {
	for _, sourceType := range []string{"azure_ai_traces", "azure_ai_trace_data_source_preview"} {
		for _, cap := range []string{"", "0", "1"} {
			t.Run(sourceType+"/"+cap, func(t *testing.T) {
				reads, posts, schemaReads := 0, 0, 0
				source := map[string]any{"type": sourceType, "agent_name": "agent", "lookback_hours": 24}
				if sourceType == "azure_ai_trace_data_source_preview" {
					source = map[string]any{
						"type": sourceType,
						"trace_source": map[string]any{
							"type": "agent_filter", "agent_name": "agent", "start_time": 1, "end_time": 2,
						},
					}
				}
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch {
					case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/eval_trace"):
						schemaReads++
						_, err := io.WriteString(w, `{"id":"eval_trace","data_source_config":{"type":"custom"}}`)
						assert.NoError(t, err)
					case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/runs"):
						reads++
						assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{"data": []any{
							map[string]any{"id": "previous", "data_source": source},
						}}))
					case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/eval_trace/runs"):
						posts++
						var request eval_api.CreateOpenAIEvalRunRequest
						assert.NoError(t, json.NewDecoder(r.Body).Decode(&request))
						if assert.NotNil(t, request.DataSource) && sourceType == "azure_ai_traces" {
							assert.Equal(t, eval_api.EvalRunDataSourceTypeTracePreview, request.DataSource.Type)
							if assert.NotNil(t, request.DataSource.TraceSource) {
								assert.Equal(t, "agent", request.DataSource.TraceSource.AgentName)
								assert.Equal(t, int64(24*60*60),
									request.DataSource.TraceSource.EndTime-request.DataSource.TraceSource.StartTime)
							}
						}
						_, err := io.WriteString(w, `{"id":"run_trace","status":"queued"}`)
						assert.NoError(t, err)
					default:
						t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
						w.WriteHeader(http.StatusBadRequest)
					}
				}))
				t.Cleanup(srv.Close)
				var out bytes.Buffer
				command := buildRunCommand("start", "")
				command.SetContext(t.Context())
				command.SetOut(&out)
				command.Flags().String("output", "json", "")
				if cap != "" {
					require.NoError(t, command.Flags().Set("max-samples", cap))
				}
				flag, err := command.Flags().GetInt("max-samples")
				require.NoError(t, err)
				action := &runStartAction{
					cmd: command, flags: &runStartFlags{
						groupName: "eval_trace", evalPath: t.TempDir(), maxSamples: flag,
					},
					newContext: func(context.Context, string) (*evalContext, error) {
						return evalContextFor(srv), nil
					},
				}
				err = action.Run()
				if cap == "" {
					require.NoError(t, err)
					assert.Equal(t, 1, reads)
					assert.Equal(t, 1, schemaReads)
					assert.Equal(t, 1, posts)
				} else {
					require.ErrorContains(t, err, "max-samples")
					local, ok := errors.AsType[*azdext.LocalError](err)
					require.True(t, ok)
					assert.Equal(t, exterrors.CodeConflictingArguments, local.Code)
					assert.Zero(t, reads)
					assert.Zero(t, schemaReads)
					assert.Zero(t, posts)
					assert.Empty(t, out.String())
				}
			})
		}
	}
}

func TestRunStartSampleCapContracts(t *testing.T) {
	for _, mode := range []string{"traces", "responses", "dataset", "simulation"} {
		for _, tc := range []struct {
			name string
			cap  int
			flag string
		}{
			{name: "configured zero"},
			{name: "configured cap", cap: 1},
			{name: "explicit zero", flag: "0"},
			{name: "zero overrides configured cap", cap: 1, flag: "0"},
			{name: "explicit positive", flag: "1"},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				group := project.Eval{Name: "quality", MaxSamples: tc.cap}
				service := identityService{id: "issued", rows: oneRow, wantVersion: "1"}
				switch mode {
				case "traces":
					group.Source = &project.SourceDecl{Type: project.SourceTypeTraces, AgentName: "agent", MaxTraces: 2}
				case "responses":
					group.Source = &project.SourceDecl{Type: project.SourceTypeResponses, ResponseIDs: []string{"response"}}
					service.responseEval = true
				case "dataset":
					group.Dataset = "golden"
				case "simulation":
					group = *runnableSimulation()
					group.Name, group.Dataset, group.MaxSamples = "quality", "golden", tc.cap
					service.rows = seedRows
				}
				values := authoredValues(t, &project.EvalConfig{
					Datasets: []project.DatasetDecl{{Name: "golden", Version: "1"}},
					Evals:    []project.Eval{group},
				})
				// An explicit zero is written out, which is what the case is about.
				values["evals"].([]any)[0].(map[string]any)["maxSamples"] = tc.cap
				body, err := yaml.Marshal(values)
				require.NoError(t, err)
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, "azure.eval.yaml"), body, 0o600))
				ec, requests := identityRunContext(t, service)
				ec.state = map[string]string{idKey("eval", "quality"): "eval_1"}
				cmd := buildRunCommand("start", "")
				cmd.SetContext(t.Context())
				cmd.SetOut(io.Discard)
				cmd.SetErr(io.Discard)
				if tc.flag != "" {
					require.NoError(t, cmd.Flags().Set("max-samples", tc.flag))
				}
				flag, err := cmd.Flags().GetInt("max-samples")
				require.NoError(t, err)
				action := &runStartAction{
					cmd: cmd, flags: &runStartFlags{
						groupName: "quality", evalPath: dir, maxSamples: flag, wait: false,
					},
					newContext: func(context.Context, string) (*evalContext, error) { return ec, nil },
				}
				err = action.Run()
				recorded := recordedIdentityRequests(requests)
				wantErr := tc.cap > 0 || flag > 0
				switch mode {
				case "traces", "responses":
					wantErr = tc.cap > 0 || tc.flag != ""
				case "dataset":
					wantErr = flag > 0 || (tc.cap > 0 && tc.flag == "")
				}
				if wantErr {
					require.ErrorContains(t, err, "max")
					if mode != "simulation" {
						local, ok := errors.AsType[*azdext.LocalError](err)
						require.True(t, ok)
						assert.Equal(t, exterrors.CodeConflictingArguments, local.Code)
					}
					assert.Empty(t, recorded, "reject caps before any service call")
					return
				}
				require.NoError(t, err)
				var posted *eval_api.CreateOpenAIEvalRunRequest
				posts := 0
				for _, request := range recorded {
					if request.method == http.MethodPost && strings.HasSuffix(request.path, "/runs") {
						require.NoError(t, json.Unmarshal(request.body, &posted))
						posts++
					}
				}
				assert.Equal(t, 1, posts)
				require.NotNil(t, posted)
				require.NotNil(t, posted.DataSource)
				switch mode {
				case "traces":
					assert.Equal(t, eval_api.EvalRunDataSourceTypeTracePreview, posted.DataSource.Type)
					require.NotNil(t, posted.DataSource.TraceSource)
					assert.Equal(t, 2, posted.DataSource.TraceSource.MaxTraces)
				case "responses":
					assert.Equal(t, eval_api.EvalRunDataSourceTypeResponses, posted.DataSource.Type)
					require.NotNil(t, posted.DataSource.ItemGenerationParams)
					require.NotNil(t, posted.DataSource.ItemGenerationParams.Source)
					assert.Equal(t, []map[string]any{{"item": map[string]any{"response_id": "response"}}},
						posted.DataSource.ItemGenerationParams.Source.Content)
				default:
					require.NotNil(t, posted.DataSource.Source)
					assert.Equal(t, eval_api.EvalRunDataContentTypeFileID, posted.DataSource.Source.Type)
					assert.Equal(t, "issued", posted.DataSource.Source.ID)
					if mode == "simulation" {
						assert.Equal(t, eval_api.EvalRunDataSourceTypeUserConversationSimulation, posted.DataSource.Type)
						require.NotNil(t, posted.DataSource.DefaultSimulationConfiguration)
						assert.Equal(t, group.Simulation.MaxTurns,
							posted.DataSource.DefaultSimulationConfiguration.MaxNumTurns)
						assert.Equal(t, group.Simulation.Conversations(),
							posted.DataSource.DefaultSimulationConfiguration.ConversationRepetitions)
					}
				}
			})
		}
	}
}

func TestRunDatasetOverrideCanHonorExplicitCap(t *testing.T) {
	dir := t.TempDir()
	cfg := project.EvalConfig{
		Datasets: []project.DatasetDecl{{Name: "golden", File: "rows.jsonl"}},
		Evals: []project.Eval{{
			Name: "quality", Source: &project.SourceDecl{Type: project.SourceTypeTraces, AgentName: "agent"},
		}},
	}
	body, err := yaml.Marshal(authoredValues(t, &cfg))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "azure.eval.yaml"), body, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "rows.jsonl"), []byte(oneRow+oneRow), 0o600))
	ec, requests := identityRunContext(t, identityService{listStatus: http.StatusNotFound, getStatus: http.StatusNotFound})
	ec.state = map[string]string{idKey("eval", "quality"): "eval_1"}
	cmd := buildRunCommand("start", "")
	cmd.SetContext(t.Context())
	cmd.SetOut(io.Discard)
	require.NoError(t, cmd.Flags().Set("dataset", "golden"))
	require.NoError(t, cmd.Flags().Set("max-samples", "1"))
	action := &runStartAction{
		cmd: cmd, flags: &runStartFlags{
			groupName: "quality", datasetName: "golden", evalPath: dir, maxSamples: 1,
		},
		newContext: func(context.Context, string) (*evalContext, error) { return ec, nil },
	}
	require.NoError(t, action.Run())
	var posted *eval_api.CreateOpenAIEvalRunRequest
	posts := 0
	for _, request := range recordedIdentityRequests(requests) {
		if request.method == http.MethodPost && strings.HasSuffix(request.path, "/runs") {
			require.NoError(t, json.Unmarshal(request.body, &posted))
			posts++
		}
	}
	assert.Equal(t, 1, posts)
	require.NotNil(t, posted)
	require.NotNil(t, posted.DataSource)
	require.NotNil(t, posted.DataSource.Source)
	assert.Equal(t, eval_api.EvalRunDataContentTypeFileContent, posted.DataSource.Source.Type)
	assert.Len(t, posted.DataSource.Source.Content, 1)
	assert.Nil(t, posted.DataSource.TraceSource)
}
