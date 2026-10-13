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
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"azure.ai.rle/internal/rollouts"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const monitorTestID = "3c27c30f5fba261c3a7a3e856b4e1388"

func isolateRolloutArtifacts(t *testing.T) string {
	t.Helper()
	t.Setenv(rleEnableAllEnvVar, "true")
	root := t.TempDir()
	t.Chdir(root)
	return filepath.Join(root, defaultRolloutOutputDir)
}

// stubRolloutMonitor keeps rollouts, where the monitor is on by default,
// from opening a real browser and blocking until Ctrl+C.
func stubRolloutMonitor(t *testing.T) {
	t.Helper()
	oldRun := runRolloutMonitor
	t.Cleanup(func() { runRolloutMonitor = oldRun })
	runRolloutMonitor = func(context.Context, rollouts.Reader, string, bool, io.Writer, io.Writer) error {
		return nil
	}
}

func TestMonitorLoadsLocalResponseWithoutCredentials(t *testing.T) {
	outputDir := isolateRolloutArtifacts(t)
	t.Setenv(foundryProjectEndpointEnvVar, "")
	t.Chdir(t.TempDir())
	response := &executeRolloutResponse{RolloutID: monitorTestID, Reward: 1}
	if _, err := writeRolloutArtifacts(outputDir, response, nil); err != nil {
		t.Fatal(err)
	}
	oldRun := runRolloutMonitor
	t.Cleanup(func() { runRolloutMonitor = oldRun })
	called := false
	runRolloutMonitor = func(
		ctx context.Context, reader rollouts.Reader, id string, noBrowser bool, out, errOut io.Writer,
	) error {
		called = true
		if !noBrowser || id != monitorTestID {
			t.Fatalf("unexpected monitor options: %s %t", id, noBrowser)
		}
		_, err := reader.Get(ctx, id)
		return err
	}
	command := newMonitorCommand()
	command.SetArgs([]string{"--rollout-id", monitorTestID, "--no-browser", "--output-dir", outputDir})
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("monitor was not started")
	}
}

func TestMonitorValidation(t *testing.T) {
	t.Setenv(rleEnableAllEnvVar, "true")
	for _, args := range [][]string{
		{"--rollout-id", monitorTestID, "extra"},
		{"--rollout-id", monitorTestID, "--output", "json"},
		{"--rollout-id", monitorTestID, "--output-dir", ""},
	} {
		command := newMonitorCommand()
		command.Flags().String("output", "default", "output")
		command.SetArgs(args)
		command.SetOut(io.Discard)
		command.SetErr(io.Discard)
		if err := command.Execute(); err == nil {
			t.Fatalf("expected validation error for %v", args)
		}
	}
	if newRolloutCommand().Flags().Lookup("no-browser") != nil {
		t.Fatal("rollout must not expose --no-browser")
	}
}

func TestMonitorRolloutIDValidation(t *testing.T) {
	t.Setenv(rleEnableAllEnvVar, "true")
	oldRun := runRolloutMonitor
	t.Cleanup(func() { runRolloutMonitor = oldRun })
	runRolloutMonitor = func(
		context.Context, rollouts.Reader, string, bool, io.Writer, io.Writer,
	) error {
		t.Fatal("invalid rollout ID must not start a monitor")
		return nil
	}
	for _, tc := range []struct {
		name    string
		args    []string
		code    string
		message string
	}{
		{"omitted", nil, "rle_monitor_rollout_id_required", "--rollout-id or --job-id is required but neither was provided."},
		{"empty", []string{"--rollout-id="},
			"rle_monitor_rollout_id_required", "--rollout-id or --job-id is required but neither was provided."},
		{"whitespace", []string{"--rollout-id", " \t "},
			"rle_monitor_rollout_id_required", "--rollout-id or --job-id is required but neither was provided."},
		{"malformed", []string{"--rollout-id", "../outside"},
			"rle_invalid_rollout_id", "rollout ID must be 32 lowercase hexadecimal characters"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := newMonitorCommand()
			command.SetArgs(append([]string{}, tc.args...))
			command.SetOut(io.Discard)
			command.SetErr(io.Discard)
			err := command.Execute()
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			if !ok {
				t.Fatalf("expected structured validation error, got %v", err)
			}
			if localErr.Code != tc.code || localErr.Message != tc.message {
				t.Fatalf("unexpected error: code=%q message=%q", localErr.Code, localErr.Message)
			}
			if localErr.Category != azdext.LocalErrorCategoryUser || localErr.Suggestion == "" {
				t.Fatalf("expected actionable user error, got %+v", localErr)
			}
		})
	}
}

func TestRolloutMonitorLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name        string
		failSave    bool
		failExecute bool
		failCleanup bool
		wantMonitor bool
		wantSkipped bool
		jsonOutput  bool
		wantError   string
	}{
		{name: "monitor opens after cleanup", wantMonitor: true},
		{name: "execution failure", failExecute: true, wantError: "RLE service"},
		{name: "save failure warns and skips monitor", failSave: true, wantSkipped: true},
		{name: "cleanup failure warns and skips monitor", failCleanup: true, wantSkipped: true},
		{name: "monitor yields to --output", jsonOutput: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outputDir := isolateRolloutArtifacts(t)
			if tc.failSave {
				if err := os.WriteFile(outputDir, []byte("not a directory"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var closed atomic.Bool
			rleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if tc.failExecute {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, `{"message":"execution rejected"}`)
					return
				}
				_, _ = io.WriteString(w, `{"rollout_id":"`+monitorTestID+`","reward":1,
					"result":{"correct":true,"exact":9007199254740993}}`)
			}))
			defer rleServer.Close()
			loomServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.HasSuffix(r.URL.Path, "/complete"):
					closed.Store(true)
					if tc.failCleanup {
						w.WriteHeader(http.StatusInternalServerError)
					}
					_, _ = io.WriteString(w, `{}`)
				case r.Method == http.MethodPost:
					_, _ = io.WriteString(w, `{"session_id":"model_abc","request_id":"request"}`)
				case r.Method == http.MethodGet:
					_, _ = io.WriteString(w, `{"status":"completed"}`)
				}
			}))
			defer loomServer.Close()
			stubRleClientEndpoint(t, rleServer.URL)
			oldLoom, oldMonitor := createLoomSessionClient, runRolloutMonitor
			t.Cleanup(func() { createLoomSessionClient, runRolloutMonitor = oldLoom, oldMonitor })
			createLoomSessionClient = func(string) (*loomSessionClient, error) {
				return testLoomSessionClientForServer(t, loomServer.URL), nil
			}
			monitorCalled := false
			runRolloutMonitor = func(
				ctx context.Context, reader rollouts.Reader, id string, noBrowser bool, out, errOut io.Writer,
			) error {
				monitorCalled = true
				if !closed.Load() || noBrowser {
					t.Fatal("monitor must open a browser only after cleanup")
				}
				snapshot, err := reader.Get(ctx, id)
				if err != nil {
					return err
				}
				if !bytes.Contains(snapshot.Response, []byte("9007199254740993")) {
					t.Fatal("result precision was lost")
				}
				return nil
			}
			command := newRolloutCommand()
			args := []string{"code_rl", "--version", "1.0.0", "--model", "model", "--rollout-id", monitorTestID}
			if tc.jsonOutput {
				command.Flags().String("output", "", "")
				args = append(args, "--output", "json")
			}
			command.SetArgs(args)
			var output bytes.Buffer
			command.SetOut(&output)
			command.SetErr(&output)
			err := command.Execute()
			if tc.wantError == "" && err != nil {
				t.Fatal(err)
			}
			if tc.wantError != "" && (err == nil || !strings.Contains(err.Error(), tc.wantError)) {
				t.Fatalf("expected %q, got %v", tc.wantError, err)
			}
			if !closed.Load() {
				t.Fatal("Loom cleanup was skipped")
			}
			if monitorCalled != tc.wantMonitor {
				t.Fatalf("unexpected monitor invocation: %t", monitorCalled)
			}
			if skipped := strings.Contains(output.String(), "The rollout monitor was not opened."); skipped != tc.wantSkipped {
				t.Fatalf("unexpected monitor skip notice (%t): %s", skipped, output.String())
			}
			if strings.Contains(output.String(), "success: false") {
				t.Fatal("missing success was reported as false")
			}
			if !tc.failSave && !tc.failExecute {
				reader := &rollouts.ArtifactReader{OutputDir: outputDir}
				snapshot, err := reader.Get(t.Context(), monitorTestID)
				if err != nil {
					t.Fatal(err)
				}
				if snapshot.Environment == nil || snapshot.Environment.Name != "code_rl" ||
					snapshot.Environment.Version != "1.0.0" {
					t.Fatalf("execution target was not preserved: %+v", snapshot.Environment)
				}
				if !strings.Contains(output.String(), "Artifacts:") {
					t.Fatal("artifact location was not printed")
				}
			}
			if tc.wantError == "" && (tc.failSave || tc.failCleanup) && !strings.Contains(output.String(), "Warning:") {
				t.Fatal("ordinary rollout must report persistence/cleanup failures")
			}
		})
	}
}

func TestPrintRolloutOptionalFields(t *testing.T) {
	for _, tc := range []struct {
		field string
		want  string
	}{
		{`"success":false`, "success: false"},
		{`"success":true`, "success: true"},
		{`"episode":{"kind":"gym_openenv","steps":[],"ungraded":true}`, "ungraded: true"},
	} {
		var response executeRolloutResponse
		if err := json.Unmarshal([]byte(`{"rollout_id":"`+monitorTestID+`","reward":0,`+tc.field+`}`), &response); err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		if err := printRolloutResult(&output, &response); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), tc.want) {
			t.Fatalf("missing %q: %s", tc.want, output.String())
		}
	}
}

func TestMonitorMissingResultDoesNotCreateClient(t *testing.T) {
	isolateRolloutArtifacts(t)
	t.Setenv(foundryProjectEndpointEnvVar, "https://account.services.ai.azure.com/api/projects/project-1")
	oldClient := createRleClient
	createRleClient = func(string) (*rleClient, error) {
		t.Fatal("local monitor must not create an authenticated client")
		return nil, errors.New("unexpected client")
	}

	t.Cleanup(func() { createRleClient = oldClient })
	command := newMonitorCommand()
	command.SetArgs([]string{"--rollout-id", monitorTestID, "--no-browser"})
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	if err := command.Execute(); err != nil {
		t.Fatalf("missing directory should warn without returning an error: %v", err)
	}
	for _, want := range []string{"Warning: no output directory exists for rollout " + monitorTestID,
		"No dashboard was opened", "--output-dir"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %q in warning: %s", want, output.String())
		}
	}
	if strings.Contains(output.String(), "Rollout monitor:") || strings.Contains(output.String(), "Local access code:") {
		t.Fatal("missing artifacts must not start a dashboard")
	}
}

func TestMonitorIncompleteArtifactsStillFail(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprint(corrupt), func(t *testing.T) {
			root := isolateRolloutArtifacts(t)
			directory := filepath.Join(root, monitorTestID)
			if err := os.MkdirAll(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			if corrupt {
				if err := os.WriteFile(filepath.Join(directory, "summary.json"), []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			command := newMonitorCommand()
			command.SetArgs([]string{"--rollout-id", monitorTestID, "--no-browser"})
			var output bytes.Buffer
			command.SetOut(&output)
			command.SetErr(&output)
			if err := command.Execute(); err == nil {
				t.Fatal("incomplete or corrupt artifacts must still return an error")
			}
			if strings.Contains(output.String(), "Warning: no output directory") {
				t.Fatal("existing directory must not be reported as absent")
			}
		})
	}
}

func TestMonitorEndpointRedactsCredentials(t *testing.T) {
	for _, raw := range []string{
		"https://user:private-password@account.services.ai.azure.com/api/projects/project?sig=private-sas#private-fragment",
		"https://user:private-password@account.services.ai.azure.com/%zz?sig=private-sas#private-fragment",
	} {
		endpoint, err := normalizeFoundryProjectEndpoint(raw)
		result := endpoint
		if err != nil {
			result += err.Error()
		}
		for _, secret := range []string{"private-password", "private-sas", "private-fragment"} {
			if strings.Contains(result, secret) {
				t.Fatalf("endpoint normalization disclosed %s", secret)
			}
		}
	}
}

func TestMonitorAlwaysVisibleAndEnabled(t *testing.T) {
	for _, development := range []string{"", "false", "true"} {
		t.Run(development, func(t *testing.T) {
			t.Setenv(rleEnableAllEnvVar, development)
			root := NewRootCommand()
			command, _, err := root.Find([]string{"monitor"})
			if err != nil {
				t.Fatal(err)
			}
			if command.Hidden {
				t.Fatal("monitor must be visible regardless of AZD_AI_RLE_ENABLE_ALL")
			}
			rollout := newRolloutCommand()
			if rollout.Flags().Lookup("monitor") != nil {
				t.Fatal("rollout must not expose a --monitor flag")
			}
			if !strings.Contains(rollout.Long, "local dashboard") {
				t.Fatal("rollout help must always describe the local dashboard")
			}
		})
	}
}
