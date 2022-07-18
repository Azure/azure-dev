// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"

	"github.com/azure/azure-dev/cli/azd/pkg/executil"
	"github.com/blang/semver/v4"
)

type BicepCli interface {
	ExternalTool
	Build(ctx context.Context, file string) (string, error)
}

func NewBicepCli(cli AzCli) BicepCli {
	return &bicepCli{
		cli: cli,
	}
}

type bicepCli struct {
	cli AzCli
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

func (cli *bicepCli) versionInfo() VersionInfo {
	return VersionInfo{
		MinimumVersion: semver.Version{
			Major: 0,
			Minor: 4,
			Patch: 1008},
		UpdateCommand: "Run \"az bicep upgrade\"  to upgrade",
	}
}

func (cli *bicepCli) CheckInstalled(ctx context.Context) (bool, error) {
	hasCli, err := cli.cli.CheckInstalled(ctx)
	if err != nil || !hasCli {
		return hasCli, err
	}

	// When installed, `az bicep install` is a no-op, otherwise it installs the latest version.
	res, err := executil.RunCommandWithShell(ctx, "az", "bicep", "install")
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

	bicepRes, err := executeCommand(ctx, "az", "bicep", "version")
	if err != nil {
		return false, fmt.Errorf("checking %s version: %w", cli.Name(), err)
	}
	bicepSemver, err := extractSemver(bicepRes)
	if err != nil {
		return false, fmt.Errorf("converting to semver version fails: %w", err)
	}
	updateDetail := cli.versionInfo()
	if bicepSemver.LT(updateDetail.MinimumVersion) {
		return false, &ErrSemver{ToolName: cli.Name(), versionInfo: updateDetail}
	}

	return true, nil
}

func (cli *bicepCli) Build(ctx context.Context, file string) (string, error) {
	sniffCliVersion := func() (string, error) {
		verRes, err := executil.RunCommandWithShell(ctx, "az", "version", "--out", "json")
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

	buildRes, err := executil.RunCommandWithShell(ctx, "az", args...)
	if err != nil {
		return "", fmt.Errorf(
			"failed running az bicep build: %s (%w)",
			buildRes.String(),
			err,
		)
	}
	return buildRes.Stdout, nil
}
