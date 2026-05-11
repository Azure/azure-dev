// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tool

import (
	"errors"
	osexec "os/exec"
	"runtime"
	"slices"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockexec"
	"github.com/stretchr/testify/require"
)

func TestNewPlatformDetector(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	pd := NewPlatformDetector(runner)

	require.NotNil(t, pd)
	require.Equal(t, runner, pd.commandRunner)
}

func TestDetect(t *testing.T) {
	t.Parallel()

	t.Run("ReturnsCurrentOS", func(t *testing.T) {
		t.Parallel()

		runner := mockexec.NewMockCommandRunner()
		// Mock all known managers as unavailable so we don't depend on
		// the host environment.
		for _, managers := range platformManagers {
			for _, mgr := range managers {
				runner.MockToolInPath(mgr, osexec.ErrNotFound)
			}
		}

		pd := NewPlatformDetector(runner)
		platform, err := pd.Detect(t.Context())

		require.NoError(t, err)
		require.Equal(t, runtime.GOOS, platform.OS)
	})

	t.Run("DetectsAvailableManagers", func(t *testing.T) {
		t.Parallel()

		runner := mockexec.NewMockCommandRunner()

		// Make "npm" available (PATH + --version succeeds).
		runner.MockToolInPath("npm", nil)
		runner.When(func(args exec.RunArgs, command string) bool {
			return args.Cmd == "npm" &&
				slices.Contains(args.Args, "--version")
		}).Respond(exec.RunResult{
			ExitCode: 0,
			Stdout:   "10.2.0",
		})

		// Make "code" available.
		runner.MockToolInPath("code", nil)
		runner.When(func(args exec.RunArgs, command string) bool {
			return args.Cmd == "code" &&
				slices.Contains(args.Args, "--version")
		}).Respond(exec.RunResult{
			ExitCode: 0,
			Stdout:   "1.85.0",
		})

		// All other managers are missing.
		for _, managers := range platformManagers {
			for _, mgr := range managers {
				if mgr == "npm" || mgr == "code" {
					continue
				}
				runner.MockToolInPath(mgr, osexec.ErrNotFound)
			}
		}

		pd := NewPlatformDetector(runner)
		platform, err := pd.Detect(t.Context())

		require.NoError(t, err)
		require.Equal(t, runtime.GOOS, platform.OS)

		// npm and code should appear; others should not.
		require.True(t, platform.HasManager("npm"))
		require.True(t, platform.HasManager("code"))

		// Platform-specific managers should NOT be present because
		// we mocked them as not found.
		for _, mgr := range platformManagers[runtime.GOOS] {
			if mgr == "npm" || mgr == "code" {
				continue
			}
			require.False(t, platform.HasManager(mgr),
				"expected %s to be absent", mgr)
		}
	})

	t.Run("NoManagersAvailable", func(t *testing.T) {
		t.Parallel()

		runner := mockexec.NewMockCommandRunner()
		for _, managers := range platformManagers {
			for _, mgr := range managers {
				runner.MockToolInPath(mgr, osexec.ErrNotFound)
			}
		}

		pd := NewPlatformDetector(runner)
		platform, err := pd.Detect(t.Context())

		require.NoError(t, err)
		require.Empty(t, platform.AvailableManagers)
	})
}

func TestIsManagerAvailable(t *testing.T) {
	t.Parallel()

	t.Run("AvailableWhenInPathAndVersionSucceeds", func(t *testing.T) {
		t.Parallel()

		runner := mockexec.NewMockCommandRunner()
		runner.MockToolInPath("winget", nil)
		runner.When(func(args exec.RunArgs, _ string) bool {
			return args.Cmd == "winget" &&
				slices.Contains(args.Args, "--version")
		}).Respond(exec.RunResult{
			ExitCode: 0,
			Stdout:   "v1.6.3133",
		})

		pd := NewPlatformDetector(runner)
		require.True(t, pd.IsManagerAvailable(t.Context(), "winget"))
	})

	t.Run("UnavailableWhenNotInPath", func(t *testing.T) {
		t.Parallel()

		runner := mockexec.NewMockCommandRunner()
		runner.MockToolInPath("brew", osexec.ErrNotFound)

		pd := NewPlatformDetector(runner)
		require.False(t, pd.IsManagerAvailable(t.Context(), "brew"))
	})

	t.Run("UnavailableWhenVersionFails", func(t *testing.T) {
		t.Parallel()

		runner := mockexec.NewMockCommandRunner()
		runner.MockToolInPath("snap", nil)
		runner.When(func(args exec.RunArgs, _ string) bool {
			return args.Cmd == "snap" &&
				slices.Contains(args.Args, "--version")
		}).SetError(errors.New("exit code: 1"))

		pd := NewPlatformDetector(runner)
		require.False(t, pd.IsManagerAvailable(t.Context(), "snap"))
	})

	t.Run("UnavailableWhenPathCheckErrors", func(t *testing.T) {
		t.Parallel()

		runner := mockexec.NewMockCommandRunner()
		runner.MockToolInPath("unknown",
			errors.New("failed searching for `unknown` on PATH"))

		pd := NewPlatformDetector(runner)
		require.False(t, pd.IsManagerAvailable(t.Context(), "unknown"))
	})
}

func TestSelectStrategy(t *testing.T) {
	t.Parallel()

	// Shared detector — SelectStrategy does not use the runner.
	pd := NewPlatformDetector(mockexec.NewMockCommandRunner())

	t.Run("ReturnsNilForNilTool", func(t *testing.T) {
		t.Parallel()

		platform := &Platform{
			OS:                "windows",
			AvailableManagers: []string{"winget"},
		}
		require.Nil(t, pd.SelectStrategy(nil, platform))
	})

	t.Run("ReturnsNilForNilStrategies", func(t *testing.T) {
		t.Parallel()

		tool := &ToolDefinition{
			Name:              "test-tool",
			InstallStrategies: nil,
		}
		platform := &Platform{
			OS:                "linux",
			AvailableManagers: []string{"apt"},
		}
		require.Nil(t, pd.SelectStrategy(tool, platform))
	})

	t.Run("ReturnsNilWhenOSNotInStrategies", func(t *testing.T) {
		t.Parallel()

		tool := &ToolDefinition{
			Name: "az",
			InstallStrategies: map[string]InstallStrategy{
				"windows": {
					PackageManager: "winget",
					PackageId:      "Microsoft.AzureCLI",
				},
			},
		}
		platform := &Platform{
			OS:                "linux",
			AvailableManagers: []string{"apt"},
		}
		require.Nil(t, pd.SelectStrategy(tool, platform))
	})

	t.Run("ReturnsStrategyForMatchingOS", func(t *testing.T) {
		t.Parallel()

		tool := &ToolDefinition{
			Name: "az",
			InstallStrategies: map[string]InstallStrategy{
				"darwin": {
					PackageManager: "brew",
					PackageId:      "azure-cli",
				},
			},
		}
		platform := &Platform{
			OS:                "darwin",
			AvailableManagers: []string{"brew", "npm"},
		}

		got := pd.SelectStrategy(tool, platform)
		require.NotNil(t, got)
		require.Equal(t, "brew", got.PackageManager)
		require.Equal(t, "azure-cli", got.PackageId)
	})

	t.Run("ReturnsStrategyEvenWhenManagerUnavailable", func(t *testing.T) {
		t.Parallel()

		tool := &ToolDefinition{
			Name: "az",
			InstallStrategies: map[string]InstallStrategy{
				"windows": {
					PackageManager: "winget",
					PackageId:      "Microsoft.AzureCLI",
				},
			},
		}
		// Platform has no managers at all.
		platform := &Platform{
			OS:                "windows",
			AvailableManagers: []string{},
		}

		got := pd.SelectStrategy(tool, platform)
		require.NotNil(t, got)
		require.Equal(t, "winget", got.PackageManager)
	})

	t.Run("ReturnsStrategyWithInstallCommand", func(t *testing.T) {
		t.Parallel()

		tool := &ToolDefinition{
			Name: "az",
			InstallStrategies: map[string]InstallStrategy{
				"linux": {
					InstallCommand: "curl -sL https://example.com | bash",
					FallbackUrl:    "https://example.com/install",
				},
			},
		}
		platform := &Platform{
			OS:                "linux",
			AvailableManagers: []string{"apt"},
		}

		got := pd.SelectStrategy(tool, platform)
		require.NotNil(t, got)
		require.Empty(t, got.PackageManager)
		require.Equal(t,
			"curl -sL https://example.com | bash",
			got.InstallCommand,
		)
	})

	t.Run("WorksWithBuiltInToolDefinitions", func(t *testing.T) {
		t.Parallel()

		azTool := FindTool("az-cli")
		require.NotNil(t, azTool)

		platform := &Platform{
			OS:                "windows",
			AvailableManagers: []string{"winget"},
		}

		got := pd.SelectStrategy(azTool, platform)
		require.NotNil(t, got)
		require.Equal(t, "winget", got.PackageManager)
		require.Equal(t, "Microsoft.AzureCLI", got.PackageId)
	})
}

func TestPlatformHasManager(t *testing.T) {
	t.Parallel()

	platform := &Platform{
		OS:                "windows",
		AvailableManagers: []string{"winget", "npm", "code"},
	}

	t.Run("ReturnsTrueForPresentManager", func(t *testing.T) {
		t.Parallel()
		require.True(t, platform.HasManager("winget"))
		require.True(t, platform.HasManager("npm"))
		require.True(t, platform.HasManager("code"))
	})

	t.Run("ReturnsFalseForAbsentManager", func(t *testing.T) {
		t.Parallel()
		require.False(t, platform.HasManager("brew"))
		require.False(t, platform.HasManager("apt"))
		require.False(t, platform.HasManager(""))
	})

	t.Run("ReturnsFalseForEmptyManagers", func(t *testing.T) {
		t.Parallel()
		empty := &Platform{
			OS:                "linux",
			AvailableManagers: []string{},
		}
		require.False(t, empty.HasManager("apt"))
	})
}

func TestPlatformManagersTable(t *testing.T) {
	t.Parallel()

	t.Run("WindowsIncludesExpectedManagers", func(t *testing.T) {
		t.Parallel()
		managers := platformManagers["windows"]
		require.Contains(t, managers, "winget")
		require.Contains(t, managers, "npm")
		require.Contains(t, managers, "code")
	})

	t.Run("DarwinIncludesExpectedManagers", func(t *testing.T) {
		t.Parallel()
		managers := platformManagers["darwin"]
		require.Contains(t, managers, "brew")
		require.Contains(t, managers, "npm")
		require.Contains(t, managers, "code")
	})

	t.Run("LinuxIncludesExpectedManagers", func(t *testing.T) {
		t.Parallel()
		managers := platformManagers["linux"]
		require.Contains(t, managers, "apt")
		require.Contains(t, managers, "snap")
		require.Contains(t, managers, "npm")
		require.Contains(t, managers, "code")
	})

	t.Run("CrossPlatformToolsOnAllOSes", func(t *testing.T) {
		t.Parallel()
		for os, managers := range platformManagers {
			require.Contains(t, managers, "npm",
				"npm missing for %s", os)
			require.Contains(t, managers, "code",
				"code missing for %s", os)
		}
	})
}
