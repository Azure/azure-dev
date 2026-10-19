// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package envkeys

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func requireRatchetTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"bash", "rg"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("literal ratchet requires %s: %v", tool, err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "bash", "-c", "echo ${BASH_VERSINFO[0]}").CombinedOutput()
	require.NoError(t, err, "%s", output)
	major, err := strconv.Atoi(strings.TrimSpace(string(output)))
	require.NoError(t, err, "parse Bash major version: %q", output)
	if major < 4 {
		t.Skipf("literal ratchet requires Bash 4 or newer; found major version %d", major)
	}
}

func runRatchet(t *testing.T, script string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "bash", append([]string{script}, args...)...).CombinedOutput()
	return string(output), err
}

func TestEnvkeyLiteralRatchet(t *testing.T) {
	requireRatchetTools(t)
	output, err := runRatchet(t, filepath.Join("..", "..", "..", "scripts", "check_envkey_literals.sh"))
	require.NoError(t, err, "%s", output)
}

func TestLiteralRatchetBehavior(t *testing.T) {
	requireRatchetTools(t)
	root := t.TempDir()
	scripts := filepath.Join(root, "scripts")
	require.NoError(t, os.MkdirAll(scripts, 0o750))
	source, err := os.ReadFile(filepath.Join("..", "..", "..", "scripts", "check_envkey_literals.sh"))
	require.NoError(t, err)
	script := filepath.Join(scripts, "check_envkey_literals.sh")
	require.NoError(t, os.WriteFile(script, source, 0o600)) // #nosec G703 -- script is under t.TempDir()
	for _, extension := range []string{"azure.ai.agents", "azure.ai.projects"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, extension), 0o750))
	}
	agents := filepath.Join(root, "azure.ai.agents")
	fixture := filepath.Join(agents, "fixture.go")
	write := func(path, text string) {
		t.Helper()
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(text), 0o600)) // #nosec G703 -- path is under t.TempDir()
	}
	// Count occurrences, including two literals on the same line and test fixtures.
	write(fixture, "package fixture\nvar keys = []string{\"AZURE_AI_A\", \"AZURE_AI_B\"}\n")
	write(filepath.Join(agents, "fixture_test.go"),
		"package fixture\nvar key = `AGENT_TEST`\nvar skip = \"AZD_AGENT_SKIP_ACR\"\n")
	output, err := runRatchet(t, script, "--counts")
	require.NoError(t, err, "%s", output)
	require.Contains(t, output, "azure.ai.agents:AZURE_AI_=2\n")
	require.Contains(t, output, "azure.ai.agents:AGENT_=1\n")
	require.Contains(t, output, "azure.ai.agents:AZD_AGENT_=1\n")
	baseline := filepath.Join(scripts, "check_envkey_literals.baseline")
	write(baseline, output)

	for _, excluded := range []string{"k8s", "nested/k8s", "internal/envkeys", "vendor/example"} {
		write(filepath.Join(agents, filepath.FromSlash(excluded), "ignored.go"),
			"package ignored\nvar key = \"AZURE_AI_IGNORED\"\n")
	}
	// Other extensions and non-Go sources are outside the ratchet's scope.
	write(filepath.Join(root, "azure.ai.connections", "ignored.go"), `var key = "AZURE_AI_IGNORED"`)
	write(filepath.Join(agents, "ignored.txt"), `"AZURE_AI_IGNORED"`)
	output, err = runRatchet(t, script)
	require.NoError(t, err, "%s", output)

	content, err := os.ReadFile(baseline)
	require.NoError(t, err)
	write(baseline, strings.ReplaceAll(string(content), "\n", "\r\n"))
	output, err = runRatchet(t, script)
	require.NoError(t, err, "%s", output)
	require.Contains(t, output, "OK:")

	write(fixture, "package fixture\nvar key = \"AZURE_AI_A\"\n")
	output, err = runRatchet(t, script)
	require.NoError(t, err, "%s", output)
	require.Contains(t, output, "decreased from 2 to 1")

	write(fixture, "package fixture\nvar keys = []string{\"AZURE_AI_A\", \"AZURE_AI_B\", \"AZURE_AI_C\"}\n")
	output, err = runRatchet(t, script)
	require.Error(t, err)
	require.Contains(t, output, "increased from 2 to 3")

	// A decrease in agents must not mask an increase in projects.
	write(fixture, "package fixture\n")
	write(filepath.Join(root, "azure.ai.projects", "fixture.go"), `var key = "AZURE_CONTAINER_REGISTRY_NEW"`)
	output, err = runRatchet(t, script)
	require.Error(t, err)
	require.Contains(t, output, "azure.ai.projects:AZURE_CONTAINER_REGISTRY_ increased from 0 to 1")

	write(baseline, "invalid baseline")
	output, err = runRatchet(t, script)
	require.Error(t, err)
	require.Contains(t, output, "invalid baseline line")
}
