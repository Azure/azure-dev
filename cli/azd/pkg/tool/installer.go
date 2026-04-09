// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tool

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
)

// InstallResult captures the outcome of an install or upgrade operation.
type InstallResult struct {
	// Tool is the definition that was installed or upgraded.
	Tool *ToolDefinition
	// Success indicates whether the operation completed successfully
	// and the tool is now available on the local machine.
	Success bool
	// InstalledVersion is the version detected after installation.
	InstalledVersion string
	// Strategy describes what was used to install the tool
	// (e.g. "winget", "brew", "manual").
	Strategy string
	// Duration is the wall-clock time the operation took.
	Duration time.Duration
	// Error holds any error encountered during the operation.
	Error error
}

// Installer defines the contract for installing and upgrading tools on
// the current platform.
type Installer interface {
	// Install attempts to install the given tool using the best
	// strategy available for the current platform.
	Install(
		ctx context.Context,
		tool *ToolDefinition,
	) (*InstallResult, error)

	// Upgrade attempts to upgrade the given tool to its latest
	// version. When no upgrade-specific command exists the
	// operation falls back to a regular install.
	Upgrade(
		ctx context.Context,
		tool *ToolDefinition,
	) (*InstallResult, error)
}

// installer is the default, unexported implementation of [Installer].
type installer struct {
	commandRunner    exec.CommandRunner
	platformDetector *PlatformDetector
	detector         Detector
	platformOnce     sync.Once
	platform         *Platform // lazily populated by ensurePlatform
	platformErr      error
}

// NewInstaller creates an [Installer] backed by the provided
// dependencies. Platform detection is deferred until the first
// Install or Upgrade call.
func NewInstaller(
	commandRunner exec.CommandRunner,
	platformDetector *PlatformDetector,
	detector Detector,
) Installer {
	return &installer{
		commandRunner:    commandRunner,
		platformDetector: platformDetector,
		detector:         detector,
	}
}

// Install detects the current platform, selects an appropriate
// strategy, runs the installation command, and verifies the result.
func (i *installer) Install(
	ctx context.Context,
	tool *ToolDefinition,
) (*InstallResult, error) {
	return i.run(ctx, tool, false)
}

// Upgrade detects the current platform, selects an appropriate
// strategy, runs the upgrade command, and verifies the result. If
// no upgrade-specific path exists the operation falls back to a
// regular install.
func (i *installer) Upgrade(
	ctx context.Context,
	tool *ToolDefinition,
) (*InstallResult, error) {
	return i.run(ctx, tool, true)
}

// run is the shared implementation for Install and Upgrade.
func (i *installer) run(
	ctx context.Context,
	tool *ToolDefinition,
	upgrade bool,
) (*InstallResult, error) {
	start := time.Now()
	result := &InstallResult{Tool: tool}

	// 1. Detect the platform (cached after the first call).
	platform, err := i.ensurePlatform(ctx)
	if err != nil {
		result.Error = fmt.Errorf(
			"detecting platform: %w", err,
		)
		result.Duration = time.Since(start)
		return result, nil
	}

	// 2. Select the best install strategy for this platform.
	strategy := i.platformDetector.SelectStrategy(
		tool, platform,
	)
	if strategy == nil {
		result.Error = fmt.Errorf(
			"no install strategy for %s on platform %s",
			tool.Name, platform.OS,
		)
		result.Duration = time.Since(start)
		return result, nil
	}

	// Determine a human-readable label for the strategy.
	strategyLabel := strategy.PackageManager
	if strategyLabel == "" {
		strategyLabel = "command"
	}
	result.Strategy = strategyLabel

	// 3. When the strategy names a package manager but has no
	//    explicit InstallCommand, verify the manager is available.
	if strategy.PackageManager != "" &&
		strategy.InstallCommand == "" &&
		!platform.HasManager(strategy.PackageManager) {
		result.Strategy = "manual"
		result.Error = i.managerUnavailableError(
			tool, strategy,
		)
		result.Duration = time.Since(start)
		return result, nil
	}

	// 4. Execute the install or upgrade command.
	if err := i.executeStrategy(
		ctx, strategy, upgrade,
	); err != nil {
		result.Error = fmt.Errorf(
			"running install command for %s: %w",
			tool.Name, err,
		)
		result.Duration = time.Since(start)
		return result, nil
	}

	// 5. Verify installation by detecting the tool again.
	status, err := i.detector.DetectTool(ctx, tool)
	if err != nil {
		result.Error = fmt.Errorf(
			"verifying installation of %s: %w",
			tool.Name, err,
		)
		result.Duration = time.Since(start)
		return result, nil
	}

	if !status.Installed {
		result.Error = fmt.Errorf(
			"%s was installed but verification failed",
			tool.Name,
		)
		result.Duration = time.Since(start)
		return result, nil
	}

	// 6. Success — record the detected version and duration.
	result.Success = true
	result.InstalledVersion = status.InstalledVersion
	result.Duration = time.Since(start)

	return result, nil
}

// ensurePlatform lazily detects the current platform using sync.Once
// to guarantee thread-safe initialization. The first context passed
// wins, which is acceptable since platform detection is OS-level and
// does not depend on request-scoped context.
func (i *installer) ensurePlatform(
	ctx context.Context,
) (*Platform, error) {
	i.platformOnce.Do(func() {
		p, err := i.platformDetector.Detect(ctx)
		if err != nil {
			i.platformErr = fmt.Errorf("platform detection: %w", err)
			return
		}
		i.platform = p
	})
	return i.platform, i.platformErr
}

// executeStrategy runs the command described by the given strategy.
// When upgrade is true the upgrade variant of the command is used
// where applicable. Commands containing shell operators (pipes,
// redirects, etc.) are executed through the system shell.
func (i *installer) executeStrategy(
	ctx context.Context,
	strategy *InstallStrategy,
	upgrade bool,
) error {
	// When the strategy has an explicit InstallCommand that uses
	// shell operators, delegate to the system shell directly so
	// that pipes and redirects work correctly (e.g.
	// "curl -sL ... | sudo bash").
	if strategy.InstallCommand != "" &&
		containsShellOperators(strategy.InstallCommand) {
		return i.executeShellCommand(ctx, strategy.InstallCommand)
	}

	cmd, args := i.buildCommand(strategy, upgrade)
	if cmd == "" {
		return fmt.Errorf("strategy produced an empty command")
	}

	runArgs := exec.NewRunArgs(cmd, args...)
	_, err := i.commandRunner.Run(ctx, runArgs)
	return err
}

// buildCommand constructs the executable name and argument list for
// the given strategy. For upgrades the package-manager upgrade
// variant is preferred; when unavailable the install command is used
// as a fallback.
func (i *installer) buildCommand(
	strategy *InstallStrategy,
	upgrade bool,
) (string, []string) {
	// For upgrades, prefer the package-manager upgrade command
	// when both PackageManager and PackageId are available.
	if upgrade &&
		strategy.PackageManager != "" &&
		strategy.PackageId != "" {
		return buildManagerCommand(
			strategy.PackageManager,
			strategy.PackageId,
			true,
		)
	}

	// Use an explicit InstallCommand when present.
	if strategy.InstallCommand != "" {
		return splitCommand(strategy.InstallCommand)
	}

	// Fall back to package-manager install command.
	if strategy.PackageManager != "" &&
		strategy.PackageId != "" {
		return buildManagerCommand(
			strategy.PackageManager,
			strategy.PackageId,
			false,
		)
	}

	return "", nil
}

// managerUnavailableError builds an [errorhandler.ErrorWithSuggestion]
// for the case where the required package manager is not installed.
func (i *installer) managerUnavailableError(
	tool *ToolDefinition,
	strategy *InstallStrategy,
) error {
	suggestion := fmt.Sprintf(
		"Package manager %q is not available. "+
			"Install it first or install %s manually.",
		strategy.PackageManager, tool.Name,
	)

	var links []errorhandler.ErrorLink
	if strategy.FallbackUrl != "" {
		suggestion = fmt.Sprintf(
			"Install %s manually from: %s",
			tool.Name, strategy.FallbackUrl,
		)
		links = append(links, errorhandler.ErrorLink{
			URL:   strategy.FallbackUrl,
			Title: tool.Name + " installation instructions",
		})
	}

	return &errorhandler.ErrorWithSuggestion{
		Err: fmt.Errorf(
			"package manager %q not available on this platform",
			strategy.PackageManager,
		),
		Message:    "Cannot install " + tool.Name,
		Suggestion: suggestion,
		Links:      links,
	}
}

// -----------------------------------------------------------------------
// Package-manager command builders
// -----------------------------------------------------------------------

// buildManagerCommand returns the command and arguments for a
// well-known package manager install or upgrade operation.
func buildManagerCommand(
	manager string,
	packageID string,
	upgrade bool,
) (string, []string) {
	switch manager {
	case "winget":
		return buildWingetCommand(packageID, upgrade)
	case "brew":
		return buildBrewCommand(packageID, upgrade)
	case "apt":
		return buildAptCommand(packageID, upgrade)
	case "npm":
		return buildNpmCommand(packageID, upgrade)
	case "code":
		return buildCodeCommand(packageID, upgrade)
	default:
		return "", nil
	}
}

func buildWingetCommand(
	packageID string, upgrade bool,
) (string, []string) {
	action := "install"
	if upgrade {
		action = "upgrade"
	}
	return "winget", []string{
		action,
		"--id", packageID,
		"--accept-source-agreements",
		"--accept-package-agreements",
		"-e",
	}
}

func buildBrewCommand(
	packageID string, upgrade bool,
) (string, []string) {
	action := "install"
	if upgrade {
		action = "upgrade"
	}
	return "brew", []string{action, packageID}
}

func buildAptCommand(
	packageID string, upgrade bool,
) (string, []string) {
	if upgrade {
		return "sudo", []string{
			"apt-get", "install",
			"--only-upgrade", "-y", packageID,
		}
	}
	return "sudo", []string{
		"apt-get", "install", "-y", packageID,
	}
}

func buildNpmCommand(
	packageID string, upgrade bool,
) (string, []string) {
	if upgrade {
		return "npm", []string{"update", "-g", packageID}
	}
	return "npm", []string{"install", "-g", packageID}
}

func buildCodeCommand(
	packageID string, upgrade bool,
) (string, []string) {
	args := []string{"--install-extension", packageID}
	if upgrade {
		args = append(args, "--force")
	}
	return "code", args
}

// -----------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------

// splitCommand splits a whitespace-delimited command string into the
// executable name and its arguments.
func splitCommand(command string) (string, []string) {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return "", nil
	}
	return parts[0], parts[1:]
}

// containsShellOperators reports whether the command string contains
// shell metacharacters (pipes, redirects, background operators, or
// command chaining) that require execution through a system shell.
func containsShellOperators(cmd string) bool {
	return strings.ContainsAny(cmd, "|><&;")
}

// executeShellCommand runs a command string through the system shell
// so that shell operators such as pipes and redirects are
// interpreted correctly.
func (i *installer) executeShellCommand(
	ctx context.Context,
	command string,
) error {
	var shell string
	var args []string

	if runtime.GOOS == "windows" {
		shell = "cmd"
		args = []string{"/C", command}
	} else {
		shell = "sh"
		args = []string{"-c", command}
	}

	runArgs := exec.NewRunArgs(shell, args...)
	_, err := i.commandRunner.Run(ctx, runArgs)
	return err
}
