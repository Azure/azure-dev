// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

type testTokenCredential struct {
	mu     sync.Mutex
	scopes []string
}

func (c *testTokenCredential) GetToken(
	_ context.Context,
	options policy.TokenRequestOptions,
) (azcore.AccessToken, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
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
		if request.URL.RawQuery != "api-version=2025-11-15-preview&continuationToken=cursor" {
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
		environmentCollectionPath+"/echo/versions/1.0.0?continuationToken=cursor",
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

func TestServiceErrorUsesServiceFailureSuggestion(t *testing.T) {
	err := serviceError(newRleHTTPError(http.StatusInternalServerError, []byte(
		`{"code":"SandboxCreationFailed","message":"The sandbox could not be created."}`,
	)))
	serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
	if !ok {
		t.Fatalf("expected ServiceError, got %T", err)
	}
	if serviceErr.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", serviceErr.StatusCode)
	}
	if serviceErr.ErrorCode != "SandboxCreationFailed" {
		t.Fatalf("expected service error code, got %q", serviceErr.ErrorCode)
	}
	if serviceErr.Suggestion != "Retry later. If the problem persists, check the RLE service status and logs." {
		t.Fatalf("unexpected service failure suggestion: %q", serviceErr.Suggestion)
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

func TestListAllVersionTasksFollowsContinuationAndReportsPerTaskStatus(t *testing.T) {
	client := newRleClientWithCredential(
		"https://account.services.ai.azure.com/api/projects/project",
		&testTokenCredential{},
	)
	client.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(request.URL.Path, "/env/versions/1.0.0/tasks") {
			t.Fatalf("unexpected path %q", request.URL.Path)
		}

		body := `{"data":[{"name":"a","diskImageConversionStatus":"Ready"}],"nextContinuationToken":"tok"}`
		if request.URL.Query().Get("continuationToken") == "tok" {
			body = `{"data":[{"name":"b","diskImageConversionStatus":"Failed","diskImageConversionError":"boom"}]}`
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})

	tasks, err := listAllVersionTasks(t.Context(), client, "env", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 || tasks[0].DiskImageConversionStatus != "Ready" || tasks[1].DiskImageConversionError != "boom" {
		t.Fatalf("unexpected tasks: %+v", tasks)
	}
}

func TestListAllVersionTasksSafetyLimit(t *testing.T) {
	for _, complete := range []bool{false, true} {
		t.Run(fmt.Sprintf("complete=%t", complete), func(t *testing.T) {
			client := newRleClientWithCredential(
				"https://account.services.ai.azure.com/api/projects/project",
				&testTokenCredential{},
			)
			calls := 0
			client.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
				calls++
				token := fmt.Sprintf("cursor-%d", calls)
				if complete && calls == environmentListMaxPages {
					token = ""
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body: io.NopCloser(strings.NewReader(fmt.Sprintf(
						`{"data":[{"name":"task-%d"}],"nextContinuationToken":%q}`, calls, token))),
				}, nil
			})
			tasks, err := listAllVersionTasks(t.Context(), client, "env", "1.0.0")
			if calls != environmentListMaxPages {
				t.Fatalf("expected %d requests, got %d", environmentListMaxPages, calls)
			}
			if complete {
				if err != nil || len(tasks) != environmentListMaxPages {
					t.Fatalf("expected all tasks from the final page, got %d tasks and %v", len(tasks), err)
				}
				return
			}
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			if !ok || localErr.Code != "rle_environment_task_list_safety_limit" || tasks != nil {
				t.Fatalf("expected a safety-limit error without partial tasks, got tasks=%v, err=%v", tasks, err)
			}
		})
	}
}
