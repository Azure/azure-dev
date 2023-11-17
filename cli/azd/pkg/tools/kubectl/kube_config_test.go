package kubectl

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_MergeKubeConfig(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	commandRunner := exec.NewCommandRunner(nil)
	cli := NewKubectl(commandRunner)
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
