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

// cSwaCliPackage is the npm package (including the version version) we execute with npx to run the SWA CLI.
const cSwaCliPackage = "@azure/static-web-apps-cli@1.1.8"

func NewSwaCli(commandRunner exec.CommandRunner) SwaCli {
	return &swaCli{
		commandRunner: commandRunner,
	}
}

type SwaCli interface {
	tools.ExternalTool

	Build(ctx context.Context, cwd string, buildProgress io.Writer) error
	Deploy(
		ctx context.Context,
		cwd string,
		tenantId string,
		subscriptionId string,
		resourceGroup string,
		appName string,
		environment string,
		deploymentToken string,
		options DeployOptions,
	) (string, error)
}

type DeployOptions struct {
	AppFolderPath            string
	OutputRelativeFolderPath string
}

type swaCli struct {
	// commandRunner allows us to stub out the CommandRunner, for testing.
	commandRunner exec.CommandRunner
}

func (cli *swaCli) Build(ctx context.Context, cwd string, buildProgress io.Writer) error {
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

func (cli *swaCli) Deploy(
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

func (cli *swaCli) CheckInstalled(_ context.Context) error {

	return tools.ToolInPath("npx")
}

func (cli *swaCli) Name() string {
	return "SWA CLI"
}

func (cli *swaCli) InstallUrl() string {
	return "https://azure.github.io/static-web-apps-cli/docs/use/install"
}

func (cli *swaCli) executeCommand(ctx context.Context, cwd string, args ...string) (exec.RunResult, error) {
	return cli.run(ctx, cwd, nil, args...)
}

func (cli *swaCli) run(ctx context.Context, cwd string, buildProgress io.Writer, args ...string) (exec.RunResult, error) {
	runArgs := exec.
		NewRunArgs("npx", "-y", cSwaCliPackage).
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
