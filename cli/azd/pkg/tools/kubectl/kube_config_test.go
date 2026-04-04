// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package kubectl

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_MergeKubeConfig(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "kubectl config view")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		// Return a valid merged kube config YAML so MergeConfigs can write it.
		return exec.NewRunResult(0, "apiVersion: v1\nkind: Config\nclusters: []\ncontexts: []\nusers: []\n", ""), nil
	})
	cli := NewCli(mockContext.CommandRunner)
	kubeConfigManager, err := NewKubeConfigManager(cli)
	require.NoError(t, err)

	config1 := createTestCluster("cluster1", "user1")
	config2 := createTestCluster("cluster2", "user2")
	config3 := createTestCluster("cluster3", "user3")

	defer func() {
		err := kubeConfigManager.DeleteKubeConfig(*mockContext.Context, "config1")
		require.NoError(t, err)
		err = kubeConfigManager.DeleteKubeConfig(*mockContext.Context, "config2")
		require.NoError(t, err)
		err = kubeConfigManager.DeleteKubeConfig(*mockContext.Context, "config3")
		require.NoError(t, err)
	}()

	kubeConfigPath, err := kubeConfigManager.SaveKubeConfig(*mockContext.Context, "config1", config1)
	require.NoError(t, err)
	require.NotEmpty(t, kubeConfigPath)
	require.Contains(t, kubeConfigPath, filepath.Join(".kube", "config1"))

	kubeConfigPath, err = kubeConfigManager.SaveKubeConfig(*mockContext.Context, "config2", config2)
	require.NoError(t, err)
	require.NotEmpty(t, kubeConfigPath)
	require.Contains(t, kubeConfigPath, filepath.Join(".kube", "config2"))

	kubeConfigPath, err = kubeConfigManager.SaveKubeConfig(*mockContext.Context, "config3", config3)
	require.NoError(t, err)
	require.NotEmpty(t, kubeConfigPath)
	require.Contains(t, kubeConfigPath, filepath.Join(".kube", "config3"))

	kubeConfigPath, err = kubeConfigManager.MergeConfigs(*mockContext.Context, "config", "config1", "config2", "config3")
	require.NoError(t, err)
	require.NotEmpty(t, kubeConfigPath)
	require.Contains(t, kubeConfigPath, filepath.Join(".kube", "config"))
}

func createTestCluster(clusterName, username string) *KubeConfig {
	return &KubeConfig{
		ApiVersion:     "v1",
		Kind:           "Config",
		CurrentContext: clusterName,
		Preferences:    KubePreferences{},
		Clusters: []*KubeCluster{
			{
				Name: clusterName,
				Cluster: KubeClusterData{
					Server: fmt.Sprintf("https://%s.eastus2.azmk8s.io:443", clusterName),
				},
			},
		},
		Users: []*KubeUser{
			{
				Name: fmt.Sprintf("%s_%s", clusterName, username),
			},
		},
		Contexts: []*KubeContext{
			{
				Name: clusterName,
				Context: KubeContextData{
					Cluster: clusterName,
					User:    fmt.Sprintf("%s_%s", clusterName, username),
				},
			},
		},
	}
}
