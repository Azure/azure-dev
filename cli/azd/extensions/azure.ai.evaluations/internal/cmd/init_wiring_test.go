// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// init scaffolds a config that azd only acts on once the root project file
// references it. Printing the block and leaving the edit to the reader is what
// made the documented flow stop between `init` and `azd up`.
func TestEnsureRootEvalService_CreatesTheProjectFileWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	action, err := ensureRootEvalService(rootConfigName, filepath.Join("evals", "azure.yaml"))
	require.NoError(t, err)
	assert.Equal(t, wiringCreated, action)

	body, err := os.ReadFile(rootConfigName)
	require.NoError(t, err)
	assert.Contains(t, string(body), "host: azure.ai.eval")
	assert.Contains(t, string(body), "$ref: ./evals/azure.yaml")
	assert.Contains(t, string(body), "name: "+filepath.Base(dir),
		"azd needs a project name, taken from the directory")
}

// An existing project file belongs to the caller, so the service is added
// without disturbing what is already declared.
func TestEnsureRootEvalService_AddsToAnExistingProject(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	existing := "name: my-app\n" +
		"services:\n" +
		"  api:\n" +
		"    host: containerapp\n" +
		"    language: python\n"
	require.NoError(t, os.WriteFile(rootConfigName, []byte(existing), 0o600))

	action, err := ensureRootEvalService(rootConfigName, filepath.Join("evals", "azure.yaml"))
	require.NoError(t, err)
	assert.Equal(t, wiringAdded, action)

	body, err := os.ReadFile(rootConfigName)
	require.NoError(t, err)
	assert.Contains(t, string(body), "host: azure.ai.eval")
	assert.Contains(t, string(body), "host: containerapp", "the existing service survives")
	assert.Contains(t, string(body), "language: python")
	assert.Contains(t, string(body), "name: my-app", "the project keeps its name")
}

// Running init twice must not declare the evals twice, which would deploy them
// twice. The name is not what identifies it — the host is.
func TestEnsureRootEvalService_LeavesAnExistingEvalServiceAlone(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	existing := "name: my-app\n" +
		"services:\n" +
		"  quality:\n" +
		"    host: azure.ai.eval\n" +
		"    $ref: ./evals/azure.yaml\n"
	require.NoError(t, os.WriteFile(rootConfigName, []byte(existing), 0o600))

	action, err := ensureRootEvalService(rootConfigName, filepath.Join("evals", "azure.yaml"))
	require.NoError(t, err)
	assert.Equal(t, wiringPresent, action)

	body, err := os.ReadFile(rootConfigName)
	require.NoError(t, err)
	assert.Equal(t, existing, string(body), "an already-wired project is untouched")
}

// A project declaring no services at all still needs the key adding.
func TestEnsureRootEvalService_AddsTheServicesKeyWhenMissing(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile(rootConfigName, []byte("name: my-app\n"), 0o600))

	action, err := ensureRootEvalService(rootConfigName, filepath.Join("evals", "azure.yaml"))
	require.NoError(t, err)
	assert.Equal(t, wiringAdded, action)

	body, err := os.ReadFile(rootConfigName)
	require.NoError(t, err)
	assert.Contains(t, string(body), "services:")
	assert.Contains(t, string(body), "host: azure.ai.eval")
}

// A file that is not a YAML mapping is someone else's to fix; the block is
// printed instead of guessing at an edit.
func TestEnsureRootEvalService_FallsBackWhenTheProjectFileIsNotAMapping(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile(rootConfigName, []byte("- not\n- a mapping\n"), 0o600))

	action, err := ensureRootEvalService(rootConfigName, filepath.Join("evals", "azure.yaml"))
	require.NoError(t, err)
	assert.Equal(t, wiringManual, action)
}

// A service called "evals" already existing for something else must not be
// overwritten.
func TestEnsureRootEvalService_DoesNotClobberAnUnrelatedEvalsService(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	existing := "name: my-app\n" +
		"services:\n" +
		"  evals:\n" +
		"    host: containerapp\n"
	require.NoError(t, os.WriteFile(rootConfigName, []byte(existing), 0o600))

	action, err := ensureRootEvalService(rootConfigName, filepath.Join("evals", "azure.yaml"))
	require.NoError(t, err)
	assert.Equal(t, wiringAdded, action)

	body, err := os.ReadFile(rootConfigName)
	require.NoError(t, err)
	assert.Contains(t, string(body), "host: containerapp", "the unrelated service is intact")
	assert.Contains(t, string(body), "evals2:", "the eval service takes a free name")
}
