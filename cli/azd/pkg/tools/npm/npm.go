// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package npm

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/executil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/blang/semver/v4"
)

type NpmCli interface {
	tools.ExternalTool
	Install(ctx context.Context, project string, onlyProduction bool) error
	Build(ctx context.Context, project string, env []string) error
}

type npmCli struct {
}

func NewNpmCli() NpmCli {
	return &npmCli{}
}

func (cli *npmCli) versionInfoNode() tools.VersionInfo {
	return tools.VersionInfo{
		MinimumVersion: semver.Version{
			Major: 16,
			Minor: 0,
			Patch: 0},
		UpdateCommand: "Visit https://nodejs.org/en/ to upgrade",
	}
}

func (cli *npmCli) CheckInstalled(ctx context.Context) (bool, error) {
	found, err := tools.ToolInPath("npm")
	if !found {
		return false, err
	}

	//check node version
	nodeRes, err := tools.ExecuteCommand(ctx, "node", "--version")
	if err != nil {
		return false, fmt.Errorf("checking %s version: %w", cli.Name(), err)
	}
	nodeSemver, err := tools.ExtractSemver(nodeRes)
	if err != nil {
		return false, fmt.Errorf("converting to semver version fails: %w", err)
	}
	updateDetailNode := cli.versionInfoNode()
	if nodeSemver.Compare(updateDetailNode.MinimumVersion) == -1 {
		return false, &tools.ErrSemver{ToolName: "Node.js", VersionInfo: updateDetailNode}
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
