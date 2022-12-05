// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package swa

import (
	"context"
	"fmt"
	"log"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

func NewSwaCli(commandRunner exec.CommandRunner) SwaCli {
	return &swaCli{
		commandRunner: commandRunner,
	}
}

type SwaCli interface {
	tools.ExternalTool

	Build(ctx context.Context, cwd string, appFolderPath string, outputRelativeFolderPath string) error
	Deploy(
		ctx context.Context,
		cwd string,
		tenantId string,
		subscriptionId string,
		resourceGroup string,
		appName string,
		appFolderPath string,
		outputRelativeFolderPath string,
		environment string,
		deploymentToken string,
	) (string, error)
}

type swaCli struct {
	// commandRunner allows us to stub out the CommandRunner, for testing.
	commandRunner exec.CommandRunner
}

func (cli *swaCli) Build(ctx context.Context, cwd string, appFolderPath string, outputRelativeFolderPath string) error {
	res, err := cli.executeCommand(ctx,
		cwd, "build",
		"--app-location", appFolderPath,
		"--output-location", outputRelativeFolderPath)

	if err != nil {
		return fmt.Errorf("swa build: %s: %w", res.String(), err)
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
	appFolderPath string,
	outputRelativeFolderPath string,
	environment string,
	deploymentToken string,
) (string, error) {
	log.Printf(
		"SWA Deploy: TenantId: %s, SubscriptionId: %s, ResourceGroup: %s, ResourceName: %s, Environment: %s",
		tenantId,
		subscriptionId,
		resourceGroup,
		appName,
		environment,
	)

	res, err := cli.executeCommand(ctx,
		cwd, "deploy",
		"--tenant-id", tenantId,
		"--subscription-id", subscriptionId,
		"--resource-group", resourceGroup,
		"--app-name", appName,
		"--app-location", appFolderPath,
		"--output-location", outputRelativeFolderPath,
		"--env", environment,
		"--no-use-keychain",
		"--deployment-token", deploymentToken)

	if err != nil {
		return "", fmt.Errorf("swa deploy: %s: %w", res.String(), err)
	}

	return res.Stdout + res.Stderr, nil
}

func (cli *swaCli) CheckInstalled(_ context.Context) (bool, error) {

	return tools.ToolInPath("npx")
}

func (cli *swaCli) Name() string {
	return "SWA CLI"
}

func (cli *swaCli) InstallUrl() string {
	return "https://azure.github.io/static-web-apps-cli/docs/use/install"
}

func (cli *swaCli) executeCommand(ctx context.Context, cwd string, args ...string) (exec.RunResult, error) {
	runArgs := exec.
		NewRunArgs("npx", "-y", "@azure/static-web-apps-cli@1.0.0").
		AppendParams(args...).
		WithCwd(cwd).
		WithEnrichError(true)

	return cli.commandRunner.Run(ctx, runArgs)
}
