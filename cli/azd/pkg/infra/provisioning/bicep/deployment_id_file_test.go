// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"encoding/json"
	"os"
	"path/filepath"
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
		t.Setenv(deploymentIdFileEnvVar, "")

		// Should not panic, should not create any file (path is empty).
		writeDeploymentIdFile(subDeployment)
	})

	t.Run("Subscription_WritesId", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "deployment-id.json")
		t.Setenv(deploymentIdFileEnvVar, path)

		writeDeploymentIdFile(subDeployment)

		assertDeploymentIdFile(t, path, subID)
	})

	t.Run("ResourceGroup_WritesId", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "deployment-id.json")
		t.Setenv(deploymentIdFileEnvVar, path)

		writeDeploymentIdFile(rgDeployment)

		assertDeploymentIdFile(t, path, rgID)
	})

	t.Run("Overwrites_ExistingFile", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "deployment-id.json")
		require.NoError(t, os.WriteFile(path, []byte("stale contents"), 0o600))
		t.Setenv(deploymentIdFileEnvVar, path)

		writeDeploymentIdFile(subDeployment)

		assertDeploymentIdFile(t, path, subID)
	})

	t.Run("UnwritablePath_DoesNotPanic", func(t *testing.T) {
		// Pointing the env var at a path whose parent doesn't exist should fail to
		// write, but must not abort provisioning (the function swallows the error).
		t.Setenv(deploymentIdFileEnvVar, filepath.Join(t.TempDir(), "missing-dir", "deployment-id.json"))

		writeDeploymentIdFile(subDeployment)
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
}

func assertDeploymentIdFile(t *testing.T, path, expectedId string) {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var doc deploymentIdFile
	require.NoError(t, json.Unmarshal(data, &doc))
	require.Equal(t, expectedId, doc.DeploymentId)
}
