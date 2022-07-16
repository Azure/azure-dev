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
	_, err := executil.RunCommandWithShell(ctx, "dotnet", "publish", project, "-c", "Release", "--output", output)
	if err != nil {
		return fmt.Errorf("failed to publish project %s (%w)", project, err)
	}
	return nil
}

func (cli *dotNetCli) Restore(ctx context.Context, project string) error {
	res, err := executil.RunCommandWithShell(ctx, "dotnet", "restore", project)
	if err != nil {
		return fmt.Errorf("failed to restore project '%s': %w (%s)", project, err, res.String())
	}
	return nil
}

func NewDotNetCli() DotNetCli {
	return &dotNetCli{}
}

func (cli *dotNetCli) InitializeSecret(ctx context.Context, project string) error {
	res, err := executil.RunCommandWithShell(ctx, "dotnet", "user-secrets", "init")
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
