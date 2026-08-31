// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package kubectl

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_MergeKubeConfig(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "kubectl config view")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		// Return a valid merged kube config YAML so MergeConfigs can write it.
		return exec.NewRunResult(0, "apiVersion: v1\nkind: Config\nclusters: []\ncontexts: []\nusers: []\n", ""), nil
	})
	cli := NewCli(mockContext.CommandRunner)
	kubeConfigManager, err := NewKubeConfigManager(cli)
	require.NoError(t, err)
	kubeConfigManager.configPath = filepath.Join(t.TempDir(), ".kube")

	config1 := createTestCluster("cluster1", "user1")
	config2 := createTestCluster("cluster2", "user2")
	config3 := createTestCluster("cluster3", "user3")

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

	if runtime.GOOS != "windows" {
		requirePermissions(t, kubeConfigManager.configPath, osutil.PermissionDirectoryOwnerOnly)
		requirePermissions(t, filepath.Join(kubeConfigManager.configPath, "config1"), osutil.PermissionFileOwnerOnly)
		requirePermissions(t, filepath.Join(kubeConfigManager.configPath, "config2"), osutil.PermissionFileOwnerOnly)
		requirePermissions(t, filepath.Join(kubeConfigManager.configPath, "config3"), osutil.PermissionFileOwnerOnly)
		requirePermissions(t, kubeConfigPath, osutil.PermissionFileOwnerOnly)
	}

	require.NoError(t, kubeConfigManager.DeleteKubeConfig(*mockContext.Context, "config1"))
	_, err = os.Stat(filepath.Join(kubeConfigManager.configPath, "config1"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func Test_KubeConfigPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not support Unix permission bits")
	}

	mockContext := mocks.NewMockContext(t.Context())
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "kubectl config view")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "apiVersion: v1\nusers:\n- name: user\n  user:\n    token: secret\n", ""), nil
	})

	configPath := filepath.Join(t.TempDir(), ".kube")
	require.NoError(t, os.Mkdir(configPath, osutil.PermissionDirectory))
	require.NoError(t, os.Chmod(configPath, osutil.PermissionDirectory))

	clusterConfigPath := filepath.Join(configPath, "cluster")
	require.NoError(t, os.WriteFile(clusterConfigPath, []byte("old"), osutil.PermissionFile))
	require.NoError(t, os.Chmod(clusterConfigPath, osutil.PermissionFile))
	mergedConfigPath := filepath.Join(configPath, "config")
	require.NoError(t, os.WriteFile(mergedConfigPath, []byte("old"), osutil.PermissionFile))
	require.NoError(t, os.Chmod(mergedConfigPath, osutil.PermissionFile))

	manager := &KubeConfigManager{
		cli:        NewCli(mockContext.CommandRunner),
		configPath: configPath,
	}

	savedPath, err := manager.SaveKubeConfig(
		*mockContext.Context,
		"cluster",
		createTestCluster("cluster", "user"),
	)
	require.NoError(t, err)
	require.Equal(t, clusterConfigPath, savedPath)

	require.NoError(t, os.Chmod(configPath, osutil.PermissionDirectory))
	mergedPath, err := manager.MergeConfigs(*mockContext.Context, "config", "cluster")
	require.NoError(t, err)
	require.Equal(t, mergedConfigPath, mergedPath)

	requirePermissions(t, configPath, osutil.PermissionDirectoryOwnerOnly)
	requirePermissions(t, clusterConfigPath, osutil.PermissionFileOwnerOnly)
	requirePermissions(t, mergedConfigPath, osutil.PermissionFileOwnerOnly)
}

func Test_EnsureKubeConfigDirectoryFailsWhenParentIsFile(t *testing.T) {
	parentPath := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(parentPath, nil, osutil.PermissionFile))

	err := ensureKubeConfigDirectory(filepath.Join(parentPath, ".kube"))

	require.Error(t, err)
}

func Test_WriteKubeConfigFailsWhenParentDoesNotExist(t *testing.T) {
	err := writeKubeConfig(filepath.Join(t.TempDir(), "missing", "config"), []byte("config"))

	require.Error(t, err)
}

func Test_SaveKubeConfigFailsWhenConfigPathIsFile(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), ".kube")
	require.NoError(t, os.WriteFile(configPath, nil, osutil.PermissionFile))

	manager := &KubeConfigManager{configPath: configPath}
	_, err := manager.SaveKubeConfig(t.Context(), "config", createTestCluster("cluster", "user"))

	require.ErrorContains(t, err, "failed creating .kube config directory")
}

func Test_MergeKubeConfigFailsWhenConfigPathIsFile(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "kubectl config view")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "apiVersion: v1\n", ""), nil
	})

	configPath := filepath.Join(t.TempDir(), ".kube")
	require.NoError(t, os.WriteFile(configPath, nil, osutil.PermissionFile))

	manager := &KubeConfigManager{
		cli:        NewCli(mockContext.CommandRunner),
		configPath: configPath,
	}
	_, err := manager.MergeConfigs(*mockContext.Context, "config", "cluster")

	require.ErrorContains(t, err, "failed securing .kube config directory")
}

func Test_AddOrUpdateContext(t *testing.T) {
	manager := &KubeConfigManager{configPath: filepath.Join(t.TempDir(), ".kube")}

	configPath, err := manager.AddOrUpdateContext(
		t.Context(),
		"cluster",
		createTestCluster("cluster", "user"),
	)

	require.NoError(t, err)
	require.Equal(t, filepath.Join(manager.configPath, "cluster"), configPath)
}

func Test_AddOrUpdateContextFailsWhenConfigPathIsFile(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), ".kube")
	require.NoError(t, os.WriteFile(configPath, nil, osutil.PermissionFile))

	manager := &KubeConfigManager{configPath: configPath}
	_, err := manager.AddOrUpdateContext(
		t.Context(),
		"cluster",
		createTestCluster("cluster", "user"),
	)

	require.ErrorContains(t, err, "failed write new kube context file")
}

func requirePermissions(t *testing.T, path string, expected os.FileMode) {
	t.Helper()

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, expected, info.Mode().Perm())
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
