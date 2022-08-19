// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dotnet

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/executil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/blang/semver/v4"
)

type DotNetCli interface {
	tools.ExternalTool
	Publish(ctx context.Context, project string, output string) error
	Restore(ctx context.Context, project string) error
	InitializeSecret(ctx context.Context, project string) error
	SetSecret(ctx context.Context, key string, value string, project string) error
}

type dotNetCli struct {
}

func (cli *dotNetCli) Name() string {
	return ".NET CLI"
}

func (cli *dotNetCli) InstallUrl() string {
	return "https://dotnet.microsoft.com/download"
}

func (cli *dotNetCli) versionInfo() tools.VersionInfo {
	return tools.VersionInfo{
		MinimumVersion: semver.Version{
			Major: 6,
			Minor: 0,
			Patch: 3},
		UpdateCommand: "Visit https://docs.microsoft.com/en-us/dotnet/core/releases-and-support to upgrade",
	}
}

func (cli *dotNetCli) CheckInstalled(ctx context.Context) (bool, error) {
	found, err := tools.ToolInPath("dotnet")
	if !found {
		return false, err
	}
	dotnetRes, err := tools.ExecuteCommand(ctx, "dotnet", "--version")
	if err != nil {
		return false, fmt.Errorf("checking %s version: %w", cli.Name(), err)
	}
	dotnetSemver, err := tools.ExtractSemver(dotnetRes)
	if err != nil {
		return false, fmt.Errorf("converting to semver version fails: %w", err)
	}
	updateDetail := cli.versionInfo()
	if dotnetSemver.LT(updateDetail.MinimumVersion) {
		return false, &tools.ErrSemver{ToolName: cli.Name(), VersionInfo: updateDetail}
	}
	return true, nil
}

func (cli *dotNetCli) Publish(ctx context.Context, project string, output string) error {
	res, err := executil.RunCommandWithShell(ctx, "dotnet", "publish", project, "-c", "Release", "--output", output)
	if err != nil {
		return fmt.Errorf("dotnet publish on project '%s' failed: %s: %w", project, res.String(), err)
	}
	return nil
}

func (cli *dotNetCli) Restore(ctx context.Context, project string) error {
	res, err := executil.RunCommandWithShell(ctx, "dotnet", "restore", project)
	if err != nil {
		return fmt.Errorf("dotnet restore on project '%s' failed: %s: %w", project, res.String(), err)
	}
	return nil
}

func (cli *dotNetCli) InitializeSecret(ctx context.Context, project string) error {
	res, err := executil.RunCommandWithShell(ctx, "dotnet", "user-secrets", "init", "--project", project)
	if err != nil {
		return fmt.Errorf("failed to initialize secrets at project '%s': %w (%s)", project, err, res.String())
	}
	return nil
}

func (cli *dotNetCli) SetSecret(ctx context.Context, key string, value string, project string) error {
	res, err := executil.RunCommand(ctx, "dotnet", "user-secrets", "set", key, value, "--project", project)
	if err != nil {
		return fmt.Errorf("failed running %s secret set %s: %w", cli.Name(), res.String(), err)
	}
	return nil
}

func NewDotNetCli() DotNetCli {
	return &dotNetCli{}
}
