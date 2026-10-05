// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"
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

func TestRemoteDatasetReadFailuresPreserveLocalState(t *testing.T) {
	for _, pin := range []string{"", "3.0"} {
		for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
			if pin == "" && status == http.StatusNotFound {
				continue
			}
			t.Run(fmt.Sprintf("pin=%s/status=%d", pin, status), func(t *testing.T) {
				ec, env, _, cfg, dir := validationFixture(t)
				decl := cfg.Datasets[0]
				decl.Version = pin
				path := filepath.Join(dir, decl.File)
				before, err := os.ReadFile(path)
				require.NoError(t, err)
				digest, err := project.Fingerprint(path)
				require.NoError(t, err)
				env.state[project.FingerprintKey("dataset", decl.Name)] = digest
				env.state[versionKey("dataset", decl.Name)] = "3.0"
				stateBefore := maps.Clone(env.state)
				var writes atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if req.Method != http.MethodGet {
						writes.Add(1)
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					if strings.HasSuffix(req.URL.Path, "/versions") {
						assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
							"value": []map[string]string{{"name": decl.Name, "version": "3.0"}},
						}))
						return
					}
					w.WriteHeader(status)
				}))
				t.Cleanup(srv.Close)
				pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
					&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
				ec.datasetClient = dataset_api.NewDatasetClientFromPipeline(srv.URL, pipeline)
				r := &evalReconciler{ec: ec}
				version, changed, err := r.EnsureDataset(t.Context(), decl, path)
				require.Error(t, err)
				assert.Empty(t, version)
				assert.False(t, changed)
				assert.Zero(t, writes.Load())
				assert.Equal(t, stateBefore, env.state)
				assert.Empty(t, env.config)
				after, err := os.ReadFile(path)
				require.NoError(t, err)
				assert.Equal(t, before, after)
			})
		}
	}
}
