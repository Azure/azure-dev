// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaieval/internal/pkg/dataset_api"
	"azureaieval/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// testEnvServer is an azd environment held in memory, so reconciliation paths
// that only run once something was recorded at the last deploy are reachable
// from a test. Without it, getEnvValue answers "" for everything and those
// branches never execute.
type testEnvServer struct {
	azdext.UnimplementedEnvironmentServiceServer
	values map[string]string
}

func (s *testEnvServer) GetValue(
	_ context.Context, req *azdext.GetEnvRequest,
) (*azdext.KeyValueResponse, error) {
	return &azdext.KeyValueResponse{Value: s.values[req.Key]}, nil
}

func (s *testEnvServer) SetValue(
	_ context.Context, req *azdext.SetEnvRequest,
) (*azdext.EmptyResponse, error) {
	if s.values == nil {
		s.values = map[string]string{}
	}
	s.values[req.Key] = req.Value
	return &azdext.EmptyResponse{}, nil
}

// newTestAzdClient serves the environment over gRPC the way azd itself does,
// rather than faking the accessor, so the client code under test is the real one.
func newTestAzdClient(t *testing.T, env *testEnvServer) *azdext.AzdClient {
	t.Helper()

	server := grpc.NewServer()
	azdext.RegisterEnvironmentServiceServer(server, env)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	client, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	return client
}

// pinnedDatasetReconciler builds a reconciler whose environment already holds
// what a previous deploy recorded for a dataset, and whose service answers a
// version read with the given status.
func pinnedDatasetReconciler(
	t *testing.T, name, version string, versionStatus int,
) (*evalReconciler, string) {
	t.Helper()

	dir := t.TempDir()
	localPath := filepath.Join(dir, name+".jsonl")
	require.NoError(t, os.WriteFile(localPath, []byte("{\"query\":\"hi\"}\n"), 0o600))

	digest, err := project.Fingerprint(localPath)
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/versions/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if versionStatus != http.StatusOK {
			w.WriteHeader(versionStatus)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		// assert, not require: this runs on the server's goroutine, and FailNow
		// there aborts mid-response and fails whichever test is running instead.
		assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"name": name, "version": version,
		}))
	}))
	t.Cleanup(srv.Close)

	env := &testEnvServer{values: map[string]string{
		project.FingerprintKey("dataset", name): digest,
		versionKey("dataset", name):             version,
	}}

	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})

	return &evalReconciler{ec: &evalContext{
		azdClient:     newTestAzdClient(t, env),
		envName:       "test",
		datasetClient: dataset_api.NewDatasetClientFromPipeline(srv.URL, pipeline),
	}}, localPath
}

// A pin settles which version to use, not whether it is still there. Reusing it
// unread let a deleted version report as unchanged while the eval pointed at
// nothing, which is what `create` did straight after `dataset delete`.
func TestEnsureDatasetRefusesAPinnedVersionTheServiceNoLongerHas(t *testing.T) {
	r, localPath := pinnedDatasetReconciler(t, "golden", "1.0", http.StatusNotFound)

	_, _, err := r.EnsureDataset(
		context.Background(),
		project.DatasetDecl{Name: "golden", Version: "1.0"},
		localPath,
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "1.0", "the version that is missing")
	assert.Contains(t, err.Error(), "versions list", "and the command that shows what is there")
}

// The ordinary case: the pin is still registered, so reconciliation reuses it
// and reports no change.
func TestEnsureDatasetReusesAPinnedVersionThatStillExists(t *testing.T) {
	r, localPath := pinnedDatasetReconciler(t, "golden", "1.0", http.StatusOK)

	version, changed, err := r.EnsureDataset(
		context.Background(),
		project.DatasetDecl{Name: "golden", Version: "1.0"},
		localPath,
	)

	require.NoError(t, err)
	assert.Equal(t, "1.0", version)
	assert.False(t, changed, "an unchanged file at a pinned version publishes nothing")
}

// A read that failed is not a read that came back empty. Failing the deploy on
// a 403 or a timeout would turn a transient service problem into a broken
// pipeline for a pin that is very probably fine.
func TestEnsureDatasetKeepsAPinnedVersionWhenTheReadFails(t *testing.T) {
	r, localPath := pinnedDatasetReconciler(t, "golden", "1.0", http.StatusForbidden)

	version, changed, err := r.EnsureDataset(
		context.Background(),
		project.DatasetDecl{Name: "golden", Version: "1.0"},
		localPath,
	)

	require.NoError(t, err)
	assert.Equal(t, "1.0", version)
	assert.False(t, changed)
}
