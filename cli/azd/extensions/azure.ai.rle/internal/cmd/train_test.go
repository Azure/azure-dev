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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"azure.ai.rle/internal/monitor"
	"azure.ai.rle/internal/project"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func TestBuildFinetuneJobRequestUsesRleEnvironmentMethod(t *testing.T) {
	flags := &rleTrainFlags{
		rleName:    "code_rl",
		rleVersion: "1.0.0",
		model:      "Qwen/Qwen3-32B",
	}

	request := buildFinetuneJobRequest(flags, "file-training", "", nil)

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
		rleName:    "code_rl",
		rleVersion: "1.0.0",
		model:      "Qwen/Qwen3-32B",
		suffix:     "custom-suffix",
	}

	request := buildFinetuneJobRequest(flags, "file-abc", "file-def", nil)

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

// Without an endpoint override, commands derive the fine-tuning endpoint from
// the Foundry project account.
func TestResolveFinetuneEndpointDefaultsToTheFoundryAccount(t *testing.T) {
	t.Setenv(rleTrainEndpointEnvVar, "")

	endpoint, err := resolveFinetuneEndpoint(
		"https://Account.services.ai.azure.com/api/projects/project",
	)
	if err != nil {
		t.Fatal(err)
	}
	if endpoint != "https://account.openai.azure.com" {
		t.Fatalf("expected Foundry account endpoint, got %q", endpoint)
	}
}

func TestResolveFinetuneEndpointPrefersTheEnvironmentOverTheDefault(t *testing.T) {
	t.Setenv(rleTrainEndpointEnvVar, "https://from-env.example.com")

	endpoint, err := resolveFinetuneEndpoint(
		"https://account.services.ai.azure.com/api/projects/project",
	)
	if err != nil {
		t.Fatal(err)
	}
	if endpoint != "https://from-env.example.com" {
		t.Fatalf("expected %s to take precedence, got %q", rleTrainEndpointEnvVar, endpoint)
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
	useRleManifestDir(t)
	stubAPIJobMonitor(t)
	trainingFilePath := filepath.Join(t.TempDir(), "training.jsonl")
	if err := os.WriteFile(trainingFilePath, []byte("{\"input\":\"example\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(foundryProjectEndpointEnvVar, "https://account.services.ai.azure.com/api/projects/project")
	t.Setenv(rleTrainEndpointEnvVar, "")

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
	command.SetContext(context.Background())
	var output bytes.Buffer
	command.SetOut(&output)
	originalFollow := followTrainingRunFunc
	followTrainingRunFunc = func(context.Context, string, string, string, string, io.Writer) (string, error) {
		return "succeeded", nil
	}
	t.Cleanup(func() { followTrainingRunFunc = originalFollow })
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
	schemaVersion := project.CurrentRleManifestSchemaVersion
	if err := project.WriteRleConfig(dir, project.RleConfig{
		SchemaVersion: &schemaVersion,
		Rle: project.RleManifest{
			Name:    name,
			Version: version,
		},
		Harness: project.RleHarnessManifest{
			Type:    project.RleTypeGym,
			Subtype: project.RleSubtypeOpenEnv,
		},
	}); err != nil {
		t.Fatal(err)
	}
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
	useRleManifestDir(t)
	t.Setenv(rleTrainEndpointEnvVar, "https://facade.example.com")
	trainingFilePath := filepath.Join(t.TempDir(), "training.jsonl")
	if err := os.WriteFile(trainingFilePath, []byte("{\"input\":\"example\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(foundryProjectEndpointEnvVar, "https://account.services.ai.azure.com/api/projects/project")

	client := newFinetuneClientWithCredential("https://account.openai.azure.com", &testTokenCredential{})
	client.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		// The upload streams through a pipe, so a transport that answers without
		// reading closes it under the writer.
		if request.Body != nil {
			if _, err := io.Copy(io.Discard, request.Body); err != nil {
				return nil, err
			}
		}
		body := `{"id":"file-training","status":"processed"}`
		if request.URL.Path == finetuneJobsPath {
			body = `{"id":"` + realMonitorJobID + `","status":"queued"}`
		}
		return &http.Response{
			StatusCode: http.StatusCreated,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})

	originalCreateClient := createFinetuneClient
	createFinetuneClient = func(string) (*finetuneClient, error) { return client, nil }
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

// The point of following a run is watching its rollouts land, so the dashboard
// runs alongside the stream rather than after it, and outlives it: the run
// ending is when the rollouts are finally all there to read.
func TestTrainServesTheRolloutDashboardUntilItIsStopped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan string, 1)
	stubAPIJobMonitor(t)
	runAPIJobMonitor = func(
		ctx context.Context, _ monitor.JobSource, jobID string, _ bool, _, _ io.Writer,
	) error {
		started <- jobID
		<-ctx.Done()
		return nil
	}

	streamed := make(chan struct{})
	originalFollow := followTrainingRunFunc
	followTrainingRunFunc = func(
		context.Context, string, string, string, string, io.Writer,
	) (string, error) {
		close(streamed)
		return "succeeded", nil
	}
	t.Cleanup(func() { followTrainingRunFunc = originalFollow })

	logsRoot := t.TempDir()
	action, output := stubbedTrain(t, ctx,
		&rleTrainFlags{logsRoot: logsRoot})

	returned := make(chan error, 1)
	go func() { returned <- action.Run() }()

	select {
	case jobID := <-started:
		if jobID != realMonitorJobID {
			t.Fatalf("dashboard opened %q, want the submitted job", jobID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("--follow did not start the job monitor")
	}

	<-streamed
	// The run is over. The command must still be serving, or the window the user
	// was sent to would close on them.
	select {
	case err := <-returned:
		t.Fatalf("train returned %v when the run finished, want the dashboard held open", err)
	case <-time.After(250 * time.Millisecond):
	}

	cancel()
	select {
	case err := <-returned:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("train did not return after the dashboard was stopped")
	}

	if !strings.Contains(output.String(), "Final status: succeeded") {
		t.Fatalf("output = %q, want the run's final status", output.String())
	}
	if !strings.Contains(output.String(), "job monitor is still running") {
		t.Fatalf("output = %q, want the monitor to outlive the run", output.String())
	}
}

// TestTrainNoFollowExitsImmediatelyWithoutMonitoringOrStreaming is the
// regression test for --no-follow: the job is still submitted, but nothing
// after that point may open the dashboard or stream artifacts, and the
// command must not block waiting on either.
func TestTrainNoFollowExitsImmediatelyWithoutMonitoringOrStreaming(t *testing.T) {
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

	originalFollow := followTrainingRunFunc
	followTrainingRunFunc = func(context.Context, string, string, string, string, io.Writer) (string, error) {
		t.Fatal("--no-follow must not stream run artifacts")
		return "", nil
	}
	t.Cleanup(func() { followTrainingRunFunc = originalFollow })

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
		"--no-follow was set; exiting without streaming logs or opening the job monitor.") {
		t.Fatalf("output = %q, want a no-follow confirmation", output.String())
	}
}

func TestTrainingOptionsReachTheRleEnvironmentBlock(t *testing.T) {
	flags := &rleTrainFlags{rleName: "math_rl", rleVersion: "1.0.6", model: "qwen3-32b-1"}
	options := map[string]any{"learning_rate": 2e-5, "max_steps": float64(50)}

	request := buildFinetuneJobRequest(flags, "file-training", "", options)

	if request.Method.RleEnvironment.Hyperparameters["max_steps"] != float64(50) {
		t.Fatalf("expected max_steps to reach the request, got %#v",
			request.Method.RleEnvironment.Hyperparameters)
	}

	// The service reads options from method.rl_environment.hyperparameters, so a
	// rename in the payload would silently stop applying them.
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var wire struct {
		Method struct {
			RleEnvironment struct {
				Hyperparameters map[string]any `json:"hyperparameters"`
			} `json:"rl_environment"`
		} `json:"method"`
	}
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if wire.Method.RleEnvironment.Hyperparameters["learning_rate"] != 2e-5 {
		t.Fatalf("options did not survive the wire shape: %s", encoded)
	}
}

func TestNoTrainingOptionsLeavesTheRequestUnchanged(t *testing.T) {
	flags := &rleTrainFlags{rleName: "math_rl", rleVersion: "1.0.6", model: "qwen3-32b-1"}

	request := buildFinetuneJobRequest(flags, "file-training", "", nil)

	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), "hyperparameters") {
		t.Fatalf("absent options must not add the field: %s", encoded)
	}
}

// ptrTo returns a pointer to v, for populating the optional *T fields of
// project.RleTrainDefaults and friends inline in a test literal.
//
//go:fix inline
func ptrTo[T any](v T) *T {
	return new(v)
}

// writeTrainRleConfigWithDefaults writes a manifest carrying [train], the
// only part of rle.toml that still feeds training options.
func writeTrainRleConfigWithDefaults(
	t *testing.T, dir string, name string, version string, train *project.RleTrainDefaults,
) {
	t.Helper()
	schemaVersion := project.CurrentRleManifestSchemaVersion
	if err := project.WriteRleConfig(dir, project.RleConfig{
		SchemaVersion: &schemaVersion,
		Rle: project.RleManifest{
			Name:    name,
			Version: version,
		},
		Harness: project.RleHarnessManifest{
			Type:    project.RleTypeGym,
			Subtype: project.RleSubtypeOpenEnv,
		},
		Train: train,
	}); err != nil {
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

// resolveTrainSettings requires --model and --training-file as flags on every
// invocation; there is no [train] manifest section to fall back to anymore, even
// when a manifest with [train] exists in the directory.
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
		Grpo: &project.RleGrpoDefaults{LearningRate: new(2e-5)},
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

// trainDefaultsOptions flattens [train.grpo] into the hyperparameters map,
// using each field's toml key verbatim as the wire key. Rollout settings are part
// of the rollout contract the environment already knows, not a training
// hyperparameter, and the function signature does not even accept them.
func TestTrainDefaultsOptionsFlattensGrpoAndExcludesRollout(t *testing.T) {
	train := &project.RleTrainDefaults{
		Grpo: &project.RleGrpoDefaults{
			LearningRate:               new(2e-5),
			GroupSize:                  new(4),
			RemoveConstantRewardGroups: new(true),
		},
	}

	options := trainDefaultsOptions(train)

	want := map[string]any{
		"learning_rate":                 2e-5,
		"group_size":                    4,
		"remove_constant_reward_groups": true,
	}
	if len(options) != len(want) {
		t.Fatalf("expected %d options, got %#v", len(want), options)
	}
	for key, value := range want {
		if options[key] != value {
			t.Fatalf("option %q = %#v, want %#v", key, options[key], value)
		}
	}
	if _, present := options["model_response_field"]; present {
		t.Fatal("GymOpenEnv's model_response_field must not reach the hyperparameters map")
	}
}

// Train that is entirely nil (or whose Grpo sub-section is nil)
// must flatten to an empty map rather than panicking.
func TestTrainDefaultsOptionsHandlesNilDefaults(t *testing.T) {
	if options := trainDefaultsOptions(nil); len(options) != 0 {
		t.Fatalf("expected an empty map for nil defaults, got %#v", options)
	}
	if options := trainDefaultsOptions(&project.RleTrainDefaults{}); len(options) != 0 {
		t.Fatalf("expected an empty map when Grpo is unset, got %#v", options)
	}
}
