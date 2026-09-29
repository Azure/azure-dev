// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaieval/internal/pkg/dataset_api"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJobShowRecoversMetadataWhilePreservingEditedFile(t *testing.T) {
	for _, tc := range []struct {
		name, jobType, localLevel, tagLevel, wantLevel string
		force                                          bool
		tags                                           map[string]string
	}{
		{name: "missing inputs", tagLevel: "conversation", wantLevel: "conversation"},
		{name: "unknown inputs", jobType: "future_type", tagLevel: "conversation", wantLevel: "conversation"},
		{name: "known job wins", jobType: "simple_qna", localLevel: "conversation",
			tagLevel: "conversation", wantLevel: "turn"},
		{name: "local state wins", jobType: "future_type", localLevel: "turn",
			tagLevel: "conversation", wantLevel: "turn"},
		{name: "untagged stays unknown"},
		{name: "force restores bytes", tagLevel: "conversation", wantLevel: "conversation", force: true},
		{name: "service generation tag", tags: map[string]string{"data_generation_type": "conversation_simulation"},
			wantLevel: "conversation"},
		{name: "portal scenario tag", tags: map[string]string{"scenario": "conversation_simulation"},
			wantLevel: "conversation"},
		{name: "unknown inputs use service tag", jobType: "future_type",
			tags: map[string]string{"data_generation_type": "simulation_seed"}, wantLevel: "conversation"},
		{name: "job type beats service tag", jobType: "simple_qna",
			tags: map[string]string{"data_generation_type": "conversation_simulation"}, wantLevel: "turn"},
		{name: "local state beats portal tag", localLevel: "turn",
			tags: map[string]string{"scenario": "conversation_simulation"}, wantLevel: "turn"},
		{name: "unknown service tag stays unknown", tags: map[string]string{"data_generation_type": "future_type"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := make(chan identityRequest, 30)
			const remote = "{\"query\":\"remote\"}\n"
			var srv *httptest.Server
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				requests <- identityRequest{r.Method, r.URL.Path, body}
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.URL.Path == "/rows.jsonl":
					_, _ = io.WriteString(w, remote)
				case strings.HasSuffix(r.URL.Path, "/credentials"):
					assert.True(t, tc.force, "preserving a file must not request download credentials")
					assert.NoError(t, json.NewEncoder(w).Encode(map[string]string{"sas_uri": srv.URL + "/rows.jsonl"}))
				default:
					assert.Equal(t, "/datasets/golden/versions/3", r.URL.Path)
					tags := seedDatasetTags(tc.tagLevel)
					if tc.tags != nil {
						tags = tc.tags
					}
					if r.Method == http.MethodPut {
						var payload struct {
							Tags map[string]string `json:"tags"`
						}
						assert.NoError(t, json.Unmarshal(body, &payload))
						assert.Equal(t, tc.wantLevel, payload.Tags[tagEvaluationLevel])
					} else {
						assert.Equal(t, http.MethodGet, r.Method)
					}
					assert.NoError(t, json.NewEncoder(w).Encode(dataset_api.Dataset{
						Name: "golden", Version: "3", Tags: tags, DataURI: srv.URL + "/rows.jsonl",
					}))
				}
			}))
			t.Cleanup(srv.Close)
			ec := evalContextFor(srv)
			env := &testEnvServer{state: map[string]string{
				project.FingerprintKey("dataset", "golden"): "original fingerprint",
				versionKey("dataset", "golden"):             "2",
			}}
			if tc.localLevel != "" {
				env.state[generationLevelKey("job_1")] = tc.localLevel
			}
			ec.azdClient = newTestAzdClient(t, env)
			ec.envName = "test"
			dir := t.TempDir()
			path := filepath.Join(dir, "datasets", "golden.jsonl")
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
			const edited = "{\"query\":\"my edited bytes\"}\r\n"
			require.NoError(t, os.WriteFile(path, []byte(edited), 0o600))
			job := datasetJobResult("golden", "3")
			if tc.jobType != "" {
				job.Inputs = &eval_api.DataGenerationInputs{
					Options: eval_api.DataGenerationOptions{Type: tc.jobType},
				}
			}
			var out bytes.Buffer
			a := &jobShowAction{
				cmd: catalogCommand(t, &out), flags: &jobFlags{path: dir, force: tc.force},
			}
			ref, err := a.collect(t.Context(), ec, datasetJobs, job, &out)
			require.NoError(t, err)
			require.NotNil(t, ref)
			assert.Equal(t, tc.wantLevel, ref.EvaluationLevel)
			assert.Equal(t, "3", ref.Version)
			cfg, err := project.OpenEvalConfig(dir)
			require.NoError(t, err)
			decl, ok := cfg.DatasetDeclaration("golden")
			require.True(t, ok)
			assert.Equal(t, tc.wantLevel, decl.Tags[tagEvaluationLevel])
			body, err := os.ReadFile(path)
			require.NoError(t, err)
			if tc.force {
				assert.Equal(t, remote, string(body))
				digest, err := project.Fingerprint(path)
				require.NoError(t, err)
				assert.Equal(t, digest, env.stored(t, project.FingerprintKey("dataset", "golden")))
				assert.Equal(t, "3", env.stored(t, versionKey("dataset", "golden")))
			} else {
				assert.Equal(t, []byte(edited), body)
				assert.Equal(t, "original fingerprint", env.stored(t, project.FingerprintKey("dataset", "golden")))
				assert.Equal(t, "2", env.stored(t, versionKey("dataset", "golden")))
				assert.Contains(t, out.String(), "--force")
			}
			reads, downloads := 0, 0
			for _, req := range recordedIdentityRequests(requests) {
				if req.method == http.MethodGet && strings.HasSuffix(req.path, "/versions/3") {
					reads++
				}
				if req.path == "/rows.jsonl" || strings.HasSuffix(req.path, "/credentials") {
					downloads++
				}
			}
			if !tc.force {
				assert.Zero(t, downloads)
			}
			assert.Equal(t, 1, reads, "reuse the metadata read for tag recovery")
		})
	}
}

func TestJobCollectionMetadataFailureDoesNotClaimRecovery(t *testing.T) {
	for _, code := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "/datasets/golden/versions/3", r.URL.Path)
				w.WriteHeader(code)
			}))
			t.Cleanup(srv.Close)
			assertJobMetadataFailure(t, evalContextFor(srv), t.Context(), false)
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	assertJobMetadataFailure(t, unregisteredRunContext(t), ctx, true)
}

func assertJobMetadataFailure(t *testing.T, ec *evalContext, ctx context.Context, cancelled bool) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "golden.jsonl")
	const edited = "edited local file\n"
	require.NoError(t, os.WriteFile(path, []byte(edited), 0o600))
	var out bytes.Buffer
	ref, err := datasetJobs.collect(ctx, ec, datasetJobResult("golden", "3"), dir, "", &out, false)
	require.Error(t, err)
	if cancelled {
		assert.ErrorIs(t, err, context.Canceled)
	}
	assert.Nil(t, ref, "do not report a recovered/default level on metadata failure")
	assert.Empty(t, out.String(), "do not claim collection succeeded")
	body, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, []byte(edited), body)
}
