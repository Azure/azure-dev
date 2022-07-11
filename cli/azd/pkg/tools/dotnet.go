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

// base version number and empty string if there's no pre-request check on version number
func (cli *dotNetCli) GetToolUpdate() ToolMetaData {
	return ToolMetaData{
		MinimumVersion: semver.Version{
			Major: 6,
			Minor: 0,
			Patch: 3},
		UpdateCommand: "Visit https://docs.microsoft.com/en-us/dotnet/core/releases-and-support to install newer",
	}
}

func (cli *dotNetCli) CheckInstalled(_ context.Context) (bool, error) {
	found, err := toolInPath("dotnet")
	if !found {
		return false, err
	}
	dotnetRes, _ := exec.Command("dotnet", "--version").Output()
	dotnetSemver, err := extractSemver(dotnetRes)
	if err != nil {
		return false, fmt.Errorf("converting to semver version fails: %w", err)
	}
	updateDetail := cli.GetToolUpdate()
	if dotnetSemver.Compare(updateDetail.MinimumVersion) == -1 {
		return false, &ErrSemver{ToolName: cli.Name(), ToolRequire: updateDetail}
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
