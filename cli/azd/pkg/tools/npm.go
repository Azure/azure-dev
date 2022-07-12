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

// base version number and empty string if there's no pre-request check on version number
func (cli *npmCli) GetToolUpdate() ToolMetaData {
	return ToolMetaData{
		MinimumVersion: semver.Version{
			Major: 8,
			Minor: 5,
			Patch: 5},
		UpdateCommand: "Run \"npm update\" to upgrade",
	}
}

func (cli *npmCli) CheckInstalled(_ context.Context) (bool, error) {
	found, err := toolInPath("npm")
	if !found {
		return false, err
	}
	npmRes, _ := exec.Command("npm", "--version").Output()
	npmSemver, err := versionToSemver(npmRes)
	if err != nil {
		return false, fmt.Errorf("converting to semver version fails: %w", err)
	}
	updateDetail := cli.GetToolUpdate()
	if npmSemver.Compare(updateDetail.MinimumVersion) == -1 {
		return false, &ErrSemver{ToolName: cli.Name(), ToolRequire: updateDetail}
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
