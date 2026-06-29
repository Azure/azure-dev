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
	"time"

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
	detectSkillHostsFn func(
		ctx context.Context,
		tool *ToolDefinition,
	) ([]InstalledSkillHost, error)
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

func (m *mockDetector) DetectSkillHosts(
	ctx context.Context,
	tool *ToolDefinition,
) ([]InstalledSkillHost, error) {
	if m.detectSkillHostsFn != nil {
		return m.detectSkillHostsFn(ctx, tool)
	}
	// Default (host-agnostic): when detectToolFn reports the skill
	// installed, treat every configured host as having that version. This
	// keeps host-selection tests passing without per-host wiring;
	// host-scoped verification is covered by tests that set
	// detectSkillHostsFn explicitly.
	if m.detectToolFn != nil && tool != nil {
		status, err := m.detectToolFn(ctx, tool)
		if err != nil {
			return nil, err
		}
		if status != nil && status.Installed {
			hosts := make([]InstalledSkillHost, 0, len(tool.SkillHosts))
			for _, h := range tool.SkillHosts {
				hosts = append(hosts, InstalledSkillHost{
					Host:    h.Host,
					Version: status.InstalledVersion,
				})
			}
			return hosts, nil
		}
	}
	return nil, nil
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
	inst.(*installer).retryBackoff = time.Millisecond // keep the test fast

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
var allSkillHostNames = []string{"copilot", "claude"}

// newSkillTool returns a minimal ToolDefinition exercising the
// codepaths covered by these tests. The host commands are simplified
// but preserve the structural distinctions (copilot/claude each with a
// MarketplaceAddCommand).
func newSkillTool() *ToolDefinition {
	return &ToolDefinition{
		Id:       "test-azure-skills",
		Name:     "Test Azure Skills",
		Category: ToolCategorySkill,
		Priority: ToolPriorityRecommended,
		SkillHosts: []SkillHost{
			{
				Host:                   "copilot",
				MarketplaceAddCommand:  []string{"plugin", "marketplace", "add", "microsoft/azure-skills"},
				PluginInstallCommand:   []string{"plugin", "install", "azure@azure-skills"},
				PluginUpdateCommand:    []string{"plugin", "update", "azure@azure-skills"},
				PluginUninstallCommand: []string{"plugin", "uninstall", "azure@azure-skills"},
				PluginListCommand:      []string{"plugin", "list"},
				PluginName:             "azure@azure-skills",
			},
			{
				Host: "claude",
				MarketplaceAddCommand: []string{
					"plugin", "marketplace", "add", "https://github.com/microsoft/azure-skills",
				},
				PluginInstallCommand:   []string{"plugin", "install", "azure@azure-skills"},
				PluginUpdateCommand:    []string{"plugin", "update", "azure@azure-skills"},
				PluginUninstallCommand: []string{"plugin", "uninstall", "azure@azure-skills"},
				PluginListCommand:      []string{"plugin", "list", "--json"},
				PluginName:             "azure@azure-skills",
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

// captureStderr redirects os.Stderr to a temp file for the duration of
// fn and returns everything written to it. NOT parallel-safe: it mutates
// the global os.Stderr, so callers must not use t.Parallel().
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stderr-*.txt")
	require.NoError(t, err)
	prev := os.Stderr
	os.Stderr = f
	t.Cleanup(func() { os.Stderr = prev })
	fn()
	os.Stderr = prev
	require.NoError(t, f.Close())
	data, err := os.ReadFile(f.Name())
	require.NoError(t, err)
	return string(data)
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
			wantPlugin: "azure@azure-skills",
		},
		{
			name:       "AllPresent_PrefersCopilot",
			present:    allSkillHostNames,
			wantHost:   "copilot",
			wantCmd:    "copilot",
			wantPlugin: "azure@azure-skills",
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
			// only after the host's install command runs, so on a fresh
			// install the pre-verify detect reports not-installed and the
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
		runner, NewPlatformDetector(runner),
		&mockDetector{
			detectSkillHostsFn: func(
				_ context.Context, _ *ToolDefinition,
			) ([]InstalledSkillHost, error) {
				return []InstalledSkillHost{{Host: "copilot", Version: "1.1.71"}}, nil
			},
		},
	)

	result, err := inst.Upgrade(t.Context(), newSkillTool())
	require.NoError(t, err)
	require.True(t, result.Success, "upgrade must succeed; err=%v", result.Error)

	assert.True(t, sawPluginUpdate, "expected `plugin update` to run on upgrade")
	assert.False(t, sawMarketplaceAdd,
		"marketplace add must NOT run on upgrade (install-only step)")
	assert.False(t, sawPluginInstall,
		"copilot upgrade must not also run plugin install")
}

// TestRunSkill_Upgrade_PrefersInstalledHost verifies that, with no
// explicit --host, an upgrade targets the host the detector reports the
// skill is installed through — not simply the first host on PATH.
func TestRunSkill_Upgrade_PrefersInstalledHost(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "copilot", "claude") // both on PATH
	runner.MockToolInPath("node", nil)

	var updatedHost string
	runner.When(func(args exec.RunArgs, _ string) bool {
		return (args.Cmd == "copilot" || args.Cmd == "claude") &&
			slices.Contains(args.Args, "update")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		updatedHost = args.Cmd
		return exec.RunResult{ExitCode: 0}, nil
	})

	// The detector reports the skill is installed via claude, even though
	// copilot is listed first; the upgrade must run against claude.
	det := &mockDetector{
		detectSkillHostsFn: func(
			_ context.Context, _ *ToolDefinition,
		) ([]InstalledSkillHost, error) {
			return []InstalledSkillHost{{Host: "claude", Version: "1.1.71"}}, nil
		},
	}

	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	result, err := inst.Upgrade(t.Context(), newSkillTool())
	require.NoError(t, err)
	require.True(t, result.Success, "result.Error=%v", result.Error)
	assert.Equal(t, "claude", result.Strategy)
	assert.Equal(t, "claude", updatedHost,
		"upgrade must target the host that has the skill (claude), "+
			"not the first on PATH (copilot)",
	)
}

// TestRunSkill_Upgrade_AllInstalledHosts verifies that, with no explicit
// --host, an upgrade refreshes EVERY host the skill is installed through
// (not just the first), so a multi-host install is kept fully current.
func TestRunSkill_Upgrade_AllInstalledHosts(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "copilot", "claude")
	runner.MockToolInPath("node", nil)

	var updatedHosts []string
	runner.When(func(args exec.RunArgs, _ string) bool {
		return (args.Cmd == "copilot" || args.Cmd == "claude") &&
			slices.Contains(args.Args, "update")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		updatedHosts = append(updatedHosts, args.Cmd)
		return exec.RunResult{ExitCode: 0}, nil
	})

	// The skill is installed through BOTH copilot and claude.
	det := &mockDetector{
		detectSkillHostsFn: func(
			_ context.Context, _ *ToolDefinition,
		) ([]InstalledSkillHost, error) {
			return []InstalledSkillHost{
				{Host: "copilot", Version: "1.1.71"},
				{Host: "claude", Version: "1.1.71"},
			}, nil
		},
	}

	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	result, err := inst.Upgrade(t.Context(), newSkillTool())
	require.NoError(t, err)
	require.True(t, result.Success, "result.Error=%v", result.Error)
	assert.ElementsMatch(t, []string{"copilot", "claude"}, updatedHosts,
		"upgrade with no --host must refresh every installed host")
	assert.Contains(t, result.Strategy, "copilot")
	assert.Contains(t, result.Strategy, "claude")
}

// TestRunSkill_Upgrade_PrintsPerHostHeader verifies a header line is
// printed before each host's (interactive) command output so the user
// can tell which host the streamed output belongs to.
func TestRunSkill_Upgrade_PrintsPerHostHeader(t *testing.T) {
	// NOT parallel: captureStderr mutates the global os.Stderr.

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "copilot", "claude")
	runner.MockToolInPath("node", nil)

	runner.When(func(args exec.RunArgs, _ string) bool {
		return (args.Cmd == "copilot" || args.Cmd == "claude") &&
			slices.Contains(args.Args, "update")
	}).Respond(exec.RunResult{ExitCode: 0})

	det := &mockDetector{
		detectSkillHostsFn: func(
			_ context.Context, _ *ToolDefinition,
		) ([]InstalledSkillHost, error) {
			return []InstalledSkillHost{
				{Host: "copilot", Version: "1.1.71"},
				{Host: "claude", Version: "1.1.71"},
			}, nil
		},
	}

	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	var result *InstallResult
	stderr := captureStderr(t, func() {
		var err error
		result, err = inst.Upgrade(t.Context(), newSkillTool())
		require.NoError(t, err)
	})

	require.True(t, result.Success, "result.Error=%v", result.Error)
	assert.Contains(t, stderr, "Upgrading Test Azure Skills in copilot")
	assert.Contains(t, stderr, "Upgrading Test Azure Skills in claude")
}

// TestRunSkill_Upgrade_NoHost_NotInstalled_ReturnsInstallGuidance
// verifies that `azd tool upgrade <skill>` with no --host, when the
// skill is installed on no available host, returns a clear "install
// first" guidance error instead of falling through to a host and
// attempting to update a plugin that was never installed (which used to
// produce a confusing "verification failed" error).
func TestRunSkill_Upgrade_NoHost_NotInstalled_ReturnsInstallGuidance(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "copilot", "claude") // both on PATH
	runner.MockToolInPath("node", nil)

	updateRan := false
	runner.When(func(args exec.RunArgs, _ string) bool {
		return (args.Cmd == "copilot" || args.Cmd == "claude") &&
			slices.Contains(args.Args, "update")
	}).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
		updateRan = true
		return exec.RunResult{ExitCode: 0}, nil
	})

	// The skill is installed on no host.
	det := &mockDetector{
		detectSkillHostsFn: func(
			_ context.Context, _ *ToolDefinition,
		) ([]InstalledSkillHost, error) {
			return nil, nil
		},
	}

	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	result, err := inst.Upgrade(t.Context(), newSkillTool())
	require.NoError(t, err)
	require.False(t, result.Success)
	assert.False(t, updateRan,
		"must not attempt to update a skill that is not installed")

	ews, ok := errors.AsType[*errorhandler.ErrorWithSuggestion](result.Error)
	require.True(t, ok,
		"expected *errorhandler.ErrorWithSuggestion, got %T: %v",
		result.Error, result.Error,
	)
	assert.NotEmpty(t, ews.Err)
	assert.NotEmpty(t, ews.Message)
	assert.NotEmpty(t, ews.Suggestion)
	assert.Contains(t, result.Error.Error(), "not installed on any available host")
	assert.Contains(t, ews.Suggestion, "azd tool install test-azure-skills")
}

// TestRunSkill_Upgrade_AllAvailable_SkipsNotInstalled verifies that
// `upgrade --host all` upgrades only the hosts that actually have the
// skill installed and skips on-PATH-but-not-installed hosts (with a
// warning to stderr) rather than erroring — a host CLI cannot upgrade a
// plugin it never installed.
func TestRunSkill_Upgrade_AllAvailable_SkipsNotInstalled(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "copilot", "claude") // both on PATH
	runner.MockToolInPath("node", nil)

	var updatedHosts []string
	runner.When(func(args exec.RunArgs, _ string) bool {
		return (args.Cmd == "copilot" || args.Cmd == "claude") &&
			slices.Contains(args.Args, "update")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		updatedHosts = append(updatedHosts, args.Cmd)
		return exec.RunResult{ExitCode: 0}, nil
	})

	// Only copilot has the skill installed.
	det := &mockDetector{
		detectSkillHostsFn: func(
			_ context.Context, _ *ToolDefinition,
		) ([]InstalledSkillHost, error) {
			return []InstalledSkillHost{{Host: "copilot", Version: "1.1.71"}}, nil
		},
	}

	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	result, err := inst.Upgrade(t.Context(), newSkillTool(), WithAllAvailableHosts())
	require.NoError(t, err)
	require.True(t, result.Success,
		"upgrade must succeed by skipping the not-installed host; err=%v",
		result.Error)
	assert.Equal(t, []string{"copilot"}, updatedHosts,
		"only the installed host (copilot) must be upgraded; claude skipped")
}

// TestRunSkill_Upgrade_AllAvailable_NoneInstalled verifies that
// `upgrade --host all` errors with a clear "not installed on any host"
// message when the skill is present on no host (and runs no update).
func TestRunSkill_Upgrade_AllAvailable_NoneInstalled(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "copilot", "claude")
	runner.MockToolInPath("node", nil)

	var updateRan bool
	runner.When(func(args exec.RunArgs, _ string) bool {
		return slices.Contains(args.Args, "update")
	}).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
		updateRan = true
		return exec.RunResult{ExitCode: 0}, nil
	})

	det := &mockDetector{
		detectSkillHostsFn: func(
			_ context.Context, _ *ToolDefinition,
		) ([]InstalledSkillHost, error) {
			return nil, nil // not installed anywhere
		},
	}

	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	result, err := inst.Upgrade(t.Context(), newSkillTool(), WithAllAvailableHosts())
	require.NoError(t, err)
	require.False(t, result.Success)
	require.NotNil(t, result.Error)
	assert.Contains(t, result.Error.Error(), "not installed on any available host")
	assert.False(t, updateRan, "no host update must run when nothing is installed")
}

// TestRunSkill_Install_AllAvailable_TargetsEveryHostOnPath verifies that
// `install --host all` installs through every host on PATH regardless of
// prior install state (unlike upgrade, install does not skip).
func TestRunSkill_Install_AllAvailable_TargetsEveryHostOnPath(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "copilot", "claude")
	runner.MockToolInPath("node", nil)

	var installedHosts []string
	runner.When(func(args exec.RunArgs, _ string) bool {
		return (args.Cmd == "copilot" || args.Cmd == "claude") &&
			(slices.Contains(args.Args, "install") || slices.Contains(args.Args, "marketplace"))
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		if slices.Contains(args.Args, "install") {
			installedHosts = append(installedHosts, args.Cmd)
		}
		return exec.RunResult{ExitCode: 0}, nil
	})

	det := &mockDetector{
		detectSkillHostsFn: func(
			_ context.Context, _ *ToolDefinition,
		) ([]InstalledSkillHost, error) {
			// Reflects post-install state for verification.
			return []InstalledSkillHost{
				{Host: "copilot", Version: "1.1.71"},
				{Host: "claude", Version: "1.1.71"},
			}, nil
		},
	}

	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	result, err := inst.Install(t.Context(), newSkillTool(), WithAllAvailableHosts())
	require.NoError(t, err)
	require.True(t, result.Success, "result.Error=%v", result.Error)
	assert.ElementsMatch(t, []string{"copilot", "claude"}, installedHosts,
		"install --host all must install through every host on PATH")
}

// TestRunSkill_Install_HostScopedVerification verifies that install
// verification is scoped to the host just installed: if claude's install
// silently no-ops while copilot already has the skill, verification must
// FAIL — it must not falsely succeed on copilot's presence (the prior
// DetectTool-based check did, reporting the wrong host's version).
func TestRunSkill_Install_HostScopedVerification(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "copilot", "claude")
	runner.MockToolInPath("node", nil)
	// Both hosts' commands exit 0 (claude's install silently no-ops).
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "copilot" || args.Cmd == "claude"
	}).Respond(exec.RunResult{ExitCode: 0})

	// Detector reports the skill installed ONLY via copilot — never claude.
	det := &mockDetector{
		detectSkillHostsFn: func(
			_ context.Context, _ *ToolDefinition,
		) ([]InstalledSkillHost, error) {
			return []InstalledSkillHost{{Host: "copilot", Version: "1.1.71"}}, nil
		},
	}

	inst := NewInstaller(runner, NewPlatformDetector(runner), det)
	inst.(*installer).retryBackoff = time.Millisecond // keep the test fast

	// Install via claude: claude is not actually registered, so
	// verification must fail rather than pass on copilot's presence.
	result, err := inst.Install(t.Context(), newSkillTool(), WithHosts("claude"))
	require.NoError(t, err)
	require.False(t, result.Success,
		"claude install must NOT be verified by copilot's presence")
	require.NotNil(t, result.Error)
	assert.Contains(t, result.Error.Error(), "claude")
}

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
	mockHostPresence(runner, "claude")
	runner.MockToolInPath("node", nil)

	wantErr := errors.New("exit status 2")
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "claude"
	}).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
		return exec.RunResult{ExitCode: 2, Stderr: "Network unreachable"}, wantErr
	})

	// Detector reports NOT installed; the non-zero install exit must
	// surface as the operation's error rather than being swallowed.
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
	inst.(*installer).retryBackoff = time.Millisecond // keep the test fast

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

// ---------------------------------------------------------------------------
// runSkill — explicit host selection (WithHosts)
// ---------------------------------------------------------------------------

func TestRunSkill_ExplicitHosts_InstallsEachHost(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "copilot", "claude")
	runner.MockToolInPath("node", nil)

	installed := map[string]bool{}
	runner.When(func(args exec.RunArgs, _ string) bool {
		return (args.Cmd == "copilot" || args.Cmd == "claude") &&
			slices.Contains(args.Args, "install") &&
			!slices.Contains(args.Args, "marketplace")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		installed[args.Cmd] = true
		return exec.RunResult{ExitCode: 0}, nil
	})
	// Marketplace-add fallback for hosts that have one.
	runner.When(func(args exec.RunArgs, _ string) bool {
		return slices.Contains(args.Args, "marketplace")
	}).Respond(exec.RunResult{ExitCode: 0})

	inst := NewInstaller(
		runner, NewPlatformDetector(runner), installedDetector("1.1.70"),
	)

	result, err := inst.Install(
		t.Context(), newSkillTool(), WithHosts("copilot", "claude"),
	)
	require.NoError(t, err)
	require.True(t, result.Success, "result.Error=%v", result.Error)
	assert.Equal(t, "copilot, claude", result.Strategy)
	assert.Equal(t, "1.1.70", result.InstalledVersion)
	assert.True(t, installed["copilot"], "copilot install must run")
	assert.True(t, installed["claude"], "claude install must run")
}

func TestRunSkill_ExplicitUnknownHost_Errors(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "copilot")
	runner.MockToolInPath("node", nil)

	inst := NewInstaller(
		runner, NewPlatformDetector(runner), installedDetector("1.1.70"),
	)

	result, err := inst.Install(
		t.Context(), newSkillTool(), WithHosts("bogus"),
	)
	require.NoError(t, err)
	require.False(t, result.Success)
	require.NotNil(t, result.Error)
	assert.Contains(t, result.Error.Error(), "not available")
	// The error names the requested host so a typo is obvious.
	assert.Contains(t, result.Error.Error(), "bogus")
}

func TestRunSkill_ExplicitHostNotPresent_Errors(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "copilot") // claude deliberately NOT on PATH
	runner.MockToolInPath("node", nil)

	inst := NewInstaller(
		runner, NewPlatformDetector(runner), installedDetector("1.1.70"),
	)

	result, err := inst.Install(
		t.Context(), newSkillTool(), WithHosts("claude"),
	)
	require.NoError(t, err)
	require.False(t, result.Success)
	require.NotNil(t, result.Error)
	assert.Contains(t, result.Error.Error(), "not available")
	// The error names the requested host so the user knows which one failed.
	assert.Contains(t, result.Error.Error(), "claude")
}

// TestRunSkill_Upgrade_DetectError_Propagated verifies that a context
// cancellation/timeout from DetectSkillHosts on the default upgrade path
// is treated as fatal rather than silently falling back to a host.
func TestRunSkill_Upgrade_DetectError_Propagated(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "copilot")
	runner.MockToolInPath("node", nil)

	det := &mockDetector{
		detectSkillHostsFn: func(
			_ context.Context, _ *ToolDefinition,
		) ([]InstalledSkillHost, error) {
			return nil, context.Canceled
		},
	}

	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	result, err := inst.Upgrade(t.Context(), newSkillTool())
	require.NoError(t, err)
	require.False(t, result.Success)
	require.ErrorIs(t, result.Error, context.Canceled)
}

// TestRunSkill_UpgradeAllHosts_DetectError_Propagated verifies the same
// for the --host all upgrade path.
func TestRunSkill_UpgradeAllHosts_DetectError_Propagated(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "copilot", "claude")
	runner.MockToolInPath("node", nil)

	det := &mockDetector{
		detectSkillHostsFn: func(
			_ context.Context, _ *ToolDefinition,
		) ([]InstalledSkillHost, error) {
			return nil, context.Canceled
		},
	}

	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	result, err := inst.Upgrade(
		t.Context(), newSkillTool(), WithAllAvailableHosts(),
	)
	require.NoError(t, err)
	require.False(t, result.Success)
	require.ErrorIs(t, result.Error, context.Canceled)
}

// ---------------------------------------------------------------------------
// AvailableSkillHosts
// ---------------------------------------------------------------------------

func TestAvailableSkillHosts_ReturnsPresentInManifestOrder(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	// Present out of manifest order; result must follow manifest order
	// (copilot, claude).
	mockHostPresence(runner, "claude", "copilot")

	inst := NewInstaller(runner, NewPlatformDetector(runner), &mockDetector{})

	assert.Equal(t,
		[]string{"copilot", "claude"},
		inst.AvailableSkillHosts(newSkillTool()),
	)
}

func TestAvailableSkillHosts_NonSkillToolReturnsNil(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	inst := NewInstaller(runner, NewPlatformDetector(runner), &mockDetector{})

	assert.Nil(t, inst.AvailableSkillHosts(&ToolDefinition{
		Id:       "not-a-skill",
		Category: ToolCategoryServer,
	}))
}

// ---------------------------------------------------------------------------
// Uninstall — non-skill (package manager) path
// ---------------------------------------------------------------------------

// TestUninstall_NonSkill_UsesPackageManagerUninstall verifies the
// package-manager removal path (npm), distinct from the explicit
// UninstallCommand path covered by TestUninstall_UsesUninstallCommand.
func TestUninstall_NonSkill_UsesPackageManagerUninstall(t *testing.T) {
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
		return args.Cmd == "npm" && slices.Contains(args.Args, "--version")
	}).Respond(exec.RunResult{ExitCode: 0, Stdout: "10.2.0"})

	// Capture the uninstall command.
	var capturedCmd string
	var capturedArgs []string
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "npm" && slices.Contains(args.Args, "uninstall")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		capturedCmd = args.Cmd
		capturedArgs = args.Args
		return exec.RunResult{ExitCode: 0}, nil
	})

	// Detector reports the tool gone, so removal verification succeeds.
	det := &mockDetector{
		detectToolFn: func(
			_ context.Context, tool *ToolDefinition,
		) (*ToolStatus, error) {
			return &ToolStatus{Tool: tool, Installed: false}, nil
		},
	}

	pd := NewPlatformDetector(runner)
	inst := NewInstaller(runner, pd, det)

	tool := &ToolDefinition{
		Id:       "test-npm-tool",
		Name:     "Test NPM Tool",
		Category: ToolCategoryCLI,
		InstallStrategies: allPlatforms(InstallStrategy{
			PackageManager: "npm",
			PackageId:      "@test/tool",
		}),
	}

	result, err := inst.Uninstall(t.Context(), tool)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Success)
	assert.Equal(t, "npm", capturedCmd)
	assert.Contains(t, capturedArgs, "uninstall")
	assert.Contains(t, capturedArgs, "@test/tool")
}

func TestUninstall_NonSkill_StillDetectedReportsFailure(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	for _, managers := range platformManagers {
		for _, mgr := range managers {
			runner.MockToolInPath(mgr, errors.New("not found"))
		}
	}
	runner.MockToolInPath("npm", nil)
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "npm" && slices.Contains(args.Args, "--version")
	}).Respond(exec.RunResult{ExitCode: 0, Stdout: "10.2.0"})
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "npm" && slices.Contains(args.Args, "uninstall")
	}).Respond(exec.RunResult{ExitCode: 0})

	// Tool remains detected after the uninstall command runs.
	det := installedDetector("1.0.0")

	pd := NewPlatformDetector(runner)
	inst := NewInstaller(runner, pd, det)

	tool := &ToolDefinition{
		Id:       "test-npm-tool",
		Name:     "Test NPM Tool",
		Category: ToolCategoryCLI,
		InstallStrategies: allPlatforms(InstallStrategy{
			PackageManager: "npm",
			PackageId:      "@test/tool",
		}),
	}

	result, err := inst.Uninstall(t.Context(), tool)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Success)
	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "still detected")
}

// TestUninstall_NonSkill_PackageManagerNoRecord_GuidesManualRemoval covers
// the case where the package manager fails to remove the tool because it no
// longer has a record of the package (e.g. a self-updating CLI replaced the
// manager-installed copy) yet azd still detects the tool. The user must get
// actionable manual-removal guidance rather than the raw package-manager
// error.
func TestUninstall_NonSkill_PackageManagerNoRecord_GuidesManualRemoval(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	for _, managers := range platformManagers {
		for _, mgr := range managers {
			runner.MockToolInPath(mgr, errors.New("not found"))
		}
	}
	runner.MockToolInPath("npm", nil)
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "npm" && slices.Contains(args.Args, "--version")
	}).Respond(exec.RunResult{ExitCode: 0, Stdout: "10.2.0"})
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "npm" && slices.Contains(args.Args, "uninstall")
	}).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
		return exec.RunResult{
			ExitCode: 1,
			Stdout:   "not installed",
		}, errors.New("exit status 1")
	})

	// The tool is still detected after the failed uninstall.
	det := installedDetector("1.0.0")

	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	tool := &ToolDefinition{
		Id:   "self-updating-cli",
		Name: "Self Updating CLI",
		// A DetectCommand that does not resolve on PATH keeps the
		// suggestion text deterministic (no machine-specific path).
		DetectCommand: "azd-nonexistent-cli-xyz",
		Category:      ToolCategoryCLI,
		InstallStrategies: allPlatforms(InstallStrategy{
			PackageManager: "npm",
			PackageId:      "@test/self-updating",
		}),
	}

	result, err := inst.Uninstall(t.Context(), tool)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Success)

	var ews *errorhandler.ErrorWithSuggestion
	require.ErrorAs(t, result.Error, &ews)
	assert.Contains(t, ews.Suggestion, "no longer has a record")
	assert.Contains(t, ews.Suggestion, "remove Self Updating CLI manually")
}

// TestUninstall_NonSkill_PackageManagerNoRecord_AlreadyGone_Succeeds covers
// the idempotent case: the package manager reports a failure but the tool is
// no longer detected, so the uninstall is already effectively complete.
func TestUninstall_NonSkill_PackageManagerNoRecord_AlreadyGone_Succeeds(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	for _, managers := range platformManagers {
		for _, mgr := range managers {
			runner.MockToolInPath(mgr, errors.New("not found"))
		}
	}
	runner.MockToolInPath("npm", nil)
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "npm" && slices.Contains(args.Args, "--version")
	}).Respond(exec.RunResult{ExitCode: 0, Stdout: "10.2.0"})
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "npm" && slices.Contains(args.Args, "uninstall")
	}).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
		return exec.RunResult{
			ExitCode: 1,
			Stdout:   "not installed",
		}, errors.New("exit status 1")
	})

	// The tool is already gone — detection reports not installed.
	det := &mockDetector{
		detectToolFn: func(_ context.Context, tool *ToolDefinition) (*ToolStatus, error) {
			return &ToolStatus{Tool: tool, Installed: false}, nil
		},
	}

	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	tool := &ToolDefinition{
		Id:            "already-gone-cli",
		Name:          "Already Gone CLI",
		DetectCommand: "azd-nonexistent-cli-xyz",
		Category:      ToolCategoryCLI,
		InstallStrategies: allPlatforms(InstallStrategy{
			PackageManager: "npm",
			PackageId:      "@test/already-gone",
		}),
	}

	result, err := inst.Uninstall(t.Context(), tool)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Success,
		"uninstall must be idempotent when the tool is already gone")
	assert.NoError(t, result.Error)
}

func TestUninstall_NonSkill_NoPackageManagerIsUnsupported(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	for _, managers := range platformManagers {
		for _, mgr := range managers {
			runner.MockToolInPath(mgr, errors.New("not found"))
		}
	}

	pd := NewPlatformDetector(runner)
	inst := NewInstaller(runner, pd, &mockDetector{})

	// Strategy installs via a custom shell command, which has no
	// automated uninstall path.
	tool := &ToolDefinition{
		Id:       "custom-tool",
		Name:     "Custom Tool",
		Category: ToolCategoryCLI,
		InstallStrategies: allPlatforms(InstallStrategy{
			InstallCommand: "curl -sL https://example.com/install.sh | bash",
		}),
	}

	result, err := inst.Uninstall(t.Context(), tool)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Success)
	require.Error(t, result.Error)
	assert.Equal(t, "manual", result.Strategy)
}

func TestUninstall_UsesUninstallCommand(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	for _, managers := range platformManagers {
		for _, mgr := range managers {
			runner.MockToolInPath(mgr, errors.New("not found"))
		}
	}

	// Capture the explicit uninstall command (e.g. azd extension uninstall).
	var capturedCmd string
	var capturedArgs []string
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "azd" && slices.Contains(args.Args, "uninstall")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		capturedCmd = args.Cmd
		capturedArgs = args.Args
		return exec.RunResult{ExitCode: 0}, nil
	})

	// Tool reported gone after the uninstall command runs.
	det := &mockDetector{
		detectToolFn: func(
			_ context.Context, tool *ToolDefinition,
		) (*ToolStatus, error) {
			return &ToolStatus{Tool: tool, Installed: false}, nil
		},
	}

	pd := NewPlatformDetector(runner)
	inst := NewInstaller(runner, pd, det)

	// An azd-extension-style tool: install/uninstall via explicit azd
	// commands, with no package manager.
	tool := &ToolDefinition{
		Id:       "azure.ai.agents",
		Name:     "azd AI Agent Extensions",
		Category: ToolCategoryAzdExtension,
		InstallStrategies: allPlatforms(InstallStrategy{
			InstallCommand:   "azd extension install azure.ai.agents --source azd",
			UninstallCommand: "azd extension uninstall azure.ai.agents",
		}),
	}

	result, err := inst.Uninstall(t.Context(), tool)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Success)
	assert.Equal(t, "command", result.Strategy)
	assert.Equal(t, "azd", capturedCmd)
	assert.Contains(t, capturedArgs, "extension")
	assert.Contains(t, capturedArgs, "uninstall")
	assert.Contains(t, capturedArgs, "azure.ai.agents")
}

// TestUninstall_UninstallCommandFails_AlreadyGone_Succeeds covers the
// idempotent case for the explicit UninstallCommand path: the command exits
// non-zero but the tool is no longer detected, so the uninstall is treated as
// already complete (mirrors the package-manager path).
func TestUninstall_UninstallCommandFails_AlreadyGone_Succeeds(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	for _, managers := range platformManagers {
		for _, mgr := range managers {
			runner.MockToolInPath(mgr, errors.New("not found"))
		}
	}
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "azd" && slices.Contains(args.Args, "uninstall")
	}).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
		return exec.RunResult{ExitCode: 1}, errors.New("extension not installed")
	})

	// Tool already not installed.
	det := &mockDetector{
		detectToolFn: func(
			_ context.Context, tool *ToolDefinition,
		) (*ToolStatus, error) {
			return &ToolStatus{Tool: tool, Installed: false}, nil
		},
	}
	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	tool := &ToolDefinition{
		Id:       "azure.ai.agents",
		Name:     "azd AI Agent Extensions",
		Category: ToolCategoryAzdExtension,
		InstallStrategies: allPlatforms(InstallStrategy{
			UninstallCommand: "azd extension uninstall azure.ai.agents",
		}),
	}

	result, err := inst.Uninstall(t.Context(), tool)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Success)
}

// TestUninstall_UninstallCommandFails_StillPresent_Errors covers the
// non-idempotent case: the command fails and the tool is still detected, so
// the failure is surfaced.
func TestUninstall_UninstallCommandFails_StillPresent_Errors(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	for _, managers := range platformManagers {
		for _, mgr := range managers {
			runner.MockToolInPath(mgr, errors.New("not found"))
		}
	}
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "azd" && slices.Contains(args.Args, "uninstall")
	}).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
		return exec.RunResult{ExitCode: 1}, errors.New("azd boom")
	})

	// Tool still detected after the failed command.
	det := installedDetector("1.0.0")
	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	tool := &ToolDefinition{
		Id:       "azure.ai.agents",
		Name:     "azd AI Agent Extensions",
		Category: ToolCategoryAzdExtension,
		InstallStrategies: allPlatforms(InstallStrategy{
			UninstallCommand: "azd extension uninstall azure.ai.agents",
		}),
	}

	result, err := inst.Uninstall(t.Context(), tool)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Success)
	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "running uninstall command")
}

func TestBuildUninstallCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		manager   string
		packageID string
		expectCmd string
		expectArg string // action keyword to find in args
	}{
		{"Winget", "winget", "Microsoft.AzureCLI", "winget", "uninstall"},
		{"Brew", "brew", "azure-cli", "brew", "uninstall"},
		{"Apt", "apt", "azure-cli", "sudo", "remove"},
		{"Npm", "npm", "@azure/mcp", "npm", "uninstall"},
		{"Code", "code", "ms-azuretools.vscode-bicep", "code", "--uninstall-extension"},
		{"UnknownManagerReturnsEmpty", "unknown-mgr", "pkg", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cmd, args := buildUninstallCommand(tt.manager, tt.packageID)

			assert.Equal(t, tt.expectCmd, cmd)
			if tt.expectArg != "" {
				assert.Contains(t, args, tt.expectArg)
				assert.Contains(t, args, tt.packageID)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Uninstall — skill (per-host) path
// ---------------------------------------------------------------------------

func TestUninstallSkill_RemovesFromInstalledHosts(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "copilot", "claude")

	// Capture which hosts ran an uninstall command.
	var uninstalledHosts []string
	runner.When(func(args exec.RunArgs, _ string) bool {
		return slices.Contains(args.Args, "uninstall")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		uninstalledHosts = append(uninstalledHosts, args.Cmd)
		return exec.RunResult{ExitCode: 0}, nil
	})

	// Installed on both hosts before uninstall; gone after.
	var uninstallStarted bool
	det := &mockDetector{
		detectSkillHostsFn: func(
			_ context.Context, tool *ToolDefinition,
		) ([]InstalledSkillHost, error) {
			if uninstallStarted {
				return nil, nil
			}
			uninstallStarted = true
			return []InstalledSkillHost{
				{Host: "copilot", Version: "1.0.0"},
				{Host: "claude", Version: "1.0.0"},
			}, nil
		},
	}

	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	result, err := inst.Uninstall(t.Context(), newSkillTool())

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Success)
	assert.ElementsMatch(t, []string{"copilot", "claude"}, uninstalledHosts)
}

func TestUninstallSkill_ExplicitHost_RemovesOnlyThatHost(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "copilot", "claude")

	var uninstalledHosts []string
	runner.When(func(args exec.RunArgs, _ string) bool {
		return slices.Contains(args.Args, "uninstall")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		uninstalledHosts = append(uninstalledHosts, args.Cmd)
		return exec.RunResult{ExitCode: 0}, nil
	})

	// After uninstall, the skill is gone from copilot (the explicit host).
	det := &mockDetector{
		detectSkillHostsFn: func(
			_ context.Context, tool *ToolDefinition,
		) ([]InstalledSkillHost, error) {
			return []InstalledSkillHost{{Host: "claude", Version: "1.0.0"}}, nil
		},
	}

	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	result, err := inst.Uninstall(t.Context(), newSkillTool(), WithHosts("copilot"))

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Success)
	assert.Equal(t, []string{"copilot"}, uninstalledHosts)
}

func TestUninstallSkill_NotInstalledAnywhere_Errors(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "copilot", "claude")

	// Skill is not installed on any host.
	det := &mockDetector{
		detectSkillHostsFn: func(
			_ context.Context, _ *ToolDefinition,
		) ([]InstalledSkillHost, error) {
			return nil, nil
		},
	}

	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	result, err := inst.Uninstall(t.Context(), newSkillTool())

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Success)
	require.Error(t, result.Error)

	var suggestion *errorhandler.ErrorWithSuggestion
	require.ErrorAs(t, result.Error, &suggestion)
	assert.Contains(t, suggestion.Suggestion, "nothing to uninstall")
}

func TestUninstallSkill_ExplicitUnknownHost_Errors(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "copilot", "claude")

	inst := NewInstaller(runner, NewPlatformDetector(runner), &mockDetector{})

	result, err := inst.Uninstall(t.Context(), newSkillTool(), WithHosts("bogus"))

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Success)
	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "not available")
}

// ---------------------------------------------------------------------------
// Uninstall — additional non-skill branch coverage
// ---------------------------------------------------------------------------

// allManagersMissing mocks every known package manager as absent from PATH.
func allManagersMissing(runner *mockexec.MockCommandRunner) {
	for _, managers := range platformManagers {
		for _, mgr := range managers {
			runner.MockToolInPath(mgr, errors.New("not found"))
		}
	}
}

func TestUninstall_DirectDownload_RemovesArtifact(t *testing.T) {
	// Not parallel: mutates AZD_CONFIG_DIR via t.Setenv.
	cfgDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", cfgDir)

	// Place the artifact where executeDirectDownload would have put it.
	toolsDir := filepath.Join(cfgDir, "tools")
	require.NoError(t, os.MkdirAll(toolsDir, 0o755))
	artifact := filepath.Join(toolsDir, "tool.bin")
	require.NoError(t, os.WriteFile(artifact, []byte("binary"), 0o600))

	runner := mockexec.NewMockCommandRunner()
	allManagersMissing(runner)

	det := &mockDetector{
		detectToolFn: func(
			_ context.Context, tool *ToolDefinition,
		) (*ToolStatus, error) {
			return &ToolStatus{Tool: tool, Installed: false}, nil
		},
	}
	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	toolDef := &ToolDefinition{
		Id:       "direct-dl",
		Name:     "Direct Download Tool",
		Category: ToolCategoryCLI,
		InstallStrategies: allPlatforms(InstallStrategy{
			DirectDownloadUrl: "https://example.com/tool.bin",
		}),
	}

	result, err := inst.Uninstall(t.Context(), toolDef)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Success)
	assert.Equal(t, "direct-download", result.Strategy)
	assert.NoFileExists(t, artifact)
}

func TestUninstall_DirectDownload_MissingFileIsIdempotent(t *testing.T) {
	// Not parallel: mutates AZD_CONFIG_DIR via t.Setenv.
	cfgDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", cfgDir)
	// No artifact file is created — uninstall must still succeed.

	runner := mockexec.NewMockCommandRunner()
	allManagersMissing(runner)

	det := &mockDetector{
		detectToolFn: func(
			_ context.Context, tool *ToolDefinition,
		) (*ToolStatus, error) {
			return &ToolStatus{Tool: tool, Installed: false}, nil
		},
	}
	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	toolDef := &ToolDefinition{
		Id:       "direct-dl",
		Name:     "Direct Download Tool",
		Category: ToolCategoryCLI,
		InstallStrategies: allPlatforms(InstallStrategy{
			DirectDownloadUrl: "https://example.com/tool.bin",
		}),
	}

	result, err := inst.Uninstall(t.Context(), toolDef)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Success)
}

func TestUninstall_NonSkill_ManagerUnavailable(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	allManagersMissing(runner) // npm not on PATH either

	inst := NewInstaller(runner, NewPlatformDetector(runner), &mockDetector{})

	tool := &ToolDefinition{
		Id:       "test-npm-tool",
		Name:     "Test NPM Tool",
		Category: ToolCategoryCLI,
		InstallStrategies: allPlatforms(InstallStrategy{
			PackageManager: "npm",
			PackageId:      "@test/tool",
		}),
	}

	result, err := inst.Uninstall(t.Context(), tool)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Success)
	assert.Equal(t, "manual", result.Strategy)

	var suggestion *errorhandler.ErrorWithSuggestion
	require.ErrorAs(t, result.Error, &suggestion)
	assert.Contains(t, suggestion.Message, "Cannot uninstall")
}

func TestUninstall_NoStrategyForPlatform(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	allManagersMissing(runner)

	inst := NewInstaller(runner, NewPlatformDetector(runner), &mockDetector{})

	// A strategy that does not cover the current OS.
	tool := &ToolDefinition{
		Id:       "no-strategy",
		Name:     "No Strategy Tool",
		Category: ToolCategoryCLI,
		InstallStrategies: map[string]InstallStrategy{
			"plan9": {PackageManager: "npm", PackageId: "x"},
		},
	}

	result, err := inst.Uninstall(t.Context(), tool)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Success)
	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "no install strategy")
}

func TestUninstall_NonSkill_CommandFails(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	allManagersMissing(runner)
	runner.MockToolInPath("npm", nil)
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "npm" && slices.Contains(args.Args, "--version")
	}).Respond(exec.RunResult{ExitCode: 0, Stdout: "10.2.0"})
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "npm" && slices.Contains(args.Args, "uninstall")
	}).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
		return exec.RunResult{ExitCode: 1}, errors.New("npm boom")
	})

	inst := NewInstaller(runner, NewPlatformDetector(runner), installedDetector("1.0.0"))

	tool := &ToolDefinition{
		Id:       "test-npm-tool",
		Name:     "Test NPM Tool",
		Category: ToolCategoryCLI,
		InstallStrategies: allPlatforms(InstallStrategy{
			PackageManager: "npm",
			PackageId:      "@test/tool",
		}),
	}

	result, err := inst.Uninstall(t.Context(), tool)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Success)
	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "running uninstall command")
}

func TestUninstall_NonSkill_VerifyDetectError(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	allManagersMissing(runner)
	runner.MockToolInPath("npm", nil)
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "npm" && slices.Contains(args.Args, "--version")
	}).Respond(exec.RunResult{ExitCode: 0, Stdout: "10.2.0"})
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "npm" && slices.Contains(args.Args, "uninstall")
	}).Respond(exec.RunResult{ExitCode: 0})

	wantErr := errors.New("detect boom")
	det := &mockDetector{
		detectToolFn: func(
			_ context.Context, _ *ToolDefinition,
		) (*ToolStatus, error) {
			return nil, wantErr
		},
	}
	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	tool := &ToolDefinition{
		Id:       "test-npm-tool",
		Name:     "Test NPM Tool",
		Category: ToolCategoryCLI,
		InstallStrategies: allPlatforms(InstallStrategy{
			PackageManager: "npm",
			PackageId:      "@test/tool",
		}),
	}

	result, err := inst.Uninstall(t.Context(), tool)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Success)
	require.Error(t, result.Error)
	assert.ErrorIs(t, result.Error, wantErr)
	assert.Contains(t, result.Error.Error(), "verifying removal")
}

// ---------------------------------------------------------------------------
// Uninstall — additional skill branch coverage
// ---------------------------------------------------------------------------

func TestUninstallSkill_NoSkillHosts_Errors(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	inst := NewInstaller(runner, NewPlatformDetector(runner), &mockDetector{})

	tool := &ToolDefinition{
		Id:       "skill-no-hosts",
		Name:     "Skill No Hosts",
		Category: ToolCategorySkill,
		// No SkillHosts configured.
	}

	result, err := inst.Uninstall(t.Context(), tool)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Success)
	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "no SkillHosts configured")
}

func TestUninstallSkill_MultipleHostFailures_Summarized(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "copilot", "claude")
	runner.When(func(args exec.RunArgs, _ string) bool {
		return slices.Contains(args.Args, "uninstall")
	}).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
		return exec.RunResult{ExitCode: 1}, errors.New("remove failed")
	})

	det := &mockDetector{
		detectSkillHostsFn: func(
			_ context.Context, _ *ToolDefinition,
		) ([]InstalledSkillHost, error) {
			return []InstalledSkillHost{
				{Host: "copilot", Version: "1.0.0"},
				{Host: "claude", Version: "1.0.0"},
			}, nil
		},
	}
	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	result, err := inst.Uninstall(t.Context(), newSkillTool())

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Success)
	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "could not be uninstalled for 2 host(s)")
}

func TestUninstallSkill_VerificationFails(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "copilot")
	runner.When(func(args exec.RunArgs, _ string) bool {
		return slices.Contains(args.Args, "uninstall")
	}).Respond(exec.RunResult{ExitCode: 0})

	// The skill remains installed on copilot after the command runs, so
	// host-scoped verification never confirms removal.
	det := &mockDetector{
		detectSkillHostsFn: func(
			_ context.Context, _ *ToolDefinition,
		) ([]InstalledSkillHost, error) {
			return []InstalledSkillHost{{Host: "copilot", Version: "1.0.0"}}, nil
		},
	}
	inst := NewInstaller(runner, NewPlatformDetector(runner), det)
	inst.(*installer).retryBackoff = time.Millisecond // keep the test fast

	result, err := inst.Uninstall(t.Context(), newSkillTool(), WithHosts("copilot"))

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Success)
	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "verification failed")
}

func TestUninstallSkill_VerifyDetectError(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "copilot")
	runner.When(func(args exec.RunArgs, _ string) bool {
		return slices.Contains(args.Args, "uninstall")
	}).Respond(exec.RunResult{ExitCode: 0})

	// Explicit --host copilot skips DetectSkillHosts during target
	// resolution, so the only call is from verification, which errors.
	wantErr := errors.New("plugin list failed")
	det := &mockDetector{
		detectSkillHostsFn: func(
			_ context.Context, _ *ToolDefinition,
		) ([]InstalledSkillHost, error) {
			return nil, wantErr
		},
	}
	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	result, err := inst.Uninstall(t.Context(), newSkillTool(), WithHosts("copilot"))

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Success)
	require.Error(t, result.Error)
	assert.ErrorIs(t, result.Error, wantErr)
}

func TestUninstallSkill_NoUninstallCommandConfigured(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	runner.MockToolInPath("copilot", nil)

	det := &mockDetector{
		detectSkillHostsFn: func(
			_ context.Context, _ *ToolDefinition,
		) ([]InstalledSkillHost, error) {
			return []InstalledSkillHost{{Host: "copilot", Version: "1.0.0"}}, nil
		},
	}
	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	// A skill host with no PluginUninstallCommand configured.
	tool := &ToolDefinition{
		Id:       "skill-no-uninstall-cmd",
		Name:     "Skill No Uninstall Cmd",
		Category: ToolCategorySkill,
		SkillHosts: []SkillHost{
			{
				Host:              "copilot",
				PluginListCommand: []string{"plugin", "list"},
				PluginName:        "azure@azure-skills",
				VersionRegex:      `(\d+\.\d+\.\d+)`,
				// No PluginUninstallCommand.
			},
		},
	}

	result, err := inst.Uninstall(t.Context(), tool, WithHosts("copilot"))

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Success)
	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "no uninstall command configured")
}
