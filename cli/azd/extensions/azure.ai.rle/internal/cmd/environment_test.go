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
	"strconv"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func TestEnvironmentListListsProjectEnvironments(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv(
		foundryProjectEndpointEnvVar,
		"https://account.services.ai.azure.com/api/projects/project-from-env",
	)

	requestCount := 0
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if r.Method != http.MethodGet || r.URL.Path != testFoundryProjectPath+environmentCollectionPath {
			t.Fatalf("unexpected environments request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("api-version"); got != foundryAPIVersion {
			t.Fatalf("expected API version %q, got %q", foundryAPIVersion, got)
		}
		if got := r.URL.Query().Get("skip"); got != "0" {
			t.Fatalf("expected skip=0, got %q", got)
		}
		if got := r.URL.Query().Get("top"); got != strconv.Itoa(environmentListPageSize) {
			t.Fatalf("expected top=%d, got %q", environmentListPageSize, got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"value": [
				{
					"id": "env-1",
					"name": "echo_env",
					"version": "1.2.0",
					"diskImageConversionStatus": "Ready",
					"updatedAtUtc": "2026-07-30T05:00:00Z"
				},
				{
					"id": "env-2",
					"name": "code_rl",
					"version": "2.0.0",
					"diskImageConversionStatus": "Pending",
					"updatedAtUtc": "2026-07-30T06:00:00Z"
				}
			],
			"count": 2
		}`))
	}))
	defer controlPlane.Close()
	stubRleClientEndpoint(t, controlPlane.URL)

	outputFormat := "default"
	command := newEnvironmentListCommand(&outputFormat)
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}

	if requestCount != 1 {
		t.Fatalf("expected one list request, got %d", requestCount)
	}
	for _, expected := range []string{
		"NAME",
		"VERSION",
		"DISK IMAGE",
		"ENVIRONMENT ID",
		"echo_env",
		"1.2.0",
		"env-1",
		"code_rl",
		"Pending",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("expected output to contain %q, got %s", expected, output.String())
		}
	}
	if !strings.HasPrefix(output.String(), "\n") || !strings.HasSuffix(output.String(), "\n\n") {
		t.Fatalf("expected blank lines around table, got %q", output.String())
	}
}

func TestEnvironmentListSupportsJSONOutput(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	if err := saveRleState(rleState{
		ProjectEndpoint: "https://account.services.ai.azure.com/api/projects/saved-project",
	}); err != nil {
		t.Fatal(err)
	}

	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"value": [{"id":"env-1","name":"echo_env","version":"1.0.0"}],
			"count": 1
		}`))
	}))
	defer controlPlane.Close()
	stubRleClientEndpoint(t, controlPlane.URL)

	outputFormat := "json"
	command := newEnvironmentListCommand(&outputFormat)
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}

	var result []environmentResource
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("expected JSON output, got %s: %v", output.String(), err)
	}
	if len(result) != 1 || result[0].Id != "env-1" || result[0].Name != "echo_env" {
		t.Fatalf("unexpected environments JSON: %#v", result)
	}
}

func TestEnvironmentListReportsEmptyProject(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv(
		foundryProjectEndpointEnvVar,
		"https://account.services.ai.azure.com/api/projects/project-from-env",
	)

	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"value":[],"count":0}`))
	}))
	defer controlPlane.Close()
	stubRleClientEndpoint(t, controlPlane.URL)

	outputFormat := "default"
	command := newEnvironmentListCommand(&outputFormat)
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "No RLE environments found") {
		t.Fatalf("expected empty project message, got %s", output.String())
	}
}

func TestEnvironmentListRequiresProjectEndpoint(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv(foundryProjectEndpointEnvVar, "")

	outputFormat := "default"
	command := newEnvironmentListCommand(&outputFormat)
	err := command.Execute()
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected LocalError, got %T: %v", err, err)
	}
	if localErr.Code != "rle_project_required" {
		t.Fatalf("expected project required code, got %q", localErr.Code)
	}
	if !strings.Contains(localErr.Suggestion, foundryProjectEndpointEnvVar) {
		t.Fatalf("expected endpoint suggestion, got %q", localErr.Suggestion)
	}
}

func TestListAllEnvironmentsPaginates(t *testing.T) {
	requestCount := 0
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		skip, err := strconv.Atoi(r.URL.Query().Get("skip"))
		if err != nil {
			t.Fatal(err)
		}

		requestCount++
		pageSize := environmentListPageSize
		if skip > 0 {
			pageSize = 1
		}
		value := make([]environmentResource, pageSize)
		for i := range value {
			value[i] = environmentResource{
				Id:   fmt.Sprintf("env-%d", skip+i),
				Name: fmt.Sprintf("environment-%d", skip+i),
			}
		}
		if err := json.NewEncoder(w).Encode(listEnvironmentsResponse{Value: value}); err != nil {
			t.Fatal(err)
		}
	}))
	defer controlPlane.Close()

	client := testRleClientForServer(t, controlPlane.URL)
	environments, err := listAllEnvironments(t.Context(), client)
	if err != nil {
		t.Fatal(err)
	}
	if requestCount != 2 {
		t.Fatalf("expected two pages, got %d", requestCount)
	}
	if len(environments) != environmentListPageSize+1 {
		t.Fatalf("expected %d environments, got %d", environmentListPageSize+1, len(environments))
	}
}

func TestListAllEnvironmentsStopsAtSafetyLimit(t *testing.T) {
	requestCount := 0
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		value := make([]environmentResource, environmentListPageSize)
		if err := json.NewEncoder(w).Encode(listEnvironmentsResponse{Value: value}); err != nil {
			t.Fatal(err)
		}
	}))
	defer controlPlane.Close()

	client := testRleClientForServer(t, controlPlane.URL)
	_, err := listAllEnvironments(t.Context(), client)
	if err == nil || !strings.Contains(err.Error(), "safety limit") {
		t.Fatalf("expected safety-limit error, got %v", err)
	}
	if requestCount != environmentListMaxPages {
		t.Fatalf("expected %d pages, got %d", environmentListMaxPages, requestCount)
	}
}

func stubRleClientEndpoint(t *testing.T, endpoint string) {
	t.Helper()
	oldCreateRleClient := createRleClient
	createRleClient = func(string) (*rleClient, error) {
		return testRleClientForServer(t, endpoint), nil
	}
	t.Cleanup(func() {
		createRleClient = oldCreateRleClient
	})
}

func testRleClientForServer(t *testing.T, endpoint string) *rleClient {
	t.Helper()
	target, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	client := newRleClientWithCredential(
		"https://rle.test"+testFoundryProjectPath,
		&testTokenCredential{},
	)
	client.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		request = request.Clone(request.Context())
		request.URL.Scheme = target.Scheme
		request.URL.Host = target.Host
		return http.DefaultTransport.RoundTrip(request)
	})
	return client
}
