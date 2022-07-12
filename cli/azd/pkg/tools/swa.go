// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"
	"fmt"
	"log"

	"github.com/azure/azure-dev/cli/azd/pkg/executil"
)

func NewSwaCli() SwaCli {
	return &swaCli{
		runWithResultFn: executil.RunWithResult,
	}
}

type SwaCli interface {
	ExternalTool

	Login(ctx context.Context, tenantId string, subscriptionId string, resourceGroup string, appName string) error
	Build(ctx context.Context, appFolderPath string, outputRelativeFolderPath string) error
	Deploy(ctx context.Context, tenantId string, subscriptionId string, resourceGroup string, appName string, appFolderPath string, outputRelativeFolderPath string, environment string, deploymentToken string) (string, error)
}

type swaCli struct {
	// runWithResultFn allows us to stub out the executil.RunWithResult, for testing.
	runWithResultFn func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error)
}

func (cli *swaCli) Login(ctx context.Context, tenantId string, subscriptionId string, resourceGroup string, appName string) error {
	res, err := cli.executeCommand(ctx, ".", "login",
		"--tenant-id", tenantId,
		"--subscription-id", subscriptionId,
		"--resource-group", resourceGroup,
		"--app-name", appName)

	if err != nil {
		return fmt.Errorf("swa login: %s: %w", res.String(), err)
	}

	return nil
}

func (cli *swaCli) Build(ctx context.Context, appFolderPath string, outputRelativeFolderPath string) error {
	res, err := cli.executeCommand(ctx,
		appFolderPath, "build",
		"--app-location", ".",
		"--output-location", outputRelativeFolderPath)

	if err != nil {
		return fmt.Errorf("swa build: %s: %w", res.String(), err)
	}

	return nil
}

func (cli *swaCli) Deploy(ctx context.Context, tenantId string, subscriptionId string, resourceGroup string, appName string, appFolderPath string, outputRelativeFolderPath string, environment string, deploymentToken string) (string, error) {
	log.Printf("SWA Deploy: TenantId: %s, SubscriptionId: %s, ResourceGroup: %s, ResourceName: %s, Environment: %s", tenantId, subscriptionId, resourceGroup, appName, environment)

	res, err := cli.executeCommand(ctx,
		appFolderPath, "deploy",
		"--tenant-id", tenantId,
		"--subscription-id", subscriptionId,
		"--resource-group", resourceGroup,
		"--app-name", appName,
		"--app-location", ".",
		"--output-location", outputRelativeFolderPath,
		"--env", environment,
		"--deployment-token", deploymentToken)

	if err != nil {
		return "", fmt.Errorf("swa deploy: %s: %w", res.String(), err)
	}

	return res.Stdout, nil
}

func (cli *swaCli) CheckInstalled(_ context.Context) (bool, error) {
	return toolInPath("npx")
}

func (cli *swaCli) Name() string {
	return "SWA CLI"
}

func (cli *swaCli) InstallUrl() string {
	return "https://azure.github.io/static-web-apps-cli/docs/use/install"
}

func (cli *swaCli) executeCommand(ctx context.Context, cwd string, args ...string) (executil.RunResult, error) {
	defaultArgs := []string{"-y", "@azure/static-web-apps-cli"}
	finalArgs := append(defaultArgs, args...)

	return cli.runWithResultFn(ctx, executil.RunArgs{
		Cmd:  "npx",
		Args: finalArgs,
		Cwd:  cwd,
	})
}
