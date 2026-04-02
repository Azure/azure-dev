// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tool

import (
	"context"
	"fmt"
	"slices"
)

// Manager is the top-level orchestrator for tool management. It wires
// together the built-in tool manifest, a [Detector] for probing the
// local machine, an [Installer] for installing and upgrading tools,
// and an [UpdateChecker] for periodic update notifications.
type Manager struct {
	manifest      []*ToolDefinition
	detector      Detector
	installer     Installer
	updateChecker *UpdateChecker
}

// NewManager creates a [Manager] that operates on the built-in tool
// registry. The detector, installer, and updateChecker are injected
// so that callers can supply test doubles when needed.
func NewManager(
	detector Detector,
	installer Installer,
	updateChecker *UpdateChecker,
) *Manager {
	return &Manager{
		manifest:      BuiltInTools(),
		detector:      detector,
		installer:     installer,
		updateChecker: updateChecker,
	}
}

// GetAllTools returns a shallow clone of the full tool manifest.
// Callers may safely modify the returned slice without affecting the
// manager's internal state.
func (m *Manager) GetAllTools() []*ToolDefinition {
	return slices.Clone(m.manifest)
}

// GetToolsByCategory returns every tool in the manifest whose
// [ToolDefinition.Category] matches the given category.
func (m *Manager) GetToolsByCategory(
	category ToolCategory,
) []*ToolDefinition {
	var result []*ToolDefinition
	for _, t := range m.manifest {
		if t.Category == category {
			result = append(result, t)
		}
	}
	return result
}

// FindTool looks up a tool by its unique identifier in the manifest.
// It returns an error when no tool with that id exists.
func (m *Manager) FindTool(id string) (*ToolDefinition, error) {
	for _, t := range m.manifest {
		if t.Id == id {
			return t, nil
		}
	}
	return nil, fmt.Errorf("finding tool %q: not found", id)
}

// DetectAll probes every tool in the manifest and returns a status
// entry for each one. Individual detection failures are captured in
// each [ToolStatus.Error]; the returned error is non-nil only for
// programming mistakes such as a nil manifest entry.
func (m *Manager) DetectAll(
	ctx context.Context,
) ([]*ToolStatus, error) {
	return m.detector.DetectAll(ctx, m.manifest)
}

// DetectTool probes a single tool identified by its unique id and
// returns its [ToolStatus]. It returns an error when the id is not
// found in the manifest.
func (m *Manager) DetectTool(
	ctx context.Context,
	id string,
) (*ToolStatus, error) {
	tool, err := m.FindTool(id)
	if err != nil {
		return nil, err
	}
	return m.detector.DetectTool(ctx, tool)
}

// InstallTools installs the requested tools by id, automatically
// resolving and prepending any missing dependencies. Tools are
// installed in dependency order: if a dependency installation fails
// the dependent tool is skipped and an error is recorded in its
// [InstallResult].
func (m *Manager) InstallTools(
	ctx context.Context,
	ids []string,
) ([]*InstallResult, error) {
	// 1. Resolve every requested id to its definition.
	requested, err := m.resolveTools(ids)
	if err != nil {
		return nil, err
	}

	// 2. Build an ordered install list: dependencies first, then
	//    the requested tools. The dependency graph in this POC is
	//    at most one level deep, so a single linear pass suffices.
	ordered, err := m.buildInstallOrder(ctx, requested)
	if err != nil {
		return nil, err
	}

	// 3. Install each tool in order, tracking failures so that
	//    dependents can be skipped.
	failed := map[string]bool{}
	var results []*InstallResult

	for _, tool := range ordered {
		if m.hasMissingDependency(tool, failed) {
			results = append(results, &InstallResult{
				Tool: tool,
				Error: fmt.Errorf(
					"skipped: a required dependency failed to install",
				),
			})
			failed[tool.Id] = true
			continue
		}

		result, installErr := m.installer.Install(ctx, tool)
		if installErr != nil {
			results = append(results, &InstallResult{
				Tool:  tool,
				Error: installErr,
			})
			failed[tool.Id] = true
			continue
		}

		if !result.Success {
			failed[tool.Id] = true
		}
		results = append(results, result)
	}

	return results, nil
}

// UpgradeTools upgrades the tools identified by the given ids. Each
// id is resolved against the manifest and then passed to the
// installer's Upgrade method.
func (m *Manager) UpgradeTools(
	ctx context.Context,
	ids []string,
) ([]*InstallResult, error) {
	tools, err := m.resolveTools(ids)
	if err != nil {
		return nil, err
	}

	var results []*InstallResult
	for _, tool := range tools {
		result, upgradeErr := m.installer.Upgrade(ctx, tool)
		if upgradeErr != nil {
			results = append(results, &InstallResult{
				Tool:  tool,
				Error: upgradeErr,
			})
			continue
		}
		results = append(results, result)
	}
	return results, nil
}

// UpgradeAll detects every tool in the manifest, filters to those
// that are already installed, and upgrades each one.
func (m *Manager) UpgradeAll(
	ctx context.Context,
) ([]*InstallResult, error) {
	statuses, err := m.detector.DetectAll(ctx, m.manifest)
	if err != nil {
		return nil, fmt.Errorf("detecting installed tools: %w", err)
	}

	var results []*InstallResult
	for _, status := range statuses {
		if !status.Installed {
			continue
		}

		result, upgradeErr := m.installer.Upgrade(ctx, status.Tool)
		if upgradeErr != nil {
			results = append(results, &InstallResult{
				Tool:  status.Tool,
				Error: upgradeErr,
			})
			continue
		}
		results = append(results, result)
	}
	return results, nil
}

// CheckForUpdates delegates to the [UpdateChecker] to check all
// tools in the manifest for available updates.
func (m *Manager) CheckForUpdates(
	ctx context.Context,
) ([]*UpdateCheckResult, error) {
	return m.updateChecker.Check(ctx, m.manifest)
}

// ShouldCheckForUpdates reports whether enough time has elapsed since
// the last update check to warrant a new one.
func (m *Manager) ShouldCheckForUpdates(
	ctx context.Context,
) bool {
	return m.updateChecker.ShouldCheck(ctx)
}

// HasUpdatesAvailable reports whether cached update-check results
// indicate that one or more tools have updates available. It returns
// a boolean flag, the count of tools with updates, and any error
// encountered while reading the cache.
func (m *Manager) HasUpdatesAvailable(
	ctx context.Context,
) (bool, int, error) {
	return m.updateChecker.HasUpdatesAvailable(ctx)
}

// MarkUpdateNotificationShown records that the user has been notified
// about available updates so that the notification is not repeated
// too soon.
func (m *Manager) MarkUpdateNotificationShown(
	ctx context.Context,
) error {
	return m.updateChecker.MarkNotificationShown(ctx)
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// resolveTools converts a list of tool ids into their corresponding
// [ToolDefinition] pointers. It returns an error if any id is not
// present in the manifest.
func (m *Manager) resolveTools(
	ids []string,
) ([]*ToolDefinition, error) {
	tools := make([]*ToolDefinition, 0, len(ids))
	for _, id := range ids {
		tool, err := m.FindTool(id)
		if err != nil {
			return nil, err
		}
		tools = append(tools, tool)
	}
	return tools, nil
}

// buildInstallOrder produces a flat, dependency-first list of tools
// to install. For each requested tool, any uninstalled dependencies
// that are not already in the list are prepended.
func (m *Manager) buildInstallOrder(
	ctx context.Context,
	requested []*ToolDefinition,
) ([]*ToolDefinition, error) {
	var ordered []*ToolDefinition
	seen := map[string]bool{}

	for _, tool := range requested {
		for _, depID := range tool.Dependencies {
			if seen[depID] {
				continue
			}

			dep, err := m.FindTool(depID)
			if err != nil {
				return nil, fmt.Errorf(
					"resolving dependency %q for tool %q: %w",
					depID, tool.Id, err,
				)
			}

			// Only add the dependency if it is not already
			// installed on the machine.
			status, detectErr := m.detector.DetectTool(ctx, dep)
			if detectErr != nil {
				return nil, fmt.Errorf(
					"detecting dependency %q: %w",
					depID, detectErr,
				)
			}

			if !status.Installed {
				ordered = append(ordered, dep)
			}
			seen[depID] = true
		}

		if !seen[tool.Id] {
			ordered = append(ordered, tool)
			seen[tool.Id] = true
		}
	}

	return ordered, nil
}

// hasMissingDependency reports whether any of the tool's declared
// dependencies are recorded in the failed set.
func (m *Manager) hasMissingDependency(
	tool *ToolDefinition,
	failed map[string]bool,
) bool {
	for _, depID := range tool.Dependencies {
		if failed[depID] {
			return true
		}
	}
	return false
}
