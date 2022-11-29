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
	// Set environment variables to be used in all terraform commands
	SetEnv(envVars []string)
	// Validates the terraform module
	Validate(ctx context.Context, modulePath string) (string, error)
	// Initializes the terraform module
	Init(ctx context.Context, modulePath string, additionalArgs ...string) (string, error)
	// Creates a deployment plan for the terraform module
	Plan(ctx context.Context, modulePath string, planFilePath string, additionalArgs ...string) (string, error)
	// Applies and provisions all resources in the terraform module
	Apply(ctx context.Context, modulePath string, additionalArgs ...string) (string, error)
	// Retrieves the output variables from the most recent deployment state
	Output(ctx context.Context, modulePath string, additionalArgs ...string) (string, error)
	// Retrieves information about the infrastructure from the current deployment state
	Show(ctx context.Context, modulePath string, additionalArgs ...string) (string, error)
	// Destroys all resources referenced in the terraform module
	Destroy(ctx context.Context, modulePath string, additionalArgs ...string) (string, error)
}

type terraformCli struct {
	commandRunner exec.CommandRunner
	env           []string
}

func NewTerraformCli(commandRunner exec.CommandRunner) TerraformCli {
	return &terraformCli{
		commandRunner: commandRunner,
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

// Set environment variables to be used in all terraform commands
func (cli *terraformCli) SetEnv(env []string) {
	cli.env = env
}

func (cli *terraformCli) runCommand(ctx context.Context, args ...string) (exec.RunResult, error) {
	runArgs := exec.
		NewRunArgs("terraform", args...).
		WithEnv(cli.env)

	return cli.commandRunner.Run(ctx, runArgs)
}

func (cli *terraformCli) runInteractive(ctx context.Context, args ...string) (exec.RunResult, error) {
	runArgs := exec.
		NewRunArgs("terraform", args...).
		WithEnv(cli.env).
		WithInteractive(true)

	return cli.commandRunner.Run(ctx, runArgs)
}

func (cli *terraformCli) unmarshalCliVersion(ctx context.Context, component string) (string, error) {
	azRes, err := tools.ExecuteCommand(ctx, cli.commandRunner, "terraform", "version", "-json")
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

func (cli *terraformCli) Plan(
	ctx context.Context,
	modulePath string,
	planFilePath string,
	additionalArgs ...string,
) (string, error) {
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

func (cli *terraformCli) Show(ctx context.Context, modulePath string, additionalArgs ...string) (string, error) {
	args := []string{
		fmt.Sprintf("-chdir=%s", modulePath), "show", "-json"}

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
