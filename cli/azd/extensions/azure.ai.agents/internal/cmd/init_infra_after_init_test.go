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

// TestMaybeEjectInfraAfterInit_EjectsFromWorkingDir covers the regression
// where `azd ai agent init -m <azure.yaml> --infra` adopted a sample but
// silently dropped the eject. The post-init eject must read azure.yaml from
// the working directory that init/scaffold left us in and write ./infra/.
func TestMaybeEjectInfraAfterInit_EjectsFromWorkingDir(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), validFoundryAzureYAML)
	t.Chdir(dir)

	withCapturedStdout(t, func() {
		require.NoError(t, maybeEjectInfraAfterInit("bicep"))
	})

	info, err := os.Stat(filepath.Join(dir, "infra", "main.bicep"))
	require.NoError(t, err, "infra/main.bicep should be written")
	assert.Greater(t, info.Size(), int64(0))
}

// TestMaybeEjectInfraAfterInit_NoopWhenFlagAbsent verifies the eject is a
// no-op (no ./infra/, no error) when --infra was not passed, even with a
// foundry azure.yaml present.
func TestMaybeEjectInfraAfterInit_NoopWhenFlagAbsent(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), validFoundryAzureYAML)
	t.Chdir(dir)

	require.NoError(t, maybeEjectInfraAfterInit(""))

	_, err := os.Stat(filepath.Join(dir, "infra"))
	assert.True(t, os.IsNotExist(err), "infra/ must not be created")
}

// TestMaybeEjectInfraAfterInit_NoopWhenNoAzureYaml verifies a missing
// azure.yaml is a silent no-op so cancelled init flows don't error.
func TestMaybeEjectInfraAfterInit_NoopWhenNoAzureYaml(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	require.NoError(t, maybeEjectInfraAfterInit("bicep"))

	_, err := os.Stat(filepath.Join(dir, "infra"))
	assert.True(t, os.IsNotExist(err), "infra/ must not be created")
}

// TestMaybeEjectInfraAfterInit_NoopWhenNonFoundry verifies a non-foundry
// azure.yaml is a silent no-op (no error, no ./infra/).
func TestMaybeEjectInfraAfterInit_NoopWhenNonFoundry(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"),
		"name: plain\nservices:\n  web:\n    host: containerapp\n")
	t.Chdir(dir)

	require.NoError(t, maybeEjectInfraAfterInit("bicep"))

	_, err := os.Stat(filepath.Join(dir, "infra"))
	assert.True(t, os.IsNotExist(err), "infra/ must not be created")
}
