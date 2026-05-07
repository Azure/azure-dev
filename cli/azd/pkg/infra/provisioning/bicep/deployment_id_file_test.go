// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/stretchr/testify/require"
)

func TestWriteDeploymentIdFile(t *testing.T) {
	dm := infra.NewDeploymentManager(nil, nil, nil)

	subDeployment := infra.NewSubscriptionDeployment(
		dm.SubscriptionScope("sub-1", "eastus2"), "my-env-1234567890")
	rgDeployment := infra.NewResourceGroupDeployment(
		dm.ResourceGroupScope("sub-1", "my-rg"), "my-env-1234567890")

	const subID = "/subscriptions/sub-1/providers/Microsoft.Resources/deployments/my-env-1234567890"
	const rgID = "/subscriptions/sub-1/resourceGroups/my-rg" +
		"/providers/Microsoft.Resources/deployments/my-env-1234567890"

	t.Run("EnvVarUnset_NoOp", func(t *testing.T) {
		resetDeploymentIdFileTruncation()
		t.Setenv(deploymentIdFileEnvVar, "")

		// Should not panic, should not create any file (path is empty).
		writeDeploymentIdFile(subDeployment, "")
	})

	t.Run("Subscription_WritesNDJSON", func(t *testing.T) {
		resetDeploymentIdFileTruncation()
		path := filepath.Join(t.TempDir(), "deployment-id.json")
		t.Setenv(deploymentIdFileEnvVar, path)

		writeDeploymentIdFile(subDeployment, "main")

		lines := readNDJSONLines(t, path)
		require.Len(t, lines, 1)
		require.Equal(t, subID, lines[0].DeploymentId)
		require.Equal(t, "main", lines[0].Layer)
	})

	t.Run("ResourceGroup_WritesNDJSON", func(t *testing.T) {
		resetDeploymentIdFileTruncation()
		path := filepath.Join(t.TempDir(), "deployment-id.json")
		t.Setenv(deploymentIdFileEnvVar, path)

		writeDeploymentIdFile(rgDeployment, "storage")

		lines := readNDJSONLines(t, path)
		require.Len(t, lines, 1)
		require.Equal(t, rgID, lines[0].DeploymentId)
		require.Equal(t, "storage", lines[0].Layer)
	})

	t.Run("EmptyLayer_WritesEmptyString", func(t *testing.T) {
		resetDeploymentIdFileTruncation()
		path := filepath.Join(t.TempDir(), "deployment-id.json")
		t.Setenv(deploymentIdFileEnvVar, path)

		writeDeploymentIdFile(subDeployment, "")

		lines := readNDJSONLines(t, path)
		require.Len(t, lines, 1)
		require.Equal(t, subID, lines[0].DeploymentId)
		require.Equal(t, "", lines[0].Layer)
	})

	t.Run("MultipleLayers_AppendsLines", func(t *testing.T) {
		resetDeploymentIdFileTruncation()
		path := filepath.Join(t.TempDir(), "deployment-id.json")
		t.Setenv(deploymentIdFileEnvVar, path)

		writeDeploymentIdFile(subDeployment, "main")
		writeDeploymentIdFile(rgDeployment, "storage")

		lines := readNDJSONLines(t, path)
		require.Len(t, lines, 2)
		require.Equal(t, subID, lines[0].DeploymentId)
		require.Equal(t, "main", lines[0].Layer)
		require.Equal(t, rgID, lines[1].DeploymentId)
		require.Equal(t, "storage", lines[1].Layer)
	})

	t.Run("TruncatesExistingFile_OnFirstWrite", func(t *testing.T) {
		resetDeploymentIdFileTruncation()
		path := filepath.Join(t.TempDir(), "deployment-id.json")
		require.NoError(t, os.WriteFile(path, []byte("stale contents from previous run\n"), 0o600))
		t.Setenv(deploymentIdFileEnvVar, path)

		writeDeploymentIdFile(subDeployment, "main")

		lines := readNDJSONLines(t, path)
		require.Len(t, lines, 1, "stale content must be gone after truncation")
		require.Equal(t, subID, lines[0].DeploymentId)
	})

	t.Run("UnwritablePath_DoesNotPanic", func(t *testing.T) {
		resetDeploymentIdFileTruncation()
		// Pointing the env var at a path whose parent doesn't exist should fail to
		// write, but must not abort provisioning (the function swallows the error).
		t.Setenv(deploymentIdFileEnvVar, filepath.Join(t.TempDir(), "missing-dir", "deployment-id.json"))

		writeDeploymentIdFile(subDeployment, "main")
	})

	t.Run("RelativePath_Rejected", func(t *testing.T) {
		resetDeploymentIdFileTruncation()
		// Relative paths are rejected because callers (e.g. IDE integrations) cannot
		// reasonably predict the process working directory; the file would land in an
		// unexpected location. The function must not write anything in that case.
		dir := t.TempDir()
		t.Chdir(dir)

		t.Setenv(deploymentIdFileEnvVar, "deployment-id.json")
		writeDeploymentIdFile(subDeployment, "main")

		_, err := os.Stat(filepath.Join(dir, "deployment-id.json"))
		require.ErrorIs(t, err, os.ErrNotExist,
			"relative paths must be ignored, not written to the working directory")
	})

	t.Run("ConcurrentWrites_ProduceCompleteLines", func(t *testing.T) {
		resetDeploymentIdFileTruncation()
		// Multiple sibling provisioning layers may invoke writeDeploymentIdFile
		// concurrently against the same path. The internal mutex must guarantee each
		// NDJSON line is complete (never a torn write).
		path := filepath.Join(t.TempDir(), "deployment-id.json")
		t.Setenv(deploymentIdFileEnvVar, path)

		const writers = 8
		var wg sync.WaitGroup
		wg.Add(writers)
		for i := range writers {
			var deployment infra.Deployment = subDeployment
			layer := "layer-sub"
			if i%2 == 0 {
				deployment = rgDeployment
				layer = "layer-rg"
			}
			go func() {
				defer wg.Done()
				writeDeploymentIdFile(deployment, layer)
			}()
		}
		wg.Wait()

		lines := readNDJSONLines(t, path)
		require.Len(t, lines, writers)
		for _, line := range lines {
			require.Contains(t, []string{subID, rgID}, line.DeploymentId)
			require.Contains(t, []string{"layer-sub", "layer-rg"}, line.Layer)
		}
	})

	t.Run("PathIsDirectory_DoesNotPanic", func(t *testing.T) {
		resetDeploymentIdFileTruncation()
		// If the path points to an existing directory rather than a file,
		// the open/write will fail. The function must handle this gracefully without
		// panicking or aborting provisioning.
		dir := t.TempDir()
		target := filepath.Join(dir, "subdir")
		require.NoError(t, os.Mkdir(target, 0o755))
		t.Setenv(deploymentIdFileEnvVar, target)

		writeDeploymentIdFile(subDeployment, "main")

		// The directory should still exist and not be replaced by a file.
		info, err := os.Stat(target)
		require.NoError(t, err)
		require.True(t, info.IsDir(), "the directory must not be replaced")
	})

	t.Run("TruncationFailure_BlocksSubsequentAppends", func(t *testing.T) {
		// Regression test: if the first call's truncation fails, a second call must
		// NOT silently append to a file that still holds stale content from a prior
		// run. The persisted truncation error must block every subsequent caller.
		resetDeploymentIdFileTruncation()
		path := filepath.Join(t.TempDir(), "deployment-id.json")
		require.NoError(t, os.WriteFile(path, []byte("stale\n"), 0o600))
		t.Setenv(deploymentIdFileEnvVar, path)

		// Simulate a truncation failure (e.g. permission denied) being observed by
		// the first writer in this process. The flag/error are package-scoped so we
		// can seed them directly without relying on platform-specific FS behavior.
		deploymentIdFileMu.Lock()
		deploymentIdFileTruncateAttempted = true
		deploymentIdFileTruncateErr = os.ErrPermission
		deploymentIdFileMu.Unlock()

		writeDeploymentIdFile(subDeployment, "main")
		writeDeploymentIdFile(rgDeployment, "storage")

		// The original stale content must still be present and untouched — no new
		// NDJSON lines should have been appended.
		got, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, "stale\n", string(got),
			"writes must be blocked when truncation has previously failed")
	})
}

func TestDeploymentResourceID(t *testing.T) {
	dm := infra.NewDeploymentManager(nil, nil, nil)

	t.Run("Subscription", func(t *testing.T) {
		dep := infra.NewSubscriptionDeployment(dm.SubscriptionScope("sub", "eastus2"), "name")
		id, err := deploymentResourceID(dep)
		require.NoError(t, err)
		require.Equal(t, "/subscriptions/sub/providers/Microsoft.Resources/deployments/name", id)
	})

	t.Run("ResourceGroup", func(t *testing.T) {
		dep := infra.NewResourceGroupDeployment(dm.ResourceGroupScope("sub", "rg"), "name")
		id, err := deploymentResourceID(dep)
		require.NoError(t, err)
		require.Equal(t, "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Resources/deployments/name", id)
	})

	t.Run("UnsupportedType_ReturnsError", func(t *testing.T) {
		// fakeDeployment (from interrupt_test.go) implements infra.Deployment but is
		// neither *infra.SubscriptionDeployment nor *infra.ResourceGroupDeployment,
		// so it exercises the default error branch.
		_, err := deploymentResourceID(&fakeDeployment{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "unsupported deployment type")
	})
}

// readNDJSONLines reads all NDJSON lines from the given file path.
func readNDJSONLines(t *testing.T, path string) []deploymentIdLine {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var lines []deploymentIdLine
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var doc deploymentIdLine
		require.NoError(t, json.Unmarshal([]byte(line), &doc),
			"each line must be valid JSON: %q", line)
		lines = append(lines, doc)
	}
	require.NoError(t, scanner.Err())
	return lines
}
