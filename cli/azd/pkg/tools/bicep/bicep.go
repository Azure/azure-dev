// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/blang/semver/v4"
)

type BicepCli interface {
	tools.ExternalTool
	Build(ctx context.Context, file string) (string, error)
}

func NewBicepCli(ctx context.Context) BicepCli {
	return &bicepCli{
		commandPath:   "bicep",
		commandRunner: exec.GetCommandRunner(ctx),
	}
}

type bicepCli struct {
	commandPath   string
	commandRunner exec.CommandRunner
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
		UpdateCommand: `Run 'az bicep upgrade or Visit https://learn.microsoft.com/en-us/azure/azure-resource-manager/bicep/install to upgrade`,
	}
}

func (cli *bicepCli) CheckInstalled(ctx context.Context) (bool, error) {
	// First, check if tool is found in path
	// This typically should return true for standalone install and false for `az` bundled install
	found, err := tools.ToolInPath("bicep")
	if err != nil {
		return false, err
	}

	// Next, check in other known locations (ex. Azure bin)
	if !found {
		bicepPath, err := findBicepPath()
		if err != nil {
			return false, err
		}

		cli.commandPath = *bicepPath
	}

	bicepRes, err := cli.runCommand(ctx, "--version")
	if err != nil {
		return false, fmt.Errorf("checking %s version: %w", cli.Name(), err)
	}

	bicepSemver, err := tools.ExtractSemver(bicepRes.Stdout)
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
	// sniffCliVersion := func() (string, error) {
	// 	verRes, err := cli.runCommand(ctx, "--version", "--out", "json")
	// 	if err != nil {
	// 		return "", fmt.Errorf("failing running az version: %s (%w)", verRes.String(), err)
	// 	}

	// 	var jsonVer struct {
	// 		AzureCli string `json:"azure-cli"`
	// 	}

	// 	if err := json.Unmarshal([]byte(verRes.Stdout), &jsonVer); err != nil {
	// 		return "", fmt.Errorf("parsing cli version json: %s: %w", verRes.Stdout, err)
	// 	}

	// 	return jsonVer.AzureCli, nil
	// }

	args := []string{"build", file, "--stdout"}

	// Workaround azure/azure-cli#22621, by passing `--no-restore` to the CLI when
	// when version 2.37.0 is installed.
	// if ver, err := sniffCliVersion(); err != nil {
	// 	log.Printf("error sniffing az cli version: %s", err.Error())
	// } else if ver == "2.37.0" {
	// 	log.Println("appending `--no-restore` to bicep arguments to work around azure/azure-dev#22621")
	// 	args = append(args, "--no-restore")
	// }

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

func (cli *bicepCli) runCommand(ctx context.Context, args ...string) (exec.RunResult, error) {
	runArgs := exec.NewRunArgs(cli.commandPath, args...)
	return cli.commandRunner.Run(ctx, runArgs)
}

type contextKey string

const (
	bicepContextKey contextKey = "bicepcli"
)

func GetBicepCli(ctx context.Context) BicepCli {
	cli, ok := ctx.Value(bicepContextKey).(BicepCli)
	if !ok {
		cli = NewBicepCli(ctx)
	}

	return cli
}

func findBicepPath() (*string, error) {
	user, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf("failed getting current user: %w", err)
	}

	commonPaths := []string{}
	azureBin := filepath.Join(user.HomeDir, ".azure", "bin")

	// When installed standalone is typically located in the following locations
	// When installed with 'az' it is inside the azure bin folder across all OSes
	if runtime.GOOS == "windows" {
		commonPaths = append(commonPaths, filepath.Join(user.HomeDir, "AppData/Local/Programs/Bicep CLI/bicep.exe"))
		commonPaths = append(commonPaths, filepath.Join(azureBin, "bicep.exe"))
	} else {
		commonPaths = append(commonPaths, "/usr/local/bin/bicep")
		commonPaths = append(commonPaths, filepath.Join(azureBin, "bicep"))
	}

	var existsErr error

	// Search and find first matching path
	// Take standalone version before az cli version
	for _, installPath := range commonPaths {
		_, existsErr = os.Stat(installPath)
		if existsErr == nil {
			return &installPath, nil
		}
	}

	return nil, fmt.Errorf("cannot find bicep path: %w", existsErr)
}
