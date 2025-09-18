// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package swa

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

// swaCliPackage is the npm package (including the version version) we execute with npx to run the SWA CLI.
const swaCliPackage = "@azure/static-web-apps-cli@latest"

var _ tools.ExternalTool = (*Cli)(nil)

func NewCli(commandRunner exec.CommandRunner) *Cli {
	return &Cli{
		commandRunner: commandRunner,
	}
}

type DeployOptions struct {
	AppFolderPath            string
	OutputRelativeFolderPath string
}

type Cli struct {
	// commandRunner allows us to stub out the CommandRunner, for testing.
	commandRunner exec.CommandRunner
}

func (cli *Cli) Build(ctx context.Context, cwd string, buildProgress io.Writer) error {
	fullAppFolderPath := filepath.Join(cwd)
	result, err := cli.run(ctx, fullAppFolderPath, buildProgress, "build", "-V")

	if err != nil {
		return fmt.Errorf("swa build: %w", err)
	}

	output := result.Stdout
	// when swa cli does not find swa-cli.config.json, it shows the message:
	//    No build options were defined.
	//    If your app needs a build step, run "swa init" to set your project configuration
	//    or use option flags to set your build commands and paths.
	// Azd used this as an error for the customer and return the full message.
	if strings.Contains(output, "No build options were defined") {
		return fmt.Errorf("swa build: %s", output)
	}

	return nil
}

func (cli *Cli) Deploy(
	ctx context.Context,
	cwd string,
	tenantId string,
	subscriptionId string,
	resourceGroup string,
	appName string,
	environment string,
	deploymentToken string,
	options DeployOptions,
) (string, error) {
	log.Printf(
		"SWA Deploy: TenantId: %s, SubscriptionId: %s, ResourceGroup: %s, ResourceName: %s, Environment: %s",
		tenantId,
		subscriptionId,
		resourceGroup,
		appName,
		environment,
	)

	args := []string{"deploy",
		"--tenant-id", tenantId,
		"--subscription-id", subscriptionId,
		"--resource-group", resourceGroup,
		"--app-name", appName,
		"--env", environment,
		"--no-use-keychain",
		"--deployment-token", deploymentToken}

	if options.AppFolderPath != "" {
		args = append(args, "--app-location", options.AppFolderPath)
	}
	if options.OutputRelativeFolderPath != "" {
		args = append(args, "--output-location", options.OutputRelativeFolderPath)
	}

	res, err := cli.executeCommand(ctx, cwd, args...)
	if err != nil {
		return "", fmt.Errorf("swa deploy: %w", err)
	}

	return res.Stdout + res.Stderr, nil
}

func (cli *Cli) CheckInstalled(_ context.Context) error {

	return cli.commandRunner.ToolInPath("npx")
}

func (cli *Cli) Name() string {
	return "SWA CLI"
}

func (cli *Cli) InstallUrl() string {
	return "https://azure.github.io/static-web-apps-cli/docs/use/install"
}

func (cli *Cli) executeCommand(ctx context.Context, cwd string, args ...string) (exec.RunResult, error) {
	return cli.run(ctx, cwd, nil, args...)
}

func (cli *Cli) run(ctx context.Context, cwd string, buildProgress io.Writer, args ...string) (exec.RunResult, error) {
	runArgs := exec.
		NewRunArgs("npx", "-y", swaCliPackage).
		AppendParams(args...).
		WithCwd(cwd)

	if buildProgress != nil {
		runArgs = runArgs.WithStdOut(buildProgress).WithStdErr(buildProgress)
	}

	return cli.commandRunner.Run(ctx, runArgs)
}

const swaConfigFileName = "swa-cli.config.json"

// check if the swa-cli.config.json file exists in the given directory
func ContainsSwaConfig(path string) (bool, error) {
	_, err := os.Stat(filepath.Join(path, swaConfigFileName))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
