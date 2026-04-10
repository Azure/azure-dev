// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tool

import (
	"context"
	"runtime"
	"slices"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
)

// platformManagers maps each operating system to the package managers that
// should be probed during detection.
var platformManagers = map[string][]string{
	"windows": {"winget", "npm", "code"},
	"darwin":  {"brew", "npm", "code"},
	"linux":   {"apt", "snap", "npm", "code"},
}

// Platform describes the detected operating system and the package managers
// that were found on the local machine.
type Platform struct {
	// OS is the operating system identifier returned by runtime.GOOS
	// (e.g. "windows", "darwin", "linux").
	OS string
	// AvailableManagers lists the package managers detected on the system
	// (e.g. ["winget", "npm"]).
	AvailableManagers []string
}

// HasManager reports whether the named package manager was detected on this
// platform.
func (p *Platform) HasManager(name string) bool {
	return slices.Contains(p.AvailableManagers, name)
}

// PlatformDetector discovers the current operating system and probes for
// available package managers using a [exec.CommandRunner].
type PlatformDetector struct {
	commandRunner exec.CommandRunner
}

// NewPlatformDetector creates a PlatformDetector that uses the provided
// CommandRunner to probe for package manager availability.
func NewPlatformDetector(commandRunner exec.CommandRunner) *PlatformDetector {
	return &PlatformDetector{
		commandRunner: commandRunner,
	}
}

// Detect identifies the current platform by reading runtime.GOOS and checking
// each known package manager for availability. Managers that are not found are
// silently skipped; the method only returns an error for unexpected failures.
func (pd *PlatformDetector) Detect(ctx context.Context) (*Platform, error) {
	osName := runtime.GOOS

	candidates := platformManagers[osName]
	available := make([]string, 0, len(candidates))

	for _, mgr := range candidates {
		if pd.IsManagerAvailable(ctx, mgr) {
			available = append(available, mgr)
		}
	}

	return &Platform{
		OS:                osName,
		AvailableManagers: available,
	}, nil
}

// IsManagerAvailable reports whether the named package manager can be found on
// the system PATH and responds to a --version invocation. A manager that cannot
// be located or executed is considered unavailable.
func (pd *PlatformDetector) IsManagerAvailable(
	ctx context.Context,
	manager string,
) bool {
	// Fast path: if the tool is not on PATH there is nothing more to check.
	if err := pd.commandRunner.ToolInPath(manager); err != nil {
		return false
	}

	// Validate the tool can actually be invoked.
	_, err := pd.commandRunner.Run(ctx, exec.RunArgs{
		Cmd:  manager,
		Args: []string{"--version"},
	})

	return err == nil
}

// SelectStrategy returns the best install strategy for the given tool on the
// detected platform. It returns nil when no strategy is defined for the
// platform's OS. When the strategy names a PackageManager that is not present
// in platform.AvailableManagers the strategy is still returned — the caller
// (installer) is responsible for handling the fallback.
func (pd *PlatformDetector) SelectStrategy(
	tool *ToolDefinition,
	platform *Platform,
) *InstallStrategy {
	if tool == nil || tool.InstallStrategies == nil {
		return nil
	}

	strategy, exists := tool.InstallStrategies[platform.OS]
	if !exists {
		return nil
	}

	return &strategy
}
