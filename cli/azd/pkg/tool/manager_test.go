// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tool

import (
	"context"
	"errors"
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
	) (*InstallResult, error)
	upgradeFn func(
		ctx context.Context,
		tool *ToolDefinition,
	) (*InstallResult, error)
}

func (m *mockInstaller) Install(
	ctx context.Context,
	tool *ToolDefinition,
) (*InstallResult, error) {
	if m.installFn != nil {
		return m.installFn(ctx, tool)
	}
	return &InstallResult{
		Tool:    tool,
		Success: true,
	}, nil
}

func (m *mockInstaller) Upgrade(
	ctx context.Context,
	tool *ToolDefinition,
) (*InstallResult, error) {
	if m.upgradeFn != nil {
		return m.upgradeFn(ctx, tool)
	}
	return &InstallResult{
		Tool:    tool,
		Success: true,
	}, nil
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

	t.Run("ResolvesUninstalledDependencies", func(t *testing.T) {
		t.Parallel()

		var installedIDs []string
		inst := &mockInstaller{
			installFn: func(
				_ context.Context,
				tool *ToolDefinition,
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
				// az-cli is NOT installed yet, triggering
				// dependency resolution.
				if tool.Id == "az-cli" {
					return &ToolStatus{
						Tool:      tool,
						Installed: false,
					}, nil
				}
				return &ToolStatus{
					Tool:      tool,
					Installed: true,
				}, nil
			},
		}

		mgr := NewManager(det, inst, nil)

		// azd-ai-extensions depends on az-cli.
		results, err := mgr.InstallTools(
			t.Context(),
			[]string{"azd-ai-extensions"},
		)

		require.NoError(t, err)
		require.Len(t, results, 2,
			"should install dep + requested tool")

		// az-cli should be first (dependency).
		assert.Equal(t, "az-cli", installedIDs[0])
		assert.Equal(t, "azd-ai-extensions", installedIDs[1])
	})

	t.Run("SkipsDependencyAlreadyInstalled", func(t *testing.T) {
		t.Parallel()

		var installedIDs []string
		inst := &mockInstaller{
			installFn: func(
				_ context.Context,
				tool *ToolDefinition,
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
				// az-cli IS already installed.
				return &ToolStatus{
					Tool:      tool,
					Installed: true,
				}, nil
			},
		}

		mgr := NewManager(det, inst, nil)
		results, err := mgr.InstallTools(
			t.Context(),
			[]string{"azd-ai-extensions"},
		)

		require.NoError(t, err)
		// Only 1 result: the requested tool (dep skipped).
		require.Len(t, results, 1)
		assert.Equal(t, "azd-ai-extensions", results[0].Tool.Id)
	})

	t.Run("FailedDependencySkipsDependent", func(t *testing.T) {
		t.Parallel()

		inst := &mockInstaller{
			installFn: func(
				_ context.Context,
				tool *ToolDefinition,
			) (*InstallResult, error) {
				if tool.Id == "az-cli" {
					return &InstallResult{
						Tool:    tool,
						Success: false,
						Error:   errors.New("install failed"),
					}, nil
				}
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
				// az-cli not installed => triggers dep install.
				if tool.Id == "az-cli" {
					return &ToolStatus{
						Tool:      tool,
						Installed: false,
					}, nil
				}
				return &ToolStatus{
					Tool:      tool,
					Installed: true,
				}, nil
			},
		}

		mgr := NewManager(det, inst, nil)
		results, err := mgr.InstallTools(
			t.Context(),
			[]string{"azd-ai-extensions"},
		)

		require.NoError(t, err)
		require.Len(t, results, 2)

		// Both should have errors: dep failed, dependent skipped.
		assert.Error(t, results[0].Error)
		assert.Error(t, results[1].Error)
		assert.Contains(t, results[1].Error.Error(),
			"dependency failed")
	})
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

	uc := NewUpdateChecker(mgr2, det, tmpDir)
	m := NewManager(det, &mockInstaller{}, uc)

	results, err := m.CheckForUpdates(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, results)
}

func TestManager_ShouldCheckForUpdates(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	mgr2 := newMockUserConfigManager()
	uc := NewUpdateChecker(mgr2, &mockDetector{}, tmpDir)

	m := NewManager(&mockDetector{}, &mockInstaller{}, uc)

	// First time — no lastUpdateCheck set — should return true.
	assert.True(t, m.ShouldCheckForUpdates(t.Context()))
}

func TestManager_HasUpdatesAvailable(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	mgr2 := newMockUserConfigManager()
	det := &mockDetector{}
	uc := NewUpdateChecker(mgr2, det, tmpDir)

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
	uc := NewUpdateChecker(mgr2, &mockDetector{}, tmpDir)

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
