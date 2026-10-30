// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func TestFinetuneCancelJobRequest(t *testing.T) {
	for _, jobID := range []string{realMonitorJobID, "ftjob-" + strings.Repeat("a", 32)} {
		t.Run(jobID, func(t *testing.T) {
			credential := &testTokenCredential{}
			client := newFinetuneClientWithCredential("https://account.openai.azure.com", credential)
			calls := 0
			type contextKey struct{}
			ctx := context.WithValue(t.Context(), contextKey{}, "cancel-request")
			client.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
				calls++
				if request.Method != http.MethodPost ||
					request.URL.Path != finetuneJobsPath+"/"+jobID+"/cancel" {
					t.Fatalf("unexpected cancel request: %s %s", request.Method, request.URL.Path)
				}
				if request.Context().Value(contextKey{}) != "cancel-request" {
					t.Fatal("expected the caller's context on the cancel request")
				}
				if request.URL.RawQuery != "" || request.Body != nil {
					t.Fatal("expected a bodyless v1 request without query parameters")
				}
				if request.Header.Get("Authorization") != "Bearer test-token" ||
					request.Header.Get("Accept") != "application/json" {
					t.Fatal("expected an authenticated JSON request")
				}
				if request.Header.Get("azureai-project") != "project" ||
					request.Header.Get("azureai-project-is-default") != "true" {
					t.Fatal("expected the same Foundry project routing as train and jobs")
				}
				if request.Header.Get(executeRolloutHeader) != "" ||
					request.Header.Get(rleEnvOverrideHeader) != "" {
					t.Fatal("cancel must not send training-only headers")
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body: io.NopCloser(strings.NewReader(
						fmt.Sprintf(`{"id":%q,"status":"cancelled","model":"base-model"}`, jobID))),
				}, nil
			})
			job, err := client.cancelJob(ctx, jobID, "project")
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 || job.Id != jobID || job.Status != "cancelled" || job.Model != "base-model" {
				t.Fatalf("unexpected cancel result: calls=%d job=%#v", calls, job)
			}
			if !slices.Equal(credential.scopes, []string{finetuneTokenScope}) {
				t.Fatalf("expected the Cognitive Services token scope, got %v", credential.scopes)
			}
		})
	}
}

func TestCancelCommandEndpointAndOutput(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		format   string
		status   string
	}{
		{
			name: "Foundry account default", endpoint: "https://account.openai.azure.com",
			format: "default", status: "cancelled",
		},
		{
			name:     "Foundry account JSON",
			endpoint: "https://account.openai.azure.com", format: "json", status: "cancelled",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv(foundryProjectEndpointEnvVar,
				"https://account.services.ai.azure.com/api/projects/project")
			oldCreate := createFinetuneClient
			t.Cleanup(func() { createFinetuneClient = oldCreate })
			calls := 0
			createFinetuneClient = func(endpoint string) (*finetuneClient, error) {
				if endpoint != test.endpoint {
					t.Fatalf("expected endpoint %q, got %q", test.endpoint, endpoint)
				}
				client := newFinetuneClientWithCredential(endpoint, &testTokenCredential{})
				client.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
					calls++
					if request.Method != http.MethodPost ||
						request.URL.Path != finetuneJobsPath+"/"+realMonitorJobID+"/cancel" {
						t.Fatalf("unexpected request: %s %s", request.Method, request.URL.Path)
					}
					return &http.Response{
						StatusCode: http.StatusOK, Header: make(http.Header),
						Body: io.NopCloser(strings.NewReader(
							fmt.Sprintf(`{"id":%q,"status":%q}`, realMonitorJobID, test.status))),
					}, nil
				})
				return client, nil
			}
			updateChecks := 0
			command := newRootCommand(extensionUpdateCheckerFunc(func(context.Context, string) (*extensionUpdate, error) {
				updateChecks++
				return &extensionUpdate{LatestVersion: "99.0.0", IsBreaking: true, Source: "rle"}, nil
			}))
			args := []string{"jobs", "cancel", " " + realMonitorJobID + " ", "--output", test.format, "--no-prompt"}
			command.SetArgs(args)
			var output, errorOutput bytes.Buffer
			command.SetOut(&output)
			command.SetErr(&errorOutput)
			if err := command.Execute(); err != nil {
				t.Fatal(err)
			}
			if updateChecks != 0 {
				t.Fatalf("job cancellation must bypass update checks, got %d checks", updateChecks)
			}
			if calls != 1 || errorOutput.Len() != 0 {
				t.Fatalf("expected one cancel request and no diagnostics: calls=%d stderr=%s", calls, errorOutput.String())
			}
			if test.format == "json" {
				var job finetuneJobResource
				if err := json.Unmarshal(output.Bytes(), &job); err != nil {
					t.Fatalf("expected only JSON output, got %q: %v", output.String(), err)
				}
				if job.Id != realMonitorJobID || job.Status != test.status {
					t.Fatalf("unexpected JSON job: %#v", job)
				}
			} else {
				expected := fmt.Sprintf(
					"Cancellation requested for fine-tuning job %s (status=%s).\n", realMonitorJobID, test.status)
				if output.String() != expected {
					t.Fatalf("expected %q, got %q", expected, output.String())
				}
			}
		})
	}
}

func TestCancelCommandRejectsInvalidInputsBeforeCreatingClient(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		project string
		format  string
		code    string
	}{
		{name: "missing job ID", code: "rle_cancel_job_id_required"},
		{name: "empty job ID", args: []string{" "}, code: "rle_cancel_job_id_required"},
		{name: "invalid job ID", args: []string{"ftjob-invalid"}, code: "rle_invalid_job_id"},
		{name: "path traversal", args: []string{"../cancel"}, code: "rle_invalid_job_id"},
		{name: "missing project", args: []string{realMonitorJobID}, code: "rle_project_required"},
		{
			name: "invalid project", args: []string{realMonitorJobID},
			project: "https://wrong.example.com", code: "rle_invalid_project_endpoint",
		},
		{
			name: "HTTP project", args: []string{realMonitorJobID},
			project: "http://account.services.ai.azure.com/api/projects/project", code: "rle_invalid_project_endpoint",
		},
		{name: "endpoint flag removed", args: []string{realMonitorJobID, "--endpoint", "https://x.example.com"}},
		{name: "multiple job IDs", args: []string{realMonitorJobID, realMonitorJobID}},
		{name: "unsupported output", args: []string{realMonitorJobID}, format: "table"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(foundryProjectEndpointEnvVar, test.project)
			oldCreate := createFinetuneClient
			t.Cleanup(func() { createFinetuneClient = oldCreate })
			createFinetuneClient = func(string) (*finetuneClient, error) {
				t.Fatal("invalid inputs must not create a client")
				return nil, errors.New("unexpected client creation")
			}
			command := newCancelCommand(&test.format)
			command.SetArgs(test.args)
			command.SetOut(io.Discard)
			command.SetErr(io.Discard)
			err := command.Execute()
			if err == nil {
				t.Fatal("expected invalid inputs to fail")
			}
			if test.code != "" {
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				if !ok || localErr.Code != test.code {
					t.Fatalf("expected LocalError %q, got %v", test.code, err)
				}
			}
		})
	}
}

func TestCancelCommandReportsServiceErrors(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 409, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			t.Setenv(foundryProjectEndpointEnvVar,
				"https://account.services.ai.azure.com/api/projects/project")
			oldCreate := createFinetuneClient
			t.Cleanup(func() { createFinetuneClient = oldCreate })
			createFinetuneClient = func(endpoint string) (*finetuneClient, error) {
				client := newFinetuneClientWithCredential(endpoint, &testTokenCredential{})
				client.httpClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: status,
						Header:     http.Header{"Apim-Request-Id": []string{"cancel-request-id"}},
						Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"cannot cancel job"}}`)),
					}, nil
				})
				return client, nil
			}
			format := "json"
			command := newCancelCommand(&format)
			command.SilenceUsage = true
			command.SetArgs([]string{realMonitorJobID})
			var output bytes.Buffer
			command.SetOut(&output)
			command.SetErr(io.Discard)
			err := command.Execute()
			serviceErr, ok := errors.AsType[*azdext.ServiceError](err)
			if !ok || serviceErr.StatusCode != status || serviceErr.ServiceName != "finetunesapi" {
				t.Fatalf("expected structured HTTP %d service error, got %v", status, err)
			}
			if !strings.Contains(serviceErr.Message, "cancel-request-id") ||
				!strings.Contains(serviceErr.Message, "cannot cancel job") || serviceErr.Suggestion == "" {
				t.Fatalf("expected service detail, request ID and suggestion, got %#v", serviceErr)
			}
			if strings.Contains(serviceErr.Suggestion, "base model") || output.Len() != 0 {
				t.Fatalf("unexpected submission guidance or success output: %#v, stdout=%s", serviceErr, output.String())
			}
		})
	}
}

func TestFinetuneCancelRejectsInvalidResponses(t *testing.T) {
	for _, body := range []string{
		"", "{}", "null", "invalid JSON",
		fmt.Sprintf(`{"id":%q}`, realMonitorJobID),
		`{"id":"another-job","status":"cancelled"}`,
		fmt.Sprintf(`{"id":%q,"status":" "}`, realMonitorJobID),
	} {
		t.Run(body, func(t *testing.T) {
			client := newFinetuneClientWithCredential("https://account.openai.azure.com", &testTokenCredential{})
			client.httpClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK, Header: make(http.Header),
					Body: io.NopCloser(strings.NewReader(body)),
				}, nil
			})
			if job, err := client.cancelJob(t.Context(), realMonitorJobID, "project"); err == nil || job != nil {
				t.Fatalf("expected invalid cancel response to fail, got job=%#v err=%v", job, err)
			}
		})
	}
}

func TestFinetuneCancelPropagatesContextCancellation(t *testing.T) {
	client := newFinetuneClientWithCredential("https://account.openai.azure.com", &testTokenCredential{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	client.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return nil, request.Context().Err()
	})
	if _, err := client.cancelJob(ctx, realMonitorJobID, "project"); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}

func TestCancelEndpointDoesNotDiscloseURLCredentials(t *testing.T) {
	//nolint:gosec // Dummy URL credentials exercise redaction; no real credentials are used.
	raw := "https://private-user:private-password@account.openai.azure.com/?sig=private-signature#private-fragment"
	endpoint, err := normalizeFinetuneEndpoint(raw)
	if err != nil {
		t.Fatal(err)
	}
	if endpoint != "https://account.openai.azure.com" {
		t.Fatalf("expected a sanitized endpoint, got %q", endpoint)
	}
	client := newFinetuneClientWithCredential(endpoint, &testTokenCredential{})
	client.httpClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connection failed")
	})
	_, err = client.cancelJob(t.Context(), realMonitorJobID, "project")
	if err == nil {
		t.Fatal("expected transport failure")
	}
	for _, secret := range []string{"private-user", "private-password", "private-signature", "private-fragment"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error disclosed URL credentials: %v", err)
		}
	}
	_, err = normalizeFinetuneEndpoint(raw + "%invalid")
	if err == nil || strings.Contains(err.Error(), "private-") {
		t.Fatalf("expected a redacted invalid URL error, got %v", err)
	}
}
