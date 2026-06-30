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
	"linux":   {"apt", "snap", "brew", "npm", "code"},
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

// SelectStrategies returns the ordered list of install strategies for the
// given tool on the detected platform, or nil when none are defined.
func (pd *PlatformDetector) SelectStrategies(
	tool *ToolDefinition,
	platform *Platform,
) []InstallStrategy {
	if tool == nil || tool.InstallStrategies == nil || platform == nil {
		return nil
	}
	return tool.InstallStrategies[platform.OS]
}

// SelectStrategy returns the preferred install strategy for the given tool on
// the detected platform: the first strategy in the platform's list that is
// usable here. A strategy with no PackageManager (an explicit InstallCommand
// or direct download) is always usable; a package-manager strategy requires
// that manager to be available. When no strategy is usable, the first is
// returned so the caller can surface a "manager unavailable" error. Returns
// nil when no strategy is defined for the platform's OS.
func (pd *PlatformDetector) SelectStrategy(
	tool *ToolDefinition,
	platform *Platform,
) *InstallStrategy {
	strategies := pd.SelectStrategies(tool, platform)
	if len(strategies) == 0 {
		return nil
	}

	for i := range strategies {
		s := &strategies[i]
		if s.PackageManager == "" || platform.HasManager(s.PackageManager) {
			return s
		}
	}

	return &strategies[0]
}
