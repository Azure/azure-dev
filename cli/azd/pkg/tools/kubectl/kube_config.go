package kubectl

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/braydonk/yaml"
)

// Manages k8s configurations available to the k8s CLI
type KubeConfigManager struct {
	cli        *Cli
	configPath string
}

// Creates a new instance of the KubeConfigManager
func NewKubeConfigManager(cli *Cli) (*KubeConfigManager, error) {
	kubeConfigDir, err := getKubeConfigDir()
	if err != nil {
		return nil, err
	}

	return &KubeConfigManager{
		cli:        cli,
		configPath: kubeConfigDir,
	}, nil
}

// Parses the raw bytes into a KubeConfig instance
func ParseKubeConfig(ctx context.Context, raw []byte) (*KubeConfig, error) {
	var existing KubeConfig
	if err := yaml.Unmarshal(raw, &existing); err != nil {
		return nil, fmt.Errorf("failed unmarshalling Kube Config YAML: %w", err)
	}

	return &existing, nil
}

// Saves the KubeConfig to the kube configuration folder with the specified name
func (kcm *KubeConfigManager) SaveKubeConfig(ctx context.Context, configName string, config *KubeConfig) (string, error) {
	kubeConfigRaw, err := yaml.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("failed marshalling KubeConfig to yaml: %w", err)
	}

	// Create .kube config folder if it doesn't already exist
	if err := os.MkdirAll(kcm.configPath, osutil.PermissionDirectory); err != nil {
		return "", fmt.Errorf("failed creating .kube config directory, %w", err)
	}

	outFilePath := filepath.Join(kcm.configPath, configName)
	err = os.WriteFile(outFilePath, kubeConfigRaw, osutil.PermissionFile)
	if err != nil {
		return "", fmt.Errorf("failed writing kube config file: %w", err)
	}

	return outFilePath, nil
}

// Deletes the KubeConfig with the specified name
func (kcm *KubeConfigManager) DeleteKubeConfig(ctx context.Context, configName string) error {
	kubeConfigPath := filepath.Join(kcm.configPath, configName)
	err := os.Remove(kubeConfigPath)
	if err != nil {
		return fmt.Errorf("failed deleting kube config file: %w", err)
	}

	return nil
}

// Merges the specified kube configs into the kube config
// This powers the use of the kubectl config set of commands that allow developers to switch between different
// k8s cluster contexts
func (kcm *KubeConfigManager) MergeConfigs(ctx context.Context, newConfigName string, path ...string) (string, error) {
	fullConfigPaths := []string{}
	for _, kubeConfigName := range path {
		fullConfigPaths = append(fullConfigPaths, filepath.Join(kcm.configPath, kubeConfigName))
	}

	kubeConfig := strings.Join(fullConfigPaths, string(os.PathListSeparator))
	kcm.cli.SetKubeConfig(kubeConfig)

	res, err := kcm.cli.ConfigView(ctx, true, true, nil)
	if err != nil {
		return "", fmt.Errorf("kubectl config view failed: %w", err)
	}

	kubeConfigRaw := []byte(res.Stdout)
	outFilePath := filepath.Join(kcm.configPath, newConfigName)
	err = os.WriteFile(outFilePath, kubeConfigRaw, osutil.PermissionFile)
	if err != nil {
		return "", fmt.Errorf("failed writing new kube config: %w", err)
	}

	return outFilePath, nil
}

// Adds a new or updates an existing KubeConfig in the main kube config
func (kcm *KubeConfigManager) AddOrUpdateContext(
	ctx context.Context,
	contextName string,
	newKubeConfig *KubeConfig,
) (string, error) {
	configPath, err := kcm.SaveKubeConfig(ctx, contextName, newKubeConfig)
	if err != nil {
		return "", fmt.Errorf("failed write new kube context file: %w", err)
	}

	return configPath, nil
}

func getKubeConfigDir() (string, error) {
	userHomeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot get user home directory: %w", err)
	}
	return filepath.Join(userHomeDir, ".kube"), nil
}
