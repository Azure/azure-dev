// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"azure.ai.rle/internal/project"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func TestReadJSONFlagOrFileReturnsNilWhenUnset(t *testing.T) {
	raw, err := readJSONFlagOrFile("--task", "", "--task-file", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if raw != nil {
		t.Fatalf("expected nil payload, got %s", raw)
	}
}

func TestReadJSONFlagOrFileReadsInlineValue(t *testing.T) {
	raw, err := readJSONFlagOrFile("--task", `{"a":1}`, "--task-file", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"a":1}` {
		t.Fatalf("expected inline payload preserved, got %s", raw)
	}
}

func TestReadJSONFlagOrFileReadsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "task.json")
	if err := os.WriteFile(path, []byte(`{"b":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := readJSONFlagOrFile("--task", "", "--task-file", path, false)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"b":2}` {
		t.Fatalf("expected file payload preserved, got %s", raw)
	}
}

func TestReadJSONFlagOrFileRejectsBothSet(t *testing.T) {
	_, err := readJSONFlagOrFile("--task", `{}`, "--task-file", "somefile.json", false)
	if err == nil {
		t.Fatal("expected error when both inline and file flags are set")
	}
}

func TestReadJSONFlagOrFileRejectsInvalidJSON(t *testing.T) {
	_, err := readJSONFlagOrFile("--task", `not json`, "--task-file", "", false)
	if err == nil {
		t.Fatal("expected error for invalid JSON payload")
	}
	if !strings.Contains(err.Error(), "--task must contain valid JSON") {
		t.Fatalf("expected error to name --task, got %v", err)
	}
}

func TestReadJSONFlagOrFileDoesNotRepairWhenDisabled(t *testing.T) {
	_, err := readJSONFlagOrFile("--task", `{seed: 32, split: train}`, "--task-file", "", false)
	if err == nil {
		t.Fatal("expected strict JSON parsing when PowerShell repair is disabled")
	}
}

func TestIsGymHarness(t *testing.T) {
	tests := []struct {
		name     string
		manifest project.RleHarnessManifest
		want     bool
	}{
		{name: "gym", manifest: project.RleHarnessManifest{Type: project.HarnessTypeGym}, want: true},
		{name: "byoh", manifest: project.RleHarnessManifest{Type: project.HarnessTypeBYOH}, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isGymHarness(test.manifest); got != test.want {
				t.Fatalf("expected %t, got %t", test.want, got)
			}
		})
	}
}

func TestRepairWindowsPowerShellJSONObject(t *testing.T) {
	repaired, ok := repairWindowsPowerShellJSONObject(
		[]byte(`{seed: 32, split: FineEnvs/data-agent-harbor-train, prompt: hello, world}`),
	)
	if !ok {
		t.Fatal("expected PowerShell-stripped Gym task to be repaired")
	}
	const expected = `{"seed":32,"split":"FineEnvs/data-agent-harbor-train","prompt":"hello, world"}`
	if string(repaired) != expected {
		t.Fatalf("expected %s, got %s", expected, repaired)
	}
}

func TestRepairWindowsPowerShellJSONObjectRejectsComplexValues(t *testing.T) {
	if _, ok := repairWindowsPowerShellJSONObject([]byte(`{seed: [32]}`)); ok {
		t.Fatal("expected complex JSON values to remain invalid")
	}
}

func TestNewRolloutIDReturnsUniqueHexValues(t *testing.T) {
	first, err := newRolloutID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := newRolloutID()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 32 {
		t.Fatalf("expected a 32-char hex rollout id, got %q", first)
	}
	if first == second {
		t.Fatal("expected distinct rollout ids across calls")
	}
}

func TestRolloutRequiresRleManifestEvenWithExplicitIdentity(t *testing.T) {
	stubRleClientEndpoint(t, "https://rle.test")
	t.Chdir(t.TempDir())

	command := newRolloutCommand()
	command.SetArgs([]string{"--model", "Qwen/Qwen3-32B"})
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)

	err := command.Execute()
	var localErr *azdext.LocalError
	if !errors.As(err, &localErr) || localErr.Code != "rle_manifest_missing" {
		t.Fatalf("expected a missing rle.toml error, got %v", err)
	}
}

func TestRolloutRequiresModel(t *testing.T) {
	stubRleClientEndpoint(t, "https://rle.test")
	t.Chdir(t.TempDir())

	command := newRolloutCommand()
	command.SetArgs([]string{})
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)

	if err := command.Execute(); err == nil {
		t.Fatal("expected an error when --model is not provided and rle.toml has no default")
	}
}

func TestRolloutFallsBackToRleConfigModelDefault(t *testing.T) {
	isolateRolloutArtifacts(t)
	stubRolloutMonitor(t)
	dir := t.TempDir()
	t.Chdir(dir)
	modelName := "Qwen/Qwen3-32B"
	schemaVersion := project.CurrentRleManifestSchemaVersion
	if err := project.WriteRleConfig(dir, project.RleConfig{
		SchemaVersion: &schemaVersion,
		Rle: project.RleManifest{
			Name:    "code_rl",
			Version: "1.0.0",
		},
		Harness: project.RleHarnessManifest{
			Type: project.HarnessTypeGym,
		},
		Model: &project.RleModelDefaults{Name: &modelName},
	}); err != nil {
		t.Fatal(err)
	}

	rleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request executeRolloutRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Policy == nil || request.Policy.ModelName != modelName {
			t.Fatalf("expected model default from rle.toml to be forwarded, got %#v", request.Policy)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rollout_id": "` + request.RolloutID + `", "reward": 1, "success": true}`))
	}))
	defer rleServer.Close()

	loomServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == loomSessionsPath:
			_, _ = w.Write([]byte(`{"session_id":"model_abc","request_id":"req-create"}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/checkpoint_sample"):
			_, _ = w.Write([]byte(`{"session_id":"model_abc","request_id":"req-checkpoint"}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/complete"):
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/request/"):
			_, _ = w.Write([]byte(`{"status":"completed"}`))
		default:
			t.Fatalf("unexpected Loom request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer loomServer.Close()

	stubRleClientEndpoint(t, rleServer.URL)
	oldCreateLoomSessionClient := createLoomSessionClient
	createLoomSessionClient = func(endpoint string) (*loomSessionClient, error) {
		return testLoomSessionClientForServer(t, loomServer.URL), nil
	}
	t.Cleanup(func() {
		createLoomSessionClient = oldCreateLoomSessionClient
	})

	command := newRolloutCommand()
	command.SetArgs([]string{"--task", `{"prompt":"hello"}`})
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)

	if err := command.Execute(); err != nil {
		t.Fatalf("expected rollout to succeed using rle.toml model default, got %v", err)
	}
	if !strings.Contains(output.String(), "success: true") {
		t.Fatalf("expected rollout result to be printed, got %s", output.String())
	}
}

func TestRolloutRunExecutesRolloutAndClosesLoomSession(t *testing.T) {
	for _, test := range []struct {
		name          string
		executionFail bool
		cleanupFail   bool
	}{
		{name: "success"},
		{name: "cleanup failure", cleanupFail: true},
		{name: "execution and cleanup failure", executionFail: true, cleanupFail: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			isolateRolloutArtifacts(t)
			stubRolloutMonitor(t)
			rleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost ||
					r.URL.Path != testFoundryProjectPath+environmentCollectionPath+"/code_rl/versions/1.0.0:executeRollout" {
					t.Fatalf("unexpected RLE request: %s %s", r.Method, r.URL.Path)
				}
				if got := r.Header.Get("aml-user-token"); got == "" {
					t.Fatal("expected aml-user-token header to be forwarded")
				}
				rawBody, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatal(err)
				}
				// Decoding into executeRolloutRequest cannot catch a wire-shape regression, because it
				// would deserialize the same struct that produced it. The raw body is what the service
				// actually parses, so the discriminator and the retired names are asserted on the JSON.
				var wire map[string]any
				if err := json.Unmarshal(rawBody, &wire); err != nil {
					t.Fatal(err)
				}
				policy, ok := wire["policy"].(map[string]any)
				if !ok {
					t.Fatalf("expected a policy object on the wire, got %v", wire)
				}
				if policy["type"] != "loom" {
					t.Fatalf("expected policy.type to be sent explicitly, got %v", policy["type"])
				}
				if _, retired := wire["model"]; retired {
					t.Fatal("the retired model object must not be sent")
				}
				if _, retired := policy["loom_session_id"]; retired {
					t.Fatal("the retired loom_session_id field must not be sent")
				}
				if _, retired := wire["agent_input"]; retired {
					t.Fatal("the retired agent_input field must not be sent; RLE derives it from the reset observation")
				}
				// The CLI names no renderer, and a blank one is rejected rather than defaulted, so the
				// whole object has to be absent rather than present and empty.
				if _, present := wire["sampling"]; present {
					t.Fatalf("expected sampling to be omitted when no renderer is named, got %v", wire["sampling"])
				}

				var request executeRolloutRequest
				if err := json.Unmarshal(rawBody, &request); err != nil {
					t.Fatal(err)
				}
				if request.Policy == nil || request.Policy.ModelName != "Qwen/Qwen3-32B" {
					t.Fatalf("expected model name to be forwarded, got %#v", request.Policy)
				}
				if request.Policy.SessionID != "session_abc" {
					t.Fatalf("expected canonical loom session id, got %q", request.Policy.SessionID)
				}
				if request.Policy.CheckpointID == "" {
					t.Fatal("expected a checkpoint id to be forwarded")
				}
				w.Header().Set("Content-Type", "application/json")
				if test.executionFail {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"error":{"message":"execution failed"}}`))
					return
				}
				_, _ = w.Write([]byte(`{
			"rollout_id": "` + request.RolloutID + `",
			"reward": 1,
			"success": true,
			"episode": {"kind": "gym", "termination_reason": "done", "steps": []}
		}`))
			}))
			defer rleServer.Close()

			loomRequests := map[string]int{}
			loomServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				loomRequests[r.Method+" "+r.URL.Path]++
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodPost && r.URL.Path == loomSessionsPath:
					_, _ = w.Write([]byte(`{"session_id":"model_abc","request_id":"req-create"}`))
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/checkpoint_sample"):
					_, _ = w.Write([]byte(`{"session_id":"model_abc","request_id":"req-checkpoint"}`))
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/complete"):
					if test.cleanupFail {
						w.WriteHeader(http.StatusInternalServerError)
						_, _ = w.Write([]byte(`{"error":{"message":"cleanup failed"}}`))
						return
					}
					_, _ = w.Write([]byte(`{}`))
				case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/request/"):
					_, _ = w.Write([]byte(`{"status":"completed"}`))
				default:
					t.Fatalf("unexpected Loom request: %s %s", r.Method, r.URL.Path)
				}
			}))
			defer loomServer.Close()

			stubRleClientEndpoint(t, rleServer.URL)

			oldCreateLoomSessionClient := createLoomSessionClient
			createLoomSessionClient = func(endpoint string) (*loomSessionClient, error) {
				return testLoomSessionClientForServer(t, loomServer.URL), nil
			}
			t.Cleanup(func() {
				createLoomSessionClient = oldCreateLoomSessionClient
			})

			command := newRolloutCommand()
			outputDir := t.TempDir()
			command.SetArgs([]string{
				"--model", "Qwen/Qwen3-32B",
				"--task", `{"prompt":"hello"}`,
				"--output-dir", outputDir,
			})
			var output, stderr bytes.Buffer
			command.SetOut(&output)
			command.SetErr(&stderr)
			command.SilenceErrors = true

			err := command.Execute()
			if test.cleanupFail {
				if err == nil {
					t.Fatal("expected failed cleanup to fail the command")
				}
				visibleMessage := azdext.WrapError(err).GetMessage() + "\n" + stderr.String()
				for _, expected := range []string{"session_abc", "resources may remain allocated", "cleanup failed"} {
					if !strings.Contains(err.Error(), expected) {
						t.Fatalf("expected cleanup error to include %q, got %v", expected, err)
					}
					if !strings.Contains(visibleMessage, expected) {
						t.Fatalf("expected user-visible cleanup diagnostic %q, got %q", expected, visibleMessage)
					}
				}
				if strings.Contains(output.String(), "resources may remain allocated") {
					t.Fatalf("cleanup warnings must not be written to stdout: %q", output.String())
				}
				if !strings.Contains(stderr.String(), "The rollout monitor was not opened.") {
					t.Fatalf("expected monitoring to be skipped, got %s", stderr.String())
				}
				if test.executionFail {
					if !strings.Contains(err.Error(), "execution failed") ||
						strings.Contains(err.Error(), "execution completed") {
						t.Fatalf("expected both failures without claiming execution completed, got %v", err)
					}
					reported := azdext.WrapError(err)
					serviceErr := reported.GetServiceError()
					if serviceErr == nil || serviceErr.GetServiceName() != "rle-service" ||
						serviceErr.GetStatusCode() != http.StatusBadRequest ||
						!strings.Contains(reported.GetMessage(), "execution failed") {
						t.Fatalf("expected the original service error classification and message, got %v", reported)
					}
				} else if !strings.Contains(err.Error(), "rollout execution completed") {
					t.Fatalf("expected completed execution to be distinguished from cleanup failure, got %v", err)
				}
			} else if err != nil {
				t.Fatalf("expected rollout to succeed, got %v", err)
			}
			if loomRequests["POST /fine_tuning/sessions/session_abc/complete"] != 1 {
				t.Fatal("expected exactly one attempt to close the Loom session")
			}
			if test.executionFail {
				return
			}
			if !strings.Contains(output.String(), "success: true") {
				t.Fatalf("expected rollout result to be printed, got %s", output.String())
			}
			if !strings.Contains(output.String(), "Artifacts:") {
				t.Fatalf("expected the artifact tree to be printed, got %s", output.String())
			}
			entries, err := os.ReadDir(outputDir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 || !entries[0].IsDir() {
				t.Fatalf("expected exactly one rollout directory, got %#v", entries)
			}
			if _, err := os.Stat(filepath.Join(outputDir, entries[0].Name(), "summary.json")); err != nil {
				t.Fatalf("expected summary.json to be written: %v", err)
			}
			if loomRequests["POST "+loomSessionsPath] != 1 {
				t.Fatalf("expected exactly one Loom session creation request, got %d", loomRequests["POST "+loomSessionsPath])
			}
			if loomRequests["POST /fine_tuning/sessions/session_abc/complete"] != 1 {
				t.Fatal("expected the Loom session to be closed on completion")
			}
		})
	}
}

func TestLoomReleasePreservesFailureCauses(t *testing.T) {
	originalFailure := errors.New("execution failed")
	cleanupFailure := errors.New("cleanup transport failed")
	for _, test := range []struct {
		name     string
		priorErr error
		canceled bool
	}{
		{name: "completed execution"},
		{name: "failed execution", priorErr: originalFailure},
		{name: "canceled execution", priorErr: context.Canceled, canceled: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			client := newLoomSessionClientWithCredential("https://loom.test", &testTokenCredential{})
			cleanupCalls := 0
			client.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
				var body string
				switch {
				case strings.HasSuffix(request.URL.Path, "/complete"):
					cleanupCalls++
					if err := request.Context().Err(); err != nil {
						t.Fatalf("cleanup must not use a canceled context: %v", err)
					}
					deadline, ok := request.Context().Deadline()
					if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > loomSessionCleanupTimeout {
						t.Fatal("cleanup must have an independent bounded timeout")
					}
					return nil, cleanupFailure
				case request.Method == http.MethodGet:
					body = `{"status":"completed"}`
				default:
					body = `{"session_id":"model_abc","request_id":"req-create"}`
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(body)),
				}, nil
			})
			command := newRolloutCommand()
			command.SetOut(io.Discard)
			command.SetErr(io.Discard)
			action := &rolloutAction{cmd: command, flags: &rolloutFlags{}, monitor: true}
			_, release, err := action.provisionLoomPolicy(ctx, client, rolloutTarget{}, "test-model", monitorTestID)
			if err != nil {
				t.Fatal(err)
			}
			if release == nil {
				t.Fatal("expected ownership of the allocated session")
			}
			if test.canceled {
				cancel()
			}
			err = test.priorErr
			release(&err)
			if !errors.Is(err, cleanupFailure) {
				t.Fatalf("expected the cleanup cause to survive, got %v", err)
			}
			if test.priorErr != nil && !errors.Is(err, test.priorErr) {
				t.Fatalf("expected the prior failure to survive, got %v", err)
			}
			if cleanupCalls != 1 || action.monitor {
				t.Fatalf("expected one cleanup attempt and no monitor: calls=%d monitor=%t", cleanupCalls, action.monitor)
			}
		})
	}
}

func TestRolloutRunWithFoundryBackendSendsAFoundryPolicyAndCreatesNoLoomSession(t *testing.T) {
	isolateRolloutArtifacts(t)
	stubRolloutMonitor(t)
	rleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		var wire map[string]any
		if err := json.Unmarshal(rawBody, &wire); err != nil {
			t.Fatal(err)
		}
		policy, ok := wire["policy"].(map[string]any)
		if !ok {
			t.Fatalf("expected a policy object on the wire, got %v", wire)
		}
		if policy["type"] != "foundry" || policy["deployment_name"] != "gpt-5-mini" {
			t.Fatalf("expected a foundry policy naming the deployment, got %v", policy)
		}
		if policy["project_endpoint"] == "" {
			t.Fatal("expected the project endpoint to be forwarded")
		}
		if _, ok := policy["model_name"]; ok {
			t.Fatalf("model_name must not be sent for a foundry policy, got %v", policy)
		}
		for _, loomOnly := range []string{"session_id", "checkpoint_id", "sequence_id"} {
			if _, present := policy[loomOnly]; present {
				t.Fatalf("%s must not be sent for a foundry policy", loomOnly)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"rollout_id": "` + wire["rollout_id"].(string) + `",
			"reward": 1,
			"success": true,
			"episode": {"kind": "gym", "termination_reason": "done", "steps": []}
		}`))
	}))
	defer rleServer.Close()

	loomServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("a foundry rollout must not call the Loom sessions API: %s %s", r.Method, r.URL.Path)
	}))
	defer loomServer.Close()

	stubRleClientEndpoint(t, rleServer.URL)
	oldCreateLoomSessionClient := createLoomSessionClient
	createLoomSessionClient = func(endpoint string) (*loomSessionClient, error) {
		return testLoomSessionClientForServer(t, loomServer.URL), nil
	}
	t.Cleanup(func() { createLoomSessionClient = oldCreateLoomSessionClient })

	command := newRolloutCommand()
	command.SetArgs([]string{
		"--mode", "eval", "--model", "gpt-5-mini",
		"--task", `{"prompt":"hello"}`,
		"--output-dir", t.TempDir(),
	})
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)

	if err := command.Execute(); err != nil {
		t.Fatalf("expected rollout to succeed, got %v", err)
	}
	if strings.Contains(output.String(), "Loom") {
		t.Fatalf("expected no Loom provisioning output, got %s", output.String())
	}
}

func TestRolloutRejectsAnInvalidBackendOrLoomOnlyFlagsOnFoundry(t *testing.T) {
	cases := map[string]struct {
		args []string
		want string
	}{
		"unknown backend": {[]string{"--mode", "serve"}, "is not supported"},
		"loom with train": {[]string{"--mode", "train", "--loom"}, "--loom"},
		"sequence id":     {[]string{"--mode", "eval", "--sequence-id", "1"}, "--sequence-id"},
		"renderer":        {[]string{"--mode", "eval", "--renderer", "qwen3"}, "--renderer"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			command := newRolloutCommand()
			command.SetArgs(append([]string{
				"--model", "m", "--task", `{}`,
			}, tc.args...))
			var output bytes.Buffer
			command.SetOut(&output)
			command.SetErr(&output)

			err := command.Execute()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected an error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func testLoomSessionClientForServer(t *testing.T, endpoint string) *loomSessionClient {
	t.Helper()
	target, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	client := newLoomSessionClientWithCredential("https://loom.test", &testTokenCredential{})
	client.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		request = request.Clone(request.Context())
		request.URL.Scheme = target.Scheme
		request.URL.Host = target.Host
		return http.DefaultTransport.RoundTrip(request)
	})
	return client
}
