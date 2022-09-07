// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package terraform

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/blang/semver/v4"
)

type TerraformCli interface {
	tools.ExternalTool
	Validate(ctx context.Context, modulePath string) (string, error)
	Init(ctx context.Context, modulePath string, additionalArgs ...string) (string, error)
	Plan(ctx context.Context, modulePath string, planFilePath string, additionalArgs ...string) (string, error)
	Apply(ctx context.Context, modulePath string, additionalArgs ...string) (string, error)
	Output(ctx context.Context, modulePath string, additionalArgs ...string) (string, error)
	Destroy(ctx context.Context, modulePath string, additionalArgs ...string) (string, error)
}

type terraformCli struct {
	cli           TerraformCli
	commandRunner exec.CommandRunner
}

type NewTerraformCliArgs struct {
	cli           TerraformCli
	commandRunner exec.CommandRunner
}

func NewTerraformCli(args NewTerraformCliArgs) TerraformCli {
	if args.commandRunner == nil {
		args.commandRunner = exec.NewCommandRunner()
	}

	return &terraformCli{
		cli:           args.cli,
		commandRunner: args.commandRunner,
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

func (cli *terraformCli) runCommand(ctx context.Context, args ...string) (exec.RunResult, error) {
	runArgs := exec.NewRunArgs("terraform", args...)
	return cli.commandRunner.Run(ctx, runArgs)
}

func (cli *terraformCli) runInteractive(ctx context.Context, args ...string) (exec.RunResult, error) {
	runArgs := exec.NewRunArgs("terraform", args...).WithInteractive(true)
	return cli.commandRunner.Run(ctx, runArgs)
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

func (cli *terraformCli) Validate(ctx context.Context, modulePath string) (string, error) {
	args := []string{fmt.Sprintf("-chdir=%s", modulePath), "validate"}

	cmdRes, err := cli.runCommand(ctx, args...)
	if err != nil {
		return "", fmt.Errorf(
			"failed running terraform validate: %s (%w)",
			cmdRes.Stderr,
			err,
		)
	}
	return cmdRes.Stdout, nil
}

func (cli *terraformCli) Init(ctx context.Context, modulePath string, additionalArgs ...string) (string, error) {
	args := []string{
		fmt.Sprintf("-chdir=%s", modulePath),
		"init",
		"-upgrade",
	}

	args = append(args, additionalArgs...)
	cmdRes, err := cli.runInteractive(ctx, args...)
	if err != nil {
		return "", fmt.Errorf(
			"failed running terraform init: %s (%w)",
			cmdRes.Stderr,
			err,
		)
	}
	return cmdRes.Stdout, nil
}

func (cli *terraformCli) Plan(ctx context.Context, modulePath string, planFilePath string, additionalArgs ...string) (string, error) {
	args := []string{
		fmt.Sprintf("-chdir=%s", modulePath),
		"plan",
		fmt.Sprintf("-out=%s", planFilePath),
		"-lock=false",
	}

	args = append(args, additionalArgs...)
	cmdRes, err := cli.runInteractive(ctx, args...)
	if err != nil {
		return "", fmt.Errorf(
			"failed running terraform plan: %s (%w)",
			cmdRes.Stderr,
			err,
		)
	}
	return cmdRes.Stdout, nil
}

func (cli *terraformCli) Apply(ctx context.Context, modulePath string, additionalArgs ...string) (string, error) {
	args := []string{
		fmt.Sprintf("-chdir=%s", modulePath),
		"apply",
		"-lock=false",
	}

	args = append(args, additionalArgs...)
	cmdRes, err := cli.runInteractive(ctx, args...)
	if err != nil {
		return "", fmt.Errorf(
			"failed running terraform apply: %s (%w)",
			cmdRes.Stderr,
			err,
		)
	}
	return cmdRes.Stdout, nil
}

func (cli *terraformCli) Output(ctx context.Context, modulePath string, additionalArgs ...string) (string, error) {
	args := []string{
		fmt.Sprintf("-chdir=%s", modulePath), "output", "-json"}

	args = append(args, additionalArgs...)
	cmdRes, err := cli.runCommand(ctx, args...)
	if err != nil {
		return "", fmt.Errorf(
			"failed running terraform output: %s (%w)",
			cmdRes.Stderr,
			err,
		)
	}
	return cmdRes.Stdout, nil
}

func (cli *terraformCli) Destroy(ctx context.Context, modulePath string, additionalArgs ...string) (string, error) {
	args := []string{
		fmt.Sprintf("-chdir=%s", modulePath),
		"destroy",
	}

	args = append(args, additionalArgs...)
	cmdRes, err := cli.runInteractive(ctx, args...)
	if err != nil {
		return "", fmt.Errorf(
			"failed running terraform destroy: %s (%w)",
			cmdRes.Stderr,
			err,
		)
	}
	return cmdRes.Stdout, nil
}

type contextKey string

const (
	terraformContextKey contextKey = "terraformcli"
)

func GetTerraformCli(ctx context.Context) TerraformCli {
	cli, ok := ctx.Value(terraformContextKey).(TerraformCli)
	if !ok {
		newCommandRunner := exec.GetCommandRunner(ctx)
		args := NewTerraformCliArgs{
			commandRunner: newCommandRunner,
		}

		cli = NewTerraformCli(args)
	}

	return cli
}
