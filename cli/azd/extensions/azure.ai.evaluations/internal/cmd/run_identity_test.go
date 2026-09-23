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
	"strconv"
	"strings"
	"testing"

	"azureaieval/internal/exterrors"
	"azureaieval/internal/pkg/dataset_api"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	listBody    string
	listStatus  int
	getStatus   int
	id          string
	version     string
	wantVersion string
	rows        string
	blobStatus  int
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
			assert.Equal(t, http.MethodPost, r.Method)
			_, _ = io.WriteString(w, `{"id":"evalrun_new","status":"queued"}`)
		case strings.HasSuffix(r.URL.Path, "/versions"):
			if service.listStatus != 0 {
				w.WriteHeader(service.listStatus)
				return
			}
			body := service.listBody
			if body == "" {
				body = `{"value":[]}`
			}
			_, _ = io.WriteString(w, body)
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

func TestRegisteredRunIdentityAndVersion(t *testing.T) {
	for _, target := range []string{"static", "agent", "model", "simulation"} {
		for _, tc := range []struct {
			name, file, pin, recorded, want string
		}{
			{name: "latest", want: "2"},
			{name: "recorded", recorded: "1", want: "1"},
			{name: "pinned", pin: "1", recorded: "2", want: "1"},
			{name: "published file", file: "golden.jsonl", recorded: "2", want: "2"},
			{name: "pinned file", file: "golden.jsonl", pin: "1", recorded: "2", want: "1"},
			{name: "file published elsewhere", file: "golden.jsonl", want: "2"},
		} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				const issuedID = "opaque-service-issued-version-id"
				rows := oneRow
				group := &project.Eval{Name: "quality", Dataset: "golden"}
				switch target {
				case "simulation":
					group = runnableSimulation()
					group.Dataset = "golden"
					rows = seedRows
				case "agent", "model":
					group.Target = &project.Target{Type: target, Name: "target"}
				}
				ec, requests := identityRunContext(t, identityService{
					listBody: `{"value":[{"version":"1"},{"version":"2"}]}`,
					id:       issuedID, rows: rows, wantVersion: tc.want,
				})
				ec.state = map[string]string{versionKey("dataset", "golden"): tc.recorded}
				config := writeCatalog(t, tc.file, tc.pin)
				if tc.file != "" {
					require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(config), tc.file), []byte("edited"), 0o600))
				}
				ds, version, err := ec.buildRunDataSource(t.Context(), group, config, 0)
				require.NoError(t, err, "published rows must be read instead of the retained file")
				assert.Equal(t, tc.want, version)
				if tc.pin != "" {
					cfg, err := project.LoadEvalConfig(config)
					require.NoError(t, err)
					ec.state[project.FingerprintKey("dataset", "golden")] = "old digest"
					require.NoError(t, ec.checkDatasetRegistered(t.Context(), cfg, group, config))
				}
				_, err = ec.evalClient.CreateOpenAIEvalRun(t.Context(), "eval_1", &eval_api.CreateOpenAIEvalRunRequest{
					Name: "identity", DataSource: ds, EvaluationLevel: group.EvaluationLevel,
					Metadata: map[string]string{metaDatasetVersion: version},
				})
				require.NoError(t, err)
				var posted map[string]any
				listReads, versionReads := 0, 0
				for _, req := range recordedIdentityRequests(requests) {
					if strings.HasSuffix(req.path, "/runs") {
						require.NoError(t, json.Unmarshal(req.body, &posted))
					}
					if strings.HasSuffix(req.path, "/versions") {
						listReads++
					}
					if strings.HasSuffix(req.path, "/versions/"+tc.want) {
						versionReads++
					}
				}
				require.NotNil(t, posted)
				assert.Equal(t, map[string]any{metaDatasetVersion: tc.want}, posted["metadata"])
				source, ok := posted["data_source"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, map[string]any{"type": "file_id", "id": issuedID}, source["source"])
				assert.Equal(t, 1, versionReads)
				wantLists := 0
				if tc.pin == "" && tc.recorded == "" {
					wantLists = 1
				}
				assert.Equal(t, wantLists, listReads, "no independent metadata resolution")
			})
		}
	}
}

func TestRegisteredRunRejectsEffectiveCaps(t *testing.T) {
	for _, file := range []string{"", "golden.jsonl"} {
		for _, flag := range []int{0, 1} {
			ec, requests := identityRunContext(t, identityService{id: "issued", rows: oneRow})
			group := &project.Eval{Name: "quality", Dataset: "golden", MaxSamples: 2}
			ds, _, err := ec.buildRunDataSource(t.Context(), group, writeCatalog(t, file, "1"),
				resolveMaxSamples(flag, group))
			require.Error(t, err)
			assert.Nil(t, ds)
			local, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			assert.Equal(t, exterrors.CodeConflictingArguments, local.Code)
			assert.Contains(t, local.Message, "max_samples")
			assert.Contains(t, local.Suggestion, "publish a smaller dataset")
			assert.Empty(t, recordedIdentityRequests(requests), "do not download or submit an unsupported cap")
		}
	}
}

func TestRegisteredRunIdentityFailuresNeverFallBack(t *testing.T) {
	for _, tc := range []struct {
		name    string
		service identityService
	}{
		{"missing id", identityService{}},
		{"blank id", identityService{id: "  "}},
		{"wrong version", identityService{id: "issued", version: "9"}},
		{"deleted version", identityService{getStatus: 404}},
		{"forbidden version", identityService{getStatus: 403}},
		{"transient failure", identityService{getStatus: 503}},
		{"unreadable rows", identityService{id: "issued", blobStatus: 403}},
		{"invalid rows", identityService{id: "issued", rows: "not JSON"}},
		{"empty rows", identityService{id: "issued"}},
	} {
		for _, file := range []string{"", "golden.jsonl"} {
			t.Run(tc.name+"/"+file, func(t *testing.T) {
				ec, requests := identityRunContext(t, tc.service)
				ds, version, err := ec.buildRunDataSource(t.Context(),
					&project.Eval{Name: "quality", Dataset: "golden"}, writeCatalog(t, file, "1"), 0)
				require.Error(t, err)
				assert.Nil(t, ds)
				assert.Empty(t, version)
				for _, req := range recordedIdentityRequests(requests) {
					assert.False(t, strings.HasSuffix(req.path, "/runs"), "failure must not create a run")
				}
			})
		}
	}
}

func TestRunRegistryLookupMustEstablishAbsence(t *testing.T) {
	for _, tc := range []struct {
		name        string
		service     identityService
		wantError   bool
		wantVersion string
	}{
		{name: "typed absence", service: identityService{listStatus: 404, getStatus: 404}},
		{name: "complete empty list", service: identityService{getStatus: 404}},
		{name: "missing list", service: identityService{listBody: `{}`, getStatus: 404}, wantError: true},
		{name: "null list", service: identityService{listBody: `{"value":null}`, getStatus: 404}, wantError: true},
		{name: "null body", service: identityService{listBody: `null`, getStatus: 404}, wantError: true},
		{name: "invalid JSON", service: identityService{listBody: `{`, getStatus: 404}, wantError: true},
		{name: "missing version", service: identityService{listBody: `{"value":[{}]}`}, wantError: true},
		{name: "listing forbidden", service: identityService{listStatus: 403}, wantError: true},
		{name: "listing transient", service: identityService{listStatus: 503}, wantError: true},
		{name: "probe forbidden", service: identityService{getStatus: 403}, wantError: true},
		{name: "probe transient", service: identityService{getStatus: 503}, wantError: true},
		{name: "listing lags", service: identityService{id: "issued", rows: oneRow}, wantVersion: "1.0"},
	} {
		for _, cap := range []int{0, 1} {
			t.Run(tc.name+"/"+strconv.Itoa(cap), func(t *testing.T) {
				ec, _ := identityRunContext(t, tc.service)
				config := writeCatalog(t, "golden.jsonl", "")
				require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(config), "golden.jsonl"),
					[]byte(oneRow+oneRow), 0o600))
				ds, version, err := ec.buildRunDataSource(t.Context(),
					&project.Eval{Name: "quality", Dataset: "golden"}, config, cap)
				if tc.wantError || (tc.wantVersion != "" && cap > 0) {
					require.Error(t, err)
					assert.Nil(t, ds)
					return
				}
				require.NoError(t, err)
				assert.Equal(t, tc.wantVersion, version)
				if version != "" {
					assert.Equal(t, eval_api.EvalRunDataContentTypeFileID, ds.Source.Type)
				} else {
					assert.Equal(t, eval_api.EvalRunDataContentTypeFileContent, ds.Source.Type)
					wantRows := 2
					if cap > 0 {
						wantRows = cap
					}
					assert.Len(t, ds.Source.Content, wantRows)
					assert.Empty(t, ds.Source.ID)
				}
			})
		}
	}
}

func TestRunRejectsUnreadablePublicationState(t *testing.T) {
	ec, requests := identityRunContext(t, identityService{})
	stateErr := errors.New("publication state unavailable")
	ec.state = map[string]string{}
	ec.stateErr = stateErr
	ds, _, err := ec.buildRunDataSource(t.Context(), &project.Eval{Name: "quality", Dataset: "golden"},
		writeCatalog(t, "golden.jsonl", ""), 0)
	require.ErrorIs(t, err, stateErr)
	assert.Nil(t, ds)
	assert.Empty(t, recordedIdentityRequests(requests))
}

func TestRegisteredRunValidatesPublishedRows(t *testing.T) {
	ec, _ := identityRunContext(t, identityService{id: "issued", rows: seedRows})
	ds, _, err := ec.buildRunDataSource(t.Context(), &project.Eval{
		Name: "quality", Dataset: "golden", Target: &project.Target{Type: project.TargetTypeAgent, Name: "agent"},
	}, writeCatalog(t, "golden.jsonl", "1"), 0)
	require.ErrorContains(t, err, `"query"`)
	assert.Nil(t, ds)
}
