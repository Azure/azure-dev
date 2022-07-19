// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/executil"
	"github.com/blang/semver/v4"
)

type DotNetCli interface {
	ExternalTool
	Publish(ctx context.Context, project string, output string) error
	Restore(ctx context.Context, project string) error
}

type dotNetCli struct {
}

func (cli *dotNetCli) Name() string {
	return ".NET CLI"
}

func (cli *dotNetCli) InstallUrl() string {
	return "https://dotnet.microsoft.com/download"
}

func (cli *dotNetCli) versionInfo() VersionInfo {
	return VersionInfo{
		MinimumVersion: semver.Version{
			Major: 6,
			Minor: 0,
			Patch: 3},
		UpdateCommand: "Visit https://docs.microsoft.com/en-us/dotnet/core/releases-and-support to upgrade",
	}
}

func (cli *dotNetCli) CheckInstalled(ctx context.Context) (bool, error) {
	found, err := toolInPath("dotnet")
	if !found {
		return false, err
	}
	dotnetRes, err := executeCommand(ctx, "dotnet", "--version")
	if err != nil {
		return false, fmt.Errorf("checking %s version: %w", cli.Name(), err)
	}
	dotnetSemver, err := extractSemver(dotnetRes)
	if err != nil {
		return false, fmt.Errorf("converting to semver version fails: %w", err)
	}
	updateDetail := cli.versionInfo()
	if dotnetSemver.LT(updateDetail.MinimumVersion) {
		return false, &ErrSemver{ToolName: cli.Name(), versionInfo: updateDetail}
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

func NewDotNetCli() DotNetCli {
	return &dotNetCli{}
}
