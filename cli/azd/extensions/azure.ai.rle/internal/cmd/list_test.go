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
		if got := r.URL.Query().Get("continuationToken"); got != "" {
			t.Fatalf("expected no initial cursor, got %q", got)
		}
		if got := r.URL.Query().Get("limit"); got != fmt.Sprintf("%d", environmentListPageSize) {
			t.Fatalf("expected limit=%d, got %q", environmentListPageSize, got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": [
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
			"nextContinuationToken": null
		}`))
	}))
	defer controlPlane.Close()
	stubRleClientEndpoint(t, controlPlane.URL)

	outputFormat := "default"
	command := newListCommand(&outputFormat)
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
			"data": [{"id":"env-1","name":"echo_env","version":"1.0.0"}],
			"nextContinuationToken": null
		}`))
	}))
	defer controlPlane.Close()
	stubRleClientEndpoint(t, controlPlane.URL)

	outputFormat := "json"
	command := newListCommand(&outputFormat)
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
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer controlPlane.Close()
	stubRleClientEndpoint(t, controlPlane.URL)

	outputFormat := "default"
	command := newListCommand(&outputFormat)
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
	command := newListCommand(&outputFormat)
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
		requestCount++
		pageSize := environmentListPageSize
		continuationToken := r.URL.Query().Get("continuationToken")
		if got := r.URL.Query().Get("after"); got != "" {
			t.Fatalf("expected no legacy after cursor, got %q", got)
		}
		if requestCount == 1 && continuationToken != "" {
			t.Fatalf("expected no initial continuation token, got %q", continuationToken)
		}
		if requestCount == 2 && continuationToken != " cursor-a " {
			t.Fatalf("expected second request continuation token %q, got %q", " cursor-a ", continuationToken)
		}
		if continuationToken != "" {
			pageSize = 1
		}
		data := make([]environmentResource, pageSize)
		for i := range data {
			data[i] = environmentResource{
				Id:   fmt.Sprintf("env-%d-%d", requestCount, i),
				Name: fmt.Sprintf("environment-%d-%d", requestCount, i),
			}
		}
		nextContinuationToken := ""
		if requestCount == 1 {
			nextContinuationToken = " cursor-a "
		}
		if err := json.NewEncoder(w).Encode(pagedEnvironmentResponse{
			Data:                  data,
			NextContinuationToken: nextContinuationToken,
		}); err != nil {
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
		data := make([]environmentResource, environmentListPageSize)
		if err := json.NewEncoder(w).Encode(pagedEnvironmentResponse{
			Data:                  data,
			NextContinuationToken: fmt.Sprintf("cursor-%d", requestCount),
		}); err != nil {
			t.Fatal(err)
		}
	}))
	defer controlPlane.Close()

	client := testRleClientForServer(t, controlPlane.URL)
	_, err := listAllEnvironments(t.Context(), client)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected safety-limit LocalError, got %T: %v", err, err)
	}
	if localErr.Code != "rle_environment_list_safety_limit" {
		t.Fatalf("expected safety-limit code, got %q", localErr.Code)
	}
	if localErr.Category != azdext.LocalErrorCategoryInternal {
		t.Fatalf("expected internal error category, got %q", localErr.Category)
	}
	if requestCount != environmentListMaxPages {
		t.Fatalf("expected %d pages, got %d", environmentListMaxPages, requestCount)
	}
}

func TestListAllEnvironmentsRejectsCursorCycles(t *testing.T) {
	requestCount := 0
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "application/json")
		switch requestCount {
		case 1:
			_, _ = w.Write(
				[]byte(`{"data":[{"id":"env-1","name":"echo_env"}],"nextContinuationToken":"cursor-a"}`),
			)
		case 2:
			_, _ = w.Write(
				[]byte(`{"data":[{"id":"env-2","name":"echo_env"}],"nextContinuationToken":"cursor-b"}`),
			)
		case 3:
			_, _ = w.Write(
				[]byte(`{"data":[{"id":"env-3","name":"echo_env"}],"nextContinuationToken":"cursor-a"}`),
			)
		default:
			t.Fatalf("unexpected extra page request %d", requestCount)
		}
	}))
	defer controlPlane.Close()

	client := testRleClientForServer(t, controlPlane.URL)
	_, err := listAllEnvironments(t.Context(), client)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected LocalError, got %T: %v", err, err)
	}
	if localErr.Code != "rle_environment_list_cursor_invalid" {
		t.Fatalf("expected cursor invalid code, got %q", localErr.Code)
	}
	if requestCount != 3 {
		t.Fatalf("expected prompt cycle detection after three requests, got %d", requestCount)
	}
}

func TestListAllEnvironmentsClassifiesRequestFailuresAsServiceErrors(t *testing.T) {
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
	}))
	defer controlPlane.Close()

	client := testRleClientForServer(t, controlPlane.URL)
	_, err := listAllEnvironments(t.Context(), client)
	serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
	if !ok {
		t.Fatalf("expected ServiceError, got %T: %v", err, err)
	}
	if serviceErr.ServiceName != "rle-service" {
		t.Fatalf("expected rle-service, got %q", serviceErr.ServiceName)
	}
}

func TestShowDisplaysEnvironmentHistory(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv(
		foundryProjectEndpointEnvVar,
		"https://account.services.ai.azure.com/api/projects/saved-project",
	)

	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == testFoundryProjectPath+environmentCollectionPath+"/echo_env":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"id":"env-1",
				"name":"echo_env",
				"version":"1.2.0",
				"diskImageConversionStatus":"Ready",
				"updatedAtUtc":"2026-07-30T05:00:00Z"
			}`))
		case r.Method == http.MethodGet &&
			r.URL.Path == testFoundryProjectPath+environmentCollectionPath+"/echo_env/versions/1.2.0":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"id":"env-1",
				"name":"echo_env",
				"version":"1.2.0",
				"diskImageConversionStatus":"Ready",
				"updatedAtUtc":"2026-07-30T05:00:00Z"
			}`))
		case r.Method == http.MethodGet &&
			r.URL.Path == testFoundryProjectPath+environmentCollectionPath+"/echo_env/versions":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"data": [
					{
						"id":"env-version-1",
						"name":"echo_env",
						"version":"1.0.0",
						"diskImageConversionStatus":"Failed",
						"updatedAtUtc":"2026-07-28T06:00:00Z",
						"createdAtUtc":"2026-07-28T05:00:00Z",
						"acrImagePath":"registry/echo:1.0.0"
					},
					{
						"id":"env-1",
						"name":"echo_env",
						"version":"1.2.0",
						"diskImageConversionStatus":"Ready",
						"updatedAtUtc":"2026-07-30T05:00:00Z",
						"createdAtUtc":"2026-07-30T05:00:00Z",
						"acrImagePath":"registry/echo:1.2.0"
					}
				]
			}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}

	}))
	defer controlPlane.Close()
	stubRleClientEndpoint(t, controlPlane.URL)

	outputFormat := "default"
	command := newShowCommand(&outputFormat)
	command.SetArgs([]string{"echo_env"})
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}

	for _, expected := range []string{
		"VERSION",
		"DISK IMAGE",
		"ENVIRONMENT ID",
		"UPDATED",
		"1.2.0",
		"Ready",
		"1.0.0",
		"Failed",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("expected output to contain %q, got %s", expected, output.String())
		}
	}
	for _, unexpected := range []string{
		"NAME",
		"ACR IMAGE",
		"echo_env",
		"registry/echo:1.2.0",
		"registry/echo:1.0.0",
		"CREATED",
		"FIELD",
		"VALUE",
		"Version history:",
	} {
		if strings.Contains(output.String(), unexpected) {
			t.Fatalf("expected one consolidated table without %q, got %s", unexpected, output.String())
		}
	}
}

func TestResolveEnvironmentVersionsRejectsCursorCycles(t *testing.T) {
	requestCount := 0
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "application/json")
		switch requestCount {
		case 1:
			if got := r.URL.Query().Get("continuationToken"); got != "" {
				t.Fatalf("expected no initial continuation token, got %q", got)
			}
			_, _ = w.Write([]byte(
				`{"data":[{"id":"env-1","name":"echo_env","version":"1.0.0"}],` +
					`"nextContinuationToken":" cursor-a "}`,
			))
		case 2:
			if got := r.URL.Query().Get("continuationToken"); got != " cursor-a " {
				t.Fatalf("expected continuation token %q, got %q", " cursor-a ", got)
			}
			_, _ = w.Write([]byte(
				`{"data":[{"id":"env-2","name":"echo_env","version":"1.1.0"}],` +
					`"nextContinuationToken":"cursor-b"}`,
			))
		case 3:
			if got := r.URL.Query().Get("continuationToken"); got != "cursor-b" {
				t.Fatalf("expected continuation token %q, got %q", "cursor-b", got)
			}
			_, _ = w.Write([]byte(
				`{"data":[{"id":"env-3","name":"echo_env","version":"1.2.0"}],` +
					`"nextContinuationToken":" cursor-a "}`,
			))
		default:
			t.Fatalf("unexpected extra page request %d", requestCount)
		}
	}))
	defer controlPlane.Close()

	client := testRleClientForServer(t, controlPlane.URL)
	_, err := resolveEnvironmentVersions(t.Context(), client, &environmentResource{
		Id:      "env-1",
		Name:    "echo_env",
		Version: "1.2.0",
	})
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected LocalError, got %T: %v", err, err)
	}
	if localErr.Code != "rle_environment_version_cursor_invalid" {
		t.Fatalf("expected version cursor invalid code, got %q", localErr.Code)
	}
	if requestCount != 3 {
		t.Fatalf("expected prompt cycle detection after three requests, got %d", requestCount)
	}
}

func TestResolveEnvironmentVersionsStopsAtSafetyLimit(t *testing.T) {
	requestCount := 0
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if err := json.NewEncoder(w).Encode(pagedEnvironmentResponse{
			NextContinuationToken: fmt.Sprintf("cursor-%d", requestCount),
		}); err != nil {
			t.Fatal(err)
		}
	}))
	defer controlPlane.Close()

	client := testRleClientForServer(t, controlPlane.URL)
	_, err := resolveEnvironmentVersions(t.Context(), client, &environmentResource{Name: "echo_env"})
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected LocalError, got %T: %v", err, err)
	}
	if localErr.Code != "rle_environment_version_list_safety_limit" {
		t.Fatalf("expected version-list safety-limit code, got %q", localErr.Code)
	}
	if requestCount != environmentListMaxPages {
		t.Fatalf("expected %d pages, got %d", environmentListMaxPages, requestCount)
	}
}

func TestShowUsesEnvironmentNameAndProjectEndpointFromState(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv(
		foundryProjectEndpointEnvVar,
		"https://other.services.ai.azure.com/api/projects/different-project",
	)
	if err := saveRleState(rleState{
		EnvironmentName: "echo_env",
		ProjectEndpoint: "https://account.services.ai.azure.com/api/projects/saved-project",
	}); err != nil {
		t.Fatal(err)
	}

	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == testFoundryProjectPath+environmentCollectionPath+"/echo_env":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"id":"env-1",
				"name":"echo_env",
				"version":"1.2.0",
				"diskImageConversionStatus":"Ready"
			}`))
		case r.Method == http.MethodGet &&
			r.URL.Path == testFoundryProjectPath+environmentCollectionPath+"/echo_env/versions":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[]}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer controlPlane.Close()
	oldCreateRleClient := createRleClient
	var resolvedProjectEndpoint string
	createRleClient = func(projectEndpoint string) (*rleClient, error) {
		resolvedProjectEndpoint = projectEndpoint
		return testRleClientForServer(t, controlPlane.URL), nil
	}
	t.Cleanup(func() {
		createRleClient = oldCreateRleClient
	})

	outputFormat := "default"
	command := newShowCommand(&outputFormat)
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "1.2.0") {
		t.Fatalf("expected API environment resolved from saved name, got %s", output.String())
	}
	if strings.Contains(output.String(), "echo_env") {
		t.Fatalf("expected environment name to be omitted from the version table, got %s", output.String())
	}
	if resolvedProjectEndpoint != "https://account.services.ai.azure.com/api/projects/saved-project" {
		t.Fatalf("expected saved project endpoint, got %q", resolvedProjectEndpoint)
	}
}

func TestResolveLatestEnvironmentByNameReportsNotFound(t *testing.T) {
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet ||
			r.URL.Path != testFoundryProjectPath+environmentCollectionPath+"/missing_env" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		http.NotFound(w, r)
	}))
	defer controlPlane.Close()

	client := testRleClientForServer(t, controlPlane.URL)
	_, err := resolveLatestEnvironmentByName(t.Context(), client, "missing_env")
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected LocalError, got %T: %v", err, err)
	}
	if localErr.Code != "rle_environment_not_found" {
		t.Fatalf("expected rle_environment_not_found, got %q", localErr.Code)
	}
}

func stubRleClientEndpoint(t *testing.T, endpoint string) {
	t.Helper()
	oldCreateRleClient := createRleClient
	oldValidateSandboxURL := validateSandboxURL
	validateSandboxURL = func(string, string) error {
		return nil
	}
	createRleClient = func(string) (*rleClient, error) {
		return testRleClientForServer(t, endpoint), nil
	}
	t.Cleanup(func() {
		createRleClient = oldCreateRleClient
		validateSandboxURL = oldValidateSandboxURL
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
