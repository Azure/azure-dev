// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOptimizeDownloadCandidateFile(t *testing.T) {
	t.Parallel()
	const remotePath = "reports/trace-analysis space+hash.json"
	const content = `{"traces":[{"classification_status":"accepted"}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "v1", r.URL.Query().Get("api-version"))
		switch r.URL.Path {
		case "/agent_optimization_jobs/job/candidates/candidate":
			_, _ = w.Write([]byte(`{"candidate_id":"candidate","files":[{"path":"` + remotePath + `","type":"trace_analysis"}]}`))
		case "/agent_optimization_jobs/job/files":
			assert.Equal(t, "candidate", r.URL.Query().Get("candidate_id"))
			assert.Equal(t, remotePath, r.URL.Query().Get("path"))
			_, _ = w.Write([]byte(content))
		default:
			t.Errorf("unexpected request %s", r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	target := filepath.Join(t.TempDir(), "report.json")
	require.NoError(t, downloadOptimizeCandidateFile(t.Context(), newTestOptimizeClient(server.URL),
		"job", "candidate", remotePath, target))
	body, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, content, string(body))
}

func TestOptimizeDownloadFailureDoesNotCreateFile(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"undeclared", "forbidden", "not found"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var downloads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/files") {
					downloads.Add(1)
					status := http.StatusForbidden
					if mode == "not found" {
						status = http.StatusNotFound
					}
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"error":{"code":"FileUnavailable","message":"Report unavailable"}}`))
					return
				}
				file := "report.json"
				if mode == "undeclared" {
					file = "other.json"
				}
				_, _ = w.Write([]byte(`{"files":[{"path":"` + file + `"}]}`))
			}))
			defer server.Close()
			target := filepath.Join(t.TempDir(), "report.json")
			err := downloadOptimizeCandidateFile(t.Context(), newTestOptimizeClient(server.URL),
				"job", "candidate", "report.json", target)
			require.Error(t, err)
			assert.NoFileExists(t, target)
			if mode == "undeclared" {
				assert.Zero(t, downloads.Load())
				assert.Contains(t, err.Error(), "not declared")
			} else {
				assert.EqualValues(t, 1, downloads.Load())
				assert.Contains(t, err.Error(), "Report unavailable")
			}
		})
	}
}

func TestOptimizeDownloadDoesNotOverwriteRacingDestination(t *testing.T) {
	t.Parallel()
	target := filepath.Join(t.TempDir(), "existing.json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/files") {
			assert.NoError(t, os.WriteFile(target, []byte("keep"), 0o600))
			_, _ = w.Write([]byte("replace"))
			return
		}
		_, _ = w.Write([]byte(`{"files":[{"path":"report.json"}]}`))
	}))
	defer server.Close()
	_, err := optimizeDownloadTarget(&optimizeDownloadFlags{file: "report.json", targetPath: target})
	require.NoError(t, err)
	err = downloadOptimizeCandidateFile(t.Context(), newTestOptimizeClient(server.URL),
		"job", "candidate", "report.json", target)
	require.ErrorContains(t, err, "creating local destination")
	body, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "keep", string(body))
}

func TestOptimizeDownloadTarget(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, remote := range []string{"reports/report.json", `reports\report.json`, "../../report.json"} {
		target, err := optimizeDownloadTarget(&optimizeDownloadFlags{file: remote})
		require.NoError(t, err)
		assert.Equal(t, "report.json", target)
	}
	for _, remote := range []string{".", "..", "/", `..\..`} {
		_, err := optimizeDownloadTarget(&optimizeDownloadFlags{file: remote})
		require.ErrorContains(t, err, "safe local filename")
	}
	require.NoError(t, os.WriteFile("existing.json", []byte("keep"), 0o600))
	_, err := optimizeDownloadTarget(&optimizeDownloadFlags{file: "existing.json"})
	require.ErrorContains(t, err, "already exists")
}

func TestOptimizeDownloadValidationBeforeAuthentication(t *testing.T) {
	t.Setenv("AZD_SERVER", "")
	for _, args := range [][]string{
		{"job", "--candidate=", "--file=report.json"},
		{"job", "--candidate=c", "--file= "},
		{"job", "--candidate=c", "--file=report.json", "--target-path="},
		{"job", "--candidate=c", "--file=report.json", "--endpoint="},
		{"job", "--candidate=c", "--file=report.json", "--endpoint=https://local", "--project-endpoint=https://project"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			cmd := newOptimizeDownloadCommand(&azdext.ExtensionContext{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs(args)
			err := cmd.Execute()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "--")
			assert.NotContains(t, err.Error(), "credential")
		})
	}
}

func TestOptimizeDownloadExistingTargetFailsBeforeAuthentication(t *testing.T) {
	t.Setenv("AZD_SERVER", "")
	target := writeTestFile(t, t.TempDir(), "existing.json", "keep")
	cmd := newOptimizeDownloadCommand(&azdext.ExtensionContext{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"job", "--candidate=c", "--file=report.json", "--target-path", target})
	require.ErrorContains(t, cmd.Execute(), "already exists")
	body, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "keep", string(body))
}

func TestOptimizeDownloadCommandContract(t *testing.T) {
	cmd := newOptimizeDownloadCommand(&azdext.ExtensionContext{})
	assert.Error(t, cmd.Args(cmd, nil))
	assert.NoError(t, cmd.Args(cmd, []string{"job"}))
	assert.Error(t, cmd.Args(cmd, []string{"job", "extra"}))
	for _, flag := range []string{"candidate", "file", "target-path", "endpoint", "project-endpoint"} {
		assert.NotNil(t, cmd.Flags().Lookup(flag))
	}
	assertOutputFlagOptions(t, cmd, "table", []string{"json", "table"})
}
