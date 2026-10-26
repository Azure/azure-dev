// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build linux

package e2elive

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	azdexec "github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const (
	a2aLiveGate  = "AZD_E2E_A2A_LIVE"
	a2aPreview   = "AZD_ALPHA_ENABLE_AGENTS_A2A"
	nativeMarker = "AZD_A2A_NATIVE_ECHO_OK"
	promptMarker = "AZD_A2A_PROMPT_TRANSLATION_OK"
)

// TestA2ALiveRetained is deliberately independent of runner: that runner tears
// resources down. This test NEVER registers resource/session/file cleanup.
func TestA2ALiveRetained(t *testing.T) {
	if os.Getenv(a2aLiveGate) != "true" {
		t.Skipf("set %s=true and the required inputs in a2a-live.md to opt in", a2aLiveGate)
	}
	inputs := map[string]string{}
	for _, key := range []string{
		"AZD_E2E_A2A_PROJECT_ID", "AZD_E2E_A2A_MODEL_DEPLOYMENT", "AZD_E2E_A2A_WORKROOT", "AZD_E2E_AZD_PATH",
	} {
		inputs[key] = strings.TrimSpace(os.Getenv(key))
		if inputs[key] == "" {
			t.Skipf("live A2A test requires %s; no commands have run", key)
		}
	}
	projectID := inputs["AZD_E2E_A2A_PROJECT_ID"]
	armPattern := `(?i)^/subscriptions/[a-f0-9-]+/resourceGroups/[^/]+/providers/` +
		`Microsoft.CognitiveServices/accounts/[^/]+/projects/[^/]+$`
	require.Regexp(t, armPattern, projectID, "supply an existing Foundry project ARM ID, not an endpoint")
	workroot := inputs["AZD_E2E_A2A_WORKROOT"]
	require.True(t, filepath.IsAbs(workroot), "workroot must be an explicit absolute artifact directory")
	azd := inputs["AZD_E2E_AZD_PATH"]
	require.True(t, filepath.IsAbs(azd), "azd binary path must be absolute; never fall back to PATH")
	info, err := os.Stat(azd)
	require.NoError(t, err)
	require.False(t, info.IsDir())
	require.NotZero(t, info.Mode()&0o111, "azd binary must be executable")
	az, err := exec.LookPath("az")
	require.NoError(t, err, "az is required for authenticated endpoint inspection/update, not deployment")
	require.NoError(t, os.MkdirAll(workroot, 0o700))
	root, err := os.MkdirTemp(workroot, "a2a-retained-")
	require.NoError(t, err)
	t.Logf("All projects, logs, private config copies and session state retained at %s", root)

	// Read existing auth/extension registration, but never modify the originals.
	// Unlike setupConfigDir, honor an explicitly selected AZD_CONFIG_DIR.
	env := os.Environ()
	for _, config := range []struct{ key, fallback, target string }{
		{"AZD_CONFIG_DIR", ".azd", "azd-config-private"},
		{"AZURE_CONFIG_DIR", ".azure", "azure-config-private"},
	} {
		source := os.Getenv(config.key)
		if source == "" {
			home, homeErr := os.UserHomeDir()
			require.NoError(t, homeErr)
			source = filepath.Join(home, config.fallback)
		}
		source, err = filepath.Abs(source)
		require.NoError(t, err)
		dest := filepath.Join(root, config.target)
		require.NoError(t, os.Mkdir(dest, 0o700))
		//nolint:gosec // G703: the opt-in caller selects an absolute config source; it is only read.
		if _, statErr := os.Stat(source); statErr == nil {
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			// Dereference config symlinks so subsequent auth/cache writes cannot
			// follow them back into user state; preserve extension executable bits.
			sourceReal, resolveErr := filepath.EvalSymlinks(source)
			require.NoError(t, resolveErr)
			rootReal, resolveErr := filepath.EvalSymlinks(root)
			require.NoError(t, resolveErr)
			relative, relativeErr := filepath.Rel(sourceReal, rootReal)
			require.NoError(t, relativeErr)
			require.True(t, relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)),
				"artifact workroot must be outside config source %s", source)
			//nolint:gosec // G702: fixed executable, no shell; canonical absolute paths follow the option terminator.
			err = exec.CommandContext(ctx, "cp", "-aL", "--", sourceReal+"/.", dest).Run()
			cancel()
			require.NoError(t, err, "copy private config from %s", source)
		} else {
			require.True(t, os.IsNotExist(statErr), "inspect config: %v", statErr)
		}
		env = a2aEnv(env, config.key, dest)
	}
	for key, value := range map[string]string{
		"AZURE_DEV_COLLECT_TELEMETRY": "no", "AZD_FORCE_TTY": "false", "NO_COLOR": "1", a2aPreview: "true",
	} {
		env = a2aEnv(env, key, value)
	}
	r := &a2aLiveRunner{t: t, root: root, azd: azd, az: az, env: env}
	ctx, cancel := context.WithTimeout(t.Context(), 75*time.Minute)
	defer cancel()

	// Init operates on a copied real Python A2A SDK fixture, not a synthetic Go
	// ZIP or a Responses implementation with a misleading A2A declaration.
	hostedDir := filepath.Join(root, "hosted-project")
	promptDir := filepath.Join(root, "prompt-project")
	for _, dir := range []string{hostedDir, promptDir} {
		require.NoError(t, os.Mkdir(dir, 0o700))
	}
	src := filepath.Join(hostedDir, "native-source")
	require.NoError(t, os.CopyFS(src, os.DirFS("testdata/a2a-hosted/native-echo-v1")))
	// MkdirTemp provides a unique suffix; agent identities are never reused.
	suffix := strings.TrimPrefix(filepath.Base(root), "a2a-retained-")
	nativeName, promptName := "a2a-native-"+suffix, "a2a-prompt-"+suffix
	t.Run("hosted", func(t *testing.T) {
		r := &a2aLiveRunner{t: t, root: r.root, azd: r.azd, az: r.az, env: r.env}
		r.must(ctx, hostedDir, "hosted-init", 5*time.Minute, true, "ai", "agent", "init",
			"--src", src, "--protocol", "a2a", "--project-id", projectID,
			"--agent-name", nativeName, "--deploy-mode", "code", "--runtime", "python_3_13",
			"--entry-point", "main.py", "--dep-resolution", "remote_build", "--no-prompt")
		hostedConfig, nativeService := a2aService(t, hostedDir)
		require.Equal(t, "hosted", nativeService["kind"])
		require.Equal(t, []any{map[string]any{"protocol": "a2a", "version": "1.0.0"}}, nativeService["protocols"])
		require.Equal(t, map[string]any{"protocols": []any{"a2a"}}, nativeService["agentEndpoint"])
		require.Equal(t, map[string]any{
			"runtime": "python_3_13", "entryPoint": "main.py", "dependencyResolution": "remote_build",
		}, nativeService["codeConfiguration"])
		r.must(ctx, hostedDir, "hosted-deploy", 15*time.Minute, true, "deploy", "--no-prompt")
		endpoint := r.projectEndpoint(ctx, hostedDir)
		nativeObject := r.getAgent(ctx, hostedDir, endpoint, nativeName, "hosted-deployed")
		a2aAssertEndpoint(t, nativeObject, []string{"a2a"})
		nativeDefinition := a2aLatestDefinition(t, nativeObject)
		require.Equal(t, "hosted", nativeDefinition["kind"])
		require.JSONEq(t, `[{"protocol":"a2a","version":"1.0.0"}]`, a2aJSON(t, nativeDefinition["protocol_versions"]))

		r.invoke(ctx, hostedDir, "hosted-message-10", true, "1.0", "", nativeMarker, true)
		nativeFile := r.requestFile("native-10", "1.0")
		r.invoke(ctx, hostedDir, "hosted-file-10", true, "1.0", nativeFile, nativeMarker, true)

		// Stale local source is deliberately NOT deployed. Explicit remote invokes
		// must still reach the deployed 1.0 agent; local capability advice is not a gate.
		nativeService["protocols"] = []any{map[string]any{"protocol": "a2a", "version": "0.3.0"}}
		stale, err := yaml.Marshal(hostedConfig)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(hostedDir, "azure.yaml"), stale, 0o600))
		r.invoke(ctx, hostedDir, "hosted-stale-local-03", true, "1.0", "", nativeMarker, true)
		// Compatibility diagnostics are developer logging (hidden without --debug).
		// The completed native Task proves the local finding did not prevent sending.
	})

	t.Run("prompt", func(t *testing.T) {
		r := &a2aLiveRunner{t: t, root: r.root, azd: r.azd, az: r.az, env: r.env}
		r.must(ctx, promptDir, "prompt-init", 5*time.Minute, true, "ai", "agent", "init",
			"--kind", "prompt", "--agent-name", promptName,
			"--model-deployment", inputs["AZD_E2E_A2A_MODEL_DEPLOYMENT"],
			"--instructions", "For every user message reply with exactly "+promptMarker+" and no other text.",
			"--project-id", projectID, "--no-prompt")
		promptDir = a2aProjectDir(t, promptDir, promptName)
		_, promptService := a2aService(t, promptDir)
		require.Equal(t, "prompt", promptService["kind"])
		require.Equal(t, inputs["AZD_E2E_A2A_MODEL_DEPLOYMENT"], promptService["model"])
		require.NotContains(t, promptService, "protocols", "prompt agents do not implement native A2A")
		require.NotContains(t, promptService, "codeConfiguration")
		r.must(ctx, promptDir, "prompt-deploy", 10*time.Minute, true, "deploy", "--no-prompt")
		promptEndpoint := r.projectEndpoint(ctx, promptDir)
		actualProject := r.must(ctx, promptDir, "prompt-project-id", time.Minute, true,
			"env", "get-value", "AZURE_AI_PROJECT_ID", "--no-prompt")
		require.Equal(t, projectID, strings.TrimSpace(string(actualProject)))
		// Prompt authored config and the endpoint-update CLI currently reject
		// agentEndpoint. Use the existing PatchAgent API shape, never a native
		// implementation declaration. Keep Responses enabled for translation.
		patchFile := filepath.Join(root, "prompt-endpoint-patch.json")
		require.NoError(t, os.WriteFile(patchFile,
			[]byte(`{"agent_endpoint":{"protocols":["responses","a2a"]}}`), 0o600))
		r.rest(ctx, promptDir, "prompt-endpoint-update", "PATCH",
			promptEndpoint+"/agents/"+promptName+"?api-version=v1", patchFile)
		promptObject := r.getAgent(ctx, promptDir, promptEndpoint, promptName, "prompt-deployed")
		a2aAssertEndpoint(t, promptObject, []string{"responses", "a2a"})
		promptDefinition := a2aLatestDefinition(t, promptObject)
		require.Equal(t, "prompt", promptDefinition["kind"])
		require.NotContains(t, promptDefinition, "protocol_versions")

		for _, version := range []string{"0.3", "1.0"} {
			r.invoke(ctx, promptDir, "prompt-message-"+version, true, version, "", promptMarker, false)
			r.invoke(ctx, promptDir, "prompt-file-"+version, true, version,
				r.requestFile("prompt-"+version, version), promptMarker, false)
		}
		// Feature-off legacy paths omit the flag and retain 0.3 message/file support.
		r.invoke(ctx, promptDir, "prompt-preview-off-message", false, "", "", promptMarker, false)
		r.invoke(ctx, promptDir, "prompt-preview-off-file", false, "",
			r.requestFile("prompt-legacy", "0.3"), promptMarker, false)
		stdout, stderr, rejectErr := r.command(ctx, promptDir, "preview-off-explicit-rejected", time.Minute, false,
			r.azd, "ai", "agent", "invoke", "--protocol", "a2a", "--a2a-version", "1.0", "hello", "--no-prompt")
		require.Error(t, rejectErr, "explicit preview flag must be rejected when preview is disabled")
		diagnostic := string(stdout) + string(stderr)
		require.Contains(t, diagnostic, "agents.a2a")
		require.Contains(t, diagnostic, a2aPreview)
		require.Contains(t, strings.ToLower(diagnostic), "disabled")
	})
	t.Logf("Test projects, logs, and session state retained at %s; Azure resources are not cleaned up", root)
}

type a2aLiveRunner struct {
	t             *testing.T
	root, azd, az string
	env           []string
	sequence      int
}

func a2aEnv(env []string, key, value string) []string {
	result := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, key+"=") {
			result = append(result, entry)
		}
	}
	return append(result, key+"="+value)
}

// command never enables debug or captures auth tokens. Redaction is defense in
// depth for unexpected CLI diagnostics; credentials remain in private config.
func (r *a2aLiveRunner) command(ctx context.Context, dir, label string, budget time.Duration,
	preview bool, binary string, args ...string,
) ([]byte, []byte, error) {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = dir
	cmd.Env = a2aEnv(r.env, a2aPreview, fmt.Sprint(preview))
	// Bound pipe draining even if a descendant escapes the process group.
	cmd.WaitDelay = 2 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	// CmdTree.Start installs process-group cancellation in the pinned azd SDK.
	// Do not use the embedded Cmd.Run: it bypasses that setup and kills only
	// the host, leaving extensions alive with inherited stdout/stderr pipes.
	tree := azdexec.CmdTree{Cmd: cmd}
	err := tree.Start()
	if err == nil {
		err = tree.Wait()
		if err != nil {
			// If the host exits first, os/exec can finish its context watcher
			// before pipe draining hits WaitDelay. Kill the remaining group
			// explicitly, rather than merely closing the child's pipes.
			tree.Kill()
		}
	}
	out, diagnostic := a2aRedact(stdout.Bytes()), a2aRedact(stderr.Bytes())
	r.sequence++
	prefix := filepath.Join(r.root, fmt.Sprintf("%02d-%s", r.sequence, label))
	metadata := map[string]any{
		"binary": binary, "args": args, "cwd": dir, "preview": preview,
		"elapsed": time.Since(start).String(), "succeeded": err == nil,
	}
	for suffix, content := range map[string][]byte{
		".stdout.txt": out, ".stderr.txt": diagnostic, ".command.json": []byte(a2aJSON(r.t, metadata)),
	} {
		require.NoError(r.t, os.WriteFile(prefix+suffix, content, 0o600))
	}
	// No arbitrary retries: even HTTP 200 may contain a terminal JSON-RPC error.
	r.t.Logf("%s: success=%t, elapsed=%s; evidence %s.*", label, err == nil, time.Since(start).Round(time.Second), prefix)
	return out, diagnostic, err
}

func (r *a2aLiveRunner) must(ctx context.Context, dir, label string, budget time.Duration,
	preview bool, args ...string,
) []byte {
	r.t.Helper()
	out, _, err := r.command(ctx, dir, label, budget, preview, r.azd, args...)
	require.NoError(r.t, err, "%s failed; inspect retained logs at %s", label, r.root)
	return out
}

func (r *a2aLiveRunner) projectEndpoint(ctx context.Context, dir string) string {
	// Read only this non-secret value; never dump the environment into logs.
	out := r.must(ctx, dir, filepath.Base(dir)+"-endpoint", time.Minute, true,
		"env", "get-value", "FOUNDRY_PROJECT_ENDPOINT", "--no-prompt")
	endpoint := strings.TrimRight(strings.TrimSpace(string(out)), "/")
	require.Regexp(r.t, `^https://[^/]+\.services\.ai\.azure\.com/api/projects/[^/]+$`, endpoint)
	return endpoint
}

func (r *a2aLiveRunner) rest(ctx context.Context, dir, label, method, url, bodyFile string) []byte {
	r.t.Helper()
	args := []string{"rest", "--method", method, "--url", url, "--resource", "https://ai.azure.com",
		"--only-show-errors", "--output", "json"}
	if bodyFile != "" {
		args = append(args, "--headers", "Content-Type=application/json", "--body", "@"+bodyFile)
	}
	out, _, err := r.command(ctx, dir, label, time.Minute, true, r.az, args...)
	require.NoError(r.t, err, "endpoint inspection/update failed; see %s", r.root)
	return out
}

func (r *a2aLiveRunner) getAgent(ctx context.Context, dir, endpoint, name, label string) map[string]any {
	var object map[string]any
	out := r.rest(ctx, dir, label, "GET", endpoint+"/agents/"+name+"?api-version=v1", "")
	require.NoError(r.t, json.Unmarshal(out, &object))
	return object
}

// a2aProjectDir handles init's documented subdirectory scaffold for prompt agents.
func a2aProjectDir(t *testing.T, dir, name string) string {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, "azure.yaml")); err == nil {
		return dir
	} else {
		require.True(t, os.IsNotExist(err), "inspect generated project: %v", err)
	}
	projectDir := filepath.Join(dir, name)
	_, err := os.Stat(filepath.Join(projectDir, "azure.yaml"))
	require.NoError(t, err, "init must create a project in the current directory or its named subdirectory")
	return projectDir
}

func a2aService(t *testing.T, dir string) (map[string]any, map[string]any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "azure.yaml"))
	require.NoError(t, err)
	var document map[string]any
	require.NoError(t, yaml.Unmarshal(data, &document))
	services := a2aMap(t, document["services"])
	var agents []map[string]any
	for _, service := range services {
		config := a2aMap(t, service)
		if config["host"] == "azure.ai.agent" {
			agents = append(agents, config)
		}
	}
	require.Len(t, agents, 1, "init must generate exactly one agent alongside its project service")
	return document, agents[0]
}

func a2aMap(t *testing.T, value any) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	require.True(t, ok, "expected object, got %T", value)
	return object
}

func a2aLatestDefinition(t *testing.T, object map[string]any) map[string]any {
	t.Helper()
	latest := a2aMap(t, a2aMap(t, object["versions"])["latest"])
	return a2aMap(t, latest["definition"])
}

func a2aAssertEndpoint(t *testing.T, object map[string]any, protocols []string) {
	t.Helper()
	endpoint := a2aMap(t, object["agent_endpoint"])
	actual, ok := endpoint["protocols"].([]any)
	require.True(t, ok, "endpoint protocols must be an array")
	expected := make([]any, len(protocols))
	for i, protocol := range protocols {
		expected[i] = protocol
	}
	require.ElementsMatch(t, expected, actual)
}

func a2aJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	require.NoError(t, err)
	return string(data)
}

func (r *a2aLiveRunner) requestFile(label, version string) string {
	method, role := "SendMessage", "ROLE_USER"
	part := map[string]any{"text": "Return the configured execution answer."}
	configuration := map[string]any{"returnImmediately": false, "acceptedOutputModes": []string{"text/plain"}}
	message := map[string]any{"messageId": label, "role": role, "parts": []any{part}}
	if version == "0.3" {
		method = "message/send"
		message["role"], message["kind"], part["kind"] = "user", "message", "text"
		configuration = map[string]any{"blocking": true, "acceptedOutputModes": []string{"text/plain"}}
	}
	request := map[string]any{"jsonrpc": "2.0", "id": label, "method": method,
		"params": map[string]any{"message": message, "configuration": configuration}}
	path := filepath.Join(r.root, label+".request.json")
	require.NoError(r.t, os.WriteFile(path, []byte(a2aJSON(r.t, request)), 0o600))
	return path
}

func (r *a2aLiveRunner) invoke(ctx context.Context, dir, label string, preview bool,
	version, file, marker string, taskRequired bool,
) {
	r.t.Helper()
	args := []string{"ai", "agent", "invoke", "--protocol", "a2a", "--output", "raw", "--no-prompt"}
	if version != "" {
		args = append(args, "--a2a-version", version)
	}
	if file == "" {
		args = append(args, "Return the configured execution answer.")
	} else {
		args = append(args, "--input-file", file)
	}
	out, _, err := r.command(ctx, dir, label, 4*time.Minute, preview, r.azd, args...)
	require.NoError(r.t, err, "%s failed; see %s", label, r.root)
	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(out)), nil)
	require.NoError(r.t, err, "raw output must contain an HTTP response")
	defer resp.Body.Close()
	require.Equal(r.t, http.StatusOK, resp.StatusCode)
	var envelope map[string]any
	require.NoError(r.t, json.NewDecoder(resp.Body).Decode(&envelope))
	wireVersion := version
	if wireVersion == "" {
		wireVersion = "0.3"
	}
	require.NoError(r.t, a2aCheckResult(envelope, wireVersion, marker, taskRequired, file == ""),
		"%s: HTTP success alone is not JSON-RPC/application success; see %s", label, r.root)
}

// a2aCheckResult inspects only answer artifacts or an agent Message, never user
// history or the echoed request. Only generated 0.3 messages may return a pending
// Task snapshot: the builder omits configuration, retaining nonblocking defaults.
// Such a snapshot proves acceptance, not completion or the execution marker.
// File requests remain caller-owned/blocking; hosted 1.0 requires a completed Task.
func a2aCheckResult(envelope map[string]any, version, marker string, taskRequired, generatedMessage bool) error {
	if envelope["jsonrpc"] != "2.0" || envelope["error"] != nil {
		return fmt.Errorf("invalid JSON-RPC envelope or error: %v", envelope["error"])
	}
	result, ok := envelope["result"].(map[string]any)
	if !ok {
		return fmt.Errorf("missing JSON-RPC result")
	}
	var task, message map[string]any
	if version == "1.0" {
		task, _ = result["task"].(map[string]any)
		message, _ = result["message"].(map[string]any)
	} else if result["kind"] == "task" {
		task = result
	} else if result["kind"] == "message" {
		message = result
	}
	var parts []any
	if task != nil {
		status, _ := task["status"].(map[string]any)
		state := "TASK_STATE_COMPLETED"
		if version == "0.3" {
			state = "completed"
		}
		id, _ := task["id"].(string)
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("task missing identity")
		}
		if version == "0.3" && generatedMessage && !taskRequired &&
			(status["state"] == "submitted" || status["state"] == "working") {
			return nil // Accepted pending snapshot only; no completed-answer claim.
		}
		if status["state"] != state {
			return fmt.Errorf("task not completed: %v", status["state"])
		}
		artifacts, _ := task["artifacts"].([]any)
		for _, artifact := range artifacts {
			object, _ := artifact.(map[string]any)
			artifactParts, _ := object["parts"].([]any)
			parts = append(parts, artifactParts...)
		}
	} else if !taskRequired && message != nil && (message["role"] == "agent" || message["role"] == "ROLE_AGENT") {
		parts, _ = message["parts"].([]any)
	} else {
		return fmt.Errorf("expected completed Task or agent Message in A2A %s result", version)
	}
	var texts []string
	for _, part := range parts {
		object, _ := part.(map[string]any)
		if text, ok := object["text"].(string); ok {
			texts = append(texts, text)
		}
	}
	if strings.TrimSpace(strings.Join(texts, "")) != marker {
		return fmt.Errorf("answer parts did not equal the expected execution marker")
	}
	return nil
}

func a2aRedact(data []byte) []byte {
	text := string(data)
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(key)
		if len(value) >= 8 && (strings.Contains(upper, "TOKEN") || strings.Contains(upper, "SECRET")) {
			text = strings.ReplaceAll(text, value, "[REDACTED]")
		}
	}
	text = regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._~+/-]+=*`).ReplaceAllString(text, "Bearer [REDACTED]")
	text = regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`).ReplaceAllString(text, "[REDACTED]")
	return []byte(text)
}

func TestA2ALiveCommandTimeoutOffline(t *testing.T) {
	for _, hostExit := range []bool{false, true} {
		name := "host-waits"
		if hostExit {
			name = "host-exits-with-child-holding-pipes"
		}
		t.Run(name, func(t *testing.T) {
			// Retain diagnostics, just as the live runner does. Never touch Azure
			// or user config; the only process we launch is our own shell tree.
			root, err := os.MkdirTemp("", "a2a-command-timeout-")
			require.NoError(t, err)
			t.Logf("Offline timeout diagnostics retained at %s", root)
			r := &a2aLiveRunner{t: t, root: root, env: os.Environ()}
			script := "sleep 10 &\nprintf '%s' \"$!\" > child.pid\nprintf 'child started\\n' >&2\n"
			if !hostExit {
				script += "wait\n"
			}
			start := time.Now()
			_, diagnostic, commandErr := r.command(t.Context(), root, "timeout", time.Second, false,
				"/bin/sh", "-c", script)
			require.Error(t, commandErr)
			require.Less(t, time.Since(start), 5*time.Second, "command must not wait for the child's pipe lifetime")
			require.Contains(t, string(diagnostic), "child started")
			pidBytes, err := os.ReadFile(filepath.Join(root, "child.pid"))
			require.NoError(t, err)
			pid, err := strconv.Atoi(string(pidBytes))
			require.NoError(t, err)
			require.Positive(t, pid)
			// A killed orphan can remain a zombie until the container's init
			// reaps it. Both absent and zombie mean it can no longer hold pipes.
			require.Eventually(t, func() bool {
				stat, statErr := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
				if os.IsNotExist(statErr) {
					return true
				}
				_, afterName, ok := strings.Cut(string(stat), ") ")
				return statErr == nil && ok && strings.HasPrefix(afterName, "Z ")
			}, 2*time.Second, 20*time.Millisecond, "owned child must be killed, not merely have its pipes closed")
			for _, suffix := range []string{".stdout.txt", ".stderr.txt", ".command.json"} {
				data, err := os.ReadFile(filepath.Join(root, "01-timeout"+suffix))
				require.NoError(t, err)
				if suffix == ".command.json" {
					var metadata map[string]any
					require.NoError(t, json.Unmarshal(data, &metadata))
					require.Equal(t, false, metadata["succeeded"])
				}
			}
		})
	}
}

func TestA2ALivePendingResultChecksOffline(t *testing.T) {
	for _, mode := range []struct {
		name, version                  string
		taskRequired, generatedMessage bool
	}{
		{"generated 03", "0.3", false, true},
		{"blocking file 03", "0.3", false, false},
		{"completed task only 03", "0.3", true, true},
		{"generated 10", "1.0", false, true},
		{"blocking file 10", "1.0", false, false},
		{"native 10", "1.0", true, true},
	} {
		t.Run(mode.name, func(t *testing.T) {
			for _, tc := range []struct {
				name, state, answer string
				id                  any
				pending, completed  bool
			}{
				{"submitted without answer", "submitted", "", "t", true, false},
				{"working without answer", "working", "", "t", true, false},
				{"submitted missing id", "submitted", "", nil, false, false},
				{"working empty id", "working", "", "", false, false},
				{"working blank id", "working", "", " ", false, false},
				{"working nonstring id", "working", "", 123, false, false},
				{"completed marker", "completed", "marker", "t", false, true},
				{"completed missing marker", "completed", "", "t", false, false},
				{"completed wrong marker", "completed", "wrong", "t", false, false},
				{"failed even with marker", "failed", "marker", "t", false, false},
				{"canceled even with marker", "canceled", "marker", "t", false, false},
				{"rejected even with marker", "rejected", "marker", "t", false, false},
				{"input required", "input-required", "marker", "t", false, false},
				{"unknown state", "unknown", "marker", "t", false, false},
				{"missing state", "", "marker", "t", false, false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					state := tc.state
					if mode.version == "1.0" && state != "" {
						state = "TASK_STATE_" + strings.ToUpper(strings.ReplaceAll(state, "-", "_"))
					}
					task := map[string]any{"kind": "task", "id": tc.id, "status": map[string]any{"state": state}}
					if tc.answer != "" {
						task["artifacts"] = []any{map[string]any{"parts": []any{map[string]any{"text": tc.answer}}}}
					}
					result := task
					if mode.version == "1.0" {
						result = map[string]any{"task": task}
					}
					envelope := map[string]any{"jsonrpc": "2.0", "result": result}
					check := func() error {
						return a2aCheckResult(envelope, mode.version, "marker", mode.taskRequired, mode.generatedMessage)
					}
					valid := tc.completed || (tc.pending && mode.version == "0.3" && mode.generatedMessage && !mode.taskRequired)
					if valid {
						require.NoError(t, check())
					} else {
						require.Error(t, check())
					}
					// An otherwise acceptable result never overrides a JSON-RPC error.
					envelope["error"] = map[string]any{"code": -32603, "message": "marker"}
					require.Error(t, check())
					delete(envelope, "error")
					envelope["jsonrpc"] = "invalid"
					require.Error(t, check())
				})
			}
		})
	}

	// Relaxing Task snapshots must not relax deterministic agent Message checks.
	for _, answer := range []string{"marker", "", "wrong"} {
		t.Run("generated 03 message answer "+answer, func(t *testing.T) {
			envelope := map[string]any{"jsonrpc": "2.0", "result": map[string]any{
				"kind": "message", "role": "agent", "parts": []any{map[string]any{"text": answer}},
			}}
			err := a2aCheckResult(envelope, "0.3", "marker", false, true)
			if answer == "marker" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestA2ALiveResultChecksOffline(t *testing.T) {
	for _, tc := range []struct {
		name, body, version string
		taskRequired, valid bool
	}{
		{"native task", `{"jsonrpc":"2.0","result":{"task":{"id":"t","status":{"state":"TASK_STATE_COMPLETED"},` +
			`"artifacts":[{"parts":[{"text":"marker"}]}]}}}`, "1.0", true, true},
		{"legacy message", `{"jsonrpc":"2.0","result":{"kind":"message","role":"agent",` +
			`"parts":[{"kind":"text","text":"marker"}]}}`, "0.3", false, true},
		{"modern message", `{"jsonrpc":"2.0","result":{"message":{"role":"ROLE_AGENT",` +
			`"parts":[{"text":"marker"}]}}}`, "1.0", false, true},
		{"error at HTTP 200", `{"jsonrpc":"2.0","error":{"code":-32601,"message":"marker"}}`, "1.0", false, false},
		{"echoed input", `{"jsonrpc":"2.0","result":{"message":{"role":"ROLE_USER",` +
			`"parts":[{"text":"marker"}]}}}`, "1.0", false, false},
		{"message not native task", `{"jsonrpc":"2.0","result":{"message":{"role":"ROLE_AGENT",` +
			`"parts":[{"text":"marker"}]}}}`, "1.0", true, false},
		{"nonterminal task", `{"jsonrpc":"2.0","result":{"task":{"id":"t","status":{"state":"TASK_STATE_WORKING"},` +
			`"artifacts":[{"parts":[{"text":"marker"}]}]}}}`, "1.0", true, false},
		{"legacy task", `{"jsonrpc":"2.0","result":{"kind":"task","id":"t","status":{"state":"completed"},` +
			`"artifacts":[{"parts":[{"kind":"text","text":"marker"}]}]}}`, "0.3", false, true},
		{"marker only in history", `{"jsonrpc":"2.0","result":{"task":{"id":"t",` +
			`"status":{"state":"TASK_STATE_COMPLETED"},"history":[{"parts":[{"text":"marker"}]}]}}}`,
			"1.0", true, false},
		{"empty result", `{"jsonrpc":"2.0","result":null}`, "1.0", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var envelope map[string]any
			require.NoError(t, json.Unmarshal([]byte(tc.body), &envelope))
			err := a2aCheckResult(envelope, tc.version, "marker", tc.taskRequired, false)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
