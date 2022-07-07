// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/executil"
)

type NpmCli interface {
	ExternalTool
	Install(ctx context.Context, project string, onlyProduction bool) error
	Build(ctx context.Context, project string, env []string) error
}

type npmCli struct {
}

func NewNpmCli() NpmCli {
	return &npmCli{}
}

func (cli *npmCli) CheckInstalled(_ context.Context) (bool, error) {
	return toolInPath("npm")
}

func (cli *npmCli) InstallUrl() string {
	return "https://nodejs.org/"
}

func (cli *npmCli) Name() string {
	return "npm CLI"
}

func (cli *npmCli) Install(ctx context.Context, project string, onlyProduction bool) error {
	res, err := executil.RunCommandWithShellAndEnvAndCwd(ctx, "npm", []string{"install", "--production", fmt.Sprintf("%t", onlyProduction)}, nil, project)
	if err != nil {
		return fmt.Errorf("failed to install project %s, %s: %w", project, res.String(), err)
	}
	return nil
}

func (cli *npmCli) Build(ctx context.Context, project string, env []string) error {
	res, err := executil.RunCommandWithShellAndEnvAndCwd(ctx, "npm", []string{"run", "build", "--if-present", "--production", "true"}, env, project)
	if err != nil {
		return fmt.Errorf("failed to build project %s, %s: %w", project, res.String(), err)
	}
	return nil
}
