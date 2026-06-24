// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build record

package ai_agents_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/azure/azure-dev/cli/azd/test/recording"
	"github.com/stretchr/testify/require"
)

// These constants must match the sanitized cassette content exactly (equal-length replacements).
// They are fake values used during recording sanitization; the recording proxy matches by URL,
// so test code and cassette must use identical strings.
const (
	testSubscriptionID = "00000000-0000-0000-0000-000000000000"
	testProjectID      = "/subscriptions/00000000-0000-0000-0000-000000000000/" +
		"resourceGroups/rg-test-agents-recording-0000000000000000000/" +
		"providers/Microsoft.CognitiveServices/accounts/test-ai-account-000/" +
		"projects/test-proj0"
)

// manifestPath returns the absolute path to the local test manifest file.
func manifestPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "testdata", "manifests", "foundry-toolbox.yaml")
}

// --- Tier 1: Recording tests (ARM calls replayed from cassette) ---

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
	session.Variables[recording.SubscriptionIdKey] = testSubscriptionID

	cli := azdcli.NewCLI(t, azdcli.WithSession(session))
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)

	projectId := os.Getenv("TEST_FOUNDRY_PROJECT_ID")
	if projectId == "" {
		projectId = session.Variables["project_id"]
	}
	if projectId == "" {
		projectId = testProjectID
	}
	session.Variables["project_id"] = projectId

	// --model-deployment's existence routes no-prompt to the "existing" branch
	// (init_from_code.go:784). The project deployment list in the cassette is empty,
	// so it falls back to selectNewModel, which resolves manifest resources[0].id
	// ("gpt-4.1") as a new deployment. The flag VALUE is not used or asserted.
	modelDeployment := os.Getenv("TEST_MODEL_DEPLOYMENT")
	if modelDeployment == "" {
		modelDeployment = "gpt-4o"
	}

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

	// Verify ARM resolution: the model deployment name is written to the azd environment
	// .env file. The --model-deployment flag's existence routes no-prompt to the "existing"
	// branch (init_from_code.go:784); the cassette's deployment list is empty, so it falls
	// back to selectNewModel which resolves manifest resources[0].id ("gpt-4.1") as a new
	// deployment. This proves ARM calls in the cassette were consumed.
	envFiles, err := filepath.Glob(filepath.Join(projectDir, ".azure", "*", ".env"))
	require.NoError(t, err)
	require.Len(t, envFiles, 1, "expected exactly one azd environment .env file")
	envFile := envFiles[0]
	envContent, err := os.ReadFile(envFile)
	require.NoError(t, err)
	envStr := string(envContent)
	// Pin to the exact value produced by manifest resources[0].id resolution.
	require.Contains(t, envStr, `AZURE_AI_MODEL_DEPLOYMENT_NAME="gpt-4.1"`,
		"model deployment should be resolved from manifest resource id via ARM catalog")

	// Cross-check: agent.yaml should also have the resolved value, not ${...} placeholder.
	agentContent, err := os.ReadFile(filepath.Join(agentDir, "agent.yaml"))
	require.NoError(t, err)
	agentStr := string(agentContent)
	require.NotContains(t, agentStr, "${AZURE_AI_MODEL_DEPLOYMENT_NAME}",
		"agent.yaml should have resolved model name, not azd env placeholder")
}

// Test_AIAgent_Init_NegativeControl_BadCassette verifies that the recording cassette is actually
// consumed during playback. With an empty cassette (no recorded interactions), the first outbound
// HTTP call through the recording proxy fails with "requested interaction not found", proving that
// the Tier 1 tests above rely on their cassettes to succeed.
//
// This test ONLY runs in playback mode. It uses a pre-committed empty cassette file.
func Test_AIAgent_Init_NegativeControl_BadCassette(t *testing.T) {
	if strings.ToLower(os.Getenv("AZURE_RECORD_MODE")) != "playback" {
		t.Skip("negative control only runs in playback mode")
	}
	t.Parallel()
	ctx, cancel := newTestContext(t)
	defer cancel()

	dir := tempDirWithDiagnostics(t)

	// Uses the pre-committed empty cassette at testdata/recordings/Test_AIAgent_Init_NegativeControl_BadCassette.yaml
	// (interactions: []), so the recording proxy has nothing to replay.
	session := recording.Start(t)
	session.Variables[recording.SubscriptionIdKey] = testSubscriptionID
	session.Variables["project_id"] = testProjectID

	cli := azdcli.NewCLI(t, azdcli.WithSession(session))
	cli.WorkingDirectory = dir
	cli.Env = append(cli.Env, os.Environ()...)

	result, err := cli.RunCommand(ctx,
		"ai", "agent", "init", "--no-prompt",
		"-m", manifestPath(t),
		"--project-id", testProjectID,
		"--model-deployment", "gpt-4o",
		"--deploy-mode", "code",
		"--runtime", "python_3_13",
		"--entry-point", "app:app",
		"--agent-name", "neg-control-agent",
		"--force",
	)
	// The first outbound call (extension registry or ARM) finds no matching recorded
	// interaction → recording proxy returns a 400 with "requested interaction not found".
	require.Error(t, err, "init should fail with empty cassette — proves cassette is consumed; stdout=%s", result.Stdout)
	combinedOutput := result.Stdout + result.Stderr
	require.Contains(t, combinedOutput, "requested interaction not found",
		"failure must come from recording proxy (no matching interaction), not from unrelated causes")
}
