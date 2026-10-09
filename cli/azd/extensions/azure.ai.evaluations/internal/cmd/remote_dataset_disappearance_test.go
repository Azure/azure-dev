// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"azureaieval/internal/pkg/dataset_api"
	"azureaieval/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRemoteDatasetDisappearanceWithoutLocalEdits(t *testing.T) {
	for _, listing := range []string{"older", "empty", "stale"} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%t", listing, prepared), func(t *testing.T) {
				ec, env, _, cfg, dir := validationFixture(t)
				decl := cfg.Datasets[0]
				path := filepath.Join(dir, decl.File)
				before, err := os.ReadFile(path)
				require.NoError(t, err)
				digest, err := project.Fingerprint(path)
				require.NoError(t, err)
				env.state[project.FingerprintKey("dataset", decl.Name)] = digest
				env.state[versionKey("dataset", decl.Name)] = "3.0"
				stateBefore := maps.Clone(env.state)
				var missing, published atomic.Bool
				var writes, points atomic.Int32
				var endpoint string
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if req.Method == http.MethodPut && strings.HasPrefix(req.URL.Path, "/upload/") {
						body, readErr := io.ReadAll(req.Body)
						assert.NoError(t, readErr)
						assert.Equal(t, before, body)
						w.WriteHeader(http.StatusCreated)
						return
					}

					if strings.HasSuffix(req.URL.Path, "/startPendingUpload") {
						writes.Add(1)
						assert.Contains(t, req.URL.Path, "/versions/4.0/",
							"do not restart or reuse deleted version history")
						assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
							"blobReference": map[string]any{
								"blobUri":    endpoint + "/upload",
								"credential": map[string]string{"sasUri": endpoint + "/upload"},
							},
						}))
						return
					}
					if req.Method == http.MethodPut {
						published.Store(true)
						assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{"name": decl.Name, "version": "4.0"}))
						return
					}
					if strings.HasSuffix(req.URL.Path, "/versions") {
						versions := []string{"2.0", "3.0"}
						if published.Load() {
							versions = []string{"2.0", "4.0"}
						} else if missing.Load() {
							switch listing {
							case "older":
								versions = []string{"2.0"}
							case "empty":
								versions = nil
							}
						}
						values := make([]map[string]string, 0, len(versions))
						for _, version := range versions {
							values = append(values, map[string]string{"name": decl.Name, "version": version})
						}
						assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{"value": values}))
						return
					}
					points.Add(1)
					version := "3.0"
					if strings.HasSuffix(req.URL.Path, "/4.0") && published.Load() {
						version = "4.0"
					} else if missing.Load() {
						w.WriteHeader(http.StatusNotFound)
						return
					}
					assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{"name": decl.Name, "version": version}))
				}))
				t.Cleanup(srv.Close)
				endpoint = srv.URL
				pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
					&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
				ec.datasetClient = dataset_api.NewDatasetClientFromPipeline(srv.URL, pipeline)
				r := &evalReconciler{ec: ec}
				version, changed, err := r.EnsureDataset(t.Context(), decl, path)
				require.NoError(t, err)
				assert.Equal(t, "3.0", version)
				assert.False(t, changed)
				if prepared {
					require.NoError(t, r.Validate(t.Context(), cfg, dir))
				}
				assert.Equal(t, stateBefore, env.state)
				assert.Empty(t, env.config)
				after, err := os.ReadFile(path)
				require.NoError(t, err)
				assert.Equal(t, before, after)
				// Only the remote version disappears; no local input or state is edited.
				missing.Store(true)
				version, changed, err = r.EnsureDataset(t.Context(), decl, path)
				require.NoError(t, err)
				assert.True(t, changed, "remote deletion must not report unchanged")
				assert.Equal(t, "4.0", version)
				assert.Equal(t, int32(1), writes.Load())
				assert.Positive(t, points.Load(), "a listing alone cannot establish version existence")
				assert.Equal(t, digest, env.stored(t, project.FingerprintKey("dataset", decl.Name)))
				assert.Equal(t, "4.0", env.stored(t, versionKey("dataset", decl.Name)))
				after, err = os.ReadFile(path)
				require.NoError(t, err)
				assert.Equal(t, before, after)
				fresh := &evalReconciler{ec: ec}
				version, changed, err = fresh.EnsureDataset(t.Context(), decl, path)
				require.NoError(t, err)
				assert.Equal(t, "4.0", version)
				assert.False(t, changed, "a repaired live version stays idempotent")
				assert.Equal(t, int32(1), writes.Load())
			})
		}
	}
}
