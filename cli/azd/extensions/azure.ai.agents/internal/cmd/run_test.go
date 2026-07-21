// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	goyaml "go.yaml.in/yaml/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestParseCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "simple command",
			input: "python main.py",
			want:  []string{"python", "main.py"},
		},
		{
			name:  "single word",
			input: "python",
			want:  []string{"python"},
		},
		{
			name:  "double-quoted argument",
			input: `python "my script.py"`,
			want:  []string{"python", "my script.py"},
		},
		{
			name:  "single-quoted argument",
			input: `python 'my script.py'`,
			want:  []string{"python", "my script.py"},
		},
		{
			name:  "multiple arguments",
			input: "dotnet run --project MyAgent.csproj",
			want:  []string{"dotnet", "run", "--project", "MyAgent.csproj"},
		},
		{
			name:  "extra spaces",
			input: "  python   main.py   --verbose  ",
			want:  []string{"python", "main.py", "--verbose"},
		},
		{
			name:  "empty string",
			input: "",
			want:  nil,
		},
		{
			name:  "only spaces",
			input: "   ",
			want:  nil,
		},
		{
			name:  "quoted string with spaces in middle",
			input: `cmd "arg one" "arg two"`,
			want:  []string{"cmd", "arg one", "arg two"},
		},
		{
			name:  "mixed quotes",
			input: `python "my app.py" --flag 'value with spaces'`,
			want:  []string{"python", "my app.py", "--flag", "value with spaces"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := parseCommand(tt.input)
			if !slices.Equal(got, tt.want) {
				t.Errorf("parseCommand(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestResolveVenvCommand(t *testing.T) {
	t.Parallel()

	t.Run("no venv directory passes through unchanged", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		input := []string{"python", "main.py"}
		got := resolveVenvCommand(dir, input)
		if !slices.Equal(got, []string{"python", "main.py"}) {
			t.Errorf("expected passthrough, got %v", got)
		}
	})

	t.Run("python resolved to venv python", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		createVenv(t, dir)

		input := []string{"python", "main.py"}
		got := resolveVenvCommand(dir, input)

		wantPython := venvPython(filepath.Join(dir, ".venv"))
		if got[0] != wantPython {
			t.Errorf("got[0] = %q, want %q", got[0], wantPython)
		}
		if got[1] != "main.py" {
			t.Errorf("got[1] = %q, want %q", got[1], "main.py")
		}
	})

	t.Run("python3 resolved to venv python", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		createVenv(t, dir)

		input := []string{"python3", "main.py"}
		got := resolveVenvCommand(dir, input)

		wantPython := venvPython(filepath.Join(dir, ".venv"))
		if got[0] != wantPython {
			t.Errorf("got[0] = %q, want %q", got[0], wantPython)
		}
	})

	t.Run("non-python command with binary in venv", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		venvDir := createVenv(t, dir)

		// Create a fake binary in the venv bin dir
		binDir := venvBinDir(venvDir)
		fakeBin := filepath.Join(binDir, "myrunner")
		if err := os.WriteFile(fakeBin, []byte(""), 0755); err != nil { //nolint:gosec // G306: test binary needs exec permission
			t.Fatal(err)
		}

		input := []string{"myrunner", "--serve"}
		got := resolveVenvCommand(dir, input)
		if got[0] != fakeBin {
			t.Errorf("got[0] = %q, want %q", got[0], fakeBin)
		}
	})

	t.Run("non-python command without binary in venv stays unchanged", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		createVenv(t, dir)

		input := []string{"node", "index.js"}
		got := resolveVenvCommand(dir, input)
		if !slices.Equal(got, []string{"node", "index.js"}) {
			t.Errorf("expected passthrough, got %v", got)
		}
	})

	t.Run("empty command", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		got := resolveVenvCommand(dir, nil)
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})
}

func TestRunCommandNoInspectorFlag(t *testing.T) {
	t.Parallel()

	cmd := newRunCommand(nil)
	if cmd.Flags().Lookup("no-inspector") == nil {
		t.Fatal("run command should expose --no-inspector")
	}
}

func TestWaitForLocalPort(t *testing.T) {
	t.Parallel()

	t.Run("succeeds when port is listening", func(t *testing.T) {
		t.Parallel()

		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()

		port := ln.Addr().(*net.TCPAddr).Port
		if err := waitForLocalPort(t.Context(), port, 10*time.Millisecond); err != nil {
			t.Fatalf("waitForLocalPort returned error: %v", err)
		}
	})

	t.Run("returns when context expires", func(t *testing.T) {
		t.Parallel()

		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		if err := ln.Close(); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
		defer cancel()

		if err := waitForLocalPort(ctx, port, 5*time.Millisecond); err == nil {
			t.Fatal("waitForLocalPort should fail for a closed port")
		}
	})
}

func TestLaunchInspectorUsesWorkflowCommand(t *testing.T) {
	t.Parallel()

	workflow := &recordingWorkflowClient{}
	if err := launchInspector(t.Context(), workflow, 9090); err != nil {
		t.Fatalf("launchInspector returned error: %v", err)
	}

	if workflow.request == nil || workflow.request.Workflow == nil || len(workflow.request.Workflow.Steps) != 1 {
		t.Fatalf("unexpected workflow request: %#v", workflow.request)
	}

	got := workflow.request.Workflow.Steps[0].Command.Args
	want := []string{"ai", "inspector", "launch", "--port", "9090", "--silent"}
	if !slices.Equal(got, want) {
		t.Fatalf("workflow args = %v, want %v", got, want)
	}
}

func TestInspectorLaunchWarningForMissingExtension(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
	}{
		{
			name: "unknown command",
			err:  status.Error(codes.Internal, `failed to run workflow: unknown command "inspector" for "azd ai"`),
		},
		{
			name: "unknown flag from unresolved extension command",
			err: status.Error(
				codes.Internal,
				`error executing step command 'ai inspector launch --port 8088 --silent': unknown flag: --port`,
			),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := inspectorLaunchWarning(tt.err)
			if !strings.Contains(got, "azd extension install azure.ai.inspector") {
				t.Fatalf("warning should include install guidance, got %q", got)
			}
		})
	}
}

func TestInspectorLaunchFailureOnlyWarns(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	workflow := &recordingWorkflowClient{
		err:    status.Error(codes.Internal, "boom"),
		called: make(chan struct{}),
	}
	var stderr lockedBuffer
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	startInspectorAfterAgentReadyWithOptions(
		ctx,
		workflow,
		ln.Addr().(*net.TCPAddr).Port,
		time.Millisecond,
		&stderr,
	)

	select {
	case <-workflow.called:
	case <-time.After(2 * time.Second):
		t.Fatal("inspector workflow was not called")
	}

	deadline := time.After(2 * time.Second)
	for !strings.Contains(stderr.String(), "Warning: Agent Inspector was not launched") {
		select {
		case <-deadline:
			t.Fatalf("stderr = %q, want inspector warning", stderr.String())
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func TestShouldWarnLoadAzdEnvironmentFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "default environment missing is normal for local-only projects",
			err:  status.Error(codes.Unknown, "default environment not found"),
		},
		{
			name: "other environment error warns",
			err:  status.Error(codes.Unknown, "environment service unavailable"),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := shouldWarnLoadAzdEnvironmentFailure(tt.err); got != tt.want {
				t.Fatalf("shouldWarnLoadAzdEnvironmentFailure() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNoInspectorSkipsWorkflowLaunch(t *testing.T) {
	t.Parallel()

	workflow := &recordingWorkflowClient{called: make(chan struct{})}
	handleInspectorAutoLaunch(t.Context(), workflow, 8088, true, true, nil, io.Discard)

	select {
	case <-workflow.called:
		t.Fatal("--no-inspector should skip workflow launch")
	case <-time.After(50 * time.Millisecond):
	}
}

type recordingWorkflowClient struct {
	request *azdext.RunWorkflowRequest
	err     error
	called  chan struct{}
}

type lockedBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.String()
}

func (c *recordingWorkflowClient) Run(
	_ context.Context,
	request *azdext.RunWorkflowRequest,
	_ ...grpc.CallOption,
) (*azdext.EmptyResponse, error) {
	c.request = request
	if c.called != nil {
		close(c.called)
	}
	return &azdext.EmptyResponse{}, c.err
}

// createVenv sets up a minimal .venv directory structure for testing.
// Returns the path to the .venv directory.
func createVenv(t *testing.T, projectDir string) string {
	t.Helper()
	venvDir := filepath.Join(projectDir, ".venv")

	var binDir string
	if runtime.GOOS == "windows" {
		binDir = filepath.Join(venvDir, "Scripts")
	} else {
		binDir = filepath.Join(venvDir, "bin")
	}
	if err := os.MkdirAll(binDir, 0750); err != nil {
		t.Fatal(err)
	}

	// Create a fake python executable
	pythonName := "python"
	if runtime.GOOS == "windows" {
		pythonName = "python.exe"
	}
	if err := os.WriteFile(filepath.Join(binDir, pythonName), []byte(""), 0755); err != nil { //nolint:gosec // G306: test binary needs exec permission
		t.Fatal(err)
	}

	return venvDir
}

func TestAppendFoundryEnvVars(t *testing.T) {
	t.Parallel()

	t.Run("does not map FOUNDRY_PROJECT_ENDPOINT to itself", func(t *testing.T) {
		t.Parallel()
		azdEnv := map[string]string{
			"FOUNDRY_PROJECT_ENDPOINT": "https://myaccount.services.ai.azure.com/api/projects/myproject",
		}
		env := appendFoundryEnvVars(nil, azdEnv, "")
		if len(env) != 0 {
			t.Errorf("expected no translated env vars, got %v", env)
		}
	})

	t.Run("maps AZURE_AI_PROJECT_ID to FOUNDRY_PROJECT_ARM_ID", func(t *testing.T) {
		t.Parallel()
		azdEnv := map[string]string{
			"AZURE_AI_PROJECT_ID": "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.CognitiveServices/accounts/acct1/projects/proj1",
		}
		env := appendFoundryEnvVars(nil, azdEnv, "")
		expected := "FOUNDRY_PROJECT_ARM_ID=/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.CognitiveServices/accounts/acct1/projects/proj1"
		if !slices.Contains(env, expected) {
			t.Errorf("expected %q in env, got %v", expected, env)
		}
	})

	t.Run("maps service-specific agent vars to FOUNDRY_AGENT_*", func(t *testing.T) {
		t.Parallel()
		azdEnv := map[string]string{
			"AGENT_MY_SVC_NAME":    "my-agent",
			"AGENT_MY_SVC_VERSION": "v3",
		}
		env := appendFoundryEnvVars(nil, azdEnv, "my-svc")
		if !slices.Contains(env, "FOUNDRY_AGENT_NAME=my-agent") {
			t.Errorf("expected FOUNDRY_AGENT_NAME=my-agent in env, got %v", env)
		}
		if !slices.Contains(env, "FOUNDRY_AGENT_VERSION=v3") {
			t.Errorf("expected FOUNDRY_AGENT_VERSION=v3 in env, got %v", env)
		}
	})

	t.Run("skips missing values", func(t *testing.T) {
		t.Parallel()
		azdEnv := map[string]string{}
		env := appendFoundryEnvVars(nil, azdEnv, "my-svc")
		if len(env) != 0 {
			t.Errorf("expected empty env, got %v", env)
		}
	})

	t.Run("includes all mappings together", func(t *testing.T) {
		t.Parallel()
		azdEnv := map[string]string{
			"FOUNDRY_PROJECT_ENDPOINT": "https://acct.services.ai.azure.com/api/projects/proj",
			"AZURE_AI_PROJECT_ID":      "/subscriptions/sub/rg/rg/acct/proj",
			"AGENT_AGENT1_NAME":        "agent1",
			"AGENT_AGENT1_VERSION":     "v1",
		}
		env := appendFoundryEnvVars(nil, azdEnv, "agent1")
		if len(env) != 3 {
			t.Errorf("expected 3 env vars, got %d: %v", len(env), env)
		}
	})

	t.Run("skips foundry key when already set in azd env", func(t *testing.T) {
		t.Parallel()
		azdEnv := map[string]string{
			"FOUNDRY_PROJECT_ENDPOINT": "https://explicit.services.ai.azure.com",
			"AGENT_MY_SVC_NAME":        "my-agent",
			"FOUNDRY_AGENT_NAME":       "explicit-agent",
		}
		env := appendFoundryEnvVars(nil, azdEnv, "my-svc")

		// Neither FOUNDRY_PROJECT_ENDPOINT nor FOUNDRY_AGENT_NAME should be
		// appended because they already exist in azdEnv (and were thus already
		// added to the env slice by the caller's loop over azdEnv).
		for _, entry := range env {
			if strings.HasPrefix(entry, "FOUNDRY_PROJECT_ENDPOINT=") ||
				strings.HasPrefix(entry, "FOUNDRY_AGENT_NAME=") {
				t.Errorf("should not translate when foundry key already in azdEnv, got %q", entry)
			}
		}

		// AZURE_AI_PROJECT_ID has no explicit FOUNDRY_PROJECT_ARM_ID, so it should still be skipped
		// (it's not in azdEnv either, so appendFoundryEnvVars skips it because the source key is empty)
		if len(env) != 0 {
			t.Errorf("expected no translated env vars, got %v", env)
		}
	})

	t.Run("skips foundry key when already set in process env slice", func(t *testing.T) {
		t.Parallel()
		// Simulate os.Environ() already containing FOUNDRY_* vars set by the user's shell
		existingEnv := []string{
			"HOME=/home/user",
			"FOUNDRY_PROJECT_ENDPOINT=https://user-shell.services.ai.azure.com",
			"FOUNDRY_AGENT_NAME=shell-agent",
		}
		azdEnv := map[string]string{
			"FOUNDRY_PROJECT_ENDPOINT": "https://from-azd.services.ai.azure.com",
			"AZURE_AI_PROJECT_ID":      "/subscriptions/sub/rg/rg/acct/proj",
			"AGENT_MY_SVC_NAME":        "my-agent",
			"AGENT_MY_SVC_VERSION":     "v2",
		}
		env := appendFoundryEnvVars(existingEnv, azdEnv, "my-svc")

		// FOUNDRY_PROJECT_ENDPOINT and FOUNDRY_AGENT_NAME should NOT be appended
		// because they already exist in the process env slice.
		foundryEndpointCount := 0
		foundryAgentNameCount := 0
		for _, entry := range env {
			if strings.HasPrefix(entry, "FOUNDRY_PROJECT_ENDPOINT=") {
				foundryEndpointCount++
			}
			if strings.HasPrefix(entry, "FOUNDRY_AGENT_NAME=") {
				foundryAgentNameCount++
			}
		}
		if foundryEndpointCount != 1 {
			t.Errorf("expected exactly 1 FOUNDRY_PROJECT_ENDPOINT entry (from shell), got %d in %v", foundryEndpointCount, env)
		}
		if foundryAgentNameCount != 1 {
			t.Errorf("expected exactly 1 FOUNDRY_AGENT_NAME entry (from shell), got %d in %v", foundryAgentNameCount, env)
		}

		// FOUNDRY_PROJECT_ARM_ID and FOUNDRY_AGENT_VERSION should still be translated
		// since they are NOT already present in the env slice.
		if !slices.Contains(env, "FOUNDRY_PROJECT_ARM_ID=/subscriptions/sub/rg/rg/acct/proj") {
			t.Errorf("expected FOUNDRY_PROJECT_ARM_ID to be translated, got %v", env)
		}
		if !slices.Contains(env, "FOUNDRY_AGENT_VERSION=v2") {
			t.Errorf("expected FOUNDRY_AGENT_VERSION to be translated, got %v", env)
		}
	})
}

func TestEnvSliceHasKeyUsesPlatformCasing(t *testing.T) {
	t.Parallel()

	env := []string{"Path=process-value"}
	if !envSliceHasKey(env, "Path") {
		t.Fatal("expected exact-case environment key to match")
	}

	got := envSliceHasKey(env, "PATH")
	want := runtime.GOOS == "windows"
	if got != want {
		t.Errorf("envSliceHasKey() = %t, want %t", got, want)
	}
}

func TestMergeConfiguredEnvironmentEntriesUsesServicePrecedence(t *testing.T) {
	t.Parallel()

	entries := mergeConfiguredEnvironmentEntries(
		[]string{"PATH=definition-value"},
		[]string{"Path=service-value"},
		true,
	)

	if len(entries) != 1 {
		t.Fatalf("expected one entry, got %v", entries)
	}
	if entries["PATH"].key != "Path" {
		t.Errorf("key = %q, want %q", entries["PATH"].key, "Path")
	}
	if entries["PATH"].value != "service-value" {
		t.Errorf("value = %q, want %q", entries["PATH"].value, "service-value")
	}
}

func TestAppendPortEnvVars(t *testing.T) {
	t.Parallel()

	t.Run("dotnet project includes ASPNETCORE_URLS", func(t *testing.T) {
		t.Parallel()
		pt := ProjectType{Language: "dotnet", StartCmd: "dotnet run"}
		env := appendPortEnvVars(nil, pt, 8088)

		if !slices.Contains(env, "PORT=8088") {
			t.Errorf("expected PORT=8088 in env, got %v", env)
		}
		if !slices.Contains(env, "ASPNETCORE_URLS=http://localhost:8088") {
			t.Errorf("expected ASPNETCORE_URLS=http://localhost:8088 in env, got %v", env)
		}
	})

	t.Run("python project does not include ASPNETCORE_URLS", func(t *testing.T) {
		t.Parallel()
		pt := ProjectType{Language: "python", StartCmd: "python main.py"}
		env := appendPortEnvVars(nil, pt, 8088)

		if !slices.Contains(env, "PORT=8088") {
			t.Errorf("expected PORT=8088 in env, got %v", env)
		}
		for _, entry := range env {
			if strings.HasPrefix(entry, "ASPNETCORE_URLS=") {
				t.Errorf("ASPNETCORE_URLS should not be set for python, got %v", env)
			}
		}
	})

	t.Run("node project does not include ASPNETCORE_URLS", func(t *testing.T) {
		t.Parallel()
		pt := ProjectType{Language: "node", StartCmd: "npm start"}
		env := appendPortEnvVars(nil, pt, 9090)

		if !slices.Contains(env, "PORT=9090") {
			t.Errorf("expected PORT=9090 in env, got %v", env)
		}
		for _, entry := range env {
			if strings.HasPrefix(entry, "ASPNETCORE_URLS=") {
				t.Errorf("ASPNETCORE_URLS should not be set for node, got %v", env)
			}
		}
	})

	t.Run("dotnet project respects custom port", func(t *testing.T) {
		t.Parallel()
		pt := ProjectType{Language: "dotnet", StartCmd: "dotnet run"}
		env := appendPortEnvVars(nil, pt, 3000)

		if !slices.Contains(env, "PORT=3000") {
			t.Errorf("expected PORT=3000 in env, got %v", env)
		}
		expected := "ASPNETCORE_URLS=http://localhost:3000"
		if !slices.Contains(env, expected) {
			t.Errorf("expected %q in env, got %v", expected, env)
		}
	})

	t.Run("preserves existing env entries", func(t *testing.T) {
		t.Parallel()
		pt := ProjectType{Language: "dotnet", StartCmd: "dotnet run"}
		existing := []string{"HOME=/home/user", "PATH=/usr/bin"}
		env := appendPortEnvVars(existing, pt, 8088)

		if len(env) != 4 {
			t.Errorf("expected 4 entries (2 existing + PORT + ASPNETCORE_URLS), got %d: %v", len(env), env)
		}
		if !slices.Contains(env, "HOME=/home/user") || !slices.Contains(env, "PATH=/usr/bin") {
			t.Errorf("existing entries not preserved, got %v", env)
		}
	})
}

// ---- waitForPortReady + fetchLiveOpenAPI (C8) ----

// listenLoopback opens a TCP listener on 127.0.0.1:0 so the tests
// pick up an OS-assigned free port. Returns the listener and its
// port; the caller is responsible for closing the listener.
func listenLoopback(t *testing.T) (net.Listener, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return ln, ln.Addr().(*net.TCPAddr).Port
}

func TestWaitForPortReady_ReturnsTrueWhenPortIsBound(t *testing.T) {
	t.Parallel()
	ln, port := listenLoopback(t)
	t.Cleanup(func() { _ = ln.Close() })
	ok := waitForPortReady(t.Context(), port, 2*time.Second)
	if !ok {
		t.Fatalf("waitForPortReady returned false for bound port %d", port)
	}
}

func TestWaitForPortReady_ReturnsFalseWhenBudgetElapses(t *testing.T) {
	t.Parallel()
	// Grab a port and immediately release it so the dial reliably
	// fails. There's still a small race where another process could
	// re-bind it; using 127.0.0.1 instead of 0.0.0.0 keeps that
	// surface tiny in CI.
	ln, port := listenLoopback(t)
	_ = ln.Close()
	start := time.Now()
	ok := waitForPortReady(t.Context(), port, 200*time.Millisecond)
	elapsed := time.Since(start)
	if ok {
		t.Fatalf("waitForPortReady returned true for closed port %d", port)
	}
	if elapsed < 150*time.Millisecond {
		t.Fatalf("waitForPortReady returned before exhausting budget (%s)", elapsed)
	}
}

func TestWaitForPortReady_ReturnsFalseOnContextCancellation(t *testing.T) {
	t.Parallel()
	ln, port := listenLoopback(t)
	_ = ln.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	ok := waitForPortReady(ctx, port, 2*time.Second)
	if ok {
		t.Fatalf("waitForPortReady returned true for cancelled ctx")
	}
}

func TestFetchLiveOpenAPI_Returns200Body(t *testing.T) {
	t.Parallel()
	body := []byte(`{"paths":{"/invocations":{"post":{}}}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/invocations/docs/openapi.json" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	// Extract the port from the test server's URL so fetchLiveOpenAPI
	// (which hard-codes localhost) targets the right listener.
	u, err := net.ResolveTCPAddr("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatalf("parse srv.URL: %v", err)
	}
	got, err := fetchLiveOpenAPI(t.Context(), u.Port)
	if err != nil {
		t.Fatalf("fetchLiveOpenAPI: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("body mismatch: got %q want %q", got, body)
	}
}

func TestFetchLiveOpenAPI_ReturnsErrorOnNon200(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	u, err := net.ResolveTCPAddr("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatalf("parse srv.URL: %v", err)
	}
	_, err = fetchLiveOpenAPI(t.Context(), u.Port)
	if err == nil {
		t.Fatalf("expected non-nil error for 500 response")
	}
	if !strings.Contains(err.Error(), "openapi.json") {
		t.Fatalf("error %q missing expected prefix", err)
	}
}

func TestFetchLiveOpenAPI_HonoursContextCancellation(t *testing.T) {
	t.Parallel()
	// Server that never responds — used to verify the supplied ctx
	// (with a short deadline) reliably aborts the call. The
	// time.Sleep mimics a slow-spec endpoint without coupling to a
	// real network failure mode.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	u, err := net.ResolveTCPAddr("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatalf("parse srv.URL: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, err = fetchLiveOpenAPI(ctx, u.Port)
	if err == nil {
		t.Fatalf("expected error from cancelled fetch")
	}
	if !errors.Is(err, context.DeadlineExceeded) &&
		!strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("error %q does not signal deadline exceeded", err)
	}
}

func TestEmitNextAfterBind_ReturnsSilentlyWhenPortNeverBinds(t *testing.T) {
	t.Parallel()
	// Grab and release a port so the dial reliably fails for the
	// duration of the test. emitNextAfterBind must return without
	// panicking even with a nil azdClient — the early-return paths
	// (non-TTY stdout in `go test`, then port-bind timeout) execute
	// before AssembleState is reached.
	ln, port := listenLoopback(t)
	_ = ln.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		// Bound the call so the default 5s budget doesn't block the
		// test. The non-TTY gate fires first in `go test` (stdout is
		// the test harness's pipe), so this primarily exercises the
		// gate; with that gate removed, waitForPortReady's
		// ctx-cancel path takes over.
		ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
		defer cancel()
		emitNextAfterBind(ctx, nil, "svc", port)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("emitNextAfterBind did not exit within 2s")
	}
}

func TestEmitNextAfterBind_ReturnsSilentlyOnContextCancellation(t *testing.T) {
	t.Parallel()
	// A live listener guarantees we'd otherwise progress past
	// waitForPortReady; cancelling ctx immediately forces the
	// goroutine to exit via the non-TTY gate or AssembleState
	// returning quickly without printing.
	ln, port := listenLoopback(t)
	t.Cleanup(func() { _ = ln.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		emitNextAfterBind(ctx, nil, "svc", port)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("emitNextAfterBind did not honor ctx cancel within 2s")
	}
}

func TestFindAgentYaml(t *testing.T) {
	t.Parallel()

	t.Run("finds agent.yaml", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte("name: test"), 0600); err != nil {
			t.Fatal(err)
		}
		got := findAgentYaml(dir)
		if got != filepath.Join(dir, "agent.yaml") {
			t.Errorf("expected agent.yaml path, got %q", got)
		}
	})

	t.Run("finds agent.yml", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "agent.yml"), []byte("name: test"), 0600); err != nil {
			t.Fatal(err)
		}
		got := findAgentYaml(dir)
		if got != filepath.Join(dir, "agent.yml") {
			t.Errorf("expected agent.yml path, got %q", got)
		}
	})

	t.Run("prefers agent.yaml over agent.yml", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte("name: yaml"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "agent.yml"), []byte("name: yml"), 0600); err != nil {
			t.Fatal(err)
		}
		got := findAgentYaml(dir)
		if got != filepath.Join(dir, "agent.yaml") {
			t.Errorf("expected agent.yaml (preferred), got %q", got)
		}
	})

	t.Run("returns empty for missing directory", func(t *testing.T) {
		got := findAgentYaml(filepath.Join(t.TempDir(), "nonexistent"))
		if got != "" {
			t.Errorf("expected empty, got %q", got)
		}
	})
}

func TestResolveAgentDefinitionEnvVars(t *testing.T) {
	t.Parallel()

	parse := func(t *testing.T, y string) *agent_yaml.ContainerAgent {
		t.Helper()
		var def agent_yaml.ContainerAgent
		if err := goyaml.Unmarshal([]byte(y), &def); err != nil {
			t.Fatalf("failed to parse agent definition: %v", err)
		}
		return &def
	}

	t.Run("hardcoded values", func(t *testing.T) {
		def := parse(t, `name: test-agent
environment_variables:
  - name: TOOLBOX_NAME
    value: my-toolbox
  - name: LOG_LEVEL
    value: debug
`)

		result, err := resolveAgentDefinitionEnvVars(t.Context(), def, nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !slices.Contains(result, "TOOLBOX_NAME=my-toolbox") {
			t.Errorf("expected TOOLBOX_NAME=my-toolbox, got %v", result)
		}
		if !slices.Contains(result, "LOG_LEVEL=debug") {
			t.Errorf("expected LOG_LEVEL=debug, got %v", result)
		}
	})

	t.Run("resolves ${VAR} references", func(t *testing.T) {
		def := parse(t, `name: test-agent
environment_variables:
  - name: MY_ENDPOINT
    value: ${FOUNDRY_PROJECT_ENDPOINT}/agents
  - name: PLAIN
    value: hardcoded
`)

		azdEnv := map[string]string{
			"FOUNDRY_PROJECT_ENDPOINT": "https://example.azure.com",
		}
		result, err := resolveAgentDefinitionEnvVars(t.Context(), def, azdEnv, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !slices.Contains(result, "MY_ENDPOINT=https://example.azure.com/agents") {
			t.Errorf("expected resolved endpoint, got %v", result)
		}
		if !slices.Contains(result, "PLAIN=hardcoded") {
			t.Errorf("expected PLAIN=hardcoded, got %v", result)
		}
	})

	t.Run("skips connection refs without endpoint", func(t *testing.T) {
		def := parse(t, `name: test-agent
environment_variables:
  - name: API_KEY
    value: "${{connections.my-conn.credentials.key}}"
  - name: STATIC
    value: hello
`)

		result, err := resolveAgentDefinitionEnvVars(t.Context(), def, nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Should have STATIC but not API_KEY (connection ref with no endpoint)
		if !slices.Contains(result, "STATIC=hello") {
			t.Errorf("expected STATIC=hello, got %v", result)
		}
		for _, entry := range result {
			if strings.HasPrefix(entry, "API_KEY=") {
				t.Errorf("did not expect API_KEY in result (no endpoint), got %v", result)
			}
		}
	})

	t.Run("returns nil for nil definition", func(t *testing.T) {
		result, err := resolveAgentDefinitionEnvVars(t.Context(), nil, nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != nil {
			t.Errorf("expected nil, got %v", result)
		}
	})

	t.Run("returns nil for empty environment_variables", func(t *testing.T) {
		def := parse(t, "name: test-agent\n")

		result, err := resolveAgentDefinitionEnvVars(t.Context(), def, nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != nil {
			t.Errorf("expected nil, got %v", result)
		}
	})

	t.Run("unresolved ${VAR} becomes empty", func(t *testing.T) {
		def := parse(t, `name: test-agent
environment_variables:
  - name: MISSING_REF
    value: ${DOES_NOT_EXIST}
`)

		result, err := resolveAgentDefinitionEnvVars(t.Context(), def, map[string]string{}, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !slices.Contains(result, "MISSING_REF=") {
			t.Errorf("expected MISSING_REF= (empty), got %v", result)
		}
	})
}

func TestResolveServiceEnvironmentVars(t *testing.T) {
	t.Parallel()

	result, err := resolveServiceEnvironmentVars(
		t.Context(),
		map[string]string{
			"ENDPOINT": "${FOUNDRY_PROJECT_ENDPOINT}/agents",
			"PROJECT":  "${{project.endpoint}}",
			"STATIC":   "value",
		},
		map[string]string{
			"FOUNDRY_PROJECT_ENDPOINT": "https://example",
		},
		"https://example/project",
	)

	if err != nil {
		t.Fatalf("resolve service environment: %v", err)
	}
	want := []string{
		"ENDPOINT=https://example/agents",
		"PROJECT=https://example/project",
		"STATIC=value",
	}
	if !slices.Equal(want, result) {
		t.Fatalf("expected %v, got %v", want, result)
	}
}

func TestVenvPip(t *testing.T) {
	t.Parallel()

	venvDir := "/project/.venv"
	result := venvPip(venvDir)

	if runtime.GOOS == "windows" {
		expected := filepath.Join(venvDir, "Scripts", "pip.exe")
		if result != expected {
			t.Errorf("expected %q, got %q", expected, result)
		}
	} else {
		expected := filepath.Join(venvDir, "bin", "pip")
		if result != expected {
			t.Errorf("expected %q, got %q", expected, result)
		}
	}
}

func TestMinPythonUvSpec(t *testing.T) {
	// The uv `--python` specifier must derive from the shared constants so the
	// uv and pip paths cannot drift apart.
	want := fmt.Sprintf(">=%d.%d", minPythonMajor, minPythonMinor)
	if got := minPythonUvSpec(); got != want {
		t.Errorf("minPythonUvSpec() = %q, want %q", got, want)
	}
}

func TestPythonVersionOK(t *testing.T) {
	tests := []struct {
		major, minor int
		want         bool
	}{
		{3, 13, true},
		{3, 14, true},
		{4, 0, true},
		{3, 12, false},
		{3, 11, false},
		{2, 7, false},
	}
	for _, tc := range tests {
		if got := pythonVersionOK(tc.major, tc.minor); got != tc.want {
			t.Errorf("pythonVersionOK(%d, %d) = %v, want %v", tc.major, tc.minor, got, tc.want)
		}
	}
}

func TestFirstCompatiblePython(t *testing.T) {
	// versionMap maps interpreter path -> version parts returned by the fake
	// version function. A missing entry simulates a candidate that fails to
	// report its version (e.g. a Windows Store stub) and should be skipped.
	newVersionFn := func(versions map[string][3]any) func(pythonInterpreter) (int, int, string, error) {
		return func(p pythonInterpreter) (int, int, string, error) {
			v, ok := versions[p.path]
			if !ok {
				return 0, 0, "", errors.New("cannot run")
			}
			return v[0].(int), v[1].(int), v[2].(string), nil
		}
	}

	t.Run("prefers first compatible candidate", func(t *testing.T) {
		// Mirrors the Windows repro: `python` (first on PATH) is 3.11 but the
		// `py -3` launcher (probed first) selects 3.13.
		candidates := []pythonInterpreter{
			{path: "py", args: []string{"-3"}},
			{path: "python", args: nil},
		}
		versionFn := newVersionFn(map[string][3]any{
			"py":     {3, 13, "3.13.2"},
			"python": {3, 11, "3.11.9"},
		})

		got, err := firstCompatiblePython(candidates, versionFn)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.path != "py" || !slices.Equal(got.args, []string{"-3"}) {
			t.Errorf("got %+v, want py -3", got)
		}
	})

	t.Run("skips too-old candidate for a later compatible one", func(t *testing.T) {
		candidates := []pythonInterpreter{
			{path: "python3"},
			{path: "python"},
		}
		versionFn := newVersionFn(map[string][3]any{
			"python3": {3, 11, "3.11.9"},
			"python":  {3, 13, "3.13.0"},
		})

		got, err := firstCompatiblePython(candidates, versionFn)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.path != "python" {
			t.Errorf("got %q, want python", got.path)
		}
	})

	t.Run("errors naming newest incompatible version", func(t *testing.T) {
		candidates := []pythonInterpreter{
			{path: "python3"},
			{path: "python"},
		}
		versionFn := newVersionFn(map[string][3]any{
			"python3": {3, 11, "3.11.9"},
			"python":  {3, 12, "3.12.4"},
		})

		_, err := firstCompatiblePython(candidates, versionFn)
		if err == nil {
			t.Fatal("expected an error when all candidates are too old")
		}
		if !strings.Contains(err.Error(), "3.13+ is required") ||
			!strings.Contains(err.Error(), "3.12.4") {
			t.Errorf("error should name the required and newest-found versions, got: %v", err)
		}
	})

	t.Run("skips candidates whose version cannot be determined", func(t *testing.T) {
		candidates := []pythonInterpreter{
			{path: "broken"},
			{path: "python"},
		}
		versionFn := newVersionFn(map[string][3]any{
			// "broken" intentionally absent -> versionFn returns an error.
			"python": {3, 13, "3.13.1"},
		})

		got, err := firstCompatiblePython(candidates, versionFn)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.path != "python" {
			t.Errorf("got %q, want python", got.path)
		}
	})

	t.Run("errors when no candidates resolve a version", func(t *testing.T) {
		_, err := firstCompatiblePython(nil, newVersionFn(nil))
		if err == nil {
			t.Fatal("expected an error when there are no candidates")
		}
		if !strings.Contains(err.Error(), "no compatible Python") {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestFindSystemPython_NoPython(t *testing.T) {
	dir := t.TempDir() // empty directory, no python on PATH
	t.Setenv("PATH", dir)
	if _, err := findSystemPython(); err == nil {
		t.Fatal("expected error when no python on PATH")
	}
}

func TestPythonVersion(t *testing.T) {
	// Requires a real python on PATH to run --version.
	candidates := pythonCandidates()
	if len(candidates) == 0 {
		t.Skip("python not available on PATH")
	}

	major, minor, raw, err := pythonVersion(candidates[0])
	if err != nil {
		// A Windows Store stub can be found by LookPath but fail to execute.
		if strings.Contains(err.Error(), "failed to check Python version") {
			t.Skip("python present but not executable")
		}
		t.Fatalf("unexpected error: %v", err)
	}
	if major <= 0 {
		t.Errorf("expected a positive major version, got %d", major)
	}
	if raw == "" {
		t.Error("expected a non-empty raw version string")
	}
	_ = minor
}

func TestPlaygroundMessagesURLUsesIPv4Loopback(t *testing.T) {
	t.Parallel()

	got := playgroundMessagesURL(8088)
	want := "http://127.0.0.1:8088/api/messages"
	if got != want {
		t.Fatalf("playgroundMessagesURL = %q, want %q", got, want)
	}
	if strings.Contains(got, "localhost") {
		t.Fatalf("playground URL must use 127.0.0.1, not localhost: %q", got)
	}
}

func TestPlaygroundCommandArgs(t *testing.T) {
	t.Parallel()

	t.Run("explicit channel", func(t *testing.T) {
		t.Parallel()

		got := playgroundCommandArgs(9090, "emulator")
		want := []string{"agentsplayground", "-e", "http://127.0.0.1:9090/api/messages", "-c", "emulator"}
		if !slices.Equal(got, want) {
			t.Fatalf("playgroundCommandArgs = %v, want %v", got, want)
		}
	})

	t.Run("empty channel falls back to default", func(t *testing.T) {
		t.Parallel()

		got := playgroundCommandArgs(8088, "")
		want := []string{"agentsplayground", "-e", "http://127.0.0.1:8088/api/messages", "-c", "emulator"}
		if !slices.Equal(got, want) {
			t.Fatalf("playgroundCommandArgs = %v, want %v", got, want)
		}
	})
}

func TestResolveActivityRunProfile(t *testing.T) {
	t.Parallel()

	t.Run("nil definition is not activity", func(t *testing.T) {
		t.Parallel()

		if resolveActivityRunProfile(nil).IsActivity {
			t.Fatal("nil definition should not resolve as activity")
		}
	})

	t.Run("activity endpoint resolves as activity", func(t *testing.T) {
		t.Parallel()

		def := &agent_yaml.ContainerAgent{
			AgentEndpoint: &agent_yaml.AgentEndpoint{Protocols: []string{"activity"}},
		}
		if !resolveActivityRunProfile(def).IsActivity {
			t.Fatal("activity endpoint should resolve as activity")
		}
	})

	t.Run("container-level activity protocol resolves as activity", func(t *testing.T) {
		t.Parallel()

		for _, name := range []string{"activity", "activity_protocol"} {
			def := &agent_yaml.ContainerAgent{
				Protocols: []agent_yaml.ProtocolVersionRecord{{Protocol: name}},
			}
			if !resolveActivityRunProfile(def).IsActivity {
				t.Fatalf("protocol %q should resolve as activity", name)
			}
		}
	})

	t.Run("non-activity definition is not activity", func(t *testing.T) {
		t.Parallel()

		if resolveActivityRunProfile(&agent_yaml.ContainerAgent{}).IsActivity {
			t.Fatal("empty definition should not resolve as activity")
		}
	})
}

func TestRunCommandActivityFlags(t *testing.T) {
	t.Parallel()

	cmd := newRunCommand(nil)
	if cmd.Flags().Lookup("no-client") == nil {
		t.Fatal("run command should expose --no-client")
	}
	channel := cmd.Flags().Lookup("channel")
	if channel == nil {
		t.Fatal("run command should expose --channel")
	}
	if channel.DefValue != defaultPlaygroundChannel {
		t.Fatalf("--channel default = %q, want %q", channel.DefValue, defaultPlaygroundChannel)
	}
	// --no-inspector is kept for back-compat but deprecated in favor of
	// --no-client: it must still resolve (so existing scripts work) yet carry
	// a deprecation message (so cobra hides it from help and warns on use).
	noInspector := cmd.Flags().Lookup("no-inspector")
	if noInspector == nil {
		t.Fatal("run command should still expose --no-inspector for back-compat")
	}
	if noInspector.Deprecated == "" {
		t.Fatal("--no-inspector should be marked deprecated in favor of --no-client")
	}
}

func TestHandlePlaygroundAutoLaunchSuppressed(t *testing.T) {
	t.Parallel()

	var buf lockedBuffer
	// Suppressed: must not warn or attempt anything even if the CLI is missing.
	handlePlaygroundAutoLaunch(t.Context(), 8088, "emulator", true, &buf)
	if buf.String() != "" {
		t.Fatalf("suppressed auto-launch should be silent, got: %q", buf.String())
	}
}

func TestMissingPlaygroundWarning(t *testing.T) {
	t.Parallel()

	warning := missingPlaygroundWarning(9090, "emulator")
	for _, want := range []string{
		"winget install agentsplayground",
		"agentsplayground -e http://127.0.0.1:9090/api/messages -c emulator",
	} {
		if !strings.Contains(warning, want) {
			t.Fatalf("warning missing %q:\n%s", want, warning)
		}
	}
}
