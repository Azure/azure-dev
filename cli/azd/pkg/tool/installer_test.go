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
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockexec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeStepRenderer records the step spinner calls the installer makes, so
// tests can assert the per-step titles and any skill-count sub-line.
type fakeStepRenderer struct {
	starts   []string
	stops    []string
	messages []string
}

func (f *fakeStepRenderer) ShowSpinner(_ context.Context, title string, _ input.SpinnerUxType) {
	f.starts = append(f.starts, title)
}

func (f *fakeStepRenderer) StopSpinner(_ context.Context, lastMessage string, _ input.SpinnerUxType) {
	f.stops = append(f.stops, lastMessage)
}

func (f *fakeStepRenderer) Message(_ context.Context, message string) {
	f.messages = append(f.messages, message)
}

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

// TestRunToolInstall_StepProgress verifies that a non-skill tool install
// renders a single step spinner for the tool (no per-host title) and no
// skill-count sub-line.
func TestRunToolInstall_StepProgress(t *testing.T) {
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
		return args.Cmd == "npm" && slices.Contains(args.Args, "install")
	}).Respond(exec.RunResult{ExitCode: 0})

	det := &mockDetector{
		detectToolFn: func(
			_ context.Context, tool *ToolDefinition,
		) (*ToolStatus, error) {
			return &ToolStatus{Tool: tool, Installed: true, InstalledVersion: "2.64.0"}, nil
		},
	}
	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	nonSkill := &ToolDefinition{
		Id:       "test-tool",
		Name:     "Test Tool",
		Category: ToolCategoryCLI,
		InstallStrategies: allPlatforms(InstallStrategy{
			PackageManager: "npm",
			PackageId:      "@test/tool",
		}),
	}

	r := &fakeStepRenderer{}
	result, err := inst.Install(t.Context(), nonSkill, WithStepProgress(r))
	require.NoError(t, err)
	require.True(t, result.Success, "install must succeed; err=%v", result.Error)

	assert.Equal(t, []string{"Installing Test Tool"}, r.starts)
	assert.Equal(t, []string{"Installing Test Tool"}, r.stops)
	assert.Empty(t, r.messages, "non-skill install reports no skill count")
}

// TestRunToolUpgrade_StepProgress_ShowsVersion verifies that a non-skill
// upgrade appends the resulting version to the step result line — the same
// treatment skills get — e.g. "Upgrading Test Tool (v2.64.0)".
func TestRunToolUpgrade_StepProgress_ShowsVersion(t *testing.T) {
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
		return args.Cmd == "npm" && slices.Contains(args.Args, "update")
	}).Respond(exec.RunResult{ExitCode: 0})

	det := &mockDetector{
		detectToolFn: func(
			_ context.Context, tool *ToolDefinition,
		) (*ToolStatus, error) {
			return &ToolStatus{Tool: tool, Installed: true, InstalledVersion: "2.64.0"}, nil
		},
	}
	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	nonSkill := &ToolDefinition{
		Id:       "test-tool",
		Name:     "Test Tool",
		Category: ToolCategoryCLI,
		InstallStrategies: allPlatforms(InstallStrategy{
			PackageManager: "npm",
			PackageId:      "@test/tool",
		}),
	}

	r := &fakeStepRenderer{}
	result, err := inst.Upgrade(t.Context(), nonSkill, WithStepProgress(r))
	require.NoError(t, err)
	require.True(t, result.Success, "upgrade must succeed; err=%v", result.Error)

	assert.Equal(t, []string{"Upgrading Test Tool"}, r.starts)
	assert.Equal(t, []string{"Upgrading Test Tool (v2.64.0)"}, r.stops,
		"a non-skill upgrade must report the resulting version, like skills")
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
		InstallStrategies: map[string][]InstallStrategy{
			"windows": {{
				InstallCommand: "curl -sL https://example.com/install.sh",
			}},
			"darwin": {{
				InstallCommand: "curl -sL https://example.com/install.sh",
			}},
			"linux": {{
				InstallCommand: "curl -sL https://example.com/install.sh",
			}},
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
		InstallStrategies: map[string][]InstallStrategy{},
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
		cask      bool
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
			name:      "BrewCaskInstall",
			manager:   "brew",
			packageID: "copilot-cli",
			upgrade:   false,
			cask:      true,
			expectCmd: "brew",
			expectArg: "--cask",
		},
		{
			name:      "BrewCaskUpgrade",
			manager:   "brew",
			packageID: "copilot-cli",
			upgrade:   true,
			cask:      true,
			expectCmd: "brew",
			expectArg: "--cask",
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
				tt.manager, tt.packageID, tt.upgrade, tt.cask,
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
				Command:                "copilot",
				MarketplaceAddCommand:  []string{"plugin", "marketplace", "add", "microsoft/azure-skills"},
				PluginInstallCommand:   []string{"plugin", "install", "azure@azure-skills"},
				PluginUpdateCommand:    []string{"plugin", "update", "azure@azure-skills"},
				PluginUninstallCommand: []string{"plugin", "uninstall", "azure@azure-skills"},
				PluginListCommand:      []string{"plugin", "list"},
				PluginName:             "azure@azure-skills",
				BinaryVersionArgs:      []string{"--version"},
				BinaryVersionRegex:     `(?m)^GitHub Copilot CLI\s+v?(\d+\.\d+\.\d+)`,
			},
			{
				Host:    "claude",
				Command: "claude",
				MarketplaceAddCommand: []string{
					"plugin", "marketplace", "add", "https://github.com/microsoft/azure-skills",
				},
				PluginInstallCommand:   []string{"plugin", "install", "azure@azure-skills"},
				PluginUpdateCommand:    []string{"plugin", "update", "azure@azure-skills"},
				PluginUninstallCommand: []string{"plugin", "uninstall", "azure@azure-skills"},
				PluginListCommand:      []string{"plugin", "list", "--json"},
				PluginName:             "azure@azure-skills",
				BinaryVersionArgs:      []string{"--version"},
				BinaryVersionRegex:     `(?m)^v?(\d+\.\d+\.\d+)\s+\(Claude Code\)`,
			},
		},
	}
}

// TestRunSkill_StepProgress verifies that a silent skill install (the host
// CLI writes nothing to the terminal) shows the step spinner for the whole
// step and stops it with the result, using the display-cased agent name. No
// skill count is emitted.
func TestRunSkill_StepProgress(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "copilot")
	runner.MockToolInPath("node", nil)

	// The version-probe ("--version") is handled by mockHostPresence; the
	// marketplace-add and plugin-install commands succeed.
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "copilot" && slices.Contains(args.Args, "plugin")
	}).Respond(exec.RunResult{ExitCode: 0})

	inst := NewInstaller(
		runner, NewPlatformDetector(runner),
		&mockDetector{
			detectSkillHostsFn: func(
				_ context.Context, _ *ToolDefinition,
			) ([]InstalledSkillHost, error) {
				return []InstalledSkillHost{{Host: "copilot", Version: "1.1.70"}}, nil
			},
		},
	)

	var r fakeStepRenderer
	result, err := inst.Install(
		t.Context(), newSkillTool(),
		WithHosts("copilot"),
		WithStepProgress(&r),
	)
	require.NoError(t, err)
	require.True(t, result.Success, "install must succeed; err=%v", result.Error)

	// A silent install (the mock host CLI writes nothing) keeps the spinner
	// for the whole step, then stops it with the result — no early teardown,
	// no streamed-output result line, and no skill count.
	assert.Equal(t, []string{"Installing Test Azure Skills in copilot"}, r.starts)
	assert.Equal(t, []string{"Installing Test Azure Skills in copilot"}, r.stops)
	assert.Empty(t, r.messages)
}

// TestParseUpgradeOutput covers extracting the version and the "already at
// latest" state from each host CLI's plugin-update output.
func TestParseUpgradeOutput(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name            string
		in              string
		wantVersion     string
		wantAlreadyLast bool
	}{
		{
			name:            "CopilotAlreadyLatest",
			in:              `updated successfully (v1.1.86, already at latest). 27 skills.`,
			wantVersion:     "1.1.86",
			wantAlreadyLast: true,
		},
		{
			name:            "ClaudeUpdatedFromTo",
			in:              `✔ Plugin "azure" updated from 1.1.73 to 1.1.86 for scope user. Restart to apply changes.`,
			wantVersion:     "1.1.86",
			wantAlreadyLast: false,
		},
		{
			name:            "ClaudeAlreadyLatest",
			in:              `✔ azure is already at the latest version (1.1.86).`,
			wantVersion:     "1.1.86",
			wantAlreadyLast: true,
		},
		{
			name:            "NoVersion",
			in:              "Plugin updated.",
			wantVersion:     "",
			wantAlreadyLast: false,
		},
		{
			name:            "Empty",
			in:              "",
			wantVersion:     "",
			wantAlreadyLast: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotVersion, gotAlreadyLast := parseUpgradeOutput(tc.in)
			assert.Equal(t, tc.wantVersion, gotVersion)
			assert.Equal(t, tc.wantAlreadyLast, gotAlreadyLast)
		})
	}
}

// TestRunSkill_Upgrade_StepResultShowsVersion verifies that an upgrade shows a
// plain in-progress spinner title and reports the version — parsed from the
// update command's output — on the result line only.
func TestRunSkill_Upgrade_StepResultShowsVersion(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "copilot")
	runner.MockToolInPath("node", nil)
	// The update command reports the new version (claude-style "from A to B").
	var upgraded bool
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "copilot" && slices.Contains(args.Args, "update")
	}).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
		upgraded = true
		return exec.RunResult{
			ExitCode: 0,
			Stdout:   `Plugin "azure" updated from 1.1.73 to 1.1.86 for scope user.`,
		}, nil
	})

	inst := NewInstaller(
		runner, NewPlatformDetector(runner),
		&mockDetector{
			detectSkillHostsFn: func(
				_ context.Context, _ *ToolDefinition,
			) ([]InstalledSkillHost, error) {
				// An actual upgrade: the old version before the update runs,
				// the new version afterwards.
				version := "1.1.73"
				if upgraded {
					version = "1.1.86"
				}
				return []InstalledSkillHost{{Host: "copilot", Version: version}}, nil
			},
		},
	)

	var r fakeStepRenderer
	result, err := inst.Upgrade(
		t.Context(), newSkillTool(),
		WithHosts("copilot"),
		WithStepProgress(&r),
	)
	require.NoError(t, err)
	require.True(t, result.Success, "upgrade must succeed; err=%v", result.Error)
	assert.False(t, result.AlreadyUpToDate, "an actual upgrade is not up-to-date")

	// Spinner title has no version; the result line appends the new version.
	assert.Equal(t, []string{"Upgrading Test Azure Skills in copilot"}, r.starts)
	assert.Equal(t, []string{"Upgrading Test Azure Skills in copilot (v1.1.86)"}, r.stops)
}

// TestRunSkill_Upgrade_AlreadyUpToDate verifies that when the host reports the
// skill is already at the latest version, the result line says so (with the
// version) and the result is flagged AlreadyUpToDate.
func TestRunSkill_Upgrade_AlreadyUpToDate(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "copilot")
	runner.MockToolInPath("node", nil)
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "copilot" && slices.Contains(args.Args, "update")
	}).Respond(exec.RunResult{
		ExitCode: 0,
		Stdout:   `updated successfully (v1.1.86, already at latest). 27 skills.`,
	})

	inst := NewInstaller(
		runner, NewPlatformDetector(runner),
		&mockDetector{
			detectSkillHostsFn: func(
				_ context.Context, _ *ToolDefinition,
			) ([]InstalledSkillHost, error) {
				return []InstalledSkillHost{{Host: "copilot", Version: "1.1.86"}}, nil
			},
		},
	)

	var r fakeStepRenderer
	result, err := inst.Upgrade(
		t.Context(), newSkillTool(),
		WithHosts("copilot"),
		WithStepProgress(&r),
	)
	require.NoError(t, err)
	require.True(t, result.Success, "upgrade must succeed; err=%v", result.Error)
	assert.True(t, result.AlreadyUpToDate, "nothing changed, so already up to date")

	assert.Equal(t, []string{"Upgrading Test Azure Skills in copilot"}, r.starts)
	assert.Equal(t, []string{"Test Azure Skills are already up to date (v1.1.86)."}, r.stops)
}

// TestRunSkill_StreamedOutputPrintedAboveSpinner verifies that when the host
// CLI writes to the terminal (a progress line or an interactive prompt),
// each line is surfaced via Message (which the console prints above the
// spinner, keeping the spinner pinned below), and the spinner is stopped with
// the step result at the end rather than torn down early.
func TestRunSkill_StreamedOutputPrintedAboveSpinner(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "copilot")
	runner.MockToolInPath("node", nil)

	// The plugin command writes to the terminal (StdOut), simulating the
	// host CLI surfacing progress or a prompt. marketplace-add is captured
	// (no StdOut) so it stays silent; only the install writes.
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "copilot" && slices.Contains(args.Args, "plugin")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		if args.StdOut != nil {
			_, _ = args.StdOut.Write([]byte("Installing plugin...\n"))
		}
		return exec.RunResult{ExitCode: 0}, nil
	})

	inst := NewInstaller(
		runner, NewPlatformDetector(runner),
		&mockDetector{
			detectSkillHostsFn: func(
				_ context.Context, _ *ToolDefinition,
			) ([]InstalledSkillHost, error) {
				return []InstalledSkillHost{{Host: "copilot", Version: "1.1.70"}}, nil
			},
		},
	)

	var r fakeStepRenderer
	result, err := inst.Install(
		t.Context(), newSkillTool(),
		WithHosts("copilot"),
		WithStepProgress(&r),
	)
	require.NoError(t, err)
	require.True(t, result.Success, "install must succeed; err=%v", result.Error)

	// The spinner is shown, the host CLI's line is surfaced via Message (the
	// console prints it above the spinner), and the spinner is stopped with
	// the step result at the end — it is kept, not torn down early.
	assert.Equal(t, []string{"Installing Test Azure Skills in copilot"}, r.starts)
	assert.Equal(t, []string{"Installing Test Azure Skills in copilot"}, r.stops)
	assert.Contains(t, r.messages, "Installing plugin...")
}

// TestSkillHost_DisplayVsCommand verifies the split between the display Host
// and the lowercase Command: the CLI is exec'd by Command, the step spinner
// uses the display Host, and --agent resolves via Command even when it
// differs from the Host.
func TestSkillHost_DisplayVsCommand(t *testing.T) {
	t.Parallel()

	skill := &ToolDefinition{
		Id:       "test-azure-skills",
		Name:     "Test Azure Skills",
		Category: ToolCategorySkill,
		SkillHosts: []SkillHost{{
			Host:                  "GitHub Copilot CLI", // display name (differs from Command)
			Command:               "copilot",            // exec binary
			MarketplaceAddCommand: []string{"plugin", "marketplace", "add", "microsoft/azure-skills"},
			PluginInstallCommand:  []string{"plugin", "install", "azure@azure-skills"},
			PluginListCommand:     []string{"plugin", "list"},
			PluginName:            "azure@azure-skills",
			BinaryVersionArgs:     []string{"--version"},
			BinaryVersionRegex:    `(?m)^GitHub Copilot CLI\s+v?(\d+\.\d+\.\d+)`,
		}},
	}

	runner := mockexec.NewMockCommandRunner()
	runner.MockToolInPath("copilot", nil) // looked up by the lowercase Command
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "copilot" && slices.Contains(args.Args, "--version")
	}).Respond(exec.RunResult{ExitCode: 0, Stdout: "GitHub Copilot CLI 1.1.70"})
	var execCmds []string
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "copilot" && slices.Contains(args.Args, "plugin")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		execCmds = append(execCmds, args.Cmd)
		return exec.RunResult{ExitCode: 0}, nil
	})

	det := &mockDetector{
		detectSkillHostsFn: func(_ context.Context, _ *ToolDefinition) ([]InstalledSkillHost, error) {
			// DetectSkillHosts reports the command identity (see
			// InstalledSkillHost.Host), so verification matches by command.
			return []InstalledSkillHost{{Host: "copilot", Version: "1.1.70"}}, nil
		},
	}
	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	// --agent "copilot" (the Command) must resolve even though the Host is the
	// full display name "GitHub Copilot CLI".
	var r fakeStepRenderer
	result, err := inst.Install(t.Context(), skill, WithHosts("copilot"), WithStepProgress(&r))
	require.NoError(t, err)
	require.True(t, result.Success, "install must succeed; err=%v", result.Error)

	require.NotEmpty(t, execCmds, "the host CLI must be exec'd")
	for _, c := range execCmds {
		assert.Equal(t, "copilot", c, "exec must use the lowercase Command, not display Host")
	}
	require.NotEmpty(t, r.starts, "the step spinner must be shown")
	assert.Equal(t, []string{"Installing Test Azure Skills in GitHub Copilot CLI"}, r.starts,
		"the step spinner must use the display-cased Host")
}

// mockHostPresence wires ToolInPath responses so only the named hosts
// resolve successfully. Pass an empty slice to mock every host as
// missing. Present hosts also get a version-probe response whose banner
// matches the host's anchored BinaryVersionRegex (see newSkillTool), so
// installer.hostUsable treats them as genuine, functional CLIs rather than
// launcher stubs. Tests that want to simulate a stub register their own
// later "--version" expectation, which wins (last match).
func mockHostPresence(
	runner *mockexec.MockCommandRunner,
	present ...string,
) {
	// Per-host `--version` banner that satisfies the host's anchored
	// BinaryVersionRegex.
	versionBanner := map[string]string{
		"copilot": "GitHub Copilot CLI 1.1.70",
		"claude":  "1.1.70 (Claude Code)",
	}
	for _, h := range allSkillHostNames {
		if slices.Contains(present, h) {
			runner.MockToolInPath(h, nil)
			host := h
			banner := versionBanner[h]
			runner.When(func(args exec.RunArgs, _ string) bool {
				return args.Cmd == host && slices.Contains(args.Args, "--version")
			}).Respond(exec.RunResult{Stdout: banner, ExitCode: 0})
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

// TestRunSkill_Install_LauncherStubHost_ReturnsInstallGuidance verifies
// that a host binary present on PATH but non-functional — e.g. the VS Code
// Copilot Chat extension's `copilot` launcher stub, which only prompts to
// install the real CLI and exits 0 — is not mistaken for a usable host.
// The install must fail with the same clean "install GitHub Copilot CLI"
// guidance as a host that is entirely absent (matching Windows), never the
// misleading "was installed via copilot but verification failed", and must
// not attempt any plugin command through the stub.
func TestRunSkill_Install_LauncherStubHost_ReturnsInstallGuidance(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	// copilot is on PATH (the launcher stub); claude is not.
	runner.MockToolInPath("copilot", nil)
	runner.MockToolInPath("claude", errors.New("not found"))
	runner.MockToolInPath("node", nil)

	// The launcher stub answers `copilot --version` with its install
	// prompt instead of a version, and exits 0. Record that the probe
	// actually ran and returned the stub's response, so this test fails
	// loudly if mock precedence ever changed and the probe silently observed
	// a different (e.g. version-shaped) response instead.
	var stubVersionProbed bool
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "copilot" && slices.Contains(args.Args, "--version")
	}).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
		stubVersionProbed = true
		return exec.RunResult{
			Stdout:   "Install GitHub Copilot CLI? ['y/N']",
			Stderr:   "Cannot find GitHub Copilot CLI (https://docs.github.com/copilot)",
			ExitCode: 0,
		}, nil
	})

	// Fail the assertion if any plugin command is attempted via the stub.
	var attemptedInstall bool
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "copilot" &&
			(slices.Contains(args.Args, "install") ||
				slices.Contains(args.Args, "marketplace") ||
				slices.Contains(args.Args, "list"))
	}).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
		attemptedInstall = true
		return exec.RunResult{ExitCode: 0}, nil
	})

	inst := NewInstaller(
		runner, NewPlatformDetector(runner), installedDetector("1.1.70"),
	)

	result, err := inst.Install(t.Context(), newSkillTool())
	require.NoError(t, err)
	require.False(t, result.Success)
	require.NotNil(t, result.Error)

	// Clean, actionable guidance — not "verification failed".
	ews, ok := errors.AsType[*errorhandler.ErrorWithSuggestion](result.Error)
	require.True(t, ok,
		"expected *errorhandler.ErrorWithSuggestion, got %T: %v",
		result.Error, result.Error,
	)
	assert.Contains(t, ews.Suggestion, "azd tool install github-copilot-cli")
	assert.NotContains(t, result.Error.Error(), "verification failed")

	// The stub must never be driven through an install/marketplace flow.
	assert.False(t, attemptedInstall,
		"must not attempt to install through a non-functional launcher stub")

	// Self-defending: the probe must have actually executed and observed the
	// stub's response (guards against a silent mock-precedence flip that would
	// otherwise let a stub be treated as usable without failing this test).
	assert.True(t, stubVersionProbed,
		"hostUsable must probe `copilot --version` and observe the stub response")
}

// TestRunSkill_Install_StubWithIncidentalVersion_Rejected verifies that a
// launcher stub is still rejected when its output contains a version-shaped
// token that is not a genuine version report — e.g. a bundled node version, a
// path build number, or even a line that starts with the real CLI's banner
// prefix but carries no version. BinaryVersionRegex is anchored to the host's
// `--version` banner, so only a real version line counts.
func TestRunSkill_Install_StubWithIncidentalVersion_Rejected(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	runner.MockToolInPath("copilot", nil)
	runner.MockToolInPath("claude", errors.New("not found"))
	runner.MockToolInPath("node", nil)

	// None of these lines is a genuine "GitHub Copilot CLI <version>"
	// report: one borrows the banner prefix without a version, others carry
	// incidental semvers (a node runtime version, a URL build number).
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "copilot" && slices.Contains(args.Args, "--version")
	}).Respond(exec.RunResult{
		Stdout: "GitHub Copilot CLI is not installed\n" +
			"Install GitHub Copilot CLI? ['y/N']\n" +
			"Downloading node v20.11.1 runtime\n" +
			"See https://example.com/releases/1.2.3 for details",
		ExitCode: 0,
	})

	// Fail loudly if any plugin command is attempted through the stub.
	var attemptedInstall bool
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "copilot" &&
			(slices.Contains(args.Args, "install") ||
				slices.Contains(args.Args, "marketplace") ||
				slices.Contains(args.Args, "list"))
	}).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
		attemptedInstall = true
		return exec.RunResult{ExitCode: 0}, nil
	})

	inst := NewInstaller(
		runner, NewPlatformDetector(runner), installedDetector("1.1.70"),
	)

	result, err := inst.Install(t.Context(), newSkillTool())
	require.NoError(t, err)
	require.False(t, result.Success,
		"incidental version text must not make a stub usable")
	require.NotNil(t, result.Error)

	_, ok := errors.AsType[*errorhandler.ErrorWithSuggestion](result.Error)
	require.True(t, ok,
		"expected install guidance, got %T: %v", result.Error, result.Error)
	assert.NotContains(t, result.Error.Error(), "verification failed")
	assert.False(t, attemptedInstall,
		"must not install through a stub that only prints incidental version text")
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
		return args.Cmd == "copilot" &&
			!slices.Contains(args.Args, "--version")
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
			!slices.Contains(args.Args, "marketplace") &&
			!slices.Contains(args.Args, "--version")
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
		return args.Cmd == "claude" &&
			!slices.Contains(args.Args, "--version")
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
		return args.Cmd == "copilot" &&
			!slices.Contains(args.Args, "--version")
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
		return args.Cmd == "copilot" &&
			!slices.Contains(args.Args, "--version")
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
		return args.Cmd == "copilot" &&
			!slices.Contains(args.Args, "--version")
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

// TestRunSkill_Install_ExplicitStubHost_Rejected is a regression guard for
// the explicit `--host` path: a host requested by name that is present on
// PATH only as a launcher stub (not a functional CLI) must be rejected the
// same way the default / `--host all` path rejects it. explicitSkillHostTargets
// uses [installer.hostUsable] (a version probe), not a bare PATH-existence
// check, so the stub fails with "not available" instead of being driven
// through an install flow that would later surface "verification failed".
func TestRunSkill_Install_ExplicitStubHost_Rejected(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	// copilot is on PATH but is a launcher stub; claude is not on PATH.
	runner.MockToolInPath("copilot", nil)
	runner.MockToolInPath("claude", errors.New("not found"))
	runner.MockToolInPath("node", nil)

	// The stub answers `copilot --version` with its install prompt instead
	// of a version banner, and exits 0. Record that the probe ran so the
	// test fails loudly if mock precedence ever let a version-shaped
	// response through instead.
	var stubVersionProbed bool
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "copilot" && slices.Contains(args.Args, "--version")
	}).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
		stubVersionProbed = true
		return exec.RunResult{
			Stdout:   "Install GitHub Copilot CLI? ['y/N']",
			Stderr:   "Cannot find GitHub Copilot CLI (https://docs.github.com/copilot)",
			ExitCode: 0,
		}, nil
	})

	// Fail if any plugin command is attempted through the stub.
	var attemptedInstall bool
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "copilot" &&
			(slices.Contains(args.Args, "install") ||
				slices.Contains(args.Args, "marketplace") ||
				slices.Contains(args.Args, "list"))
	}).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
		attemptedInstall = true
		return exec.RunResult{ExitCode: 0}, nil
	})

	inst := NewInstaller(
		runner, NewPlatformDetector(runner), installedDetector("1.1.70"),
	)

	result, err := inst.Install(
		t.Context(), newSkillTool(), WithHosts("copilot"),
	)
	require.NoError(t, err)
	require.False(t, result.Success)
	require.NotNil(t, result.Error)

	// The explicit host is rejected up front, naming the requested host —
	// not driven through an install that fails verification.
	assert.Contains(t, result.Error.Error(), "not available")
	assert.Contains(t, result.Error.Error(), "copilot")
	assert.NotContains(t, result.Error.Error(), "verification failed")
	assert.False(t, attemptedInstall,
		"must not attempt to install through a non-functional launcher stub")
	assert.True(t, stubVersionProbed,
		"explicitSkillHostTargets must probe `copilot --version` via hostUsable")
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

	commands, names := inst.AvailableSkillHosts(t.Context(), newSkillTool())
	// newSkillTool uses Host == Command, so both slices match.
	assert.Equal(t, []string{"copilot", "claude"}, commands)
	assert.Equal(t, []string{"copilot", "claude"}, names)
}

func TestAvailableSkillHosts_NonSkillToolReturnsNil(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	inst := NewInstaller(runner, NewPlatformDetector(runner), &mockDetector{})

	commands, names := inst.AvailableSkillHosts(t.Context(), &ToolDefinition{
		Id:       "not-a-skill",
		Category: ToolCategoryServer,
	})
	assert.Nil(t, commands)
	assert.Nil(t, names)
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

// copilotMultiMethodTool returns a github-copilot-cli-like tool with the
// multi-method matrix on every platform (npm, brew cask, install script).
func copilotMultiMethodTool() *ToolDefinition {
	return &ToolDefinition{
		Id:            "github-copilot-cli",
		Name:          "GitHub Copilot CLI",
		DetectCommand: "copilot",
		Category:      ToolCategoryCLI,
		InstallStrategies: allPlatforms(
			InstallStrategy{PackageManager: "npm", PackageId: "@github/copilot"},
			InstallStrategy{PackageManager: "brew", PackageId: "copilot-cli", Cask: true},
			InstallStrategy{InstallCommand: "curl -fsSL https://gh.io/copilot-install | bash"},
		),
	}
}

// TestUninstall_MultiMethod_NpmOwns_UninstallsViaNpm verifies detect-then-remove
// picks the package manager that actually has the tool: npm reports it
// installed, so azd uninstalls via npm even though npm is not the first
// strategy on every platform.
func TestUninstall_MultiMethod_NpmOwns_UninstallsViaNpm(t *testing.T) {
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
	// Detection: npm has a record of the package.
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "npm" && slices.Contains(args.Args, "ls")
	}).Respond(exec.RunResult{ExitCode: 0, Stdout: "@github/copilot@1.0.0"})

	var capturedArgs []string
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "npm" && slices.Contains(args.Args, "uninstall")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		capturedArgs = args.Args
		return exec.RunResult{ExitCode: 0}, nil
	})

	det := &mockDetector{detectToolFn: func(
		_ context.Context, tool *ToolDefinition,
	) (*ToolStatus, error) {
		return &ToolStatus{Tool: tool, Installed: false}, nil
	}}

	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	result, err := inst.Uninstall(t.Context(), copilotMultiMethodTool())

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Success, "result.Error=%v", result.Error)
	assert.Contains(t, capturedArgs, "uninstall")
	assert.Contains(t, capturedArgs, "@github/copilot")
}

// TestUninstall_MultiMethod_NoManagerOwns_GuidesBinaryRemoval verifies the
// detect-then-remove fallback: no package manager has a record of the tool but
// it is still detected on PATH (a script/direct install), so azd names the
// actual binary to remove.
func TestUninstall_MultiMethod_NoManagerOwns_GuidesBinaryRemoval(t *testing.T) {
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
	// Detection: npm does NOT have the package (exit non-zero).
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "npm" && slices.Contains(args.Args, "ls")
	}).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
		return exec.RunResult{ExitCode: 1, Stdout: "(empty)"}, errors.New("exit status 1")
	})

	// The tool is still detected on PATH (installed via the script).
	det := installedDetector("1.2.3")

	inst := NewInstaller(runner, NewPlatformDetector(runner), det)
	inst.(*installer).lookPath = func(string) (string, error) {
		return "/home/dev/.local/bin/copilot", nil
	}

	result, err := inst.Uninstall(t.Context(), copilotMultiMethodTool())

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Success)

	var ews *errorhandler.ErrorWithSuggestion
	require.ErrorAs(t, result.Error, &ews)
	assert.Contains(t, ews.Suggestion, "install script")
	assert.Contains(t, ews.Suggestion, "/home/dev/.local/bin/copilot")
}

// TestManagerHasPackage verifies the per-manager "is installed" probes.
func TestManagerHasPackage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		strategy InstallStrategy
		respond  func(*mockexec.MockCommandRunner)
		want     bool
	}{
		{
			name:     "NpmInstalled",
			strategy: InstallStrategy{PackageManager: "npm", PackageId: "@github/copilot"},
			respond: func(r *mockexec.MockCommandRunner) {
				r.When(func(a exec.RunArgs, _ string) bool { return a.Cmd == "npm" }).
					Respond(exec.RunResult{ExitCode: 0, Stdout: "@github/copilot@1.0.0"})
			},
			want: true,
		},
		{
			name:     "NpmNotInstalled",
			strategy: InstallStrategy{PackageManager: "npm", PackageId: "@github/copilot"},
			respond: func(r *mockexec.MockCommandRunner) {
				r.When(func(a exec.RunArgs, _ string) bool { return a.Cmd == "npm" }).
					RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
						return exec.RunResult{ExitCode: 1, Stdout: "(empty)"}, errors.New("exit 1")
					})
			},
			want: false,
		},
		{
			name:     "BrewCaskInstalled",
			strategy: InstallStrategy{PackageManager: "brew", PackageId: "copilot-cli", Cask: true},
			respond: func(r *mockexec.MockCommandRunner) {
				r.When(func(a exec.RunArgs, _ string) bool { return a.Cmd == "brew" }).
					Respond(exec.RunResult{ExitCode: 0, Stdout: "/opt/homebrew/.../copilot"})
			},
			want: true,
		},
		{
			name:     "BrewCaskNotInstalled",
			strategy: InstallStrategy{PackageManager: "brew", PackageId: "copilot-cli", Cask: true},
			respond: func(r *mockexec.MockCommandRunner) {
				r.When(func(a exec.RunArgs, _ string) bool { return a.Cmd == "brew" }).
					RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
						return exec.RunResult{ExitCode: 1}, errors.New("not installed")
					})
			},
			want: false,
		},
		{
			name:     "WingetInstalled",
			strategy: InstallStrategy{PackageManager: "winget", PackageId: "GitHub.Copilot"},
			respond: func(r *mockexec.MockCommandRunner) {
				r.When(func(a exec.RunArgs, _ string) bool { return a.Cmd == "winget" }).
					Respond(exec.RunResult{ExitCode: 0, Stdout: "GitHub Copilot  GitHub.Copilot  1.0"})
			},
			want: true,
		},
		{
			name:     "WingetNotInstalled",
			strategy: InstallStrategy{PackageManager: "winget", PackageId: "GitHub.Copilot"},
			respond: func(r *mockexec.MockCommandRunner) {
				r.When(func(a exec.RunArgs, _ string) bool { return a.Cmd == "winget" }).
					Respond(exec.RunResult{ExitCode: 0, Stdout: "No installed package found matching input criteria."})
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			runner := mockexec.NewMockCommandRunner()
			tt.respond(runner)
			inst := NewInstaller(runner, NewPlatformDetector(runner), &mockDetector{})
			got := inst.(*installer).managerHasPackage(t.Context(), &tt.strategy)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestSelfManagedRemovalSuggestion verifies the manual-removal guidance both
// when the binary path is resolvable and when it is not.
func TestSelfManagedRemovalSuggestion(t *testing.T) {
	t.Parallel()

	tool := &ToolDefinition{Name: "GitHub Copilot CLI", DetectCommand: "copilot"}

	withPath := selfManagedRemovalSuggestion(tool, "/home/dev/.local/bin/copilot")
	assert.Contains(t, withPath, "install script")
	assert.Contains(t, withPath, "/home/dev/.local/bin/copilot")
	assert.Contains(t, withPath, "Remove it manually")

	noPath := selfManagedRemovalSuggestion(tool, "")
	assert.Contains(t, noPath, `"copilot"`)
	assert.Contains(t, noPath, "from your PATH manually")
}

// TestIsSystemBinaryPath verifies detection of system-owned binary locations.
func TestIsSystemBinaryPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		want bool
	}{
		{"/usr/local/bin/copilot", true},
		{"/usr/bin/copilot", true},
		{"/opt/tools/bin/copilot", true},
		{"/bin/sh", true},
		{"/Library/Foo/copilot", true},
		{"/home/dev/.local/bin/copilot", false},
		{`C:\Users\dev\copilot.exe`, false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isSystemBinaryPath(tt.path))
		})
	}
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
// longer has a record of the package (a self-updating CLI replaced the
// manager-installed copy) yet azd still detects the tool. The user must get
// actionable manual-removal guidance. This is winget-specific (see
// packageManagerLostRecord), so it runs on Windows.
func TestUninstall_NonSkill_PackageManagerNoRecord_GuidesManualRemoval(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "windows" {
		t.Skip("winget (the lost-record case) is only available on Windows")
	}

	runner := mockexec.NewMockCommandRunner()
	for _, managers := range platformManagers {
		for _, mgr := range managers {
			runner.MockToolInPath(mgr, errors.New("not found"))
		}
	}
	runner.MockToolInPath("winget", nil)
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "winget" && slices.Contains(args.Args, "--version")
	}).Respond(exec.RunResult{ExitCode: 0, Stdout: "v1.8.0"})
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "winget" && slices.Contains(args.Args, "uninstall")
	}).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
		// winget lost its record of the package after a self-update; it
		// reports the no-package error on stdout.
		return exec.RunResult{
			ExitCode: int(wingetNoPackageFoundExitCode),
			Stdout: "Found Self Updating CLI [GitHub.Copilot]\n" +
				"No installed package found matching input criteria.",
		}, errors.New("exit status 0x8a150014")
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
			PackageManager: "winget",
			PackageId:      "GitHub.Copilot",
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

// TestUninstall_NonSkill_PackageManagerNoRecord_EmptyOutput_FallsBackToError
// covers the lost-record case where winget signals the failure via its exit
// code alone, with no stdout/stderr text. The surfaced error must fall back to
// the command error instead of being blank.
func TestUninstall_NonSkill_PackageManagerNoRecord_EmptyOutput_FallsBackToError(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "windows" {
		t.Skip("winget (the lost-record case) is only available on Windows")
	}

	runner := mockexec.NewMockCommandRunner()
	for _, managers := range platformManagers {
		for _, mgr := range managers {
			runner.MockToolInPath(mgr, errors.New("not found"))
		}
	}
	runner.MockToolInPath("winget", nil)
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "winget" && slices.Contains(args.Args, "--version")
	}).Respond(exec.RunResult{ExitCode: 0, Stdout: "v1.8.0"})
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "winget" && slices.Contains(args.Args, "uninstall")
	}).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
		// Lost record signalled by exit code only — no output text.
		return exec.RunResult{ExitCode: int(wingetNoPackageFoundExitCode)},
			errors.New("exit status 0x8a150014")
	})

	det := installedDetector("1.0.0")
	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	tool := &ToolDefinition{
		Id:            "self-updating-cli",
		Name:          "Self Updating CLI",
		DetectCommand: "azd-nonexistent-cli-xyz",
		Category:      ToolCategoryCLI,
		InstallStrategies: allPlatforms(InstallStrategy{
			PackageManager: "winget",
			PackageId:      "GitHub.Copilot",
		}),
	}

	result, err := inst.Uninstall(t.Context(), tool)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Success)

	var ews *errorhandler.ErrorWithSuggestion
	require.ErrorAs(t, result.Error, &ews)
	// The surfaced error must not be blank; it falls back to the command error.
	assert.Contains(t, ews.Error(), "exit status 0x8a150014")
	assert.Contains(t, ews.Suggestion, "no longer has a record")
}

// TestUninstall_NonSkill_PackageManagerGenericFailure_ReturnsErrorDirectly
// covers a package-manager uninstall that fails for a reason OTHER than a
// lost record (here, a permissions error) while azd still detects the tool.
// The user must get the actual package-manager error returned directly, not
// the misleading "no longer has a record / updated outside the package
// manager" guidance, which only applies to self-updating CLIs.
func TestUninstall_NonSkill_PackageManagerGenericFailure_ReturnsErrorDirectly(t *testing.T) {
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
		// A failure with no "not installed" signal — the manager still has a
		// record, it simply could not complete the removal.
		return exec.RunResult{
			ExitCode: 1,
			Stderr:   "npm error code EACCES\nnpm error syscall unlink\nnpm error errno -13",
		}, errors.New("exit status 1")
	})

	// The tool is still detected after the failed uninstall.
	det := installedDetector("1.0.0")

	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	tool := &ToolDefinition{
		Id:            "locked-cli",
		Name:          "Locked CLI",
		DetectCommand: "azd-nonexistent-cli-xyz",
		Category:      ToolCategoryCLI,
		InstallStrategies: allPlatforms(InstallStrategy{
			PackageManager: "npm",
			PackageId:      "@test/locked",
		}),
	}

	result, err := inst.Uninstall(t.Context(), tool)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Success)
	require.Error(t, result.Error)

	// The error is returned directly: no "lost record" suggestion is attached.
	_, isSuggestion := errors.AsType[*errorhandler.ErrorWithSuggestion](result.Error)
	assert.False(t, isSuggestion,
		"generic failures should return the error directly, not a suggestion")
	assert.NotContains(t, result.Error.Error(), "no longer has a record")
	assert.NotContains(t, result.Error.Error(), "updated outside the")
	// The actual package-manager message is surfaced.
	assert.Contains(t, result.Error.Error(), "uninstalling Locked CLI with npm failed")
	assert.Contains(t, result.Error.Error(), "npm error errno -13")
}

// TestPackageManagerLostRecord verifies the per-manager detection of the
// "manager no longer has a record of the package" signature, which gates the
// self-updating-CLI uninstall guidance. winget is matched by its
// locale-independent exit code as well as its message.
// TestPackageManagerLostRecord verifies the lost-record detection, which is
// winget-specific. brew, npm and apt are intentionally NOT special-cased
// (their "nothing to remove" uninstalls typically exit 0 and their installs
// are unaffected by self-updates), so they must report false even when their
// output resembles a "not installed" message.
func TestPackageManagerLostRecord(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		packageManager string
		res            exec.RunResult
		want           bool
	}{
		{
			name:           "WingetNoPackageMessage",
			packageManager: "winget",
			res:            exec.RunResult{ExitCode: 1, Stdout: "No installed package found matching input criteria."},
			want:           true,
		},
		{
			name:           "WingetNoPackageExitCodeGoValue",
			packageManager: "winget",
			// Go's os/exec surfaces winget's 0x8A150014 as this int on Windows.
			res:  exec.RunResult{ExitCode: int(wingetNoPackageFoundExitCode)},
			want: true,
		},
		{
			name:           "WingetNoPackageExitCodeSigned",
			packageManager: "winget",
			// The same code as a shell-style signed int32 must also match.
			res:  exec.RunResult{ExitCode: -1978335212},
			want: true,
		},
		{
			name:           "WingetGenericFailureNotLostRecord",
			packageManager: "winget",
			res:            exec.RunResult{ExitCode: 1, Stderr: "Access is denied."},
			want:           false,
		},
		{
			// brew exits 1 with this, but brew installs survive self-updates,
			// so it is not treated as a lost-record case.
			name:           "BrewNotSpecialCased",
			packageManager: "brew",
			res:            exec.RunResult{ExitCode: 1, Stderr: "Error: No such keg: /usr/local/Cellar/foo"},
			want:           false,
		},
		{
			name:           "AptNotSpecialCased",
			packageManager: "apt",
			res:            exec.RunResult{ExitCode: 100, Stderr: "E: Unable to locate package foo"},
			want:           false,
		},
		{
			// npm uninstall of a missing package actually exits 0 ("up to
			// date"); even a "not installed" string must not match.
			name:           "NpmNotSpecialCased",
			packageManager: "npm",
			res:            exec.RunResult{ExitCode: 1, Stdout: "not installed"},
			want:           false,
		},
		{
			name:           "UnknownManager",
			packageManager: "code",
			res:            exec.RunResult{ExitCode: 1, Stderr: "no installed package found"},
			want:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, packageManagerLostRecord(tt.packageManager, tt.res))
		})
	}
}

// TestUninstall_NonSkill_VSCodeDependencyConflict_GuidesDependentRemoval covers
// the case where VS Code refuses to remove an extension because other
// installed extensions depend on it (exit code 1 with a "depends on this"
// message, which `--force` does not override). The user must get accurate
// guidance to remove the dependent extensions first, not the generic
// "no record" message used for self-updating CLIs.
func TestUninstall_NonSkill_VSCodeDependencyConflict_GuidesDependentRemoval(t *testing.T) {
	t.Parallel()

	const dependencyMsg = "Cannot uninstall 'Azure Resources' extension. " +
		"'Azure App Service' extension depends on this."

	runner := mockexec.NewMockCommandRunner()
	for _, managers := range platformManagers {
		for _, mgr := range managers {
			runner.MockToolInPath(mgr, errors.New("not found"))
		}
	}
	runner.MockToolInPath("code", nil)
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "code" && slices.Contains(args.Args, "--version")
	}).Respond(exec.RunResult{ExitCode: 0, Stdout: "1.95.0"})
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "code" &&
			slices.Contains(args.Args, "--uninstall-extension")
	}).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
		return exec.RunResult{
			ExitCode: 1,
			Stdout:   "Uninstalling ms-azuretools.vscode-azureresourcegroups...",
			Stderr:   dependencyMsg,
		}, errors.New("exit status 1")
	})

	// The extension is still detected after the blocked uninstall.
	det := installedDetector("0.12.7")

	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	tool := &ToolDefinition{
		Id:       "vscode-azure-tools",
		Name:     "Azure Tools VS Code Extension",
		Category: ToolCategoryVSCodeExtension,
		InstallStrategies: allPlatforms(InstallStrategy{
			PackageManager: "code",
			PackageId:      "ms-azuretools.vscode-azureresourcegroups",
		}),
	}

	result, err := inst.Uninstall(t.Context(), tool)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Success)

	var ews *errorhandler.ErrorWithSuggestion
	require.ErrorAs(t, result.Error, &ews)
	assert.Contains(t, ews.Suggestion, "other installed VS Code extensions")
	assert.Contains(t, ews.Suggestion, "Uninstall the dependent extension(s) first")
	// The specific dependent extensions reported by VS Code are surfaced in
	// the underlying error.
	assert.Contains(t, ews.Err.Error(), dependencyMsg)
	// The misleading "no record" guidance must not be used here.
	assert.NotContains(t, ews.Suggestion, "no longer has a record")
}

// TestVscodeDependencyConflict verifies detection of VS Code's dependency
// rejection across phrasings, including the exact multi-dependent message
// from issue #8830 ("...and other extension depend on this."), which uses
// the plural "depend on this" rather than the single-dependent "depends on
// this".
func TestVscodeDependencyConflict(t *testing.T) {
	t.Parallel()

	// Verbatim stderr from the azd tool uninstall --debug log in issue #8830.
	const issue8830Stderr = "Cannot uninstall 'Azure Resources' extension. " +
		"'GitHub Copilot for Azure', 'Azure App Service' and other extension depend on this."

	tests := []struct {
		name           string
		packageManager string
		stderr         string
		wantDetail     string
		wantOK         bool
	}{
		{
			name:           "MultiDependentPluralFromIssue8830",
			packageManager: "code",
			stderr:         issue8830Stderr,
			wantDetail:     issue8830Stderr,
			wantOK:         true,
		},
		{
			name:           "SingleDependent",
			packageManager: "code",
			stderr:         "Cannot uninstall 'Azure Resources' extension. 'Azure App Service' extension depends on this.",
			wantDetail:     "Cannot uninstall 'Azure Resources' extension. 'Azure App Service' extension depends on this.",
			wantOK:         true,
		},
		{
			name:           "TrimsSurroundingWhitespace",
			packageManager: "code",
			stderr:         "\n  'Foo' extension depends on this.  \n",
			wantDetail:     "'Foo' extension depends on this.",
			wantOK:         true,
		},
		{
			name:           "NonCodeManagerIgnored",
			packageManager: "winget",
			stderr:         "something depends on this",
			wantDetail:     "",
			wantOK:         false,
		},
		{
			name:           "UnrelatedCodeErrorIgnored",
			packageManager: "code",
			stderr:         "Extension 'ms-azuretools.vscode-azureresourcegroups' is not installed.",
			wantDetail:     "",
			wantOK:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			detail, ok := vscodeDependencyConflict(tt.packageManager, tt.stderr)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantDetail, detail)
		})
	}
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
		cask      bool
		expectCmd string
		expectArg string // action keyword to find in args
	}{
		{"Winget", "winget", "Microsoft.AzureCLI", false, "winget", "uninstall"},
		{"Brew", "brew", "azure-cli", false, "brew", "uninstall"},
		{"BrewCask", "brew", "copilot-cli", true, "brew", "--cask"},
		{"Apt", "apt", "azure-cli", false, "sudo", "remove"},
		{"Npm", "npm", "@azure/mcp", false, "npm", "uninstall"},
		{"Code", "code", "ms-azuretools.vscode-bicep", false, "code", "--uninstall-extension"},
		{"UnknownManagerReturnsEmpty", "unknown-mgr", "pkg", false, "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cmd, args := buildUninstallCommand(tt.manager, tt.packageID, tt.cask)

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

// TestRunSkillUninstall_StepProgress verifies the step spinner is rendered
// per host (shown then stopped, capitalized) for a skill uninstall, with no
// skill-count sub-line.
func TestRunSkillUninstall_StepProgress(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "copilot")
	runner.When(func(args exec.RunArgs, _ string) bool {
		return slices.Contains(args.Args, "uninstall")
	}).Respond(exec.RunResult{ExitCode: 0})

	// After uninstall, copilot no longer reports the skill so verification
	// passes.
	det := &mockDetector{
		detectSkillHostsFn: func(
			_ context.Context, _ *ToolDefinition,
		) ([]InstalledSkillHost, error) {
			return nil, nil
		},
	}

	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	var r fakeStepRenderer
	result, err := inst.Uninstall(
		t.Context(), newSkillTool(),
		WithHosts("copilot"),
		WithStepProgress(&r),
	)
	require.NoError(t, err)
	require.True(t, result.Success, "uninstall must succeed; err=%v", result.Error)

	// A silent uninstall keeps the spinner for the whole step, then stops it
	// with the result.
	assert.Equal(t, []string{"Uninstalling Test Azure Skills from copilot"}, r.starts)
	assert.Equal(t, []string{"Uninstalling Test Azure Skills from copilot"}, r.stops)
	assert.Empty(t, r.messages)
}

// TestRunSkillUninstall_StepProgress_SuccessHidesOutput verifies that when the
// host CLI prints output and the uninstall completes without error, that
// output is NOT surfaced above the spinner — a successful step stays quiet.
func TestRunSkillUninstall_StepProgress_SuccessHidesOutput(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "copilot")
	runner.When(func(args exec.RunArgs, _ string) bool {
		return slices.Contains(args.Args, "uninstall")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		if args.StdOut != nil {
			_, _ = args.StdOut.Write([]byte("Plugin \"azure@azure-skills\" uninstalled successfully.\n"))
		}
		return exec.RunResult{ExitCode: 0}, nil
	})

	// After uninstall the skill is gone, so verification passes.
	det := &mockDetector{
		detectSkillHostsFn: func(
			_ context.Context, _ *ToolDefinition,
		) ([]InstalledSkillHost, error) {
			return nil, nil
		},
	}
	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	var r fakeStepRenderer
	result, err := inst.Uninstall(
		t.Context(), newSkillTool(),
		WithHosts("copilot"),
		WithStepProgress(&r),
	)
	require.NoError(t, err)
	require.True(t, result.Success, "uninstall must succeed; err=%v", result.Error)

	assert.Empty(t, r.messages,
		"host CLI output must be hidden when uninstall completes without error")
	assert.Equal(t, []string{"Uninstalling Test Azure Skills from copilot"}, r.stops)
}

// TestRunSkillUninstall_StepProgress_FailureShowsOutput verifies the converse:
// when the uninstall fails, the buffered host CLI output is replayed so the
// user can see what went wrong.
func TestRunSkillUninstall_StepProgress_FailureShowsOutput(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	mockHostPresence(runner, "copilot")
	runner.When(func(args exec.RunArgs, _ string) bool {
		return slices.Contains(args.Args, "uninstall")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		if args.StdOut != nil {
			_, _ = args.StdOut.Write([]byte("could not remove plugin\n"))
		}
		return exec.RunResult{ExitCode: 1}, errors.New("exit code 1")
	})

	det := &mockDetector{
		detectSkillHostsFn: func(
			_ context.Context, _ *ToolDefinition,
		) ([]InstalledSkillHost, error) {
			return []InstalledSkillHost{{Host: "copilot", Version: "1.0.0"}}, nil
		},
	}
	inst := NewInstaller(runner, NewPlatformDetector(runner), det)

	var r fakeStepRenderer
	result, _ := inst.Uninstall(
		t.Context(), newSkillTool(),
		WithHosts("copilot"),
		WithStepProgress(&r),
	)
	require.False(t, result.Success)
	require.Error(t, result.Error)

	assert.Contains(t, r.messages, "could not remove plugin",
		"host CLI output must be shown when uninstall fails")
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
		InstallStrategies: map[string][]InstallStrategy{
			"plan9": {{PackageManager: "npm", PackageId: "x"}},
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
				Command:           "copilot",
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

// TestHostUsable_OnPathProbeMemoizedAcrossPasses verifies that an on-PATH
// host's `--version` is spawned at most once per command even though host
// resolution probes it from several call sites: the result is memoized on the
// installer for its lifetime (one process == one command).
func TestHostUsable_OnPathProbeMemoizedAcrossPasses(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	runner.MockToolInPath("copilot", nil)
	runner.MockToolInPath("claude", errors.New("not found"))

	var copilotVersionCalls int
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "copilot" && slices.Contains(args.Args, "--version")
	}).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
		copilotVersionCalls++
		return exec.RunResult{Stdout: "GitHub Copilot CLI 1.1.70", ExitCode: 0}, nil
	})

	inst := NewInstaller(runner, NewPlatformDetector(runner), &mockDetector{})

	// Two separate host-resolution passes on the same installer.
	commands1, _ := inst.AvailableSkillHosts(t.Context(), newSkillTool())
	require.Equal(t, []string{"copilot"}, commands1)
	commands2, _ := inst.AvailableSkillHosts(t.Context(), newSkillTool())
	require.Equal(t, []string{"copilot"}, commands2)

	assert.Equal(t, 1, copilotVersionCalls,
		"on-PATH host `--version` must be probed at most once per command")
}

// TestHostUsable_NotOnPathResultNotMemoized verifies that a not-on-PATH result
// is never cached, so a host installed earlier in the same command (e.g.
// `azd tool install github-copilot-cli azure-skills`) is still picked up by a
// later resolution pass.
func TestHostUsable_NotOnPathResultNotMemoized(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	runner.MockToolInPath("copilot", errors.New("not found"))
	runner.MockToolInPath("claude", errors.New("not found"))

	inst := NewInstaller(runner, NewPlatformDetector(runner), &mockDetector{})

	// First pass: copilot absent.
	commandsAbsent, _ := inst.AvailableSkillHosts(t.Context(), newSkillTool())
	require.Empty(t, commandsAbsent)

	// copilot becomes available and functional mid-command.
	runner.MockToolInPath("copilot", nil)
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "copilot" && slices.Contains(args.Args, "--version")
	}).Respond(exec.RunResult{Stdout: "GitHub Copilot CLI 1.1.70", ExitCode: 0})

	// Second pass picks it up: the earlier "not on PATH" result was not cached.
	commandsPresent, _ := inst.AvailableSkillHosts(t.Context(), newSkillTool())
	assert.Equal(t, []string{"copilot"}, commandsPresent)
}

// TestLineWriter_Write_SerializesEmit drives lineWriter.Write from many
// goroutines at once — as os/exec does when a command's stdout and stderr are
// written concurrently — with an unsynchronized emit (mirroring the buffered
// slice in renderSkillStep). The mutex must serialize emit so no line is lost
// or corrupted. Run under -race to catch a regression.
func TestLineWriter_Write_SerializesEmit(t *testing.T) {
	var got []string // deliberately unsynchronized, like renderSkillStep's buffer
	lw := &lineWriter{emit: func(s string) { got = append(got, s) }}

	const writers = 100
	var wg sync.WaitGroup
	for range writers {
		wg.Go(func() {
			_, _ = lw.Write([]byte("a\nb\nc\n")) // 3 lines per write
		})
	}
	wg.Wait()

	assert.Len(t, got, writers*3,
		"every emitted line must be recorded without loss under concurrent writes")
}
