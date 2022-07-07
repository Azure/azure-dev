// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/executil"
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

func (cli *dotNetCli) CheckInstalled(_ context.Context) (bool, error) {
	return toolInPath("dotnet")
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
