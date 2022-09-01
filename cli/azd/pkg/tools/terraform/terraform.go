// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package terraform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/executil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/blang/semver/v4"
)

var (
	//Fix me
	ErrTerraformNotInitialized = errors.New("cli is not logged in. Try running \"azd login\" to fix")
)

type TerraformCli interface {
	tools.ExternalTool
	Validate(ctx context.Context, file string) (string, error)
	RunCommand(ctx context.Context, args ...string) (executil.RunResult, error)
}

type terraformCli struct {
	cli             TerraformCli
	runWithResultFn func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error)
}

type NewTerraformCliArgs struct {
	cli             TerraformCli
	RunWithResultFn func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error)
}

func NewTerraformCli(args NewTerraformCliArgs) TerraformCli {
	if args.RunWithResultFn == nil {
		args.RunWithResultFn = executil.RunWithResult
	}

	return &terraformCli{
		cli:             args.cli,
		runWithResultFn: args.RunWithResultFn,
	}
}

func (cli *terraformCli) Name() string {
	return "Terraform CLI"
}

// Doc  to be added for terraform install
func (cli *terraformCli) InstallUrl() string {
	return "https://aka.ms/azure-dev/terraform-install"
}

func (cli *terraformCli) versionInfo() tools.VersionInfo {
	return tools.VersionInfo{
		MinimumVersion: semver.Version{
			Major: 1,
			Minor: 1,
			Patch: 7},
		UpdateCommand: "Download newer version from https://www.terraform.io/downloads",
	}
}

func (cli *terraformCli) CheckInstalled(ctx context.Context) (bool, error) {
	found, err := tools.ToolInPath("terraform")
	if !found {
		return false, err
	}
	tfVer, err := cli.unmarshalCliVersion(ctx, "terraform_version")
	if err != nil {
		return false, fmt.Errorf("checking %s version:  %w", cli.Name(), err)
	}
	tfSemver, err := semver.Parse(tfVer)
	if err != nil {
		return false, fmt.Errorf("converting to semver version fails: %w", err)
	}
	updateDetail := cli.versionInfo()
	if tfSemver.LT(updateDetail.MinimumVersion) {
		return false, &tools.ErrSemver{ToolName: cli.Name(), VersionInfo: updateDetail}
	}
	return true, nil
}

func (cli *terraformCli) RunCommand(ctx context.Context, args ...string) (executil.RunResult, error) {
	runArgs := executil.RunArgs{
		Cmd:  "terraform",
		Args: args,
	}

	return cli.runWithResultFn(ctx, runArgs)
}

func (cli *terraformCli) unmarshalCliVersion(ctx context.Context, component string) (string, error) {
	azRes, err := tools.ExecuteCommand(ctx, "terraform", "version", "-json")
	if err != nil {
		return "", err
	}
	var tfVerMap map[string]interface{}
	err = json.Unmarshal([]byte(azRes), &tfVerMap)
	if err != nil {
		return "", err
	}
	version, ok := tfVerMap[component].(string)
	if !ok {
		return "", fmt.Errorf("reading %s component '%s' version failed", cli.Name(), component)
	}
	return version, nil
}

func (cli *terraformCli) Validate(ctx context.Context, path string) (string, error) {
	args := []string{fmt.Sprintf("-chdir=%s", path), "validate"}

	validateRes, err := cli.RunCommand(ctx, args...)
	if err != nil {
		return "", fmt.Errorf(
			"failed running terraform validate: %s (%w)",
			validateRes.String(),
			err,
		)
	}
	return validateRes.Stdout, nil
}

type contextKey string

const (
	terraformContextKey contextKey = "terraformcli"
)

func GetTerraformCli(ctx context.Context) TerraformCli {
	cli, ok := ctx.Value(terraformContextKey).(TerraformCli)
	if !ok {
		execUtilFn := executil.GetCommandRunner(ctx)
		args := NewTerraformCliArgs{
			RunWithResultFn: execUtilFn,
		}
		cli = NewTerraformCli(args)
	}

	return cli
}
