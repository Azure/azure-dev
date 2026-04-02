// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tool

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockexec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// mockDetector — simple in-package mock for the Detector interface
// ---------------------------------------------------------------------------

type mockDetector struct {
	detectToolFn func(
		ctx context.Context,
		tool *ToolDefinition,
	) (*ToolStatus, error)
	detectAllFn func(
		ctx context.Context,
		tools []*ToolDefinition,
	) ([]*ToolStatus, error)
}

func (m *mockDetector) DetectTool(
	ctx context.Context,
	tool *ToolDefinition,
) (*ToolStatus, error) {
	if m.detectToolFn != nil {
		return m.detectToolFn(ctx, tool)
	}
	return &ToolStatus{Tool: tool}, nil
}

func (m *mockDetector) DetectAll(
	ctx context.Context,
	tools []*ToolDefinition,
) ([]*ToolStatus, error) {
	if m.detectAllFn != nil {
		return m.detectAllFn(ctx, tools)
	}
	results := make([]*ToolStatus, len(tools))
	for i, t := range tools {
		results[i] = &ToolStatus{Tool: t}
	}
	return results, nil
}

// ---------------------------------------------------------------------------
// Install tests
// ---------------------------------------------------------------------------

func TestInstall_WithPackageManager(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()

	// Platform detection: npm is available (cross-platform).
	for _, managers := range platformManagers {
		for _, mgr := range managers {
			runner.MockToolInPath(mgr, errors.New("not found"))
		}
	}
	runner.MockToolInPath("npm", nil)
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "npm" &&
			slices.Contains(args.Args, "--version")
	}).Respond(exec.RunResult{ExitCode: 0, Stdout: "10.2.0"})

	// Capture the install command.
	var capturedCmd string
	var capturedArgs []string
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "npm" &&
			slices.Contains(args.Args, "install")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		capturedCmd = args.Cmd
		capturedArgs = args.Args
		return exec.RunResult{ExitCode: 0}, nil
	})

	det := &mockDetector{
		detectToolFn: func(
			_ context.Context, tool *ToolDefinition,
		) (*ToolStatus, error) {
			return &ToolStatus{
				Tool:             tool,
				Installed:        true,
				InstalledVersion: "2.64.0",
			}, nil
		},
	}

	pd := NewPlatformDetector(runner)
	inst := NewInstaller(runner, pd, det)

	// Use allPlatforms so the test works on any OS.
	tool := &ToolDefinition{
		Id:       "test-tool",
		Name:     "Test Tool",
		Category: ToolCategoryCLI,
		InstallStrategies: allPlatforms(InstallStrategy{
			PackageManager: "npm",
			PackageId:      "@test/tool",
		}),
	}

	result, err := inst.Install(t.Context(), tool)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Success)
	assert.Equal(t, "2.64.0", result.InstalledVersion)

	// Verify the correct command was constructed.
	assert.Equal(t, "npm", capturedCmd)
	assert.Contains(t, capturedArgs, "install")
	assert.Contains(t, capturedArgs, "@test/tool")
}

func TestInstall_WithInstallCommand(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()

	// Platform detection: no managers available.
	for _, managers := range platformManagers {
		for _, mgr := range managers {
			runner.MockToolInPath(mgr, errors.New("not found"))
		}
	}

	// The install command itself.
	var capturedCmd string
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "curl"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		capturedCmd = args.Cmd
		return exec.RunResult{ExitCode: 0}, nil
	})

	det := &mockDetector{
		detectToolFn: func(
			_ context.Context, tool *ToolDefinition,
		) (*ToolStatus, error) {
			return &ToolStatus{
				Tool:             tool,
				Installed:        true,
				InstalledVersion: "1.0.0",
			}, nil
		},
	}

	pd := NewPlatformDetector(runner)
	inst := NewInstaller(runner, pd, det)

	tool := &ToolDefinition{
		Id:       "custom-tool",
		Name:     "Custom Tool",
		Category: ToolCategoryCLI,
		InstallStrategies: map[string]InstallStrategy{
			"windows": {
				InstallCommand: "curl -sL https://example.com/install.sh",
			},
			"darwin": {
				InstallCommand: "curl -sL https://example.com/install.sh",
			},
			"linux": {
				InstallCommand: "curl -sL https://example.com/install.sh",
			},
		},
	}

	result, err := inst.Install(t.Context(), tool)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Success)
	assert.Equal(t, "curl", capturedCmd)
}

func TestInstall_ManagerUnavailable_FallbackError(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()

	// All managers unavailable.
	for _, managers := range platformManagers {
		for _, mgr := range managers {
			runner.MockToolInPath(mgr, errors.New("not found"))
		}
	}

	det := &mockDetector{}
	pd := NewPlatformDetector(runner)
	inst := NewInstaller(runner, pd, det)

	tool := &ToolDefinition{
		Id:       "needs-manager",
		Name:     "Needs Manager",
		Category: ToolCategoryCLI,
		InstallStrategies: allPlatforms(InstallStrategy{
			PackageManager: "special-mgr",
			PackageId:      "Some.Package",
			FallbackUrl:    "https://example.com/install",
		}),
	}

	result, err := inst.Install(t.Context(), tool)

	require.NoError(t, err) // error is in result, not returned
	require.NotNil(t, result)
	assert.False(t, result.Success)
	assert.Equal(t, "manual", result.Strategy)
	require.Error(t, result.Error)

	// Check it is an ErrorWithSuggestion.
	var ewi *errorhandler.ErrorWithSuggestion
	assert.True(t, errors.As(result.Error, &ewi),
		"expected ErrorWithSuggestion")
	if ewi != nil {
		assert.Contains(t, ewi.Suggestion, "https://example.com/install")
	}
}

func TestInstall_NoStrategyForPlatform(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()

	// All managers unavailable.
	for _, managers := range platformManagers {
		for _, mgr := range managers {
			runner.MockToolInPath(mgr, errors.New("not found"))
		}
	}

	det := &mockDetector{}
	pd := NewPlatformDetector(runner)
	inst := NewInstaller(runner, pd, det)

	tool := &ToolDefinition{
		Id:                "no-strategy",
		Name:              "No Strategy",
		Category:          ToolCategoryCLI,
		InstallStrategies: map[string]InstallStrategy{},
	}

	result, err := inst.Install(t.Context(), tool)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Success)
	assert.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "no install strategy")
}

func TestInstall_VerificationFails(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()

	// Platform detection.
	for _, managers := range platformManagers {
		for _, mgr := range managers {
			runner.MockToolInPath(mgr, errors.New("not found"))
		}
	}

	// Install command itself.
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "curl"
	}).Respond(exec.RunResult{ExitCode: 0})

	// Detection after install: not found.
	det := &mockDetector{
		detectToolFn: func(
			_ context.Context, tool *ToolDefinition,
		) (*ToolStatus, error) {
			return &ToolStatus{
				Tool:      tool,
				Installed: false,
			}, nil
		},
	}

	pd := NewPlatformDetector(runner)
	inst := NewInstaller(runner, pd, det)

	tool := &ToolDefinition{
		Id:       "verify-fail",
		Name:     "Verify Fail",
		Category: ToolCategoryCLI,
		InstallStrategies: allPlatforms(InstallStrategy{
			InstallCommand: "curl -sL https://example.com/install.sh",
		}),
	}

	result, err := inst.Install(t.Context(), tool)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Success)
	assert.Contains(t, result.Error.Error(), "verification failed")
}

// ---------------------------------------------------------------------------
// Upgrade tests
// ---------------------------------------------------------------------------

func TestUpgrade_UsesUpgradeCommand(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()

	// Platform detection: npm available on all platforms.
	for _, managers := range platformManagers {
		for _, mgr := range managers {
			runner.MockToolInPath(mgr, errors.New("not found"))
		}
	}
	runner.MockToolInPath("npm", nil)
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "npm" &&
			slices.Contains(args.Args, "--version")
	}).Respond(exec.RunResult{ExitCode: 0, Stdout: "10.2.0"})

	// Capture the upgrade command.
	var capturedCmd string
	var capturedArgs []string
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "npm" &&
			(slices.Contains(args.Args, "update") ||
				slices.Contains(args.Args, "install"))
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		capturedCmd = args.Cmd
		capturedArgs = args.Args
		return exec.RunResult{ExitCode: 0}, nil
	})

	det := &mockDetector{
		detectToolFn: func(
			_ context.Context, tool *ToolDefinition,
		) (*ToolStatus, error) {
			return &ToolStatus{
				Tool:             tool,
				Installed:        true,
				InstalledVersion: "1.1.0",
			}, nil
		},
	}

	pd := NewPlatformDetector(runner)
	inst := NewInstaller(runner, pd, det)

	// Use allPlatforms so the strategy works regardless of OS.
	tool := &ToolDefinition{
		Id:       "test-npm-tool",
		Name:     "Test NPM Tool",
		Category: ToolCategoryCLI,
		InstallStrategies: allPlatforms(InstallStrategy{
			PackageManager: "npm",
			PackageId:      "@test/tool",
		}),
	}

	result, err := inst.Upgrade(t.Context(), tool)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Success)
	assert.Equal(t, "npm", capturedCmd)

	// npm upgrade uses "update" subcommand.
	assert.Contains(t, capturedArgs, "update")
	assert.Contains(t, capturedArgs, "@test/tool")
}

// ---------------------------------------------------------------------------
// buildManagerCommand / buildCommand helpers
// ---------------------------------------------------------------------------

func TestBuildManagerCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		manager   string
		packageID string
		upgrade   bool
		expectCmd string
		expectArg string // action keyword to find in args
	}{
		{
			name:      "WingetInstall",
			manager:   "winget",
			packageID: "Microsoft.AzureCLI",
			upgrade:   false,
			expectCmd: "winget",
			expectArg: "install",
		},
		{
			name:      "WingetUpgrade",
			manager:   "winget",
			packageID: "Microsoft.AzureCLI",
			upgrade:   true,
			expectCmd: "winget",
			expectArg: "upgrade",
		},
		{
			name:      "BrewInstall",
			manager:   "brew",
			packageID: "azure-cli",
			upgrade:   false,
			expectCmd: "brew",
			expectArg: "install",
		},
		{
			name:      "BrewUpgrade",
			manager:   "brew",
			packageID: "azure-cli",
			upgrade:   true,
			expectCmd: "brew",
			expectArg: "upgrade",
		},
		{
			name:      "AptInstall",
			manager:   "apt",
			packageID: "azure-cli",
			upgrade:   false,
			expectCmd: "sudo",
			expectArg: "install",
		},
		{
			name:      "AptUpgrade",
			manager:   "apt",
			packageID: "azure-cli",
			upgrade:   true,
			expectCmd: "sudo",
			expectArg: "--only-upgrade",
		},
		{
			name:      "NpmInstall",
			manager:   "npm",
			packageID: "@azure/mcp",
			upgrade:   false,
			expectCmd: "npm",
			expectArg: "install",
		},
		{
			name:      "NpmUpgrade",
			manager:   "npm",
			packageID: "@azure/mcp",
			upgrade:   true,
			expectCmd: "npm",
			expectArg: "update",
		},
		{
			name:      "CodeInstall",
			manager:   "code",
			packageID: "ms-azuretools.vscode-bicep",
			upgrade:   false,
			expectCmd: "code",
			expectArg: "--install-extension",
		},
		{
			name:      "CodeUpgrade",
			manager:   "code",
			packageID: "ms-azuretools.vscode-bicep",
			upgrade:   true,
			expectCmd: "code",
			expectArg: "--force",
		},
		{
			name:      "UnknownManagerReturnsEmpty",
			manager:   "unknown-mgr",
			packageID: "pkg",
			upgrade:   false,
			expectCmd: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cmd, args := buildManagerCommand(
				tt.manager, tt.packageID, tt.upgrade,
			)

			assert.Equal(t, tt.expectCmd, cmd)
			if tt.expectArg != "" {
				assert.Contains(t, args, tt.expectArg)
			}
		})
	}
}

func TestSplitCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		command    string
		expectCmd  string
		expectArgs []string
	}{
		{
			name:       "SimpleCommand",
			command:    "npm install -g @azure/mcp",
			expectCmd:  "npm",
			expectArgs: []string{"install", "-g", "@azure/mcp"},
		},
		{
			name:       "SingleBinary",
			command:    "docker",
			expectCmd:  "docker",
			expectArgs: []string{},
		},
		{
			name:      "EmptyString",
			command:   "",
			expectCmd: "",
		},
		{
			name:       "ExtraWhitespace",
			command:    "  curl   -sL   https://example.com  ",
			expectCmd:  "curl",
			expectArgs: []string{"-sL", "https://example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cmd, args := splitCommand(tt.command)
			assert.Equal(t, tt.expectCmd, cmd)
			if tt.expectArgs != nil {
				assert.Equal(t, tt.expectArgs, args)
			}
		})
	}
}
