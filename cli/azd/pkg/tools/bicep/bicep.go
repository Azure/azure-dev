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
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/convert"
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
		commandRunner: exec.GetCommandRunner(ctx),
	}
}

type bicepCli struct {
	commandPath   *string
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
			Patch: 9,
		},
		//nolint:lll
		UpdateCommand: `Run 'az bicep upgrade or Visit https://aka.ms/azure-dev/bicep-install to upgrade`,
	}
}

func (cli *bicepCli) CheckInstalled(ctx context.Context) (bool, error) {
	bicepRes, err := cli.runCommand(ctx, "--version")
	if err != nil {
		return false, fmt.Errorf("checking %s version: %w", cli.Name(), err)
	}

	bicepSemver, err := tools.ExtractVersion(bicepRes.Stdout)
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
	args := []string{"build", file, "--stdout"}
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
	if cli.commandPath == nil {
		commandPath, err := findBicepPath()
		if err != nil {
			return exec.RunResult{}, err
		}

		cli.commandPath = commandPath
	}

	runArgs := exec.NewRunArgs(*cli.commandPath, args...)
	return cli.commandRunner.Run(ctx, runArgs)
}

type contextKey string

const (
	bicepContextKey         contextKey = "bicepcli"
	defaultBicepCommandPath string     = "bicep"
	envNameAzureConfigDir   string     = "AZURE_CONFIG_DIR"
)

func GetBicepCli(ctx context.Context) BicepCli {
	cli, ok := ctx.Value(bicepContextKey).(BicepCli)
	if !ok {
		cli = NewBicepCli(ctx)
	}

	return cli
}

// Finds the bicep command path
// Search in PATH, otherwise looks for standalone and az installation locations
func findBicepPath() (*string, error) {
	// First, check if tool is found in path
	// This typically should return true for standalone install and false for `az` bundled install
	found, err := tools.ToolInPath(defaultBicepCommandPath)
	if err != nil {
		return nil, err
	}

	if found {
		return convert.RefOf(defaultBicepCommandPath), nil
	}

	// If not found in path, check in 2 locations
	// Default location for standalone install
	// Default location for az bicep install
	user, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf("failed getting current user: %w", err)
	}

	var bicepFilename string
	var bicepStandalonePath string

	if runtime.GOOS == "windows" {
		bicepFilename = "bicep.exe"
		bicepStandalonePath = filepath.Join(user.HomeDir, fmt.Sprintf("AppData\\Local\\Programs\\Bicep CLI\\%s", bicepFilename))
	} else {
		bicepFilename = "bicep"
		bicepStandalonePath = fmt.Sprintf("/usr/local/bin/%s", bicepFilename)
	}

	azureBin := filepath.Join(user.HomeDir, ".azure", "bin")
	commonPaths := []string{}

	// If AZURE_CONFIG_DIR is defined, check there first
	azureConfigDir := os.Getenv(envNameAzureConfigDir)
	if strings.TrimSpace(azureConfigDir) != "" {
		commonPaths = append(commonPaths, filepath.Join(azureConfigDir, "bin", bicepFilename))
	}

	// Check for standalone installation
	commonPaths = append(commonPaths, bicepStandalonePath)

	// Otherwise look in standard azure bin dir
	commonPaths = append(commonPaths, filepath.Join(azureBin, bicepFilename))

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
