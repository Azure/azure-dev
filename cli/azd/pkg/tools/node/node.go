// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package node

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

// Cli defines the interface for Node.js package manager operations.
// Separate implementations for npm, pnpm, and yarn handle PM-specific behaviors.
type Cli interface {
	tools.ExternalTool
	Install(ctx context.Context, projectPath string, env []string) error
	RunScript(ctx context.Context, projectPath string, scriptName string, env []string) error
	Prune(ctx context.Context, projectPath string, production bool, env []string) error
	PackageManager() PackageManagerKind
}

// NewCli creates a CLI using the default package manager (npm).
func NewCli(commandRunner exec.CommandRunner) Cli {
	return &npmCli{baseCli: baseCli{commandRunner: commandRunner}}
}

// NewCliWithPackageManager creates a CLI for the specified package manager.
func NewCliWithPackageManager(commandRunner exec.CommandRunner, pm PackageManagerKind) Cli {
	switch pm {
	case PackageManagerPnpm:
		return &pnpmCli{baseCli: baseCli{commandRunner: commandRunner}}
	case PackageManagerYarn:
		return &yarnCli{baseCli: baseCli{commandRunner: commandRunner}}
	default:
		return &npmCli{baseCli: baseCli{commandRunner: commandRunner}}
	}
}

// baseCli provides shared functionality for all package manager implementations.
type baseCli struct {
	commandRunner exec.CommandRunner
}

func (b *baseCli) versionInfoNode() tools.VersionInfo {
	return tools.VersionInfo{
		MinimumVersion: semver.Version{Major: 18, Minor: 0, Patch: 0},
		UpdateCommand:  "Visit https://nodejs.org/en/ to upgrade",
	}
}

func (b *baseCli) checkInstalled(ctx context.Context, pmBinary string, pmDisplayName string) error {
	err := b.commandRunner.ToolInPath(pmBinary)
	if err != nil {
		return err
	}

	// Check node version (required for all Node.js package managers)
	nodeRes, err := tools.ExecuteCommand(ctx, b.commandRunner, "node", "--version")
	if err != nil {
		return fmt.Errorf("checking %s version: %w", pmDisplayName, err)
	}
	nodeSemver, err := tools.ExtractVersion(nodeRes)
	if err != nil {
		return fmt.Errorf("converting to semver version fails: %w", err)
	}
	updateDetailNode := b.versionInfoNode()
	if nodeSemver.Compare(updateDetailNode.MinimumVersion) == -1 {
		return &tools.ErrSemver{ToolName: "Node.js", VersionInfo: updateDetailNode}
	}
	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// npm implementation
// ──────────────────────────────────────────────────────────────────────────────

type npmCli struct{ baseCli }

func (c *npmCli) Name() string                       { return "npm CLI" }
func (c *npmCli) InstallUrl() string                 { return "https://nodejs.org/" }
func (c *npmCli) PackageManager() PackageManagerKind { return PackageManagerNpm }

func (c *npmCli) CheckInstalled(ctx context.Context) error {
	return c.checkInstalled(ctx, "npm", c.Name())
}

func (c *npmCli) Install(ctx context.Context, projectPath string, env []string) error {
	runArgs := exec.NewRunArgs("npm", "install", "--no-audit", "--no-fund", "--prefer-offline").
		WithCwd(projectPath).WithEnv(env)
	if _, err := c.commandRunner.Run(ctx, runArgs); err != nil {
		return fmt.Errorf("failed to install project %s using npm: %w", projectPath, err)
	}
	return nil
}

func (c *npmCli) RunScript(ctx context.Context, projectPath string, scriptName string, env []string) error {
	// npm supports --if-present after the script name
	runArgs := exec.NewRunArgs("npm", "run", scriptName, "--if-present").WithCwd(projectPath).WithEnv(env)
	if _, err := c.commandRunner.Run(ctx, runArgs); err != nil {
		return fmt.Errorf("failed to run npm script %s: %w", scriptName, err)
	}
	return nil
}

func (c *npmCli) Prune(ctx context.Context, projectPath string, production bool, env []string) error {
	runArgs := exec.NewRunArgs("npm", "prune").WithCwd(projectPath).WithEnv(env)
	if production {
		runArgs = runArgs.AppendParams("--production")
	}
	if _, err := c.commandRunner.Run(ctx, runArgs); err != nil {
		return fmt.Errorf("failed to prune npm packages: %w", err)
	}
	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// pnpm implementation
// ──────────────────────────────────────────────────────────────────────────────

type pnpmCli struct{ baseCli }

func (c *pnpmCli) Name() string                       { return "pnpm CLI" }
func (c *pnpmCli) InstallUrl() string                 { return "https://pnpm.io/installation" }
func (c *pnpmCli) PackageManager() PackageManagerKind { return PackageManagerPnpm }

func (c *pnpmCli) CheckInstalled(ctx context.Context) error {
	return c.checkInstalled(ctx, "pnpm", c.Name())
}

func (c *pnpmCli) Install(ctx context.Context, projectPath string, env []string) error {
	runArgs := exec.NewRunArgs("pnpm", "install", "--prefer-offline").WithCwd(projectPath).WithEnv(env)
	if _, err := c.commandRunner.Run(ctx, runArgs); err != nil {
		return fmt.Errorf("failed to install project %s using pnpm: %w", projectPath, err)
	}
	return nil
}

func (c *pnpmCli) RunScript(ctx context.Context, projectPath string, scriptName string, env []string) error {
	// pnpm requires --if-present before the script name per its CLI spec
	runArgs := exec.NewRunArgs("pnpm", "run", "--if-present", scriptName).WithCwd(projectPath).WithEnv(env)
	if _, err := c.commandRunner.Run(ctx, runArgs); err != nil {
		return fmt.Errorf("failed to run pnpm script %s: %w", scriptName, err)
	}
	return nil
}

func (c *pnpmCli) Prune(ctx context.Context, projectPath string, production bool, env []string) error {
	// pnpm uses --prod instead of --production
	runArgs := exec.NewRunArgs("pnpm", "prune").WithCwd(projectPath).WithEnv(env)
	if production {
		runArgs = runArgs.AppendParams("--prod")
	}
	if _, err := c.commandRunner.Run(ctx, runArgs); err != nil {
		return fmt.Errorf("failed to prune pnpm packages: %w", err)
	}
	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// yarn implementation
// ──────────────────────────────────────────────────────────────────────────────

type yarnCli struct{ baseCli }

func (c *yarnCli) Name() string                       { return "yarn CLI" }
func (c *yarnCli) InstallUrl() string                 { return "https://yarnpkg.com/getting-started/install" }
func (c *yarnCli) PackageManager() PackageManagerKind { return PackageManagerYarn }

func (c *yarnCli) CheckInstalled(ctx context.Context) error {
	return c.checkInstalled(ctx, "yarn", c.Name())
}

func (c *yarnCli) Install(ctx context.Context, projectPath string, env []string) error {
	// Yarn Berry (v2+) does not support --prefer-offline and deprecated --non-interactive.
	// Plain install works for both Classic (v1) and Berry (v2+).
	runArgs := exec.NewRunArgs("yarn", "install").WithCwd(projectPath).WithEnv(env)
	if _, err := c.commandRunner.Run(ctx, runArgs); err != nil {
		return fmt.Errorf("failed to install project %s using yarn: %w", projectPath, err)
	}
	return nil
}

func (c *yarnCli) RunScript(ctx context.Context, projectPath string, scriptName string, env []string) error {
	// Yarn has no --if-present flag. To replicate npm's --if-present behavior
	// (silently succeed when the script doesn't exist), we check package.json first.
	exists, err := scriptExistsInPackageJSON(projectPath, scriptName)
	if err != nil {
		return fmt.Errorf("failed to check yarn script %s: %w", scriptName, err)
	}
	if !exists {
		return nil
	}
	runArgs := exec.NewRunArgs("yarn", "run", scriptName).WithCwd(projectPath).WithEnv(env)
	if _, err := c.commandRunner.Run(ctx, runArgs); err != nil {
		return fmt.Errorf("failed to run yarn script %s: %w", scriptName, err)
	}
	return nil
}

func (c *yarnCli) Prune(ctx context.Context, projectPath string, production bool, env []string) error {
	// Yarn v2+ (Berry) removed the prune command. Running `yarn install --production`
	// works for Classic (v1). On Berry, --production is deprecated and may not be supported
	// in newer versions (v4+). The Berry alternative `yarn workspaces focus --all --production`
	// requires the yarn workspace-tools plugin.
	// This is a known limitation for Yarn Berry users; most Berry+azd projects use
	// nodeLinker: node-modules where this remains functional.
	runArgs := exec.NewRunArgs("yarn", "install").WithCwd(projectPath).WithEnv(env)
	if production {
		runArgs = runArgs.AppendParams("--production")
	}
	if _, err := c.commandRunner.Run(ctx, runArgs); err != nil {
		return fmt.Errorf("failed to prune yarn packages: %w", err)
	}
	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Shared helpers
// ──────────────────────────────────────────────────────────────────────────────

// scriptExistsInPackageJSON checks if a named script is defined in the project's package.json.
// Returns (false, nil) if package.json doesn't exist (script definitively absent).
// Returns an error for I/O problems or invalid JSON so broken projects fail loudly.
func scriptExistsInPackageJSON(projectPath string, scriptName string) (bool, error) {
	data, err := os.ReadFile(filepath.Join(projectPath, "package.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("reading package.json: %w", err)
	}

	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return false, fmt.Errorf("parsing package.json: %w", err)
	}

	_, exists := pkg.Scripts[scriptName]
	return exists, nil
}
