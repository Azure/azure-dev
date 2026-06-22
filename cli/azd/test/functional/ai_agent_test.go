// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build record

package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/azure/azure-dev/cli/azd/test/recording"
	"github.com/stretchr/testify/require"
)

// manifestPath returns the absolute path to the local test manifest file.
func manifestPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "testdata", "manifests", "foundry-toolbox.yaml")
}

// --- Tier 0: Offline tests (no Azure, no recording needed) ---

// Test_AIAgent_Version verifies the extension version command works.
func Test_AIAgent_Version(t *testing.T) {
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)

	result, err := cli.RunCommand(ctx, "ai", "agent", "version")
	require.NoError(t, err, "stdout=%s, stderr=%s", result.Stdout, result.Stderr)
	require.Contains(t, result.Stdout, "Version:")
}

// Test_AIAgent_Help verifies the extension help command lists subcommands.
func Test_AIAgent_Help(t *testing.T) {
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)

	result, err := cli.RunCommand(ctx, "ai", "agent", "--help")
	require.NoError(t, err, "stdout=%s, stderr=%s", result.Stdout, result.Stderr)
	require.Contains(t, result.Stdout, "init")
	require.Contains(t, result.Stdout, "invoke")
}

// Test_AIAgent_Init_NoPrompt_Defer verifies --no-prompt defer path (no ARM calls).
// When --project-id is omitted, the extension writes scaffold files without calling ARM.
// Uses a recording session for fake auth (extension validates login status).
func Test_AIAgent_Init_NoPrompt_Defer(t *testing.T) {
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)

	session := recording.Start(t)
	session.Variables[recording.SubscriptionIdKey] = "00000000-0000-0000-0000-000000000000"

	cli := azdcli.NewCLI(t, azdcli.WithSession(session))
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)

	result, err := cli.RunCommand(ctx,
		"ai", "agent", "init", "--no-prompt",
		"-m", manifestPath(t),
		"--deploy-mode", "code",
		"--runtime", "python_3_13",
		"--entry-point", "app:app",
		"--agent-name", "test-defer-agent",
		"--force",
	)
	require.NoError(t, err, "ai agent init failed: stdout=%s, stderr=%s", result.Stdout, result.Stderr)
	require.Contains(t, result.Stdout, "AI agent definition added to your azd project successfully!")

	// Verify generated files exist under agent source dir
	// Init creates: <dir>/test-defer-agent/src/test-defer-agent/agent.yaml
	agentDir := filepath.Join(dir, "test-defer-agent", "src", "test-defer-agent")
	require.DirExists(t, agentDir)
	require.FileExists(t, filepath.Join(agentDir, "agent.yaml"))
}

// Test_AIAgent_Init_NoPrompt_MissingFlags verifies --no-prompt without required flags errors.
func Test_AIAgent_Init_NoPrompt_MissingFlags(t *testing.T) {
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)

	// Missing -m flag should fail
	result, err := cli.RunCommand(ctx,
		"ai", "agent", "init", "--no-prompt",
		"--deploy-mode", "code",
	)
	require.Error(t, err, "should fail without -m flag: stdout=%s", result.Stdout)
}

// Test_AIAgent_SampleList verifies sample list returns results.
func Test_AIAgent_SampleList(t *testing.T) {
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)

	result, err := cli.RunCommand(ctx, "ai", "agent", "sample", "list")
	require.NoError(t, err, "stdout=%s, stderr=%s", result.Stdout, result.Stderr)
	require.Greater(t, len(result.Stdout), 50, "sample list output too short")
}

// Test_AIAgent_SampleList_JSON verifies sample list --output json returns valid JSON array.
func Test_AIAgent_SampleList_JSON(t *testing.T) {
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)

	result, err := cli.RunCommand(ctx, "ai", "agent", "sample", "list", "--output", "json")
	require.NoError(t, err, "stdout=%s, stderr=%s", result.Stdout, result.Stderr)

	var output map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(result.Stdout), &output), "output is not valid JSON: %s", result.Stdout)
	require.Contains(t, output, "templates", "expected 'templates' key in JSON output")
}

// Test_AIAgent_Doctor_Help verifies doctor --help shows usage.
func Test_AIAgent_Doctor_Help(t *testing.T) {
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)

	result, err := cli.RunCommand(ctx, "ai", "agent", "doctor", "--help")
	require.NoError(t, err, "stdout=%s, stderr=%s", result.Stdout, result.Stderr)
	require.Contains(t, result.Stdout, "doctor")
}

// --- Tier 1: Recording tests (ARM calls replayed from cassette) ---

// Test_AIAgent_Init_NoPrompt_WithProject verifies init with --project-id resolves the project,
// configures models, and generates all scaffold files. Uses recording proxy for ARM calls.
//
// Record:  AZURE_RECORD_MODE=record TEST_FOUNDRY_PROJECT_ID=<arm-id> go test -tags=record -run Test_AIAgent_Init_NoPrompt_WithProject -v -timeout 10m
// Replay:  AZURE_RECORD_MODE=playback go test -tags=record -run Test_AIAgent_Init_NoPrompt_WithProject -v -timeout 5m
func Test_AIAgent_Init_NoPrompt_WithProject(t *testing.T) {
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)
	t.Logf("DIR: %s", dir)

	session := recording.Start(t)
	subscriptionId := "1756abc0-3554-4341-8d6a-46674962ea19"
	session.Variables[recording.SubscriptionIdKey] = subscriptionId

	cli := azdcli.NewCLI(t, azdcli.WithSession(session))
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)

	projectId := os.Getenv("TEST_FOUNDRY_PROJECT_ID")
	if projectId == "" {
		projectId = session.Variables["project_id"]
	}
	if projectId == "" {
		projectId = "/subscriptions/1756abc0-3554-4341-8d6a-46674962ea19/resourceGroups/rg-hello-world-python-responses-dev-79ba4103/providers/Microsoft.CognitiveServices/accounts/wujia-6956-resource/projects/wujia-1670"
	}
	session.Variables["project_id"] = projectId

	modelDeployment := os.Getenv("TEST_MODEL_DEPLOYMENT")
	if modelDeployment == "" {
		modelDeployment = session.Variables["model_deployment"]
	}
	if modelDeployment == "" {
		modelDeployment = "gpt-4o"
	}
	session.Variables["model_deployment"] = modelDeployment

	result, err := cli.RunCommand(ctx,
		"ai", "agent", "init", "--no-prompt",
		"-m", manifestPath(t),
		"--project-id", projectId,
		"--model-deployment", modelDeployment,
		"--deploy-mode", "code",
		"--runtime", "python_3_13",
		"--entry-point", "app:app",
		"--agent-name", "pr-gate-test-agent",
		"--force",
	)
	require.NoError(t, err, "ai agent init failed: stdout=%s, stderr=%s", result.Stdout, result.Stderr)

	// Verify success
	require.Contains(t, result.Stdout, "AI agent definition added to your azd project successfully!")

	// Init creates a project directory named after the agent inside the working dir.
	// Layout: <dir>/pr-gate-test-agent/azure.yaml (project root)
	//         <dir>/pr-gate-test-agent/src/pr-gate-test-agent/agent.yaml (agent source)
	projectDir := filepath.Join(dir, "pr-gate-test-agent")
	agentDir := filepath.Join(projectDir, "src", "pr-gate-test-agent")

	// Verify project structure
	require.FileExists(t, filepath.Join(projectDir, "azure.yaml"))
	require.DirExists(t, agentDir)
	require.FileExists(t, filepath.Join(agentDir, "agent.yaml"))

	// Verify agent.yaml has a resolved model deployment value (not a placeholder).
	// The key AZURE_AI_MODEL_DEPLOYMENT_NAME is always present, but its value is only
	// resolved to an actual model name (e.g. "gpt-4.1") when ARM calls succeed.
	// If ARM fails, the value would be the template placeholder "{{AZURE_AI_MODEL_DEPLOYMENT_NAME}}".
	agentContent, err := os.ReadFile(filepath.Join(agentDir, "agent.yaml"))
	require.NoError(t, err)
	agentStr := string(agentContent)
	require.Contains(t, agentStr, "AZURE_AI_MODEL_DEPLOYMENT_NAME")
	require.NotContains(t, agentStr, "{{AZURE_AI_MODEL_DEPLOYMENT_NAME}}", "model deployment should be resolved, not a placeholder")
}
