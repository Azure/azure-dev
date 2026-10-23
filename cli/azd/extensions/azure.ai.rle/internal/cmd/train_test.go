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
	"math"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"azure.ai.rle/internal/monitor"
	"azure.ai.rle/internal/project"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/require"
)

func TestBuildFinetuneJobRequestUsesRleEnvironmentMethod(t *testing.T) {
	flags := &rleTrainFlags{
		rleName:    "code_rl",
		rleVersion: "1.0.0",
		model:      "Qwen/Qwen3-32B",
	}

	resolved, err := resolveFinetuneRleConfig(validTrainConfig("code_rl", "1.0.0"))
	require.NoError(t, err)
	request := buildFinetuneJobRequest(flags, "file-training", "", resolved)

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
		rleName:    "code_rl",
		rleVersion: "1.0.0",
		model:      "Qwen/Qwen3-32B",
		suffix:     "custom-suffix",
	}

	resolved, err := resolveFinetuneRleConfig(validTrainConfig("code_rl", "1.0.0"))
	require.NoError(t, err)
	request := buildFinetuneJobRequest(flags, "file-abc", "file-def", resolved)

	if request.TrainingFile != "file-abc" {
		t.Fatalf("expected training_file to be set, got %q", request.TrainingFile)
	}
	if request.ValidationFile == nil || *request.ValidationFile != "file-def" {
		t.Fatalf("expected validation_file to be set, got %#v", request.ValidationFile)
	}
	if request.Suffix == nil || *request.Suffix != "custom-suffix" {
		t.Fatalf("expected suffix to be set, got %#v", request.Suffix)
	}
}

func TestTrainCommandRequiresTrainingFile(t *testing.T) {
	// --training-file is not marked required on the cobra command (cobra would
	// reject the run before the manifest read is ever attempted),
	// but resolveTrainSettings still requires it and the error has to name it.
	useRleManifestDir(t)
	cmd := newTrainCommand()
	cmd.SetArgs([]string{
		"--model", "Qwen/Qwen3-32B",
	})
	cmd.SilenceUsage = true
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err := cmd.Execute()
	var localErr *azdext.LocalError
	if !errors.As(err, &localErr) || localErr.Code != "rle_train_setting_required" {
		t.Fatalf("expected a missing training file error, got %v", err)
	}
	if !strings.Contains(localErr.Message, "training file") {
		t.Fatalf("the error should name the training file: %q", localErr.Message)
	}
}

func TestTrainRequiresRleManifestEvenWithExplicitIdentity(t *testing.T) {
	t.Chdir(t.TempDir())
	cmd := newTrainCommand()
	cmd.SetArgs([]string{
		"--model", "Qwen/Qwen3-32B",
		"--training-file", "training.jsonl",
	})
	cmd.SilenceUsage = true
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err := cmd.Execute()
	var localErr *azdext.LocalError
	if !errors.As(err, &localErr) || localErr.Code != "rle_manifest_missing" {
		t.Fatalf("expected a missing rle.toml error, got %v", err)
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

func TestFinetuneEndpointDerivesAccountFromFoundryProject(t *testing.T) {
	endpoint, err := finetuneEndpointFromFoundryProject(
		"https://My-Account.services.ai.azure.com/api/projects/project",
	)
	if err != nil {
		t.Fatal(err)
	}
	if endpoint != "https://my-account.openai.azure.com" {
		t.Fatalf("expected endpoint derived from the Foundry account, got %q", endpoint)
	}
}

func TestFinetuneEndpointRejectsInvalidFoundryHost(t *testing.T) {
	_, err := finetuneEndpointFromFoundryProject("https://example.com/api/projects/project")
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok || localErr.Code != "rle_invalid_project_endpoint" {
		t.Fatalf("expected invalid project endpoint error, got %v", err)
	}
}

func TestNormalizeFinetuneEndpointRejectsNonHTTPS(t *testing.T) {
	_, err := normalizeFinetuneEndpoint("http://resource.openai.azure.com")
	if err == nil || !strings.Contains(err.Error(), "must use https") {
		t.Fatalf("expected https-only error, got %v", err)
	}
}

func TestFinetuneClientSendsProjectHeadersAndAuthenticates(t *testing.T) {
	for _, wantOverride := range []string{"", "math_rl:1.0.8"} {
		t.Run("override="+wantOverride, func(t *testing.T) {
			t.Setenv(rleEnvOverrideEnvVar, wantOverride)
			testFinetuneClientHeaders(t, wantOverride)
		})
	}
}

func testFinetuneClientHeaders(t *testing.T, wantOverride string) {
	credential := &finetuneSubmissionCredential{}
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
		if request.Header.Get(executeRolloutHeader) != "foundry-user-token" {
			t.Fatal("expected the separate Foundry user token in aml-user-token")
		}
		if got := request.Header.Get(rleEnvOverrideHeader); got != wantOverride {
			t.Fatalf("expected %s=%q, got %q", rleEnvOverrideHeader, wantOverride, got)
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
	if len(credential.scopes) != 2 || credential.scopes[0] != foundryTokenScope ||
		credential.scopes[1] != finetuneTokenScope {
		t.Fatalf("expected fine-tuning token scope %q, got %v", finetuneTokenScope, credential.scopes)
	}
}

type finetuneSubmissionCredential struct {
	scopes []string
	err    error
	empty  bool
}

func (c *finetuneSubmissionCredential) GetToken(
	_ context.Context, options policy.TokenRequestOptions,
) (azcore.AccessToken, error) {
	c.scopes = append(c.scopes, options.Scopes...)
	if options.Scopes[0] == foundryTokenScope {
		if c.err != nil || c.empty {
			return azcore.AccessToken{}, c.err
		}
		return azcore.AccessToken{Token: "foundry-user-token", ExpiresOn: time.Now().Add(time.Hour)}, nil // #nosec G101 -- Fake token used only by the test credential.
	}
	return azcore.AccessToken{Token: "test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func TestFinetuneSubmissionStopsWhenFoundryTokenUnavailable(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(fmt.Sprintf("empty=%v", empty), func(t *testing.T) {
			credential := &finetuneSubmissionCredential{empty: empty}
			if !empty {
				credential.err = errors.New("credential unavailable")
			}
			client := newFinetuneClientWithCredential("https://resource.openai.azure.com", credential)
			client.httpClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("job must not be submitted without a Foundry user token")
				return nil, errors.New("unexpected submission")
			})
			if _, err := client.createJob(t.Context(), finetuneJobCreationRequest{}, "project"); err == nil {
				t.Fatal("expected token acquisition error")
			}
		})
	}
}

func TestTrainActionUploadsLocalFileBeforeSubmittingJob(t *testing.T) {
	useTrainManifestDir(t)
	stubAPIJobMonitor(t)
	trainingFilePath := filepath.Join(t.TempDir(), "training.jsonl")
	if err := os.WriteFile(trainingFilePath, []byte("{\"input\":\"example\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(foundryProjectEndpointEnvVar, "https://account.services.ai.azure.com/api/projects/project")

	client := newFinetuneClientWithCredential("https://account.openai.azure.com", &testTokenCredential{})
	uploadCount := 0
	client.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case finetuneFilesPath:
			if request.Header.Get(executeRolloutHeader) != "" {
				t.Fatal("file uploads must not forward the evaluation user token")
			}
			uploadCount++
			if _, err := io.ReadAll(request.Body); err != nil {
				t.Fatal(err)
			}
			return &http.Response{
				StatusCode: http.StatusCreated,
				Body:       io.NopCloser(strings.NewReader(`{"id":"file-training","status":"processed"}`)),
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
				Body:       io.NopCloser(strings.NewReader(`{"id":"` + realMonitorJobID + `","status":"queued"}`)),
				Header:     make(http.Header),
			}, nil
		default:
			t.Fatalf("unexpected request path %q", request.URL.Path)
			return nil, nil
		}
	})

	originalCreateClient := createFinetuneClient
	createFinetuneClient = func(endpoint string) (*finetuneClient, error) {
		if endpoint != "https://account.openai.azure.com" {
			t.Fatalf("expected the Foundry account endpoint, got %q", endpoint)
		}
		return client, nil
	}
	t.Cleanup(func() {
		createFinetuneClient = originalCreateClient
	})

	command := newTrainCommand()
	command.SetContext(t.Context())
	var output bytes.Buffer
	command.SetOut(&output)
	action := &trainAction{
		cmd: command,
		flags: &rleTrainFlags{
			rleName:      "code_rl",
			rleVersion:   "1.0.0",
			model:        "Qwen/Qwen3-32B",
			trainingFile: trainingFilePath,
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
	if !strings.Contains(output.String(), "Submitted fine-tuning job "+realMonitorJobID) {
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

func writeTrainRleConfig(t *testing.T, dir string, name string, version string) {
	t.Helper()
	if err := project.WriteRleConfig(dir, validTrainConfig(name, version)); err != nil {
		t.Fatal(err)
	}
}

func validTrainConfig(name, version string) project.RleConfig {
	return project.RleConfig{
		SchemaVersion: new(project.CurrentRleManifestSchemaVersion),
		Rle:           project.RleManifest{Name: name, Version: version},
		Harness:       project.RleHarnessManifest{Type: project.HarnessTypeGym},
		Model:         &project.RleModelDefaults{Name: new("registered-model"), RendererName: new("resolved-renderer")},
		Train: &project.RleTrainDefaults{Grpo: &project.RleGrpoDefaults{
			LearningRate:               new(1e-5),
			GroupSize:                  new(8),
			GroupsPerBatch:             new(4),
			LoraRank:                   new(16),
			NumEpochs:                  new(3),
			EvalEvery:                  new(0),
			SaveEvery:                  new(0),
			RemoveConstantRewardGroups: new(false),
			MinTrainableGroupMembers:   new(2),
		}},
	}
}

func useTrainManifestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	writeTrainRleConfig(t, dir, "code_rl", "1.0.0")
	return dir
}

func TestTrainFallsBackToRleConfigNameAndVersion(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeTrainRleConfig(t, dir, "code_rl", "1.2.3")

	action := &trainAction{flags: &rleTrainFlags{model: "m", trainingFile: "t"}}
	if _, err := action.resolveTrainSettings(); err != nil {
		t.Fatal(err)
	}

	if action.flags.rleName != "code_rl" {
		t.Fatalf("expected the name from rle.toml, got %q", action.flags.rleName)
	}
	if action.flags.rleVersion != "1.2.3" {
		t.Fatalf("expected the version from rle.toml, got %q", action.flags.rleVersion)
	}
}

func TestTrainWithoutRleConfigOrFlagsFails(t *testing.T) {
	t.Chdir(t.TempDir())

	action := &trainAction{flags: &rleTrainFlags{}}
	_, err := action.resolveTrainSettings()
	if err == nil {
		t.Fatal("expected an error when rle.toml is missing")
	}

	var localErr *azdext.LocalError
	if !errors.As(err, &localErr) || localErr.Code != "rle_manifest_missing" {
		t.Fatalf("expected a missing rle.toml error, got %v", err)
	}
}

func TestTrainCommandNoLongerRequiresRleFlags(t *testing.T) {
	t.Chdir(t.TempDir())

	cmd := newTrainCommand()
	cmd.SetArgs([]string{"--model", "Qwen/Qwen3-32B"})
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error when --training-file is not set")
	}
	if strings.Contains(err.Error(), "rle-name") || strings.Contains(err.Error(), "rle-version") {
		t.Fatalf("expected rle-name and rle-version to be optional, got %v", err)
	}
}

// stubbedTrain wires train to a transport that accepts an upload and a job
// creation, and returns the action alongside the buffer it writes to.
func stubbedTrain(t *testing.T, ctx context.Context, flags *rleTrainFlags) (*trainAction, *bytes.Buffer) {
	t.Helper()
	useTrainManifestDir(t)
	trainingFilePath := filepath.Join(t.TempDir(), "training.jsonl")
	if err := os.WriteFile(trainingFilePath, []byte("{\"input\":\"example\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(foundryProjectEndpointEnvVar, "https://account.services.ai.azure.com/api/projects/project")

	client := newFinetuneClientWithCredential("https://account.openai.azure.com", &testTokenCredential{})
	submissions := 0
	client.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost ||
			(request.URL.Path != finetuneFilesPath && request.URL.Path != finetuneJobsPath) {
			t.Errorf("unexpected training request: %s %s", request.Method, request.URL.Path)
			return nil, fmt.Errorf("unexpected training request: %s %s", request.Method, request.URL.Path)
		}
		// The upload streams through a pipe, so a transport that answers without
		// reading closes it under the writer.
		if request.Body != nil {
			if _, err := io.Copy(io.Discard, request.Body); err != nil {
				return nil, err
			}
		}
		body := `{"id":"file-training","status":"processed"}`
		if request.URL.Path == finetuneJobsPath {
			submissions++
			if submissions != 1 {
				t.Error("training must not resubmit the remote job")
			}
			body = `{"id":"` + realMonitorJobID + `","status":"queued"}`
		}
		return &http.Response{
			StatusCode: http.StatusCreated,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})

	originalCreateClient := createFinetuneClient
	createFinetuneClient = func(endpoint string) (*finetuneClient, error) {
		if endpoint != "https://account.openai.azure.com" {
			return nil, fmt.Errorf("unexpected fine-tuning endpoint: %s", endpoint)
		}
		return client, nil
	}
	t.Cleanup(func() { createFinetuneClient = originalCreateClient })

	command := newTrainCommand()
	command.SetContext(ctx)
	output := &bytes.Buffer{}
	command.SetOut(output)
	command.SetErr(output)

	flags.rleName = "code_rl"
	flags.rleVersion = "1.0.0"
	flags.model = "Qwen/Qwen3-32B"
	flags.trainingFile = trainingFilePath
	return &trainAction{cmd: command, flags: flags}, output
}

func TestTrainMonitorsUntilCanceledWithoutCancelingTheJob(t *testing.T) {
	for _, returnCancellation := range []bool{false, true} {
		t.Run(fmt.Sprintf("monitor-returns-cancellation=%v", returnCancellation), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			started := make(chan string, 1)
			stubAPIJobMonitor(t)
			runAPIJobMonitor = func(
				ctx context.Context, _ monitor.JobSource, jobID string, _ bool, _, _ io.Writer,
			) error {
				started <- jobID
				<-ctx.Done()
				if returnCancellation {
					return ctx.Err()
				}
				return nil
			}
			action, output := stubbedTrain(t, ctx, &rleTrainFlags{})
			returned := make(chan error, 1)
			go func() { returned <- action.Run() }()
			select {
			case jobID := <-started:
				require.Equal(t, realMonitorJobID, jobID)
			case <-time.After(5 * time.Second):
				t.Fatal("train did not start the job monitor")
			}
			select {
			case err := <-returned:
				t.Fatalf("train returned before monitoring stopped: %v", err)
			default:
			}
			cancel()
			select {
			case err := <-returned:
				require.NoError(t, err)
			case <-time.After(5 * time.Second):
				t.Fatal("train did not return after monitoring was canceled")
			}
			require.Contains(t, output.String(), "Stopped local monitoring for job "+realMonitorJobID)
			require.Contains(t, output.String(), "The remote job was not canceled.")
		})
	}
}

func TestTrainNoFollowExitsImmediatelyWithoutMonitoring(t *testing.T) {
	action, output := stubbedTrain(t, t.Context(), &rleTrainFlags{noFollow: true})

	originalCreateRle := createRleClient
	createRleClient = func(string) (*rleClient, error) {
		t.Fatal("--no-follow must not open the job monitor")
		return nil, nil
	}
	t.Cleanup(func() { createRleClient = originalCreateRle })

	originalRunAPI := runAPIJobMonitor
	runAPIJobMonitor = func(context.Context, monitor.JobSource, string, bool, io.Writer, io.Writer) error {
		t.Fatal("--no-follow must not open the job monitor")
		return nil
	}
	t.Cleanup(func() { runAPIJobMonitor = originalRunAPI })

	done := make(chan error, 1)
	go func() { done <- action.Run() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("--no-follow should return immediately, but train is still blocking")
	}

	if !strings.Contains(output.String(),
		"Monitor this job anytime with: azd ai rle monitor --job-id "+realMonitorJobID) {
		t.Fatalf("output = %q, want the reproducible monitor command", output.String())
	}
	if !strings.Contains(output.String(),
		"--no-follow was set; exiting without opening the job monitor.") {
		t.Fatalf("output = %q, want a no-follow confirmation", output.String())
	}
}

// ptrTo returns a pointer to v, for populating the optional *T fields of
// project.RleTrainDefaults and friends inline in a test literal.
//
//go:fix inline
func ptrTo[T any](v T) *T {
	return new(v)
}

// writeTrainRleConfigWithDefaults writes a manifest carrying explicit [train.grpo] values.
func writeTrainRleConfigWithDefaults(
	t *testing.T, dir string, name string, version string, train *project.RleTrainDefaults,
) {
	t.Helper()
	config := validTrainConfig(name, version)
	config.Train = train
	if err := project.WriteRleConfig(dir, config); err != nil {
		t.Fatal(err)
	}
}

func TestTrainReportsWhatIsMissingWithoutFlagsOrManifestSettings(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeTrainRleConfig(t, dir, "code_rl", "1.2.3")

	action := &trainAction{flags: &rleTrainFlags{}}
	_, err := action.resolveTrainSettings()
	if err == nil {
		t.Fatal("expected a missing model to be reported")
	}
	var localErr *azdext.LocalError
	if !errors.As(err, &localErr) || localErr.Code != "rle_train_setting_required" {
		t.Fatalf("expected a user-facing error, got %#v", err)
	}
	// There is no manifest fallback for model/training-file/validation-file/suffix
	// anymore, so the suggestion must point at the flag, not at rle.toml.
	if !strings.Contains(localErr.Suggestion, "--model") {
		t.Fatalf("the suggestion should point at --model: %q", localErr.Suggestion)
	}
	if strings.Contains(localErr.Suggestion, "rle.toml") {
		t.Fatalf("the suggestion must not point at rle.toml: %q", localErr.Suggestion)
	}
}

// The FT model and input datasets are invocation flags, independent of registration settings.
func TestTrainRequiresModelAndTrainingFileFlagsDespiteManifestDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeTrainRleConfigWithDefaults(t, dir, "code_rl", "1.2.3", &project.RleTrainDefaults{
		Grpo: &project.RleGrpoDefaults{
			LearningRate: new(2e-5),
		},
	})

	tests := []struct {
		name  string
		flags *rleTrainFlags
		want  string
	}{
		{name: "missing model", flags: &rleTrainFlags{trainingFile: "t"}, want: "model"},
		{name: "missing training file", flags: &rleTrainFlags{model: "m"}, want: "training file"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			action := &trainAction{flags: test.flags}
			_, err := action.resolveTrainSettings()
			var localErr *azdext.LocalError
			if !errors.As(err, &localErr) || localErr.Code != "rle_train_setting_required" {
				t.Fatalf("expected a missing setting error, got %v", err)
			}
			if !strings.Contains(localErr.Message, test.want) {
				t.Fatalf("expected the error to name %q, got %q", test.want, localErr.Message)
			}
		})
	}
}

// resolveTrainSettings treats --validation-file and --suffix as optional: a
// manifest with [train] but no flags for either must still succeed.
func TestTrainValidationFileAndSuffixAreOptional(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeTrainRleConfigWithDefaults(t, dir, "code_rl", "1.2.3", &project.RleTrainDefaults{
		Grpo: validTrainConfig("code_rl", "1.2.3").Train.Grpo,
	})

	action := &trainAction{flags: &rleTrainFlags{model: "m", trainingFile: "t"}}
	if _, err := action.resolveTrainSettings(); err != nil {
		t.Fatal(err)
	}
	if action.flags.validationFile != "" || action.flags.suffix != "" {
		t.Fatalf("expected validation file and suffix to stay empty, got %q %q",
			action.flags.validationFile, action.flags.suffix)
	}
}

func TestTrainSubmitsResolvedSnapshot(t *testing.T) {
	tests := []struct {
		name        string
		harness     project.RleHarnessManifest
		harnessJSON string
		rollout     *project.RleRolloutDefaults
		rolloutJSON string
		optionals   bool
	}{
		{
			name:        "openenv omitted rollout",
			harness:     project.RleHarnessManifest{Type: project.HarnessTypeGym},
			harnessJSON: `{"type":"gym"}`,
			rolloutJSON: `{}`,
		},
		{
			name:        "openenv explicit empty rollout",
			harness:     project.RleHarnessManifest{Type: project.HarnessTypeGym},
			harnessJSON: `{"type":"gym"}`,
			rollout:     &project.RleRolloutDefaults{},
			rolloutJSON: `{}`,
		},
		{
			name:        "openenv response field and optionals",
			harness:     project.RleHarnessManifest{Type: project.HarnessTypeGym},
			harnessJSON: `{"type":"gym"}`,
			rollout: &project.RleRolloutDefaults{GymOpenEnv: &project.RleGymOpenEnvDefaults{
				ModelResponseField: new("response"),
			}},
			rolloutJSON: `{"gym_openenv":{"model_response_field":"response"}}`,
			optionals:   true,
		},
		{
			name: "byoh responses",
			harness: project.RleHarnessManifest{
				Type: project.HarnessTypeBYOH, Protocol: project.HarnessProtocolResponses,
				Endpoint: new("https://acct.services.ai.azure.com/api/projects/p/agents/ci-agent/endpoint/protocols/openai/responses"),
			},
			harnessJSON: `{"type":"byoh","endpoint":"https://acct.services.ai.azure.com/api/projects/p/agents/ci-agent/endpoint/protocols/openai/responses","protocol":"responses"}`,
			rolloutJSON: `{}`,
		},
		{
			name: "byoh rle-invocation",
			harness: project.RleHarnessManifest{
				Type: project.HarnessTypeBYOH, Protocol: project.HarnessProtocolRleInvocation,
				Endpoint: new("https://harness.example.com/rle/"),
			},
			harnessJSON: `{"type":"byoh","endpoint":"https://harness.example.com/rle/","protocol":"rle-invocation"}`,
			rolloutJSON: `{}`,
		},
		{
			name: "byoh without protocol",
			harness: project.RleHarnessManifest{
				Type: project.HarnessTypeBYOH, Endpoint: new("https://harness.example.com/rle/"),
			},
			harnessJSON: `{"type":"byoh","endpoint":"https://harness.example.com/rle/"}`,
			rolloutJSON: `{}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := validTrainConfig("code_rl", "1.0.0")
			config.Rle.ImageTag = new("registration-only")
			config.Harness = test.harness
			config.Rollout = test.rollout
			optionalJSON := ""
			if test.optionals {
				config.Train.Grpo.MaxSteps = new(50)
				config.Train.Grpo.SamplingSeed = new(0)
				optionalJSON = `,"max_steps":50,"sampling_seed":0`
			}
			expected := fmt.Sprintf(`{
				"model":"explicit-model","training_file":"file-training","trainingType":1,
				"validation_file":"file-validation","suffix":"explicit-suffix",
				"method":{"type":"rl_environment","rl_environment":{
					"name":"code_rl","version":"1.0.0","harness":%s,"schema_version":"1.0.0",
					"model":{"renderer_name":"resolved-renderer"},
					"train":{"grpo":{"learning_rate":0.00001,"group_size":8,"groups_per_batch":4,
						"lora_rank":16,"num_epochs":3,"eval_every":0,"save_every":0,
						"remove_constant_reward_groups":false,"min_trainable_group_members":2%s}},
					"rollout":%s
				}}
			}`, test.harnessJSON, optionalJSON, test.rolloutJSON)
			runTrainSubmissionTest(t, config, expected)
		})
	}
}

func runTrainSubmissionTest(t *testing.T, config project.RleConfig, expected string) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, project.WriteRleConfig(dir, config))
	runTrainSubmissionFromLocalManifest(t, expected)
}

func runTrainSubmissionFromLocalManifest(t *testing.T, expected string) {
	t.Helper()
	t.Setenv(foundryProjectEndpointEnvVar, "https://account.services.ai.azure.com/api/projects/project")
	require.NoError(t, os.WriteFile("training.jsonl", []byte("{\"input\":\"train\"}\n"), 0600))
	require.NoError(t, os.WriteFile("validation.jsonl", []byte("{\"input\":\"eval\"}\n"), 0600))
	client := newFinetuneClientWithCredential("https://account.openai.azure.com", &finetuneSubmissionCredential{})
	var calls []string
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls = append(calls, req.URL.Path)
		body := ""
		switch req.URL.Path {
		case finetuneFilesPath:
			require.NoError(t, req.ParseMultipartForm(1<<20))
			require.Equal(t, "fine-tune", req.FormValue("purpose"))
			file, header, err := req.FormFile("file")
			require.NoError(t, err)
			require.NoError(t, file.Close())
			require.NoError(t, req.MultipartForm.RemoveAll())
			id := "file-training"
			if header.Filename == "validation.jsonl" {
				id = "file-validation"
			}
			body = `{"id":"` + id + `","status":"processed"}`
		case finetuneJobsPath:
			payload, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			require.JSONEq(t, expected, string(payload))
			body = `{"id":"ftjob-resolved","status":"queued"}`
		default:
			t.Fatalf("unexpected downstream call %s", req.URL)
		}
		return &http.Response{
			StatusCode: http.StatusCreated, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(body)),
		}, nil
	})
	originalCreateClient := createFinetuneClient
	createFinetuneClient = func(string) (*finetuneClient, error) { return client, nil }
	t.Cleanup(func() { createFinetuneClient = originalCreateClient })
	originalCreateRle := createRleClient
	createRleClient = func(string) (*rleClient, error) {
		t.Fatal("train must use the local snapshot, not registered defaults")
		return nil, errors.New("unexpected RLE client creation")
	}
	t.Cleanup(func() { createRleClient = originalCreateRle })
	command := newTrainCommand()
	command.SetContext(t.Context())
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{
		"--model", "explicit-model", "--training-file", "training.jsonl",
		"--validation-file", "validation.jsonl", "--suffix", "explicit-suffix", "--no-follow",
	})
	require.NoError(t, command.Execute())
	require.Equal(t, []string{finetuneFilesPath, finetuneFilesPath, finetuneJobsPath}, calls)
	require.Contains(t, output.String(), "Applying resolved training configuration from rle.toml.")
}

func TestTrainRejectsMissingConfigurationBeforeDownstreamCalls(t *testing.T) {
	fields := []string{
		"LearningRate", "GroupSize", "GroupsPerBatch", "LoraRank", "NumEpochs",
		"EvalEvery", "SaveEvery", "RemoveConstantRewardGroups", "MinTrainableGroupMembers",
	}
	for _, field := range fields {
		t.Run(field, func(t *testing.T) {
			config := validTrainConfig("code_rl", "1.0.0")
			value := reflect.ValueOf(config.Train.Grpo).Elem().FieldByName(field)
			value.SetZero()
			structField, ok := reflect.TypeFor[project.RleGrpoDefaults]().FieldByName(field)
			require.True(t, ok)
			key, _, _ := strings.Cut(structField.Tag.Get("toml"), ",")
			err := runInvalidTrainConfig(t, config)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok, "%v", err)
			require.Equal(t, "rle_train_configuration_required", localErr.Code)
			require.Contains(t, localErr.Message, "[train.grpo]: "+key)
			require.Contains(t, localErr.Suggestion, "rle.toml")
			require.Contains(t, localErr.Suggestion, "explicit values")
		})
	}
	for _, name := range []string{"missing train", "missing grpo", "legacy manifest"} {
		t.Run(name, func(t *testing.T) {
			config := validTrainConfig("code_rl", "1.0.0")
			switch name {
			case "missing train":
				config.Train = nil
			case "missing grpo":
				config.Train.Grpo = nil
			case "legacy manifest":
				config.SchemaVersion = nil
				config.Model = nil
				config.Train = nil
			}
			err := runInvalidTrainConfig(t, config)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok, "%v", err)
			for _, field := range fields {
				structField, ok := reflect.TypeFor[project.RleGrpoDefaults]().FieldByName(field)
				require.True(t, ok)
				require.Contains(t, localErr.Message, strings.Split(structField.Tag.Get("toml"), ",")[0])
			}
			require.NotContains(t, localErr.Message, "max_steps")
			require.NotContains(t, localErr.Message, "sampling_seed")
			if name == "legacy manifest" {
				require.Contains(t, localErr.Message, "root-level schema_version")
				require.Contains(t, localErr.Message, "[model]: renderer_name")
			}
		})
	}
}

func runInvalidTrainConfig(t *testing.T, config project.RleConfig) error {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	// Marshal without normalization so the command, not fixture construction, validates invalid input.
	data, err := toml.Marshal(config)
	require.NoError(t, err)
	return runInvalidTrainManifest(t, dir, data)
}

func runInvalidTrainManifest(t *testing.T, dir string, data []byte) error {
	t.Helper()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile(project.RleConfigFile, data, 0600))
	originalCreateClient := createFinetuneClient
	createFinetuneClient = func(string) (*finetuneClient, error) {
		t.Fatal("invalid local input must fail before client creation, authentication, upload or submission")
		return nil, errors.New("unexpected client creation")
	}
	t.Cleanup(func() { createFinetuneClient = originalCreateClient })
	originalCreateRle := createRleClient
	createRleClient = func(string) (*rleClient, error) {
		t.Fatal("train must not fetch registered defaults")
		return nil, errors.New("unexpected RLE client creation")
	}
	t.Cleanup(func() { createRleClient = originalCreateRle })
	command := newTrainCommand()
	command.SetContext(t.Context())
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	command.SetArgs([]string{"--model", "explicit-model", "--training-file", "missing-file.jsonl", "--no-follow"})
	err := command.Execute()
	require.Error(t, err)
	return err
}

func TestTrainRejectsInvalidConfigurationBeforeDownstreamCalls(t *testing.T) {
	tests := []struct {
		name   string
		change func(*project.RleConfig)
		want   string
	}{
		{"missing renderer", func(c *project.RleConfig) { c.Model.RendererName = nil }, "[model]: renderer_name"},
		{"blank renderer", func(c *project.RleConfig) { c.Model.RendererName = new(" ") }, "[model]: renderer_name"},
		{"missing schema", func(c *project.RleConfig) { c.SchemaVersion = nil }, "schema_version"},
		{"invalid schema", func(c *project.RleConfig) { c.SchemaVersion = new("2.0.0") }, "schema_version"},
		{"nan learning rate", func(c *project.RleConfig) { c.Train.Grpo.LearningRate = new(math.NaN()) }, "learning_rate"},
		{"infinite learning rate", func(c *project.RleConfig) {
			c.Train.Grpo.LearningRate = new(math.Inf(1))
		}, "learning_rate"},
		{"zero learning rate", func(c *project.RleConfig) { c.Train.Grpo.LearningRate = new(0.0) }, "learning_rate"},
		{"too many trainable members", func(c *project.RleConfig) {
			c.Train.Grpo.MinTrainableGroupMembers = new(9)
		}, "min_trainable_group_members"},
		{"missing response field", func(c *project.RleConfig) {
			c.Rollout = &project.RleRolloutDefaults{GymOpenEnv: &project.RleGymOpenEnvDefaults{}}
		}, "model_response_field"},
		{"blank response field", func(c *project.RleConfig) {
			c.Rollout = &project.RleRolloutDefaults{GymOpenEnv: &project.RleGymOpenEnvDefaults{ModelResponseField: new(" ")}}
		}, "model_response_field"},
		{"byoh requires endpoint", func(c *project.RleConfig) {
			c.Harness = project.RleHarnessManifest{Type: project.HarnessTypeBYOH}
		}, "endpoint"},
		{"gym forbids endpoint", func(c *project.RleConfig) { c.Harness.Endpoint = new("https://harness.example.com/") }, "endpoint"},
		{"gym forbids protocol", func(c *project.RleConfig) { c.Harness.Protocol = project.HarnessProtocolResponses }, "protocol"},
		{"byoh rejects unknown protocol", func(c *project.RleConfig) {
			c.Harness = project.RleHarnessManifest{
				Type: project.HarnessTypeBYOH, Endpoint: new("https://harness.example.com/"), Protocol: "grpc",
			}
		}, "protocol"},
		{"gym rollout on byoh", func(c *project.RleConfig) {
			c.Harness = project.RleHarnessManifest{
				Type: project.HarnessTypeBYOH, Endpoint: new("https://harness.example.com/"),
			}
			c.Rollout = &project.RleRolloutDefaults{GymOpenEnv: &project.RleGymOpenEnvDefaults{
				ModelResponseField: new("response"),
			}}
		}, "[rollout.gym_openenv]"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := validTrainConfig("code_rl", "1.0.0")
			test.change(&config)
			err := runInvalidTrainConfig(t, config)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok, "%v", err)
			require.Contains(t, localErr.Message, test.want)
			require.NotEmpty(t, localErr.Suggestion)
		})
	}
	for _, field := range []string{
		"GroupSize", "GroupsPerBatch", "LoraRank", "NumEpochs", "MaxSteps",
		"EvalEvery", "SaveEvery", "SamplingSeed", "MinTrainableGroupMembers",
	} {
		for _, invalid := range []int{-1, math.MaxInt32 + 1} {
			t.Run(fmt.Sprintf("%s=%d", field, invalid), func(t *testing.T) {
				config := validTrainConfig("code_rl", "1.0.0")
				reflect.ValueOf(config.Train.Grpo).Elem().FieldByName(field).Set(reflect.ValueOf(new(invalid)))
				err := runInvalidTrainConfig(t, config)
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok, "%v", err)
				require.Equal(t, "rle_manifest_default_invalid", localErr.Code)
			})
		}
	}
}

func TestTrainRejectsUnsupportedOrMistypedTomlBeforeDownstreamCalls(t *testing.T) {
	for _, section := range []string{
		"\n[train.options]\nlearning_rate = 1e-5\n",
		"\n[train.grpo]\ngroup_size = true\n",
		"\n[train.grpo]\nremove_constant_reward_groups = \"false\"\n",
		"\n[train.grpo]\nlearning_rate = \"1e-5\"\n",
		"\n[rollout.gym_openenv]\nmodel_response_field = 1\n",
	} {
		t.Run(section, func(t *testing.T) {
			config := validTrainConfig("code_rl", "1.0.0")
			config.Train = nil
			base, err := toml.Marshal(config)
			require.NoError(t, err)
			err = runInvalidTrainManifest(t, t.TempDir(), append(base, []byte(section)...))
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok, "%v", err)
			require.Equal(t, "rle_manifest_invalid", localErr.Code)
		})
	}
}

func TestTrainAcceptsLatestMathSampleManifest(t *testing.T) {
	// Canonical examples/gym/openenv/math_rl/rle.toml model configuration from rle-samples.
	const manifest = `schema_version = "1.0.0"

[rle]
name = "math_rl"
version = "1.0.0"

[harness]
type = "gym"

[model]
name = "Qwen/Qwen3-32B"
renderer_name = "qwen3_ext"

[train.grpo]
learning_rate = 2e-5
lora_rank = 32
group_size = 8
groups_per_batch = 16
num_epochs = 1
max_steps = 150
eval_every = 6
save_every = 6
remove_constant_reward_groups = false
sampling_seed = 42
min_trainable_group_members = 2
`
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile(project.RleConfigFile, []byte(manifest), 0600))
	runTrainSubmissionFromLocalManifest(t, `{
		"model":"explicit-model","training_file":"file-training","trainingType":1,
		"validation_file":"file-validation","suffix":"explicit-suffix",
		"method":{"type":"rl_environment","rl_environment":{
			"name":"math_rl","version":"1.0.0","harness":{"type":"gym"},
			"schema_version":"1.0.0","model":{"renderer_name":"qwen3_ext"},
			"train":{"grpo":{"learning_rate":0.00002,"lora_rank":32,"group_size":8,
				"groups_per_batch":16,"num_epochs":1,"max_steps":150,"eval_every":6,"save_every":6,
				"remove_constant_reward_groups":false,"sampling_seed":42,"min_trainable_group_members":2}},
			"rollout":{}
		}}
	}`)
}
