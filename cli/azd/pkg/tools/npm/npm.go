// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package npm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/blang/semver/v4"
)

var _ tools.ExternalTool = (*Cli)(nil)

type Cli struct {
	commandRunner  exec.CommandRunner
	packageManager PackageManagerKind
}

func NewCli(commandRunner exec.CommandRunner) *Cli {
	return &Cli{
		commandRunner:  commandRunner,
		packageManager: PackageManagerNpm,
	}
}

// NewCliWithPackageManager creates a Cli that uses the specified package manager.
func NewCliWithPackageManager(commandRunner exec.CommandRunner, pm PackageManagerKind) *Cli {
	return &Cli{
		commandRunner:  commandRunner,
		packageManager: pm,
	}
}

// PackageManager returns the package manager kind used by this CLI instance.
func (cli *Cli) PackageManager() PackageManagerKind {
	return cli.packageManager
}

func (cli *Cli) versionInfoNode() tools.VersionInfo {
	return tools.VersionInfo{
		MinimumVersion: semver.Version{
			Major: 18,
			Minor: 0,
			Patch: 0},
		UpdateCommand: "Visit https://nodejs.org/en/ to upgrade",
	}
}

func (cli *Cli) CheckInstalled(ctx context.Context) error {
	// Check that the package manager binary is in PATH
	err := cli.commandRunner.ToolInPath(string(cli.packageManager))
	if err != nil {
		return err
	}

	// Check node version (required for all Node.js package managers)
	nodeRes, err := tools.ExecuteCommand(ctx, cli.commandRunner, "node", "--version")
	if err != nil {
		return fmt.Errorf("checking %s version: %w", cli.Name(), err)
	}
	nodeSemver, err := tools.ExtractVersion(nodeRes)
	if err != nil {
		return fmt.Errorf("converting to semver version fails: %w", err)
	}
	updateDetailNode := cli.versionInfoNode()
	if nodeSemver.Compare(updateDetailNode.MinimumVersion) == -1 {
		return &tools.ErrSemver{ToolName: "Node.js", VersionInfo: updateDetailNode}
	}

	return nil
}

func (cli *Cli) InstallUrl() string {
	switch cli.packageManager {
	case PackageManagerPnpm:
		return "https://pnpm.io/installation"
	case PackageManagerYarn:
		return "https://yarnpkg.com/getting-started/install"
	default:
		return "https://nodejs.org/"
	}
}

func (cli *Cli) Name() string {
	switch cli.packageManager {
	case PackageManagerPnpm:
		return "pnpm CLI"
	case PackageManagerYarn:
		return "yarn CLI"
	default:
		return "npm CLI"
	}
}

func (cli *Cli) Install(ctx context.Context, project string) error {
	pm := string(cli.packageManager)

	// Build install args with PM-specific flags matching azd-app patterns.
	// These flags suppress noisy output, avoid interactive prompts, and speed up installs.
	var runArgs exec.RunArgs
	switch cli.packageManager {
	case PackageManagerPnpm:
		runArgs = exec.NewRunArgs(pm, "install", "--prefer-offline").WithCwd(project)
	case PackageManagerYarn:
		// Yarn Berry (v2+) does not support --prefer-offline and deprecated --non-interactive.
		// Plain install works for both Classic (v1) and Berry (v2+).
		runArgs = exec.NewRunArgs(pm, "install").WithCwd(project)
	default:
		runArgs = exec.NewRunArgs(pm, "install", "--no-audit", "--no-fund", "--prefer-offline").WithCwd(project)
	}

	_, err := cli.commandRunner.Run(ctx, runArgs)

	if err != nil {
		return fmt.Errorf("failed to install project %s using %s: %w", project, pm, err)
	}
	return nil
}

func (cli *Cli) RunScript(ctx context.Context, projectPath string, scriptName string) error {
	pm := string(cli.packageManager)

	// Yarn does not support the --if-present flag. To replicate npm's --if-present behavior
	// (silently succeed when the script doesn't exist), we check package.json first.
	if cli.packageManager == PackageManagerYarn {
		if !scriptExistsInPackageJSON(projectPath, scriptName) {
			return nil
		}
		runArgs := exec.NewRunArgs(pm, "run", scriptName).WithCwd(projectPath)
		_, err := cli.commandRunner.Run(ctx, runArgs)
		if err != nil {
			return fmt.Errorf("failed to run %s script %s, %w", pm, scriptName, err)
		}
		return nil
	}

	// npm supports --if-present after the script name.
	// pnpm requires --if-present before the script name per its CLI spec.
	var runArgs exec.RunArgs
	if cli.packageManager == PackageManagerPnpm {
		runArgs = exec.NewRunArgs(pm, "run", "--if-present", scriptName).WithCwd(projectPath)
	} else {
		runArgs = exec.NewRunArgs(pm, "run", scriptName, "--if-present").WithCwd(projectPath)
	}
	_, err := cli.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("failed to run %s script %s, %w", pm, scriptName, err)
	}

	return nil
}

func (cli *Cli) Prune(ctx context.Context, projectPath string, production bool) error {
	pm := string(cli.packageManager)

	var runArgs exec.RunArgs
	switch cli.packageManager {
	case PackageManagerPnpm:
		// pnpm uses --prod instead of --production
		runArgs = exec.NewRunArgs(pm, "prune").WithCwd(projectPath)
		if production {
			runArgs = runArgs.AppendParams("--prod")
		}
	case PackageManagerYarn:
		// Yarn v2+ (Berry) removed the prune command. Running `yarn install --production`
		// works for Classic (v1). On Berry, --production is deprecated and may not be supported
		// in newer versions (v4+). The Berry alternative `yarn workspaces focus --all --production`
		// requires the yarn workspace-tools plugin.
		// This is a known limitation for Yarn Berry users; most Berry+azd projects use
		// nodeLinker: node-modules where this remains functional.
		runArgs = exec.NewRunArgs(pm, "install").WithCwd(projectPath)
		if production {
			runArgs = runArgs.AppendParams("--production")
		}
	default:
		runArgs = exec.NewRunArgs(pm, "prune").WithCwd(projectPath)
		if production {
			runArgs = runArgs.AppendParams("--production")
		}
	}

	_, err := cli.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("failed pruning %s packages, %w", pm, err)
	}

	return nil
}

// scriptExistsInPackageJSON checks if a named script is defined in the project's package.json.
func scriptExistsInPackageJSON(projectPath string, scriptName string) bool {
	data, err := os.ReadFile(filepath.Join(projectPath, "package.json"))
	if err != nil {
		return false
	}

	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return false
	}

	_, exists := pkg.Scripts[scriptName]
	return exists
}
