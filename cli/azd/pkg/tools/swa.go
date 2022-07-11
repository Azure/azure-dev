// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/azure/azure-dev/cli/azd/pkg/executil"
	"github.com/blang/semver/v4"
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
	Deploy(ctx context.Context, tenantId string, subscriptionId string, resourceGroup string, appName string, appFolderPath string, outputRelativeFolderPath string, environment string) (string, error)
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

func (cli *swaCli) Deploy(ctx context.Context, tenantId string, subscriptionId string, resourceGroup string, appName string, appFolderPath string, outputRelativeFolderPath string, environment string) (string, error) {
	res, err := cli.executeCommand(ctx,
		appFolderPath, "deploy",
		"--tenant-id", tenantId,
		"--subscription-id", subscriptionId,
		"--resource-group", resourceGroup,
		"--app-name", appName,
		"--app-location", ".",
		"--output-location", outputRelativeFolderPath,
		"--env", environment)

	if err != nil {
		return "", fmt.Errorf("swa deploy: %s: %w", res.String(), err)
	}

	return res.Stdout, nil
}

// base version number and empty string if there's no pre-request check on version number
func (cli *swaCli) GetToolUpdate() ToolMetaData {
	return ToolMetaData{
		MinimumVersion: semver.Version{
			Major: 1,
			Minor: 0,
			Patch: 0,
		},
		UpdateCommand: "Visit https://github.com/Azure/static-web-apps-cli/releases to install newer",
	}
}

func (cli *swaCli) CheckInstalled(_ context.Context) (bool, error) {
	found, err := toolInPath("npx")
	if !found {
		return false, err
	}
	swaRes, _ := exec.Command("npx", "@azure/static-web-apps-cli", "--version").Output()
	swaSemver, err := extractSemver(swaRes)
	if err != nil {
		return false, fmt.Errorf("converting to semver version fails: %w", err)
	}
	updateDetail := cli.GetToolUpdate()
	if swaSemver.Compare(updateDetail.MinimumVersion) == -1 {
		return false, &ErrSemver{ToolName: cli.Name(), ToolRequire: updateDetail}
	}
	return true, nil
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
