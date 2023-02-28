package kubectl

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"gopkg.in/yaml.v3"
)

type KubeConfigManager struct {
	cli        KubectlCli
	configPath string
}

func NewKubeConfigManager(cli KubectlCli) (*KubeConfigManager, error) {
	kubeConfigDir, err := getKubeConfigDir()
	if err != nil {
		return nil, err
	}

	return &KubeConfigManager{
		cli:        cli,
		configPath: kubeConfigDir,
	}, nil
}

func ParseKubeConfig(ctx context.Context, raw []byte) (*KubeConfig, error) {
	var existing KubeConfig
	if err := yaml.Unmarshal(raw, &existing); err != nil {
		return nil, fmt.Errorf("failed unmarshalling Kube Config YAML: %w", err)
	}

	return &existing, nil
}

func (kcm *KubeConfigManager) SaveKubeConfig(ctx context.Context, configName string, config *KubeConfig) error {
	kubeConfigRaw, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed marshalling KubeConfig to yaml: %w", err)
	}

	// Create .kube config folder if it doesn't already exist
	_, err = os.Stat(kcm.configPath)
	if err != nil {
		if err := os.MkdirAll(kcm.configPath, osutil.PermissionDirectory); err != nil {
			return fmt.Errorf("failed creating .kube config directory, %w", err)
		}
	}

	outFilePath := filepath.Join(kcm.configPath, configName)
	err = os.WriteFile(outFilePath, kubeConfigRaw, osutil.PermissionFile)
	if err != nil {
		return fmt.Errorf("failed writing kube config file: %w", err)
	}

	return nil
}

func (kcm *KubeConfigManager) DeleteKubeConfig(ctx context.Context, configName string) error {
	kubeConfigPath := filepath.Join(kcm.configPath, configName)
	err := os.Remove(kubeConfigPath)
	if err != nil {
		return fmt.Errorf("failed deleting kube config file: %w", err)
	}

	return nil
}

func (kcm *KubeConfigManager) MergeConfigs(ctx context.Context, newConfigName string, path ...string) error {
	fullConfigPaths := []string{}
	for _, kubeConfigName := range path {
		fullConfigPaths = append(fullConfigPaths, filepath.Join(kcm.configPath, kubeConfigName))
	}

	envValues := map[string]string{
		"KUBECONFIG": strings.Join(fullConfigPaths, string(os.PathListSeparator)),
	}
	kcm.cli.SetEnv(envValues)
	res, err := kcm.cli.ConfigView(ctx, true, true, nil)
	if err != nil {
		return fmt.Errorf("kubectl config view failed: %w", err)
	}

	kubeConfigRaw := []byte(res.Stdout)
	outFilePath := filepath.Join(kcm.configPath, newConfigName)
	err = os.WriteFile(outFilePath, kubeConfigRaw, osutil.PermissionFile)
	if err != nil {
		return fmt.Errorf("failed writing new kube config: %w", err)
	}

	return nil
}

func (kcm *KubeConfigManager) AddOrUpdateContext(ctx context.Context, contextName string, newKubeConfig *KubeConfig) error {
	err := kcm.SaveKubeConfig(ctx, contextName, newKubeConfig)
	if err != nil {
		return fmt.Errorf("failed write new kube context file: %w", err)
	}

	err = kcm.MergeConfigs(ctx, "config", contextName)
	if err != nil {
		return fmt.Errorf("failed merging KUBE configs: %w", err)
	}

	return nil
}

func getKubeConfigDir() (string, error) {
	userHomeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot get user home directory: %w", err)
	}
	return filepath.Join(userHomeDir, ".kube"), nil
}
