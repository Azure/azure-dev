// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func TestJobsListsRleFineTuningJobs(t *testing.T) {
	t.Setenv(
		foundryProjectEndpointEnvVar,
		"https://account.services.ai.azure.com/api/projects/project-from-env",
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != finetuneJobsPath {
			t.Fatalf("unexpected jobs request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("limit"); got != fmt.Sprintf("%d", finetuneJobsPageSize) {
			t.Fatalf("expected limit=%d, got %q", finetuneJobsPageSize, got)
		}
		if got := r.URL.Query().Get("metadata[method]"); got != finetuneMethodTypeRleEnvironment {
			t.Fatalf("expected RLE method filter, got %q", got)
		}
		if got := r.URL.Query().Get("after"); got != "" {
			t.Fatalf("expected no initial after cursor, got %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("expected authenticated request, got %q", got)
		}
		if got := r.Header.Get("azureai-project"); got != "project-from-env" {
			t.Fatalf("expected project header, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": [
				{
					"id": "ftjob-rle",
					"status": "running",
					"model": "Qwen/Qwen3-32B",
					"method": {
						"type": "rl_environment",
						"rl_environment": {"name": "code_rl", "version": "1.0.0"}
					}
				},
				{
					"id": "ftjob-supervised",
					"status": "succeeded",
					"model": "Qwen/Qwen3-32B",
					"method": {"type": "supervised"}
				}
			],
			"has_more": false
		}`))
	}))
	defer server.Close()
	stubFinetuneClientEndpoint(t, server.URL)

	outputFormat := "default"
	command := newJobsCommand(&outputFormat)
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}

	for _, expected := range []string{"JOB ID", "STATUS", "MODEL", "RLE", "VERSION", "ftjob-rle", "code_rl", "1.0.0"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("expected output to contain %q, got %s", expected, output.String())
		}
	}
	if strings.Contains(output.String(), "ftjob-supervised") {
		t.Fatalf("expected client-side filtering to exclude non-RLE job, got %s", output.String())
	}
}

func TestJobsSupportsJSONOutput(t *testing.T) {
	t.Setenv(
		foundryProjectEndpointEnvVar,
		"https://account.services.ai.azure.com/api/projects/project-from-env",
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": [{
				"id": "ftjob-rle",
				"status": "queued",
				"method": {
					"type": "rl_environment",
					"rl_environment": {"name": "code_rl", "version": "2.0.0"}
				}
			}],
			"has_more": false
		}`))
	}))
	defer server.Close()
	stubFinetuneClientEndpoint(t, server.URL)

	outputFormat := "json"
	command := newJobsCommand(&outputFormat)
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}

	var jobs []finetuneJobResource
	if err := json.Unmarshal(output.Bytes(), &jobs); err != nil {
		t.Fatalf("expected JSON output, got %s: %v", output.String(), err)
	}
	if len(jobs) != 1 || jobs[0].Id != "ftjob-rle" || jobs[0].Method.RleEnvironment.Version != "2.0.0" {
		t.Fatalf("unexpected jobs JSON: %#v", jobs)
	}
}

func TestListAllRleJobsPaginatesUsingLastRawJobID(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if got := r.URL.Query().Get("metadata[method]"); got != finetuneMethodTypeRleEnvironment {
			t.Fatalf("expected RLE method filter, got %q", got)
		}
		switch requestCount {
		case 1:
			if got := r.URL.Query().Get("after"); got != "" {
				t.Fatalf("expected no initial after cursor, got %q", got)
			}
			_, _ = w.Write([]byte(`{
				"data": [
					{"id":"ftjob-rle-1","method":{"type":"rl_environment","rl_environment":{"name":"code_rl","version":"1.0.0"}}},
					{"id":"ftjob-supervised","method":{"type":"supervised"}}
				],
				"has_more": true
			}`))
		case 2:
			if got := r.URL.Query().Get("after"); got != "ftjob-supervised" {
				t.Fatalf("expected cursor from last raw job, got %q", got)
			}
			_, _ = w.Write([]byte(`{
				"data": [{"id":"ftjob-rle-2","method":{"type":"RL_ENVIRONMENT","rl_environment":{"name":"echo_env","version":"1.1.0"}}}],
				"has_more": false
			}`))
		default:
			t.Fatalf("unexpected request %d", requestCount)
		}
	}))
	defer server.Close()

	jobs, err := listAllRleJobs(t.Context(), testFinetuneClientForServer(t, server.URL), "project")
	if err != nil {
		t.Fatal(err)
	}
	if requestCount != 2 {
		t.Fatalf("expected two pages, got %d", requestCount)
	}
	if len(jobs) != 2 || jobs[0].Id != "ftjob-rle-1" || jobs[1].Id != "ftjob-rle-2" {
		t.Fatalf("unexpected RLE jobs: %#v", jobs)
	}
}

func TestJobsRequiresProjectEndpoint(t *testing.T) {
	t.Setenv(foundryProjectEndpointEnvVar, "")

	outputFormat := "default"
	command := newJobsCommand(&outputFormat)
	err := command.Execute()
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected LocalError, got %T: %v", err, err)
	}
	if localErr.Code != "rle_project_required" {
		t.Fatalf("expected project required code, got %q", localErr.Code)
	}
}

func stubFinetuneClientEndpoint(t *testing.T, endpoint string) {
	t.Helper()
	originalCreateFinetuneClient := createFinetuneClient
	createFinetuneClient = func(actualEndpoint string) (*finetuneClient, error) {
		if actualEndpoint != "https://account.openai.azure.com" {
			t.Fatalf("expected derived fine-tuning endpoint, got %q", actualEndpoint)
		}
		return testFinetuneClientForServer(t, endpoint), nil
	}
	t.Cleanup(func() {
		createFinetuneClient = originalCreateFinetuneClient
	})
}

func testFinetuneClientForServer(t *testing.T, endpoint string) *finetuneClient {
	t.Helper()
	target, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	client := newFinetuneClientWithCredential("https://resource.openai.azure.com", &testTokenCredential{})
	client.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		request = request.Clone(request.Context())
		request.URL.Scheme = target.Scheme
		request.URL.Host = target.Host
		return http.DefaultTransport.RoundTrip(request)
	})
	return client
}
