// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

type testTokenCredential struct {
	scopes []string
}

func (c *testTokenCredential) GetToken(
	_ context.Context,
	options policy.TokenRequestOptions,
) (azcore.AccessToken, error) {
	c.scopes = options.Scopes
	return azcore.AccessToken{Token: "test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestFoundryAPIVersionValue(t *testing.T) {
	t.Parallel()

	if foundryAPIVersion != "2025-11-15-preview" {
		t.Fatalf("expected Foundry API version %q, got %q", "2025-11-15-preview", foundryAPIVersion)
	}
}

func TestRleClientAuthenticatesFoundryRequests(t *testing.T) {
	credential := &testTokenCredential{}
	client := newRleClientWithCredential(
		"https://account.services.ai.azure.com/api/projects/project",
		credential,
	)
	client.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if got := request.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("expected bearer token, got %q", got)
		}
		if got := request.URL.Query().Get("api-version"); got != foundryAPIVersion {
			t.Fatalf("expected API version %q, got %q", foundryAPIVersion, got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("{}")),
			Header:     make(http.Header),
		}, nil
	})

	if err := client.do(t.Context(), http.MethodGet, environmentCollectionPath, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(credential.scopes) != 1 || credential.scopes[0] != foundryTokenScope {
		t.Fatalf("expected Foundry token scope %q, got %v", foundryTokenScope, credential.scopes)
	}
}

func TestRleClientRefusesAuthenticationOverHTTP(t *testing.T) {
	client := newRleClientWithCredential(
		"http://localhost:5000",
		&testTokenCredential{},
	)

	err := client.do(t.Context(), http.MethodGet, environmentCollectionPath, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "requires an HTTPS") {
		t.Fatalf("expected HTTPS authentication error, got %v", err)
	}
}

func TestRleClientPreservesEnvironmentRequest(t *testing.T) {
	client := newRleClientWithCredential(
		"https://account.services.ai.azure.com/api/projects/project",
		&testTokenCredential{},
	)
	client.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if request.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", request.Method)
		}
		if request.URL.RawQuery != "after=cursor&api-version=2025-11-15-preview" {
			t.Fatalf("expected unchanged query and API version, got %q", request.URL.RawQuery)
		}
		if string(body) != `{"value":"same-body"}` {
			t.Fatalf("expected unchanged request body, got %s", body)
		}

		expectedPath := environmentCollectionPath + "/echo/versions/1.0.0"
		if request.URL.Path != "/api/projects/project"+expectedPath {
			t.Fatalf("expected path %q, got %q", expectedPath, request.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusCreated,
			Body:       io.NopCloser(strings.NewReader("{}")),
			Header:     make(http.Header),
		}, nil
	})

	err := client.do(
		t.Context(),
		http.MethodPost,
		environmentCollectionPath+"/echo/versions/1.0.0?after=cursor",
		map[string]string{"value": "same-body"},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestCreateInstanceGroupUsesPublicVersionedRoute(t *testing.T) {
	client := newRleClientWithCredential(
		"https://account.services.ai.azure.com/api/projects/project",
		&testTokenCredential{},
	)
	client.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", request.Method)
		}
		expectedPath := "/api/projects/project/rl_environments/code_rl/versions/1.0.0/instance_groups"
		if request.URL.Path != expectedPath {
			t.Fatalf("expected path %q, got %q", expectedPath, request.URL.Path)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != `{"maxActiveInstances":1}` {
			t.Fatalf("expected one-instance group request, got %s", body)
		}
		return &http.Response{
			StatusCode: http.StatusCreated,
			Body: io.NopCloser(strings.NewReader(
				`{"id":"group-1","environmentName":"code_rl","environmentVersion":"1.0.0","maxActiveInstances":1}`,
			)),
			Header: make(http.Header),
		}, nil
	})

	group, err := client.createInstanceGroup(t.Context(), "code_rl", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if group.Id != "group-1" || group.EnvironmentVersion != "1.0.0" {
		t.Fatalf("unexpected instance group: %#v", group)
	}
}

func TestNewRleClientRejectsUntrustedEndpoint(t *testing.T) {
	_, err := newRleClient("https://attacker.example/api/projects/project-1")
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected LocalError, got %T", err)
	}
	if localErr.Code != "rle_invalid_project_endpoint" {
		t.Fatalf("expected invalid endpoint code, got %q", localErr.Code)
	}
}

func TestServiceErrorSuggestionShowsFoundryProjectEndpoint(t *testing.T) {
	err := serviceError(errors.New("dial tcp failed"))
	serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
	if !ok {
		t.Fatalf("expected ServiceError, got %T", err)
	}

	for _, expected := range []string{
		"Ensure the Foundry project endpoint",
		foundryProjectEndpointEnvVar,
		"enabled for RLE",
	} {
		if !strings.Contains(serviceErr.Suggestion, expected) {
			t.Fatalf("expected suggestion to contain %q, got %q", expected, serviceErr.Suggestion)
		}
	}
}

func TestResolvePublishStateUsesFoundryProjectEndpointEnvironment(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv(foundryProjectEndpointEnvVar, "https://ACCOUNT.services.ai.azure.com/api/projects/project-from-env/")

	state, initialized, err := resolvePublishState()
	if err != nil {
		t.Fatal(err)
	}
	if initialized {
		t.Fatal("expected no saved state")
	}
	if state.ProjectEndpoint != "https://account.services.ai.azure.com/api/projects/project-from-env" {
		t.Fatalf("expected normalized project endpoint, got %q", state.ProjectEndpoint)
	}
}

func TestResolvePublishStateUsesSavedProjectEndpointFallback(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	if err := saveRleState(rleState{
		EnvironmentName: "saved-env",
		ProjectEndpoint: "https://account.services.ai.azure.com/api/projects/saved-project",
	}); err != nil {
		t.Fatal(err)
	}

	state, initialized, err := resolvePublishState()
	if err != nil {
		t.Fatal(err)
	}
	if !initialized {
		t.Fatal("expected saved state")
	}
	if state.ProjectEndpoint != "https://account.services.ai.azure.com/api/projects/saved-project" {
		t.Fatalf("expected saved project endpoint fallback, got %q", state.ProjectEndpoint)
	}
}

func TestProjectNameFromFoundryEndpoint(t *testing.T) {
	projectName, err := projectNameFromFoundryEndpoint(
		"https://account.services.ai.azure.com/api/projects/my-project",
	)
	if err != nil {
		t.Fatal(err)
	}
	if projectName != "my-project" {
		t.Fatalf("expected project name from endpoint, got %q", projectName)
	}
}

func TestProjectEndpointRequiresProjectPath(t *testing.T) {
	_, err := normalizeFoundryProjectEndpoint("https://account.services.ai.azure.com/api/not-projects/my-project")
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected LocalError, got %T", err)
	}
	if localErr.Code != "rle_invalid_project_endpoint" {
		t.Fatalf("expected invalid endpoint code, got %q", localErr.Code)
	}
}
