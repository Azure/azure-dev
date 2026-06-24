// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tool

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// mockInstaller — in-package mock for the Installer interface
// ---------------------------------------------------------------------------

type mockInstaller struct {
	installFn func(
		ctx context.Context,
		tool *ToolDefinition,
		opts ...InstallOption,
	) (*InstallResult, error)
	upgradeFn func(
		ctx context.Context,
		tool *ToolDefinition,
		opts ...InstallOption,
	) (*InstallResult, error)
	availableSkillHostsFn func(tool *ToolDefinition) []string
}

func (m *mockInstaller) Install(
	ctx context.Context,
	tool *ToolDefinition,
	opts ...InstallOption,
) (*InstallResult, error) {
	if m.installFn != nil {
		return m.installFn(ctx, tool, opts...)
	}
	return &InstallResult{
		Tool:    tool,
		Success: true,
	}, nil
}

func (m *mockInstaller) Upgrade(
	ctx context.Context,
	tool *ToolDefinition,
	opts ...InstallOption,
) (*InstallResult, error) {
	if m.upgradeFn != nil {
		return m.upgradeFn(ctx, tool, opts...)
	}
	return &InstallResult{
		Tool:    tool,
		Success: true,
	}, nil
}

func (m *mockInstaller) AvailableSkillHosts(tool *ToolDefinition) []string {
	if m.availableSkillHostsFn != nil {
		return m.availableSkillHostsFn(tool)
	}
	return nil
}

// ---------------------------------------------------------------------------
// GetAllTools
// ---------------------------------------------------------------------------

func TestManager_GetAllTools(t *testing.T) {
	t.Parallel()

	mgr := NewManager(&mockDetector{}, &mockInstaller{}, nil)
	tools := mgr.GetAllTools()

	require.Len(t, tools, len(BuiltInTools()))

	// Mutating returned slice should not affect manager.
	tools[0] = &ToolDefinition{Id: "mutated"}
	second := mgr.GetAllTools()
	assert.NotEqual(t, "mutated", second[0].Id)
}

// ---------------------------------------------------------------------------
// GetToolsByCategory
// ---------------------------------------------------------------------------

func TestManager_GetToolsByCategory(t *testing.T) {
	t.Parallel()

	mgr := NewManager(&mockDetector{}, &mockInstaller{}, nil)
	cliTools := mgr.GetToolsByCategory(ToolCategoryCLI)

	require.NotEmpty(t, cliTools)
	for _, tool := range cliTools {
		assert.Equal(t, ToolCategoryCLI, tool.Category)
	}
}

// ---------------------------------------------------------------------------
// FindTool
// ---------------------------------------------------------------------------

func TestManager_FindTool(t *testing.T) {
	t.Parallel()

	t.Run("Found", func(t *testing.T) {
		t.Parallel()

		mgr := NewManager(
			&mockDetector{}, &mockInstaller{}, nil,
		)

		tool, err := mgr.FindTool("az-cli")
		require.NoError(t, err)
		require.NotNil(t, tool)
		assert.Equal(t, "az-cli", tool.Id)
	})

	t.Run("NotFound", func(t *testing.T) {
		t.Parallel()

		mgr := NewManager(
			&mockDetector{}, &mockInstaller{}, nil,
		)

		tool, err := mgr.FindTool("nonexistent")
		require.Error(t, err)
		assert.Nil(t, tool)
		assert.Contains(t, err.Error(), "not found")
	})
}

// ---------------------------------------------------------------------------
// DetectAll / DetectTool
// ---------------------------------------------------------------------------

func TestManager_DetectAll(t *testing.T) {
	t.Parallel()

	det := &mockDetector{
		detectAllFn: func(
			_ context.Context,
			tools []*ToolDefinition,
		) ([]*ToolStatus, error) {
			results := make([]*ToolStatus, len(tools))
			for i, tool := range tools {
				results[i] = &ToolStatus{
					Tool:      tool,
					Installed: true,
				}
			}
			return results, nil
		},
	}

	mgr := NewManager(det, &mockInstaller{}, nil)
	results, err := mgr.DetectAll(t.Context())

	require.NoError(t, err)
	assert.Len(t, results, len(BuiltInTools()))
}

func TestManager_DetectTool(t *testing.T) {
	t.Parallel()

	t.Run("KnownToolDelegatesToDetector", func(t *testing.T) {
		t.Parallel()

		det := &mockDetector{
			detectToolFn: func(
				_ context.Context,
				tool *ToolDefinition,
			) (*ToolStatus, error) {
				return &ToolStatus{
					Tool:             tool,
					Installed:        true,
					InstalledVersion: "2.64.0",
				}, nil
			},
		}

		mgr := NewManager(det, &mockInstaller{}, nil)
		status, err := mgr.DetectTool(t.Context(), "az-cli")

		require.NoError(t, err)
		require.NotNil(t, status)
		assert.True(t, status.Installed)
		assert.Equal(t, "2.64.0", status.InstalledVersion)
	})

	t.Run("UnknownToolReturnsError", func(t *testing.T) {
		t.Parallel()

		mgr := NewManager(
			&mockDetector{}, &mockInstaller{}, nil,
		)

		status, err := mgr.DetectTool(t.Context(), "nonexistent")
		require.Error(t, err)
		assert.Nil(t, status)
	})
}

// ---------------------------------------------------------------------------
// InstallTools
// ---------------------------------------------------------------------------

func TestManager_InstallTools(t *testing.T) {
	t.Parallel()

	t.Run("ValidIDsInstalled", func(t *testing.T) {
		t.Parallel()

		var installedIDs []string
		inst := &mockInstaller{
			installFn: func(
				_ context.Context,
				tool *ToolDefinition,
				_ ...InstallOption,
			) (*InstallResult, error) {
				installedIDs = append(installedIDs, tool.Id)
				return &InstallResult{
					Tool:    tool,
					Success: true,
				}, nil
			},
		}

		det := &mockDetector{
			detectToolFn: func(
				_ context.Context,
				tool *ToolDefinition,
			) (*ToolStatus, error) {
				// All dependencies are pre-installed.
				return &ToolStatus{
					Tool:      tool,
					Installed: true,
				}, nil
			},
		}

		mgr := NewManager(det, inst, nil)
		results, err := mgr.InstallTools(
			t.Context(),
			[]string{"az-cli"},
		)

		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.True(t, results[0].Success)
		assert.Contains(t, installedIDs, "az-cli")
	})

	t.Run("UnknownIDReturnsError", func(t *testing.T) {
		t.Parallel()

		mgr := NewManager(
			&mockDetector{}, &mockInstaller{}, nil,
		)

		results, err := mgr.InstallTools(
			t.Context(),
			[]string{"nonexistent"},
		)

		require.Error(t, err)
		assert.Nil(t, results)
	})
}

func TestManager_InstallToolsDependencyResolution(t *testing.T) {
	t.Parallel()

	// Two synthetic tools: "dependent" requires "base".
	baseTool := func() *ToolDefinition {
		return &ToolDefinition{Id: "base"}
	}
	dependentTool := func() *ToolDefinition {
		return &ToolDefinition{
			Id:           "dependent",
			Dependencies: []string{"base"},
		}
	}

	// newMgr builds a Manager whose manifest contains only the
	// synthetic tools so the test is isolated from BuiltInTools().
	newMgr := func(det Detector, inst Installer) *Manager {
		return &Manager{
			manifest:  []*ToolDefinition{baseTool(), dependentTool()},
			detector:  det,
			installer: inst,
		}
	}

	t.Run("ResolvesUninstalledDependencies", func(t *testing.T) {
		t.Parallel()

		var installedIDs []string
		inst := &mockInstaller{
			installFn: func(
				_ context.Context,
				tool *ToolDefinition,
				_ ...InstallOption,
			) (*InstallResult, error) {
				installedIDs = append(installedIDs, tool.Id)
				return &InstallResult{Tool: tool, Success: true}, nil
			},
		}

		// "base" is not installed, triggering dependency resolution.
		det := &mockDetector{
			detectToolFn: func(
				_ context.Context,
				tool *ToolDefinition,
			) (*ToolStatus, error) {
				return &ToolStatus{
					Tool:      tool,
					Installed: tool.Id != "base",
				}, nil
			},
		}

		mgr := newMgr(det, inst)
		results, err := mgr.InstallTools(t.Context(), []string{"dependent"})

		require.NoError(t, err)
		require.Len(t, results, 2, "should install dep + requested tool")
		assert.Equal(t, []string{"base", "dependent"}, installedIDs,
			"dependency must be installed first")
	})

	t.Run("SkipsDependencyAlreadyInstalled", func(t *testing.T) {
		t.Parallel()

		var installedIDs []string
		inst := &mockInstaller{
			installFn: func(
				_ context.Context,
				tool *ToolDefinition,
				_ ...InstallOption,
			) (*InstallResult, error) {
				installedIDs = append(installedIDs, tool.Id)
				return &InstallResult{Tool: tool, Success: true}, nil
			},
		}

		// Everything already installed; dependency must be skipped.
		det := &mockDetector{
			detectToolFn: func(
				_ context.Context,
				tool *ToolDefinition,
			) (*ToolStatus, error) {
				return &ToolStatus{Tool: tool, Installed: true}, nil
			},
		}

		mgr := newMgr(det, inst)
		results, err := mgr.InstallTools(t.Context(), []string{"dependent"})

		require.NoError(t, err)
		require.Len(t, results, 1, "dep should be skipped")
		assert.Equal(t, "dependent", results[0].Tool.Id)
	})

	t.Run("FailedDependencySkipsDependent", func(t *testing.T) {
		t.Parallel()

		inst := &mockInstaller{
			installFn: func(
				_ context.Context,
				tool *ToolDefinition,
				_ ...InstallOption,
			) (*InstallResult, error) {
				if tool.Id == "base" {
					return &InstallResult{
						Tool:    tool,
						Success: false,
						Error:   errors.New("install failed"),
					}, nil
				}
				return &InstallResult{Tool: tool, Success: true}, nil
			},
		}

		det := &mockDetector{
			detectToolFn: func(
				_ context.Context,
				tool *ToolDefinition,
			) (*ToolStatus, error) {
				return &ToolStatus{
					Tool:      tool,
					Installed: tool.Id != "base",
				}, nil
			},
		}

		mgr := newMgr(det, inst)
		results, err := mgr.InstallTools(t.Context(), []string{"dependent"})

		require.NoError(t, err)
		require.Len(t, results, 2)
		// Dep result records the install failure; dependent is skipped.
		assert.Error(t, results[0].Error)
		assert.Error(t, results[1].Error)
		assert.Contains(t, results[1].Error.Error(), "dependency")
	})
}

// TestManifest_SkillsListedAfterHostCLIs verifies the ordering invariant
// the install flow relies on: every skill tool appears AFTER any agent
// host CLI it could install through (e.g. github-copilot-cli) in the
// built-in manifest. Batch installs (--all, interactive picker) derive
// their order from the manifest, so this ordering is what guarantees the
// host CLI is installed before the skill — no runtime re-sorting needed.
func TestManifest_SkillsListedAfterHostCLIs(t *testing.T) {
	t.Parallel()

	tools := BuiltInTools()
	indexOf := func(id string) int {
		return slices.IndexFunc(tools, func(td *ToolDefinition) bool {
			return td.Id == id
		})
	}

	// Maps a host binary name to the manifest tool id that provides it.
	hostToolID := map[string]string{
		"copilot": "github-copilot-cli",
	}

	for _, td := range tools {
		if td.Category != ToolCategorySkill {
			continue
		}
		skillIdx := indexOf(td.Id)
		for _, host := range td.SkillHosts {
			cliID, ok := hostToolID[host.Host]
			if !ok {
				continue // host has no installable CLI in the manifest
			}
			cliIdx := indexOf(cliID)
			if cliIdx < 0 {
				continue
			}
			assert.Greater(t, skillIdx, cliIdx,
				"skill %q must be listed after its host CLI %q in the "+
					"manifest so batch installs install the host CLI first",
				td.Id, cliID)
		}
	}
}

// ---------------------------------------------------------------------------
// UpgradeTools
// ---------------------------------------------------------------------------

func TestManager_UpgradeTools(t *testing.T) {
	t.Parallel()

	t.Run("DelegatesToInstaller", func(t *testing.T) {
		t.Parallel()

		var upgradedIDs []string
		inst := &mockInstaller{
			upgradeFn: func(
				_ context.Context,
				tool *ToolDefinition,
				_ ...InstallOption,
			) (*InstallResult, error) {
				upgradedIDs = append(upgradedIDs, tool.Id)
				return &InstallResult{
					Tool:    tool,
					Success: true,
				}, nil
			},
		}

		mgr := NewManager(
			&mockDetector{}, inst, nil,
		)

		results, err := mgr.UpgradeTools(
			t.Context(),
			[]string{"az-cli", "github-copilot-cli"},
		)

		require.NoError(t, err)
		require.Len(t, results, 2)
		assert.Contains(t, upgradedIDs, "az-cli")
		assert.Contains(t, upgradedIDs, "github-copilot-cli")
	})

	t.Run("UnknownIDReturnsError", func(t *testing.T) {
		t.Parallel()

		mgr := NewManager(
			&mockDetector{}, &mockInstaller{}, nil,
		)

		results, err := mgr.UpgradeTools(
			t.Context(),
			[]string{"nonexistent"},
		)

		require.Error(t, err)
		assert.Nil(t, results)
	})
}

// ---------------------------------------------------------------------------
// Manager — UpdateChecker delegation methods
// ---------------------------------------------------------------------------

func TestManager_CheckForUpdates(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	mgr2 := newMockUserConfigManager()

	det := &mockDetector{
		detectAllFn: func(
			_ context.Context,
			tools []*ToolDefinition,
		) ([]*ToolStatus, error) {
			results := make([]*ToolStatus, len(tools))
			for i, tool := range tools {
				results[i] = &ToolStatus{
					Tool:             tool,
					Installed:        true,
					InstalledVersion: "1.0.0",
				}
			}
			return results, nil
		},
	}

	uc := NewUpdateChecker(mgr2, det, staticDir(tmpDir), nil)
	m := NewManager(det, &mockInstaller{}, uc)

	results, err := m.CheckForUpdates(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, results)
}

func TestManager_ShouldCheckForUpdates(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	mgr2 := newMockUserConfigManager()
	uc := NewUpdateChecker(mgr2, &mockDetector{}, staticDir(tmpDir), nil)

	m := NewManager(&mockDetector{}, &mockInstaller{}, uc)

	// First time — no lastUpdateCheck set — should return true.
	assert.True(t, m.ShouldCheckForUpdates(t.Context()))
}

func TestManager_HasUpdatesAvailable(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	mgr2 := newMockUserConfigManager()
	det := &mockDetector{}
	uc := NewUpdateChecker(mgr2, det, staticDir(tmpDir), nil)

	m := NewManager(det, &mockInstaller{}, uc)

	hasUpdates, count, err := m.HasUpdatesAvailable(t.Context())
	require.NoError(t, err)
	assert.False(t, hasUpdates)
	assert.Equal(t, 0, count)
}

func TestManager_MarkUpdateNotificationShown(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	mgr2 := newMockUserConfigManager()
	uc := NewUpdateChecker(mgr2, &mockDetector{}, staticDir(tmpDir), nil)

	m := NewManager(&mockDetector{}, &mockInstaller{}, uc)
	err := m.MarkUpdateNotificationShown(t.Context())
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// UpgradeAll
// ---------------------------------------------------------------------------

func TestManager_UpgradeAll(t *testing.T) {
	t.Parallel()

	t.Run("OnlyUpgradesInstalledTools", func(t *testing.T) {
		t.Parallel()

		var upgradedIDs []string
		inst := &mockInstaller{
			upgradeFn: func(
				_ context.Context,
				tool *ToolDefinition,
				_ ...InstallOption,
			) (*InstallResult, error) {
				upgradedIDs = append(upgradedIDs, tool.Id)
				return &InstallResult{
					Tool:    tool,
					Success: true,
				}, nil
			},
		}

		det := &mockDetector{
			detectAllFn: func(
				_ context.Context,
				tools []*ToolDefinition,
			) ([]*ToolStatus, error) {
				results := make([]*ToolStatus, len(tools))
				for i, tool := range tools {
					// Only first tool is installed.
					results[i] = &ToolStatus{
						Tool:      tool,
						Installed: i == 0,
					}
				}
				return results, nil
			},
		}

		mgr := NewManager(det, inst, nil)
		results, err := mgr.UpgradeAll(t.Context())

		require.NoError(t, err)
		require.Len(t, results, 1, "only installed tools upgraded")
	})
}

// ---------------------------------------------------------------------------
// AvailableSkillHosts
// ---------------------------------------------------------------------------

func TestManager_AvailableSkillHosts(t *testing.T) {
	installer := &mockInstaller{
		availableSkillHostsFn: func(_ *ToolDefinition) []string {
			return []string{"copilot", "claude"}
		},
	}
	m := NewManager(&mockDetector{}, installer, nil)

	got := m.AvailableSkillHosts(&ToolDefinition{
		Id:       "azure-skills",
		Category: ToolCategorySkill,
	})
	assert.Equal(t, []string{"copilot", "claude"}, got)
}
