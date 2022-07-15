// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/executil"
	"github.com/blang/semver/v4"
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

func (cli *npmCli) versionInfoNode() VersionInfo {
	return VersionInfo{
		MinimumVersion: semver.Version{
			Major: 16,
			Minor: 0,
			Patch: 0},
		UpdateCommand: "Visit https://nodejs.org/en/ to upgrade",
	}
}

func (cli *npmCli) CheckInstalled(ctx context.Context) (bool, error) {
	found, err := toolInPath("npm")
	if !found {
		return false, err
	}

	//check node version
	nodeRes, err := executeCommand(ctx, "node", "--version")
	if err != nil {
		return false, fmt.Errorf("checking %s version: %w", cli.Name(), err)
	}
	nodeSemver, err := extractSemver(nodeRes)
	if err != nil {
		return false, fmt.Errorf("converting to semver version fails: %w", err)
	}
	updateDetailNode := cli.versionInfoNode()
	if nodeSemver.Compare(updateDetailNode.MinimumVersion) == -1 {
		return false, &ErrSemver{ToolName: cli.Name(), versionInfo: updateDetailNode}
	}

	return true, nil
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
