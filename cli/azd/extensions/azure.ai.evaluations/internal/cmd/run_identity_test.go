// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func unregisteredRunContext(t *testing.T) *evalContext {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	return &evalContext{datasetClient: dataset_api.NewDatasetClientFromPipeline(srv.URL, pipeline)}
}

type identityRequest struct {
	method string
	path   string
	body   []byte
}

type identityService struct {
	versions    []dataset_api.Dataset
	listStatus  int
	listBody    *string
	getStatus   int
	id          string
	version     string
	wantVersion string
	rows        string
	blobStatus  int
	previous    []*eval_api.OpenAIEvalRun
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
				_, _ = io.WriteString(w, `{"id":"evalrun_new","status":"queued"}`)
			}
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
			if service.getStatus != 0 {
				w.WriteHeader(service.getStatus)
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
	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	return &evalContext{
		datasetClient: dataset_api.NewDatasetClientFromPipeline(srv.URL, pipeline),
		evalClient:    eval_api.NewEvalClientFromPipeline(srv.URL, pipeline),
	}, requests
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

func TestRegisteredRunIdentityOnTheWire(t *testing.T) {
	for _, target := range []string{"static", "agent", "model"} {
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
				ec, requests := identityRunContext(t, identityService{
					versions:    []dataset_api.Dataset{{Version: "1"}, {Version: "3"}, {Version: "2"}},
					id:          issuedID,
					rows:        rows,
					wantVersion: tc.want,
				})
				ec.state = map[string]string{versionKey("dataset", "golden"): tc.recorded}
				group := &project.Eval{Name: "quality", Dataset: "golden"}
				if target != "static" {
					group.Target = &project.Target{Type: target, Name: "target"}
				}
				config := writeCatalog(t, tc.file, tc.pin)
				if tc.file != "" {
					require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(config), tc.file), []byte("not JSON"), 0o600))
				}
				ds, version, err := ec.buildRunDataSource(t.Context(), group, config, 0)
				require.NoError(t, err, "published rows, not the retained local file, must be read")
				assert.Equal(t, tc.want, version)
				submitIdentitySource(t, ec, ds, version, "")
				posted := identityPostedSource(t, requests)
				source, ok := posted["source"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, map[string]any{"type": "file_id", "id": issuedID}, source)
				if target == "static" {
					assert.Equal(t, "jsonl", posted["type"])
					assert.NotContains(t, posted, "target")
					assert.NotContains(t, posted, "input_messages")
				} else {
					assert.Equal(t, "azure_ai_target_completions", posted["type"])
					assert.Contains(t, posted, "target")
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
				assert.Contains(t, local.Message, "max_samples")
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
		{name: "successful empty listing with absent probes", getStatus: 404},
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
				{ID: "evalrun_old", DataSource: ds, EvaluationLevel: "conversation"},
			}})
			reused, level, err := ec.reuseDataSourceFromLastRun(t.Context(), "eval_1")
			require.NoError(t, err)
			assert.Equal(t, "conversation", level)
			submitIdentitySource(t, ec, reused, "", level)
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
	for _, request := range recordedIdentityRequests(requests) {
		assert.True(t, request.method == http.MethodGet || strings.HasSuffix(request.path, "/credentials"))
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
			_, err := runMaxSamples(cmd, 1, group)
			require.Error(t, err)
			local, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			assert.Equal(t, exterrors.CodeConflictingArguments, local.Code)
		}
	}
}

func TestRunRejectsConfiguredSourceCaps(t *testing.T) {
	for _, source := range []*project.SourceDecl{
		{Type: project.SourceTypeTraces, AgentName: "agent"},
		{Type: project.SourceTypeResponses, ResponseIDs: []string{"response"}},
	} {
		ec := &evalContext{}
		group := &project.Eval{Name: "source", Source: source, MaxSamples: 1}
		ds, _, err := ec.buildRunDataSource(t.Context(), group, "", resolveMaxSamples(0, group))
		require.ErrorContains(t, err, "max_samples")
		assert.Nil(t, ds)
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
			assert.Equal(t, "azure_ai_user_conversation_simulation_preview", posted["type"])
			assert.Equal(t, map[string]any{"type": "file_id", "id": tc.id}, posted["source"])
			assert.NotContains(t, posted, "input_messages")
			assert.Contains(t, posted, "model_configuration")
		})
	}
}
