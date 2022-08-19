// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"

	"github.com/azure/azure-dev/cli/azd/pkg/executil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/blang/semver/v4"
)

type BicepCli interface {
	tools.ExternalTool
	Build(ctx context.Context, file string) (string, error)
}

func NewBicepCli(args NewBicepCliArgs) BicepCli {
	if args.RunWithResultFn == nil {
		args.RunWithResultFn = executil.RunWithResult
	}

	return &bicepCli{
		cli:             args.AzCli,
		runWithResultFn: args.RunWithResultFn,
	}
}

type NewBicepCliArgs struct {
	AzCli           azcli.AzCli
	RunWithResultFn func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error)
}

type bicepCli struct {
	cli             azcli.AzCli
	runWithResultFn func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error)
}

var isBicepNotFoundRegex = regexp.MustCompile(`Bicep CLI not found\.`)

func isBicepNotFoundMessage(s string) bool {
	return isBicepNotFoundRegex.MatchString(s)
}

func (cli *bicepCli) Name() string {
	return "Bicep CLI"
}

func (cli *bicepCli) InstallUrl() string {
	return "https://aka.ms/azure-dev/bicep-install"
}

func (cli *bicepCli) versionInfo() tools.VersionInfo {
	return tools.VersionInfo{
		MinimumVersion: semver.Version{
			Major: 0,
			Minor: 8,
			Patch: 9},
		UpdateCommand: "Run \"az bicep upgrade\"  to upgrade",
	}
}

func (cli *bicepCli) CheckInstalled(ctx context.Context) (bool, error) {
	hasCli, err := cli.cli.CheckInstalled(ctx)
	if err != nil || !hasCli {
		return hasCli, err
	}

	// When installed, `az bicep install` is a no-op, otherwise it installs the latest version.
	res, err := cli.runCommand(ctx, "bicep", "install")
	switch {
	case isBicepNotFoundMessage(res.Stderr):
		return false, nil
	case err != nil:
		return false, fmt.Errorf(
			"failed running az bicep install: %s (%w)",
			res.String(),
			err,
		)
	}

	bicepRes, err := tools.ExecuteCommand(ctx, "az", "bicep", "version")
	if err != nil {
		return false, fmt.Errorf("checking %s version: %w", cli.Name(), err)
	}
	bicepSemver, err := tools.ExtractSemver(bicepRes)
	if err != nil {
		return false, fmt.Errorf("converting to semver version fails: %w", err)
	}
	updateDetail := cli.versionInfo()
	if bicepSemver.LT(updateDetail.MinimumVersion) {
		return false, &tools.ErrSemver{ToolName: cli.Name(), VersionInfo: updateDetail}
	}

	return true, nil
}

func (cli *bicepCli) Build(ctx context.Context, file string) (string, error) {
	sniffCliVersion := func() (string, error) {
		verRes, err := cli.runCommand(ctx, "version", "--out", "json")
		if err != nil {
			return "", fmt.Errorf("failing running az version: %s (%w)", verRes.String(), err)
		}

		var jsonVer struct {
			AzureCli string `json:"azure-cli"`
		}

		if err := json.Unmarshal([]byte(verRes.Stdout), &jsonVer); err != nil {
			return "", fmt.Errorf("parsing cli version json: %s: %w", verRes.Stdout, err)
		}

		return jsonVer.AzureCli, nil
	}

	args := []string{"bicep", "build", "--file", file, "--stdout"}

	// Workaround azure/azure-cli#22621, by passing `--no-restore` to the CLI when
	// when version 2.37.0 is installed.
	if ver, err := sniffCliVersion(); err != nil {
		log.Printf("error sniffing az cli version: %s", err.Error())
	} else if ver == "2.37.0" {
		log.Println("appending `--no-restore` to bicep arguments to work around azure/azure-dev#22621")
		args = append(args, "--no-restore")
	}

	buildRes, err := cli.runCommand(ctx, args...)
	if err != nil {
		return "", fmt.Errorf(
			"failed running az bicep build: %s (%w)",
			buildRes.String(),
			err,
		)
	}
	return buildRes.Stdout, nil
}

func (cli *bicepCli) runCommand(ctx context.Context, args ...string) (executil.RunResult, error) {
	runArgs := executil.RunArgs{
		Cmd:  "az",
		Args: args,
	}

	return cli.runWithResultFn(ctx, runArgs)
}
