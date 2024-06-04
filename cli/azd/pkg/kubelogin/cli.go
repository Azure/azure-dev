package kubelogin

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

// Cli is a wrapper around the kubelogin CLI
type Cli struct {
	commandRunner exec.CommandRunner
}

// NewCli creates a new instance of the kubelogin CLI wrapper
func NewCli(commandRunner exec.CommandRunner) *Cli {
	return &Cli{
		commandRunner: commandRunner,
	}
}

// Gets the name of the Tool
func (cli *Cli) Name() string {
	return "kubelogin"
}

// Returns the installation URL to install the kubelogin CLI
func (cli *Cli) InstallUrl() string {
	return "https://aka.ms/azure-dev/kubelogin-install"
}

// Checks whether or not the kubelogin CLI is installed and available within the PATH
func (cli *Cli) CheckInstalled(ctx context.Context) error {
	if err := tools.ToolInPath("kubelogin"); err != nil {
		return err
	}

	return nil
}

// ConvertKubeConfig converts a kubeconfig file to use the exec auth module
func (c *Cli) ConvertKubeConfig(ctx context.Context, options *ConvertOptions) error {
	if options == nil {
		options = &ConvertOptions{}
	}

	if options.Login == "" {
		options.Login = "azd"
	}

	runArgs := exec.NewRunArgs("kubelogin", "convert-kubeconfig", "--login", options.Login)
	if options.KubeConfig != "" {
		runArgs = runArgs.AppendParams("--kubeconfig", options.KubeConfig)
	}

	if options.TenantId != "" {
		runArgs = runArgs.AppendParams("--tenant-id", options.TenantId)
	}

	if options.Context != "" {
		runArgs = runArgs.AppendParams("--context", options.Context)
	}

	if _, err := c.commandRunner.Run(ctx, runArgs); err != nil {
		return fmt.Errorf("converting kubeconfig: %w", err)
	}

	return nil
}

// ConvertOptions are the options for converting a kubeconfig file
type ConvertOptions struct {
	// Login method to use (defaults to azd)
	Login string
	// AAD tenant ID
	TenantId string
	// The name of the kubeconfig context to use
	Context string
	// KubeConfig is the path to the kube config file
	KubeConfig string
}
