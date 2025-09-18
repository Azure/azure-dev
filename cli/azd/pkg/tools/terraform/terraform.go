// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package terraform

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/blang/semver/v4"
)

var _ tools.ExternalTool = (*Cli)(nil)

type Cli struct {
	commandRunner exec.CommandRunner
	env           []string
}

func NewCli(commandRunner exec.CommandRunner) *Cli {
	return &Cli{
		commandRunner: commandRunner,
	}
}

func (cli *Cli) Name() string {
	return "Terraform CLI"
}

// Doc  to be added for terraform install
func (cli *Cli) InstallUrl() string {
	return "https://aka.ms/azure-dev/terraform-install"
}

func (cli *Cli) versionInfo() tools.VersionInfo {
	return tools.VersionInfo{
		MinimumVersion: semver.Version{
			Major: 1,
			Minor: 1,
			Patch: 7},
		UpdateCommand: "Download newer version from https://www.terraform.io/downloads",
	}
}

func (cli *Cli) CheckInstalled(ctx context.Context) error {
	err := cli.commandRunner.ToolInPath("terraform")
	if err != nil {
		return err
	}
	tfVer, err := cli.unmarshalCliVersion(ctx, "terraform_version")
	if err != nil {
		return fmt.Errorf("checking %s version:  %w", cli.Name(), err)
	}

	log.Printf("terraform version: %s", tfVer)

	tfSemver, err := semver.Parse(tfVer)
	if err != nil {
		return fmt.Errorf("converting to semver version fails: %w", err)
	}
	updateDetail := cli.versionInfo()
	if tfSemver.LT(updateDetail.MinimumVersion) {
		return &tools.ErrSemver{ToolName: cli.Name(), VersionInfo: updateDetail}
	}
	return nil
}

// Set environment variables to be used in all terraform commands
func (cli *Cli) SetEnv(env []string) {
	cli.env = env
}

func (cli *Cli) runCommand(ctx context.Context, args ...string) (exec.RunResult, error) {
	runArgs := exec.
		NewRunArgs("terraform", args...).
		WithEnv(cli.env)

	return cli.commandRunner.Run(ctx, runArgs)
}

func (cli *Cli) runInteractive(ctx context.Context, args ...string) (exec.RunResult, error) {
	runArgs := exec.
		NewRunArgs("terraform", args...).
		WithEnv(cli.env).
		WithInteractive(true)

	return cli.commandRunner.Run(ctx, runArgs)
}

func (cli *Cli) unmarshalCliVersion(ctx context.Context, component string) (string, error) {
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

func (cli *Cli) Validate(ctx context.Context, modulePath string) (string, error) {
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

func (cli *Cli) Init(ctx context.Context, modulePath string, additionalArgs ...string) (string, error) {
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

func (cli *Cli) Plan(
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

func (cli *Cli) Apply(ctx context.Context, modulePath string, additionalArgs ...string) (string, error) {
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

func (cli *Cli) Output(ctx context.Context, modulePath string, additionalArgs ...string) (string, error) {
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

func (cli *Cli) Show(ctx context.Context, modulePath string, additionalArgs ...string) (string, error) {
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

func (cli *Cli) Destroy(ctx context.Context, modulePath string, additionalArgs ...string) (string, error) {
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
