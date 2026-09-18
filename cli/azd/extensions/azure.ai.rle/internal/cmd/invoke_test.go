// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadJSONFlagOrFileReturnsNilWhenUnset(t *testing.T) {
	raw, err := readJSONFlagOrFile("--task", "", "--task-file", "")
	if err != nil {
		t.Fatal(err)
	}
	if raw != nil {
		t.Fatalf("expected nil payload, got %s", raw)
	}
}

func TestReadJSONFlagOrFileReadsInlineValue(t *testing.T) {
	raw, err := readJSONFlagOrFile("--task", `{"a":1}`, "--task-file", "")
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
	raw, err := readJSONFlagOrFile("--task", "", "--task-file", path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"b":2}` {
		t.Fatalf("expected file payload preserved, got %s", raw)
	}
}

func TestReadJSONFlagOrFileRejectsBothSet(t *testing.T) {
	_, err := readJSONFlagOrFile("--task", `{}`, "--task-file", "somefile.json")
	if err == nil {
		t.Fatal("expected error when both inline and file flags are set")
	}
}

func TestReadJSONFlagOrFileRejectsInvalidJSON(t *testing.T) {
	_, err := readJSONFlagOrFile("--task", `not json`, "--task-file", "")
	if err == nil {
		t.Fatal("expected error for invalid JSON payload")
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

func TestInvokeRequiresModel(t *testing.T) {
	stubRleClientEndpoint(t, "https://rle.test")

	command := newInvokeCommand()
	command.SetArgs([]string{"code_rl", "--version", "1.0.0"})
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)

	if err := command.Execute(); err == nil {
		t.Fatal("expected an error when --model is not provided")
	}
}

func TestInvokeRunExecutesRolloutAndClosesLoomSession(t *testing.T) {
	rleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost ||
			r.URL.Path != testFoundryProjectPath+environmentCollectionPath+"/code_rl/versions/1.0.0:executeRollout" {
			t.Fatalf("unexpected RLE request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("aml-user-token"); got == "" {
			t.Fatal("expected aml-user-token header to be forwarded")
		}
		var request executeRolloutRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Model == nil || request.Model.ModelName != "Qwen/Qwen3-32B" {
			t.Fatalf("expected model name to be forwarded, got %#v", request.Model)
		}
		if request.Model.LoomSessionID != "session_abc" {
			t.Fatalf("expected canonical loom session id, got %q", request.Model.LoomSessionID)
		}
		if request.Model.CheckpointID == "" {
			t.Fatal("expected a checkpoint id to be forwarded")
		}
		w.Header().Set("Content-Type", "application/json")
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

	command := newInvokeCommand()
	command.SetArgs([]string{
		"code_rl", "--version", "1.0.0",
		"--model", "Qwen/Qwen3-32B",
		"--task", `{"prompt":"hello"}`,
	})
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)

	if err := command.Execute(); err != nil {
		t.Fatalf("expected invoke to succeed, got %v", err)
	}
	if !strings.Contains(output.String(), "success: true") {
		t.Fatalf("expected rollout result to be printed, got %s", output.String())
	}
	if loomRequests["POST "+loomSessionsPath] != 1 {
		t.Fatalf("expected exactly one Loom session creation request, got %d", loomRequests["POST "+loomSessionsPath])
	}
	if loomRequests["POST /fine_tuning/sessions/session_abc/complete"] != 1 {
		t.Fatal("expected the Loom session to be closed on completion")
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
