// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func TestBuildFinetuneJobRequestUsesRleEnvironmentMethod(t *testing.T) {
	flags := &rleTrainFlags{
		rleName:    "code_rl",
		rleVersion: "1.0.0",
		model:      "Qwen/Qwen3-32B",
	}

	request := buildFinetuneJobRequest(flags)

	if request.Model != "Qwen/Qwen3-32B" {
		t.Fatalf("expected model to map from flags, got %q", request.Model)
	}
	if request.TrainingFile != "" {
		t.Fatalf("expected no training file by default, got %q", request.TrainingFile)
	}
	if request.Method == nil || request.Method.Type != finetuneMethodTypeRleEnvironment {
		t.Fatalf("expected rl_environment method, got %#v", request.Method)
	}
	if request.Method.RleEnvironment.Name != "code_rl" || request.Method.RleEnvironment.Version != "1.0.0" {
		t.Fatalf("expected rl_environment name/version from flags, got %#v", request.Method.RleEnvironment)
	}
	if request.Method.RleEnvironment.MaxEpisodeSteps != nil {
		t.Fatal("expected max_episode_steps to be omitted when not provided")
	}
	if request.ValidationFile != nil || request.Suffix != nil {
		t.Fatal("expected optional fields to be omitted when not provided")
	}

	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "training_file") {
		t.Fatalf("expected training_file to be omitted from JSON when empty, got %s", data)
	}
}

func TestBuildFinetuneJobRequestIncludesOptionalFields(t *testing.T) {
	flags := &rleTrainFlags{
		rleName:         "code_rl",
		rleVersion:      "1.0.0",
		model:           "Qwen/Qwen3-32B",
		trainingFile:    "file-abc",
		validationFile:  "file-def",
		suffix:          "custom-suffix",
		maxEpisodeSteps: 32,
	}

	request := buildFinetuneJobRequest(flags)

	if request.TrainingFile != "file-abc" {
		t.Fatalf("expected training_file to be set, got %q", request.TrainingFile)
	}
	if request.ValidationFile == nil || *request.ValidationFile != "file-def" {
		t.Fatalf("expected validation_file to be set, got %#v", request.ValidationFile)
	}
	if request.Suffix == nil || *request.Suffix != "custom-suffix" {
		t.Fatalf("expected suffix to be set, got %#v", request.Suffix)
	}
	if request.Method.RleEnvironment.MaxEpisodeSteps == nil || *request.Method.RleEnvironment.MaxEpisodeSteps != 32 {
		t.Fatalf("expected max_episode_steps to be set, got %#v", request.Method.RleEnvironment.MaxEpisodeSteps)
	}
}

func TestResolveFinetuneEndpointPrefersFlagOverEnvVar(t *testing.T) {
	t.Setenv(finetuneEndpointEnvVar, "https://from-env.openai.azure.com")

	endpoint, err := resolveFinetuneEndpoint("https://from-flag.openai.azure.com")
	if err != nil {
		t.Fatal(err)
	}
	if endpoint != "https://from-flag.openai.azure.com" {
		t.Fatalf("expected flag value to take precedence, got %q", endpoint)
	}
}

func TestResolveFinetuneEndpointFallsBackToEnvVar(t *testing.T) {
	t.Setenv(finetuneEndpointEnvVar, "https://from-env.openai.azure.com/")

	endpoint, err := resolveFinetuneEndpoint("")
	if err != nil {
		t.Fatal(err)
	}
	if endpoint != "https://from-env.openai.azure.com" {
		t.Fatalf("expected normalized env var endpoint, got %q", endpoint)
	}
}

func TestResolveFinetuneEndpointRequiresValue(t *testing.T) {
	t.Setenv(finetuneEndpointEnvVar, "")

	_, err := resolveFinetuneEndpoint("")
	if err == nil || !strings.Contains(err.Error(), "A fine-tuning API endpoint is required") {
		t.Fatalf("expected missing endpoint error, got %v", err)
	}
}

func TestNormalizeFinetuneEndpointRejectsNonHTTPS(t *testing.T) {
	_, err := normalizeFinetuneEndpoint("http://resource.openai.azure.com")
	if err == nil || !strings.Contains(err.Error(), "must use https") {
		t.Fatalf("expected https-only error, got %v", err)
	}
}

func TestFinetuneClientSendsProjectHeadersAndAuthenticates(t *testing.T) {
	credential := &testTokenCredential{}
	client := newFinetuneClientWithCredential("https://resource.openai.azure.com", credential)
	client.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if got := request.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("expected bearer token, got %q", got)
		}
		if got := request.URL.Path; got != finetuneJobsPath {
			t.Fatalf("expected path %q, got %q", finetuneJobsPath, got)
		}
		if got := request.URL.Query().Get("api-version"); got != "" {
			t.Fatalf("expected no api-version query param, got %q", got)
		}
		if got := request.Header.Get("azureai-project"); got != "myproject" {
			t.Fatalf("expected azureai-project header, got %q", got)
		}
		if got := request.Header.Get("azureai-project-is-default"); got != "true" {
			t.Fatalf("expected azureai-project-is-default=true, got %q", got)
		}
		return &http.Response{
			StatusCode: http.StatusCreated,
			Body:       io.NopCloser(strings.NewReader(`{"id":"ftjob-1","status":"queued"}`)),
			Header:     make(http.Header),
		}, nil
	})

	job, err := client.createJob(t.Context(), finetuneJobCreationRequest{}, "myproject")
	if err != nil {
		t.Fatal(err)
	}
	if job.Id != "ftjob-1" || job.Status != "queued" {
		t.Fatalf("expected decoded job response, got %#v", job)
	}
	if len(credential.scopes) != 1 || credential.scopes[0] != finetuneTokenScope {
		t.Fatalf("expected fine-tuning token scope %q, got %v", finetuneTokenScope, credential.scopes)
	}
}

func TestFinetuneClientSurfacesHTTPErrors(t *testing.T) {
	client := newFinetuneClientWithCredential("https://resource.openai.azure.com", &testTokenCredential{})
	client.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"InvalidPayload"}}`)),
			Header:     make(http.Header),
		}, nil
	})

	_, err := client.createJob(t.Context(), finetuneJobCreationRequest{}, "myproject")
	if err == nil {
		t.Fatal("expected an error for HTTP 400")
	}
	wrapped := finetuneServiceError(err)
	serviceErr, ok := wrapped.(*azdext.ServiceError)
	if !ok {
		t.Fatalf("expected a *azdext.ServiceError, got %T", wrapped)
	}
	if serviceErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected status code %d, got %d", http.StatusBadRequest, serviceErr.StatusCode)
	}
	if !strings.Contains(serviceErr.Suggestion, "Loom-eligible") {
		t.Fatalf("expected Loom-eligibility guidance in suggestion, got %v", serviceErr.Suggestion)
	}
}
