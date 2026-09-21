// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
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

	request := buildFinetuneJobRequest(flags, "file-training", "")

	if request.Model != "Qwen/Qwen3-32B" {
		t.Fatalf("expected model to map from flags, got %q", request.Model)
	}
	if request.TrainingFile != "file-training" {
		t.Fatalf("expected training_file to be set, got %q", request.TrainingFile)
	}
	if request.TrainingType != finetuneTrainingTypeGlobalStandard {
		t.Fatalf("expected GlobalStandard training type, got %d", request.TrainingType)
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
	if !strings.Contains(string(data), `"training_file":"file-training"`) {
		t.Fatalf("expected training_file in JSON, got %s", data)
	}
	if !strings.Contains(string(data), `"trainingType":1`) {
		t.Fatalf("expected GlobalStandard trainingType in JSON, got %s", data)
	}
}

func TestBuildFinetuneJobRequestIncludesOptionalFields(t *testing.T) {
	flags := &rleTrainFlags{
		rleName:         "code_rl",
		rleVersion:      "1.0.0",
		model:           "Qwen/Qwen3-32B",
		suffix:          "custom-suffix",
		maxEpisodeSteps: 32,
	}

	request := buildFinetuneJobRequest(flags, "file-abc", "file-def")

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

func TestTrainCommandRequiresTrainingFile(t *testing.T) {
	cmd := newTrainCommand()
	cmd.SetArgs([]string{
		"--rle-name", "code_rl",
		"--rle-version", "1.0.0",
		"--model", "Qwen/Qwen3-32B",
	})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `required flag(s) "training-file" not set`) {
		t.Fatalf("expected missing training-file error, got %v", err)
	}
}

func TestResolveLocalFilePath(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "training.jsonl")
	if err := os.WriteFile(filePath, []byte("{\"input\":\"example\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		raw      string
		required bool
		want     string
		wantCode string
	}{
		{name: "trims valid local path", raw: " " + filePath + " ", required: true, want: filePath},
		{name: "allows optional empty path", raw: "  ", required: false, want: ""},
		{name: "rejects missing file", raw: filepath.Join(t.TempDir(), "missing.jsonl"), required: true, wantCode: "rle_train_file_unavailable"},
		{name: "rejects directory", raw: t.TempDir(), required: true, wantCode: "rle_train_file_not_regular"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolveLocalFilePath(test.raw, "training", test.required)
			if test.wantCode != "" {
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				if !ok {
					t.Fatalf("expected LocalError, got %T", err)
				}
				if localErr.Code != test.wantCode {
					t.Fatalf("expected error code %q, got %q", test.wantCode, localErr.Code)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("expected %q, got %q", test.want, got)
			}
		})
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

func TestTrainActionUploadsLocalFileBeforeSubmittingJob(t *testing.T) {
	trainingFilePath := filepath.Join(t.TempDir(), "training.jsonl")
	if err := os.WriteFile(trainingFilePath, []byte("{\"input\":\"example\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(foundryProjectEndpointEnvVar, "https://account.services.ai.azure.com/api/projects/project")

	client := newFinetuneClientWithCredential("https://resource.openai.azure.com", &testTokenCredential{})
	uploadCount := 0
	client.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case finetuneFilesPath:
			uploadCount++
			if _, err := io.ReadAll(request.Body); err != nil {
				t.Fatal(err)
			}
			return &http.Response{
				StatusCode: http.StatusCreated,
				Body:       io.NopCloser(strings.NewReader(`{"id":"file-training"}`)),
				Header:     make(http.Header),
			}, nil
		case finetuneJobsPath:
			if uploadCount != 1 {
				t.Fatalf("expected file upload before job creation, got %d uploads", uploadCount)
			}
			var jobRequest finetuneJobCreationRequest
			if err := json.NewDecoder(request.Body).Decode(&jobRequest); err != nil {
				t.Fatal(err)
			}
			if jobRequest.TrainingFile != "file-training" {
				t.Fatalf("expected uploaded training file ID, got %q", jobRequest.TrainingFile)
			}
			if jobRequest.Method == nil || jobRequest.Method.RleEnvironment.Name != "code_rl" {
				t.Fatalf("expected RLE request, got %#v", jobRequest.Method)
			}
			return &http.Response{
				StatusCode: http.StatusCreated,
				Body:       io.NopCloser(strings.NewReader(`{"id":"ftjob-1","status":"queued"}`)),
				Header:     make(http.Header),
			}, nil
		default:
			t.Fatalf("unexpected request path %q", request.URL.Path)
			return nil, nil
		}
	})

	originalCreateClient := createFinetuneClient
	createFinetuneClient = func(endpoint string) (*finetuneClient, error) {
		if endpoint != "https://resource.openai.azure.com" {
			t.Fatalf("expected configured endpoint, got %q", endpoint)
		}
		return client, nil
	}
	t.Cleanup(func() {
		createFinetuneClient = originalCreateClient
	})

	command := newTrainCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	action := &trainAction{
		cmd: command,
		flags: &rleTrainFlags{
			rleName:      "code_rl",
			rleVersion:   "1.0.0",
			model:        "Qwen/Qwen3-32B",
			trainingFile: trainingFilePath,
			endpoint:     "https://resource.openai.azure.com",
		},
	}

	if err := action.Run(); err != nil {
		t.Fatal(err)
	}
	if uploadCount != 1 {
		t.Fatalf("expected one uploaded file, got %d", uploadCount)
	}
	if !strings.Contains(output.String(), "Uploaded training file as file-training.") {
		t.Fatalf("expected upload progress output, got %q", output.String())
	}
	if !strings.Contains(output.String(), "Submitted fine-tuning job ftjob-1") {
		t.Fatalf("expected job output, got %q", output.String())
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
	serviceErr, ok := errors.AsType[*azdext.ServiceError](wrapped)
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
