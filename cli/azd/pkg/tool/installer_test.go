// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tool

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
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
// Verification retry tests (non-CLI tools)
// ---------------------------------------------------------------------------

func TestInstall_NonCLI_RetriesVerification(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()

	// Platform detection: npm available.
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

	// Install command succeeds.
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "npm" &&
			slices.Contains(args.Args, "install")
	}).Respond(exec.RunResult{ExitCode: 0})

	// Detection fails twice, then succeeds on third attempt.
	var callCount atomic.Int32
	det := &mockDetector{
		detectToolFn: func(
			_ context.Context, tool *ToolDefinition,
		) (*ToolStatus, error) {
			n := callCount.Add(1)
			if n <= 2 {
				return &ToolStatus{
					Tool:      tool,
					Installed: false,
				}, nil
			}
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
		Id:       "mcp-server",
		Name:     "MCP Server",
		Category: ToolCategoryServer,
		InstallStrategies: allPlatforms(InstallStrategy{
			PackageManager: "npm",
			PackageId:      "@azure/mcp-server",
		}),
	}

	result, err := inst.Install(t.Context(), tool)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Success)
	assert.Equal(t, "1.0.0", result.InstalledVersion)
	assert.Equal(t, int32(3), callCount.Load(),
		"expected 3 detect calls (2 failures + 1 success)")
}

func TestInstall_NonCLI_ExhaustsRetries(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()

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

	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "npm" &&
			slices.Contains(args.Args, "install")
	}).Respond(exec.RunResult{ExitCode: 0})

	// Detection always fails.
	var callCount atomic.Int32
	det := &mockDetector{
		detectToolFn: func(
			_ context.Context, tool *ToolDefinition,
		) (*ToolStatus, error) {
			callCount.Add(1)
			return &ToolStatus{
				Tool:      tool,
				Installed: false,
			}, nil
		},
	}

	pd := NewPlatformDetector(runner)
	inst := NewInstaller(runner, pd, det)

	tool := &ToolDefinition{
		Id:       "ext-tool",
		Name:     "VS Code Extension",
		Category: ToolCategoryVSCodeExtension,
		InstallStrategies: allPlatforms(InstallStrategy{
			PackageManager: "npm",
			PackageId:      "@test/ext",
		}),
	}

	result, err := inst.Install(t.Context(), tool)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Success)
	assert.Contains(t, result.Error.Error(), "verification failed")
	assert.Equal(t, int32(4), callCount.Load(),
		"expected 4 detect calls (1 initial + 3 retries)")
}

func TestInstall_NonCLI_RespectsContextCancellation(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()

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

	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "npm" &&
			slices.Contains(args.Args, "install")
	}).Respond(exec.RunResult{ExitCode: 0})

	// Detection always fails — but context will be cancelled.
	var callCount atomic.Int32
	det := &mockDetector{
		detectToolFn: func(
			_ context.Context, tool *ToolDefinition,
		) (*ToolStatus, error) {
			callCount.Add(1)
			return &ToolStatus{
				Tool:      tool,
				Installed: false,
			}, nil
		},
	}

	pd := NewPlatformDetector(runner)
	inst := NewInstaller(runner, pd, det)

	tool := &ToolDefinition{
		Id:       "lib-tool",
		Name:     "Lib Tool",
		Category: ToolCategoryAzdExtension,
		InstallStrategies: allPlatforms(InstallStrategy{
			PackageManager: "npm",
			PackageId:      "@test/lib",
		}),
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancel immediately

	result, err := inst.Install(ctx, tool)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Success)
	// Should have detected once, then ctx was cancelled before retry sleep.
	assert.Equal(t, int32(1), callCount.Load(),
		"expected only 1 detect call before context cancellation")
	assert.Contains(t, result.Error.Error(), "context canceled")
}

func TestInstall_CLI_NoRetry(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()

	for _, managers := range platformManagers {
		for _, mgr := range managers {
			runner.MockToolInPath(mgr, errors.New("not found"))
		}
	}

	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "curl"
	}).Respond(exec.RunResult{ExitCode: 0})

	// Detection always fails.
	var callCount atomic.Int32
	det := &mockDetector{
		detectToolFn: func(
			_ context.Context, tool *ToolDefinition,
		) (*ToolStatus, error) {
			callCount.Add(1)
			return &ToolStatus{
				Tool:      tool,
				Installed: false,
			}, nil
		},
	}

	pd := NewPlatformDetector(runner)
	inst := NewInstaller(runner, pd, det)

	tool := &ToolDefinition{
		Id:       "cli-tool",
		Name:     "CLI Tool",
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
	assert.Equal(t, int32(1), callCount.Load(),
		"CLI tools should not retry — expected exactly 1 detect call")
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

// ---------------------------------------------------------------------------
// validateChecksum tests
// ---------------------------------------------------------------------------

func TestValidateChecksum_SHA256_Match(t *testing.T) {
	t.Parallel()

	content := []byte("hello world")
	tmpFile := filepath.Join(t.TempDir(), "test.bin")
	require.NoError(t, os.WriteFile(tmpFile, content, 0o600))

	sum := sha256.Sum256(content)
	expected := hex.EncodeToString(sum[:])

	err := validateChecksum(tmpFile, Checksum{
		Algorithm: "sha256",
		Value:     expected,
	})
	require.NoError(t, err)
}

func TestValidateChecksum_SHA256_Mismatch(t *testing.T) {
	t.Parallel()

	content := []byte("hello world")
	tmpFile := filepath.Join(t.TempDir(), "test.bin")
	require.NoError(t, os.WriteFile(tmpFile, content, 0o600))

	err := validateChecksum(tmpFile, Checksum{
		Algorithm: "sha256",
		Value: "0000000000000000000000000000" +
			"000000000000000000000000000000000000",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checksum verification failed")
}

func TestValidateChecksum_SkippedWhenEmpty(t *testing.T) {
	t.Parallel()

	err := validateChecksum("/nonexistent", Checksum{})
	require.NoError(t, err)
}

func TestValidateChecksum_UnsupportedAlgorithm(t *testing.T) {
	t.Parallel()

	tmpFile := filepath.Join(t.TempDir(), "test.bin")
	require.NoError(t, os.WriteFile(
		tmpFile, []byte("data"), 0o600,
	))

	err := validateChecksum(tmpFile, Checksum{
		Algorithm: "md5",
		Value:     "abc",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported checksum algorithm")
}

func TestValidateChecksum_PartialConfig_AlgorithmOnly(t *testing.T) {
	t.Parallel()

	err := validateChecksum("/nonexistent", Checksum{
		Algorithm: "sha256",
		Value:     "",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "value is empty")
	assert.Contains(t, err.Error(), "sha256")
}

func TestValidateChecksum_PartialConfig_ValueOnly(t *testing.T) {
	t.Parallel()

	err := validateChecksum("/nonexistent", Checksum{
		Algorithm: "",
		Value:     "abc123",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "algorithm is empty")
}

// ---------------------------------------------------------------------------
// Direct download tests
// ---------------------------------------------------------------------------

func TestInstall_DirectDownload_WithChecksum(t *testing.T) {
	t.Parallel()

	content := []byte("#!/bin/sh\necho hello")
	sum := sha256.Sum256(content)
	expectedChecksum := hex.EncodeToString(sum[:])

	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.Write(content) //nolint:errcheck
		},
	))
	defer srv.Close()

	runner := mockexec.NewMockCommandRunner()
	for _, managers := range platformManagers {
		for _, mgr := range managers {
			runner.MockToolInPath(
				mgr, errors.New("not found"),
			)
		}
	}

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
	inst := NewInstallerWithHTTPClient(
		runner, pd, det, srv.Client(),
	)

	toolDef := &ToolDefinition{
		Id:       "direct-dl",
		Name:     "Direct Download Tool",
		Category: ToolCategoryCLI,
		InstallStrategies: allPlatforms(InstallStrategy{
			DirectDownloadUrl: srv.URL + "/tool.bin",
			Checksum: Checksum{
				Algorithm: "sha256",
				Value:     expectedChecksum,
			},
		}),
	}

	result, err := inst.Install(t.Context(), toolDef)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Success)
	assert.Equal(t, "direct-download", result.Strategy)
}

func TestInstall_DirectDownload_ChecksumMismatch(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			//nolint:errcheck
			w.Write([]byte("actual content"))
		},
	))
	defer srv.Close()

	runner := mockexec.NewMockCommandRunner()
	for _, managers := range platformManagers {
		for _, mgr := range managers {
			runner.MockToolInPath(
				mgr, errors.New("not found"),
			)
		}
	}

	det := &mockDetector{}
	pd := NewPlatformDetector(runner)
	inst := NewInstallerWithHTTPClient(
		runner, pd, det, srv.Client(),
	)

	toolDef := &ToolDefinition{
		Id:       "bad-checksum",
		Name:     "Bad Checksum Tool",
		Category: ToolCategoryCLI,
		InstallStrategies: allPlatforms(InstallStrategy{
			DirectDownloadUrl: srv.URL + "/tool.bin",
			Checksum: Checksum{
				Algorithm: "sha256",
				Value: "0000000000000000000000000000" +
					"000000000000000000000000000000000000",
			},
		}),
	}

	result, err := inst.Install(t.Context(), toolDef)

	require.NoError(t, err) // error in result, not returned
	require.NotNil(t, result)
	assert.False(t, result.Success)
	require.Error(t, result.Error)
	assert.Contains(
		t, result.Error.Error(),
		"checksum verification failed",
	)
}

// ---------------------------------------------------------------------------
// AggregateInstallResults
// ---------------------------------------------------------------------------

func TestAggregateInstallResults_AllSuccess(t *testing.T) {
	results := []*InstallResult{
		{Tool: &ToolDefinition{Id: "kubectl"}, Success: true},
		{Tool: &ToolDefinition{Id: "helm"}, Success: true},
	}

	successCount, failureCount, failedIDs := AggregateInstallResults(results, nil, []string{"kubectl", "helm"})

	assert.Equal(t, 2, successCount)
	assert.Equal(t, 0, failureCount)
	assert.Empty(t, failedIDs)
}

func TestAggregateInstallResults_MixedResults(t *testing.T) {
	results := []*InstallResult{
		{Tool: &ToolDefinition{Id: "kubectl"}, Success: true},
		{Tool: &ToolDefinition{Id: "terraform"}, Success: false},
		{Tool: &ToolDefinition{Id: "helm"}, Success: false},
	}

	successCount, failureCount, failedIDs := AggregateInstallResults(
		results, nil, []string{"kubectl", "terraform", "helm"})

	assert.Equal(t, 1, successCount)
	assert.Equal(t, 2, failureCount)
	// Sorted, so "helm" comes before "terraform" even though terraform appeared first in results.
	assert.Equal(t, []string{"helm", "terraform"}, failedIDs)
}

func TestAggregateInstallResults_OperationErrorSynthesizesFailures(t *testing.T) {
	// Batch call failed and produced no per-tool results: synthesize failures
	// for every requested tool so the aggregate isn't silently zero.
	successCount, failureCount, failedIDs := AggregateInstallResults(
		nil, errors.New("batch failed"), []string{"terraform", "helm", "kubectl"})

	assert.Equal(t, 0, successCount)
	assert.Equal(t, 3, failureCount)
	assert.Equal(t, []string{"helm", "kubectl", "terraform"}, failedIDs)
}

func TestAggregateInstallResults_OperationErrorWithResultsTrustsResults(t *testing.T) {
	// Per-tool results exist alongside opErr: trust the per-tool entries — synthesizing would double-count.
	results := []*InstallResult{
		{Tool: &ToolDefinition{Id: "kubectl"}, Success: true},
		{Tool: &ToolDefinition{Id: "helm"}, Success: false},
	}

	successCount, failureCount, failedIDs := AggregateInstallResults(
		results, errors.New("batch ended with errors"), []string{"kubectl", "helm", "terraform"})

	assert.Equal(t, 1, successCount)
	assert.Equal(t, 1, failureCount)
	assert.Equal(t, []string{"helm"}, failedIDs)
}

func TestAggregateInstallResults_NilToolInFailureSkipped(t *testing.T) {
	// A failure with a nil Tool can't contribute an ID; it still bumps the count but is omitted from failed_ids.
	results := []*InstallResult{
		{Tool: nil, Success: false},
		{Tool: &ToolDefinition{Id: "helm"}, Success: false},
	}

	successCount, failureCount, failedIDs := AggregateInstallResults(results, nil, []string{"helm", "kubectl"})

	assert.Equal(t, 0, successCount)
	assert.Equal(t, 2, failureCount)
	assert.Equal(t, []string{"helm"}, failedIDs)
}

func TestAggregateInstallResults_EmptyInputs(t *testing.T) {
	successCount, failureCount, failedIDs := AggregateInstallResults(nil, nil, nil)

	assert.Equal(t, 0, successCount)
	assert.Equal(t, 0, failureCount)
	assert.Empty(t, failedIDs)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// allSkillHostNames lists the host binary names checked by the prereq
// logic; tests must explicitly mock every entry so ToolInPath does not
// fall back to the real PATH on the developer's machine.
var allSkillHostNames = []string{"apm", "copilot", "claude", "gemini", "codex"}

// newSkillTool returns a minimal ToolDefinition exercising the
// codepaths covered by these tests. The host commands are simplified
// but preserve the structural distinctions (MarketplaceAddCommand on
// copilot/claude/codex, gemini bare, codex's two-step upgrade).
func newSkillTool() *ToolDefinition {
	return &ToolDefinition{
		Id:       "test-azure-skills",
		Name:     "Test Azure Skills",
		Category: ToolCategorySkill,
		Priority: ToolPriorityRecommended,
		SkillHosts: []SkillHost{
			{
				Host:                 "apm",
				PluginInstallCommand: []string{"install", "microsoft/azure-skills", "-g"},
				PluginUpdateCommand:  []string{"update", "microsoft/azure-skills", "-y", "-g"},
				PluginListCommand:    []string{"deps", "list", "-g"},
				PluginName:           "microsoft/azure-skills",
			},
			{
				Host:                  "copilot",
				MarketplaceAddCommand: []string{"plugin", "marketplace", "add", "microsoft/azure-skills"},
				PluginInstallCommand:  []string{"plugin", "install", "azure@azure-skills"},
				PluginUpdateCommand:   []string{"plugin", "update", "azure@azure-skills"},
				PluginListCommand:     []string{"plugin", "list"},
				PluginName:            "azure@azure-skills",
			},
			{
				Host:                  "claude",
				MarketplaceAddCommand: []string{"plugin", "marketplace", "add", "https://github.com/microsoft/azure-skills"},
				PluginInstallCommand:  []string{"plugin", "install", "azure"},
				PluginUpdateCommand:   []string{"plugin", "update", "azure@azure-skills"},
				PluginListCommand:     []string{"plugin", "list", "azure@azure-skills"},
				PluginName:            "azure@azure-skills",
			},
			{
				Host:                 "gemini",
				PluginInstallCommand: []string{"extensions", "install", "https://github.com/microsoft/azure-skills"},
				PluginUpdateCommand:  []string{"extensions", "update", "azure"},
				PluginListCommand:    []string{"extensions", "list"},
				PluginName:           "azure",
			},
			{
				Host:                  "codex",
				MarketplaceAddCommand: []string{"plugin", "marketplace", "add", "microsoft/azure-skills"},
				PluginInstallCommand:  []string{"plugin", "add", "azure@azure-skills"},
				PluginUpdateCommand:   []string{"plugin", "marketplace", "upgrade", "azure-skills"},
				PluginListCommand:     []string{"plugin", "list"},
				PluginName:            "azure@azure-skills",
			},
		},
	}
}

// mockHostPresence wires ToolInPath responses so only the named hosts
// resolve successfully. Pass an empty slice to mock every host as
// missing.
func mockHostPresence(
	runner *mockexec.MockCommandRunner,
	present ...string,
) {
	for _, h := range allSkillHostNames {
		if slices.Contains(present, h) {
			runner.MockToolInPath(h, nil)
		} else {
			runner.MockToolInPath(h, errors.New("not found"))
		}
	}
}

// captureLog redirects log output for the duration of fn and returns
// what was written. Used to assert that the SOFT node prereq warning
// was emitted via log.Printf.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Default().Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	fn()
	return buf.String()
}

// installedDetector is a mockDetector that always reports the tool as
// installed with the given version.
func installedDetector(version string) *mockDetector {
	return &mockDetector{
		detectToolFn: func(
			_ context.Context, tool *ToolDefinition,
		) (*ToolStatus, error) {
			return &ToolStatus{
				Tool:             tool,
				Installed:        true,
				InstalledVersion: version,
			}, nil
		},
	}
}

// ---------------------------------------------------------------------------
// runSkill — host selection
// ---------------------------------------------------------------------------

func TestRunSkill_PicksFirstAvailableHost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		present    []string
		wantHost   string
		wantCmd    string
		wantPlugin string
	}{
		{
			name:       "ApmOnly",
			present:    []string{"apm"},
			wantHost:   "apm",
			wantCmd:    "apm",
			wantPlugin: "microsoft/azure-skills",
		},
		{
			name:       "CopilotOnly",
			present:    []string{"copilot"},
			wantHost:   "copilot",
			wantCmd:    "copilot",
			wantPlugin: "azure@azure-skills",
		},
		{
			name:       "ClaudeOnly",
			present:    []string{"claude"},
			wantHost:   "claude",
			wantCmd:    "claude",
			wantPlugin: "azure",
		},
		{
			name:       "GeminiOnly",
			present:    []string{"gemini"},
			wantHost:   "gemini",
			wantCmd:    "gemini",
			wantPlugin: "https://github.com/microsoft/azure-skills",
		},
		{
			name:       "CodexOnly",
			present:    []string{"codex"},
			wantHost:   "codex",
			wantCmd:    "codex",
			wantPlugin: "azure@azure-skills",
		},
		{
			name:       "AllPresent_PrefersApm",
			present:    allSkillHostNames,
			wantHost:   "apm",
			wantCmd:    "apm",
			wantPlugin: "microsoft/azure-skills",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runner := mockexec.NewMockCommandRunner()
			mockHostPresence(runner, tc.present...)
			runner.MockToolInPath("node", nil)

			var ranInstall bool
			var capturedArgs []string
			runner.When(func(args exec.RunArgs, _ string) bool {
				return args.Cmd == tc.wantCmd &&
					(slices.Contains(args.Args, "install") ||
						slices.Contains(args.Args, "add"))
			}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				// Marketplace add and plugin install both pattern-match on
				// "install"/"add"; capture only the plugin-install call,
				// which is the one that includes the plugin reference.
				if slices.Contains(args.Args, tc.wantPlugin) {
					ranInstall = true
					capturedArgs = append([]string{}, args.Args...)
				}
				return exec.RunResult{ExitCode: 0}, nil
			})
			// Marketplace-add fallback for hosts that have one.
			runner.When(func(args exec.RunArgs, _ string) bool {
				return slices.Contains(args.Args, "marketplace")
			}).Respond(exec.RunResult{ExitCode: 0})

			// Detector reflects reality: the skill becomes "installed"
			// only after the host's install command runs. This matters
			// for gemini, which pre-detects before installing (and skips
			// a redundant install when already present) — on a fresh
			// install the pre-detect must report not-installed so the
			// install command still runs.
			det := &mockDetector{
				detectToolFn: func(
					_ context.Context, tool *ToolDefinition,
				) (*ToolStatus, error) {
					version := ""
					if ranInstall {
						version = "1.1.70"
					}
					return &ToolStatus{
						Tool:             tool,
						Installed:        ranInstall,
						InstalledVersion: version,
					}, nil
				},
			}

			inst := NewInstaller(
				runner, NewPlatformDetector(runner), det,
			)

			result, err := inst.Install(t.Context(), newSkillTool())
			require.NoError(t, err)
			require.True(t,
				result.Success,
				"expected success; result.Error=%v", result.Error,
			)
			assert.Equal(t, tc.wantHost, result.Strategy)
			assert.Equal(t, "1.1.70", result.InstalledVersion)
			assert.True(t, ranInstall,
				"expected plugin install command to run; args=%v",
				capturedArgs,
			)
		})
	}
}

func TestRunSkill_NoHost_FailsWithSuggestion(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner /* none present */)
	runner.MockToolInPath("node", nil)

	inst := NewInstaller(
		runner, NewPlatformDetector(runner), installedDetector("1.1.70"),
	)

	result, err := inst.Install(t.Context(), newSkillTool())
	require.NoError(t, err)
	require.False(t, result.Success)
	require.NotNil(t, result.Error)

	ews, ok := errors.AsType[*errorhandler.ErrorWithSuggestion](result.Error)
	require.True(t, ok,
		"expected *errorhandler.ErrorWithSuggestion, got %T: %v",
		result.Error, result.Error,
	)

	// All four fields must be populated so the YAML error pipeline
	// does not strip user guidance (AGENTS.md completeness rule).
	assert.NotEmpty(t, ews.Err)
	assert.NotEmpty(t, ews.Message)
	assert.NotEmpty(t, ews.Suggestion)
	assert.NotEmpty(t, ews.Links)

	// Suggestion must point users at the single recommended fix.
	assert.Contains(t, ews.Suggestion, "azd tool install github-copilot-cli")

	// One link — GitHub Copilot CLI install docs.
	require.Len(t, ews.Links, 1)
	assert.Contains(t, ews.Links[0].Title, "GitHub Copilot CLI")
}

// ---------------------------------------------------------------------------
// runSkill — node soft prereq
// ---------------------------------------------------------------------------

func TestRunSkill_NodeMissing_WarnsButProceeds(t *testing.T) {
	// NOT parallel: captureLog mutates the global logger via
	// log.SetOutput, which would race/interfere with other t.Parallel()
	// tests in this package.

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "copilot")
	// node intentionally NOT on PATH — must produce only a warning.
	runner.MockToolInPath("node", errors.New("not found"))

	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "copilot"
	}).Respond(exec.RunResult{ExitCode: 0})

	inst := NewInstaller(
		runner, NewPlatformDetector(runner), installedDetector("1.1.70"),
	)

	var result *InstallResult
	logged := captureLog(t, func() {
		var err error
		result, err = inst.Install(t.Context(), newSkillTool())
		require.NoError(t, err)
	})

	require.True(t,
		result.Success,
		"node missing must NOT block install; result.Error=%v",
		result.Error,
	)
	// The runSkill flow surfaces a stdout warning via output.WithWarningFormat
	// (not captured here); we assert on the log-side diagnostic to confirm
	// the SOFT prereq path executed.
	assert.Contains(t, strings.ToLower(logged), "node not found")
}

// ---------------------------------------------------------------------------
// runSkill — upgrade
// ---------------------------------------------------------------------------

func TestRunSkill_Upgrade_RunsUpdateCommand_NotMarketplaceAdd(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "copilot")
	runner.MockToolInPath("node", nil)

	var sawMarketplaceAdd bool
	var sawPluginUpdate bool
	var sawPluginInstall bool

	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "copilot"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		joined := strings.Join(args.Args, " ")
		switch {
		case strings.Contains(joined, "marketplace add"):
			sawMarketplaceAdd = true
		case strings.HasPrefix(joined, "plugin update"):
			sawPluginUpdate = true
		case strings.HasPrefix(joined, "plugin install"):
			sawPluginInstall = true
		}
		return exec.RunResult{ExitCode: 0}, nil
	})

	inst := NewInstaller(
		runner, NewPlatformDetector(runner), installedDetector("1.1.71"),
	)

	result, err := inst.Upgrade(t.Context(), newSkillTool())
	require.NoError(t, err)
	require.True(t, result.Success, "upgrade must succeed; err=%v", result.Error)

	assert.True(t, sawPluginUpdate, "expected `plugin update` to run on upgrade")
	assert.False(t, sawMarketplaceAdd,
		"marketplace add must NOT run on upgrade (install-only step)")
	assert.False(t, sawPluginInstall,
		"copilot upgrade must not also run plugin install "+
			"(the install-after-update step is codex-specific)")
}

func TestRunSkill_CodexUpgrade_RunsUpdateThenInstall(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "codex")
	runner.MockToolInPath("node", nil)

	var calls []string
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "codex"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		calls = append(calls, strings.Join(args.Args, " "))
		return exec.RunResult{ExitCode: 0}, nil
	})

	inst := NewInstaller(
		runner, NewPlatformDetector(runner), installedDetector("1.1.71"),
	)

	result, err := inst.Upgrade(t.Context(), newSkillTool())
	require.NoError(t, err)
	require.True(t, result.Success, "codex upgrade must succeed; err=%v", result.Error)

	// Codex needs two commands on upgrade: marketplace catalog refresh
	// (PluginUpdateCommand) followed by the install command, since the
	// catalog refresh alone does not land the new version on disk.
	require.GreaterOrEqual(t, len(calls), 2,
		"expected at least 2 codex calls; got %v", calls,
	)
	updateIdx := slices.IndexFunc(calls, func(s string) bool {
		return strings.HasPrefix(s, "plugin marketplace upgrade")
	})
	installIdx := slices.IndexFunc(calls, func(s string) bool {
		return strings.HasPrefix(s, "plugin add")
	})
	require.GreaterOrEqual(t, updateIdx, 0,
		"missing `plugin marketplace upgrade` call; got %v", calls,
	)
	require.GreaterOrEqual(t, installIdx, 0,
		"missing `plugin add` call after update; got %v", calls,
	)
	assert.Less(t, updateIdx, installIdx,
		"update must run BEFORE install; got %v", calls,
	)
}

// ---------------------------------------------------------------------------
// Marketplace add error tolerance
// ---------------------------------------------------------------------------

func TestRunSkill_MarketplaceAlreadyRegistered_StillInstalls(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "copilot")
	runner.MockToolInPath("node", nil)

	var installRan bool

	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "copilot" && slices.Contains(args.Args, "marketplace")
	}).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
		// Mirror real copilot behaviour: exit non-zero with the
		// "already registered" message.
		return exec.RunResult{
				ExitCode: 1,
				Stderr:   "Failed to add marketplace: Error: Marketplace \"azure-skills\" already registered",
			},
			errors.New("exit status 1")
	})

	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "copilot" &&
			!slices.Contains(args.Args, "marketplace") &&
			slices.Contains(args.Args, "install")
	}).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
		installRan = true
		return exec.RunResult{ExitCode: 0}, nil
	})

	inst := NewInstaller(
		runner, NewPlatformDetector(runner), installedDetector("1.1.70"),
	)

	result, err := inst.Install(t.Context(), newSkillTool())
	require.NoError(t, err)
	require.True(t, result.Success,
		"\"already registered\" must be treated as success; err=%v",
		result.Error,
	)
	assert.True(t, installRan,
		"install step must run even when marketplace add reports already-registered",
	)
}

func TestRunSkill_MarketplaceRealError_FailsBeforeInstall(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "copilot")
	runner.MockToolInPath("node", nil)

	var installRan bool

	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "copilot" && slices.Contains(args.Args, "marketplace")
	}).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
		return exec.RunResult{
				ExitCode: 1,
				Stderr:   "Error: failed to fetch https://github.com/...: 404 not found",
			},
			errors.New("exit status 1")
	})

	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "copilot" &&
			!slices.Contains(args.Args, "marketplace")
	}).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
		installRan = true
		return exec.RunResult{ExitCode: 0}, nil
	})

	// Detector reports NOT installed so the runtime cannot rescue a
	// real marketplace-add failure via post-install verification.
	det := &mockDetector{
		detectToolFn: func(_ context.Context, tool *ToolDefinition) (*ToolStatus, error) {
			return &ToolStatus{Tool: tool, Installed: false}, nil
		},
	}

	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	result, err := inst.Install(t.Context(), newSkillTool())
	require.NoError(t, err)
	require.False(t, result.Success)
	require.NotNil(t, result.Error)
	assert.Contains(t, result.Error.Error(), "marketplace")
	assert.False(t, installRan,
		"plugin install must NOT run when marketplace add fails for real",
	)
}

// ---------------------------------------------------------------------------
// Install error surfacing — install command exit code is authoritative.
// ---------------------------------------------------------------------------

func TestRunSkill_InstallCommandFails_SurfacesError(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "gemini")
	runner.MockToolInPath("node", nil)

	wantErr := errors.New("exit status 2")
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "gemini"
	}).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
		return exec.RunResult{ExitCode: 2, Stderr: "Network unreachable"}, wantErr
	})

	// Detector reports NOT installed so gemini's pre-detect proceeds to
	// the install attempt; the non-zero install exit must then surface
	// as the operation's error rather than being swallowed.
	det := &mockDetector{
		detectToolFn: func(_ context.Context, tool *ToolDefinition) (*ToolStatus, error) {
			return &ToolStatus{Tool: tool, Installed: false}, nil
		},
	}

	inst := NewInstaller(
		runner, NewPlatformDetector(runner), det,
	)

	result, err := inst.Install(t.Context(), newSkillTool())
	require.NoError(t, err)
	require.False(t, result.Success)
	require.NotNil(t, result.Error)
	assert.ErrorIs(t, result.Error, wantErr)
}

// ---------------------------------------------------------------------------
// runSkill — gemini idempotent pre-detect (already installed)
// ---------------------------------------------------------------------------

func TestRunSkill_GeminiAlreadyInstalled_SkipsInstall(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "gemini")
	runner.MockToolInPath("node", nil)

	var installRan bool
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "gemini"
	}).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
		installRan = true
		return exec.RunResult{ExitCode: 0}, nil
	})

	// Gemini has no idempotent re-install, so a fresh install pre-detects
	// and treats an already-present skill as a successful no-op without
	// running the install command.
	det := &mockDetector{
		detectToolFn: func(
			_ context.Context, tool *ToolDefinition,
		) (*ToolStatus, error) {
			return &ToolStatus{
				Tool:             tool,
				Installed:        true,
				InstalledVersion: "1.1.70",
			}, nil
		},
	}

	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	result, err := inst.Install(t.Context(), newSkillTool())
	require.NoError(t, err)
	require.True(t, result.Success, "result.Error=%v", result.Error)
	assert.Equal(t, "gemini", result.Strategy)
	assert.Equal(t, "1.1.70", result.InstalledVersion)
	assert.False(t, installRan,
		"gemini install must be skipped when the skill is already present",
	)
}

// ---------------------------------------------------------------------------
// runSkill — no SkillHosts configured
// ---------------------------------------------------------------------------

func TestRunSkill_NoSkillHosts_Errors(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	inst := NewInstaller(runner, NewPlatformDetector(runner), &mockDetector{})

	tool := &ToolDefinition{
		Id:       "empty-skill",
		Name:     "Empty Skill",
		Category: ToolCategorySkill,
		// No SkillHosts configured.
	}

	result, err := inst.Install(t.Context(), tool)
	require.NoError(t, err)
	require.False(t, result.Success)
	require.NotNil(t, result.Error)
	assert.Contains(t, result.Error.Error(), "no SkillHosts configured")
}

// ---------------------------------------------------------------------------
// runSkill — verification detector error is surfaced
// ---------------------------------------------------------------------------

func TestRunSkill_VerifyDetectorError_Surfaces(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "copilot")
	runner.MockToolInPath("node", nil)
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "copilot"
	}).Respond(exec.RunResult{ExitCode: 0})

	wantErr := errors.New("plugin list failed")
	det := &mockDetector{
		detectToolFn: func(
			_ context.Context, _ *ToolDefinition,
		) (*ToolStatus, error) {
			return nil, wantErr
		},
	}

	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	result, err := inst.Install(t.Context(), newSkillTool())
	require.NoError(t, err)
	require.False(t, result.Success)
	require.NotNil(t, result.Error)
	assert.ErrorIs(t, result.Error, wantErr)
	assert.Contains(t, result.Error.Error(), "verifying installation")
}

// ---------------------------------------------------------------------------
// runSkill — install succeeds but verification never confirms
// ---------------------------------------------------------------------------

func TestRunSkill_VerificationFails_AfterRetries(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "copilot")
	runner.MockToolInPath("node", nil)
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "copilot"
	}).Respond(exec.RunResult{ExitCode: 0})

	// Detection never reports the skill installed, so verification
	// exhausts all attempts (1 initial + 3 retries) and fails.
	var callCount atomic.Int32
	det := &mockDetector{
		detectToolFn: func(
			_ context.Context, tool *ToolDefinition,
		) (*ToolStatus, error) {
			callCount.Add(1)
			return &ToolStatus{Tool: tool, Installed: false}, nil
		},
	}

	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	result, err := inst.Install(t.Context(), newSkillTool())
	require.NoError(t, err)
	require.False(t, result.Success)
	require.NotNil(t, result.Error)
	assert.Contains(t, result.Error.Error(), "verification failed")
	assert.Equal(t, int32(4), callCount.Load(),
		"expected 4 detect calls (1 initial + 3 retries)",
	)
}

// ---------------------------------------------------------------------------
// runSkill — context cancellation during verification backoff
// ---------------------------------------------------------------------------

func TestRunSkill_ContextCanceledDuringVerify(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "copilot")
	runner.MockToolInPath("node", nil)
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "copilot"
	}).Respond(exec.RunResult{ExitCode: 0})

	var callCount atomic.Int32
	det := &mockDetector{
		detectToolFn: func(
			_ context.Context, tool *ToolDefinition,
		) (*ToolStatus, error) {
			callCount.Add(1)
			return &ToolStatus{Tool: tool, Installed: false}, nil
		},
	}

	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancel before the first retry sleep

	result, err := inst.Install(ctx, newSkillTool())
	require.NoError(t, err)
	require.False(t, result.Success)
	require.NotNil(t, result.Error)
	assert.Equal(t, int32(1), callCount.Load(),
		"expected only 1 detect call before context cancellation",
	)
	assert.Contains(t, result.Error.Error(), "context canceled")
}
