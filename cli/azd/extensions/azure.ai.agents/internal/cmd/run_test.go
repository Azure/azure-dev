// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
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
		time.Second,
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

	t.Run("maps AZURE_AI_PROJECT_ENDPOINT to FOUNDRY_PROJECT_ENDPOINT", func(t *testing.T) {
		t.Parallel()
		azdEnv := map[string]string{
			"AZURE_AI_PROJECT_ENDPOINT": "https://myaccount.services.ai.azure.com/api/projects/myproject",
		}
		env := appendFoundryEnvVars(nil, azdEnv, "")
		expected := "FOUNDRY_PROJECT_ENDPOINT=https://myaccount.services.ai.azure.com/api/projects/myproject"
		if !slices.Contains(env, expected) {
			t.Errorf("expected %q in env, got %v", expected, env)
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
			"AZURE_AI_PROJECT_ENDPOINT": "https://acct.services.ai.azure.com/api/projects/proj",
			"AZURE_AI_PROJECT_ID":       "/subscriptions/sub/rg/rg/acct/proj",
			"AGENT_AGENT1_NAME":         "agent1",
			"AGENT_AGENT1_VERSION":      "v1",
		}
		env := appendFoundryEnvVars(nil, azdEnv, "agent1")
		if len(env) != 4 {
			t.Errorf("expected 4 env vars, got %d: %v", len(env), env)
		}
	})

	t.Run("skips foundry key when already set in azd env", func(t *testing.T) {
		t.Parallel()
		azdEnv := map[string]string{
			"AZURE_AI_PROJECT_ENDPOINT": "https://from-azd.services.ai.azure.com",
			"FOUNDRY_PROJECT_ENDPOINT":  "https://explicit.services.ai.azure.com",
			"AGENT_MY_SVC_NAME":         "my-agent",
			"FOUNDRY_AGENT_NAME":        "explicit-agent",
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
			"AZURE_AI_PROJECT_ENDPOINT": "https://from-azd.services.ai.azure.com",
			"AZURE_AI_PROJECT_ID":       "/subscriptions/sub/rg/rg/acct/proj",
			"AGENT_MY_SVC_NAME":         "my-agent",
			"AGENT_MY_SVC_VERSION":      "v2",
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
		if env[0] != "HOME=/home/user" || env[1] != "PATH=/usr/bin" {
			t.Errorf("existing entries not preserved, got %v", env)
		}
	})
}
