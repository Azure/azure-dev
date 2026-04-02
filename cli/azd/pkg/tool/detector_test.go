// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tool

import (
	"context"
	"errors"
	osexec "os/exec"
	"slices"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockexec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// DetectTool — CLI tools
// ---------------------------------------------------------------------------

func TestDetectTool_CLI(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		tool             *ToolDefinition
		setup            func(*mockexec.MockCommandRunner)
		expectInstalled  bool
		expectVersion    string
		expectError      bool
		expectErrContain string
	}{
		{
			name: "InstalledWithVersion",
			tool: &ToolDefinition{
				Id:            "az-cli",
				Category:      ToolCategoryCLI,
				DetectCommand: "az",
				VersionArgs:   []string{"--version"},
				VersionRegex:  `azure-cli\s+(\d+\.\d+\.\d+)`,
			},
			setup: func(runner *mockexec.MockCommandRunner) {
				runner.MockToolInPath("az", nil)
				runner.When(func(args exec.RunArgs, _ string) bool {
					return args.Cmd == "az" &&
						slices.Contains(args.Args, "--version")
				}).Respond(exec.RunResult{
					ExitCode: 0,
					Stdout:   "azure-cli 2.64.0\ncore 2.64.0",
				})
			},
			expectInstalled: true,
			expectVersion:   "2.64.0",
		},
		{
			name: "NotInstalledWhenNotOnPATH",
			tool: &ToolDefinition{
				Id:            "az-cli",
				Category:      ToolCategoryCLI,
				DetectCommand: "az",
				VersionArgs:   []string{"--version"},
				VersionRegex:  `azure-cli\s+(\d+\.\d+\.\d+)`,
			},
			setup: func(runner *mockexec.MockCommandRunner) {
				runner.MockToolInPath("az", osexec.ErrNotFound)
			},
			expectInstalled: false,
		},
		{
			name: "InstalledButVersionRegexDoesNotMatch",
			tool: &ToolDefinition{
				Id:            "custom-cli",
				Category:      ToolCategoryCLI,
				DetectCommand: "custom",
				VersionArgs:   []string{"--version"},
				VersionRegex:  `custom-cli\s+(\d+\.\d+\.\d+)`,
			},
			setup: func(runner *mockexec.MockCommandRunner) {
				runner.MockToolInPath("custom", nil)
				runner.When(func(args exec.RunArgs, _ string) bool {
					return args.Cmd == "custom"
				}).Respond(exec.RunResult{
					ExitCode: 0,
					Stdout:   "completely different output format",
				})
			},
			expectInstalled: true,
			expectVersion:   "", // regex doesn't match
		},
		{
			name: "InstalledWithNonZeroExitStillParsesVersion",
			tool: &ToolDefinition{
				Id:            "quirky-cli",
				Category:      ToolCategoryCLI,
				DetectCommand: "quirky",
				VersionArgs:   []string{"--version"},
				VersionRegex:  `(\d+\.\d+\.\d+)`,
			},
			setup: func(runner *mockexec.MockCommandRunner) {
				runner.MockToolInPath("quirky", nil)
				runner.When(func(args exec.RunArgs, _ string) bool {
					return args.Cmd == "quirky"
				}).RespondFn(
					func(args exec.RunArgs) (exec.RunResult, error) {
						return exec.RunResult{
							ExitCode: 1,
							Stderr:   "quirky 1.2.3",
						}, errors.New("exit status 1")
					},
				)
			},
			expectInstalled: true,
			expectVersion:   "1.2.3",
		},
		{
			name: "EmptyDetectCommandReturnsNotInstalled",
			tool: &ToolDefinition{
				Id:            "no-detect",
				Category:      ToolCategoryCLI,
				DetectCommand: "",
			},
			setup:           func(_ *mockexec.MockCommandRunner) {},
			expectInstalled: false,
		},
		{
			name: "ContextCancelledReturnsError",
			tool: &ToolDefinition{
				Id:            "slow-cli",
				Category:      ToolCategoryCLI,
				DetectCommand: "slow",
				VersionArgs:   []string{"--version"},
			},
			setup: func(runner *mockexec.MockCommandRunner) {
				runner.MockToolInPath("slow", nil)
				runner.When(func(args exec.RunArgs, _ string) bool {
					return args.Cmd == "slow"
				}).SetError(context.Canceled)
			},
			expectInstalled:  false,
			expectError:      true,
			expectErrContain: "running slow",
		},
		{
			name: "VersionParsedFromStderr",
			tool: &ToolDefinition{
				Id:            "stderr-ver",
				Category:      ToolCategoryCLI,
				DetectCommand: "stderr-ver",
				VersionArgs:   []string{"--version"},
				VersionRegex:  `v(\d+\.\d+\.\d+)`,
			},
			setup: func(runner *mockexec.MockCommandRunner) {
				runner.MockToolInPath("stderr-ver", nil)
				runner.When(func(args exec.RunArgs, _ string) bool {
					return args.Cmd == "stderr-ver"
				}).Respond(exec.RunResult{
					ExitCode: 0,
					Stdout:   "",
					Stderr:   "stderr-ver v3.5.1",
				})
			},
			expectInstalled: true,
			expectVersion:   "3.5.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			runner := mockexec.NewMockCommandRunner()
			tt.setup(runner)

			d := NewDetector(runner)
			status, err := d.DetectTool(t.Context(), tt.tool)

			require.NoError(t, err)
			require.NotNil(t, status)
			assert.Equal(t, tt.expectInstalled, status.Installed)
			assert.Equal(t, tt.expectVersion, status.InstalledVersion)

			if tt.expectError {
				require.Error(t, status.Error)
				if tt.expectErrContain != "" {
					assert.Contains(t, status.Error.Error(),
						tt.expectErrContain)
				}
			} else {
				assert.NoError(t, status.Error)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// DetectTool — VS Code extensions
// ---------------------------------------------------------------------------

func TestDetectTool_Extension(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		tool             *ToolDefinition
		setup            func(*mockexec.MockCommandRunner)
		expectInstalled  bool
		expectVersion    string
		expectError      bool
		expectErrContain string
	}{
		{
			name: "ExtensionFoundInList",
			tool: &ToolDefinition{
				Id:            "vscode-bicep",
				Category:      ToolCategoryExtension,
				DetectCommand: "code",
				VersionArgs: []string{
					"--list-extensions", "--show-versions",
				},
				VersionRegex: `ms-azuretools\.vscode-bicep@(\d+\.\d+\.\d+)`,
			},
			setup: func(runner *mockexec.MockCommandRunner) {
				runner.MockToolInPath("code", nil)
				runner.When(func(args exec.RunArgs, _ string) bool {
					return args.Cmd == "code" &&
						slices.Contains(args.Args, "--list-extensions")
				}).Respond(exec.RunResult{
					ExitCode: 0,
					Stdout: "ms-azuretools.vscode-bicep@0.29.47\n" +
						"ms-python.python@2024.0.1\n",
				})
			},
			expectInstalled: true,
			expectVersion:   "0.29.47",
		},
		{
			name: "ExtensionNotInList",
			tool: &ToolDefinition{
				Id:            "vscode-bicep",
				Category:      ToolCategoryExtension,
				DetectCommand: "code",
				VersionArgs: []string{
					"--list-extensions", "--show-versions",
				},
				VersionRegex: `ms-azuretools\.vscode-bicep@(\d+\.\d+\.\d+)`,
			},
			setup: func(runner *mockexec.MockCommandRunner) {
				runner.MockToolInPath("code", nil)
				runner.When(func(args exec.RunArgs, _ string) bool {
					return args.Cmd == "code"
				}).Respond(exec.RunResult{
					ExitCode: 0,
					Stdout:   "ms-python.python@2024.0.1\n",
				})
			},
			expectInstalled: false,
			expectVersion:   "",
		},
		{
			name: "CodeNotOnPATH",
			tool: &ToolDefinition{
				Id:            "vscode-bicep",
				Category:      ToolCategoryExtension,
				DetectCommand: "code",
				VersionArgs: []string{
					"--list-extensions", "--show-versions",
				},
				VersionRegex: `ms-azuretools\.vscode-bicep@(\d+\.\d+\.\d+)`,
			},
			setup: func(runner *mockexec.MockCommandRunner) {
				runner.MockToolInPath("code", osexec.ErrNotFound)
			},
			expectInstalled: false,
			expectVersion:   "",
		},
		{
			name: "DefaultsDetectCommandToCode",
			tool: &ToolDefinition{
				Id:            "vscode-ext",
				Category:      ToolCategoryExtension,
				DetectCommand: "", // empty => defaults to "code"
				VersionArgs: []string{
					"--list-extensions", "--show-versions",
				},
				VersionRegex: `my-ext@(\d+\.\d+\.\d+)`,
			},
			setup: func(runner *mockexec.MockCommandRunner) {
				runner.MockToolInPath("code", nil)
				runner.When(func(args exec.RunArgs, _ string) bool {
					return args.Cmd == "code"
				}).Respond(exec.RunResult{
					ExitCode: 0,
					Stdout:   "my-ext@1.0.0\n",
				})
			},
			expectInstalled: true,
			expectVersion:   "1.0.0",
		},
		{
			name: "ContextCancelledReturnsError",
			tool: &ToolDefinition{
				Id:            "vscode-ext-timeout",
				Category:      ToolCategoryExtension,
				DetectCommand: "code",
				VersionArgs: []string{
					"--list-extensions", "--show-versions",
				},
			},
			setup: func(runner *mockexec.MockCommandRunner) {
				runner.MockToolInPath("code", nil)
				runner.When(func(args exec.RunArgs, _ string) bool {
					return args.Cmd == "code"
				}).SetError(context.Canceled)
			},
			expectInstalled:  false,
			expectError:      true,
			expectErrContain: "listing VS Code extensions",
		},
		{
			name: "NonErrNotFoundPathError",
			tool: &ToolDefinition{
				Id:            "vscode-ext-perms",
				Category:      ToolCategoryExtension,
				DetectCommand: "code",
			},
			setup: func(runner *mockexec.MockCommandRunner) {
				runner.MockToolInPath("code",
					errors.New("permission denied"))
			},
			expectInstalled:  false,
			expectError:      true,
			expectErrContain: "checking PATH",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			runner := mockexec.NewMockCommandRunner()
			tt.setup(runner)

			d := NewDetector(runner)
			status, err := d.DetectTool(t.Context(), tt.tool)

			require.NoError(t, err)
			require.NotNil(t, status)
			assert.Equal(t, tt.expectInstalled, status.Installed)
			assert.Equal(t, tt.expectVersion, status.InstalledVersion)

			if tt.expectError {
				require.Error(t, status.Error)
				if tt.expectErrContain != "" {
					assert.Contains(t, status.Error.Error(),
						tt.expectErrContain)
				}
			} else {
				assert.NoError(t, status.Error)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// DetectTool — Server / Library (commandBased)
// ---------------------------------------------------------------------------

func TestDetectTool_CommandBased(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		tool             *ToolDefinition
		setup            func(*mockexec.MockCommandRunner)
		expectInstalled  bool
		expectVersion    string
		expectError      bool
		expectErrContain string
	}{
		{
			name: "ServerToolDetected",
			tool: &ToolDefinition{
				Id:            "azure-mcp-server",
				Category:      ToolCategoryServer,
				DetectCommand: "npx",
				VersionArgs:   []string{"@azure/mcp@latest", "--version"},
				VersionRegex:  `(\d+\.\d+\.\d+)`,
			},
			setup: func(runner *mockexec.MockCommandRunner) {
				runner.MockToolInPath("npx", nil)
				runner.When(func(args exec.RunArgs, _ string) bool {
					return args.Cmd == "npx"
				}).Respond(exec.RunResult{
					ExitCode: 0,
					Stdout:   "1.0.0",
				})
			},
			expectInstalled: true,
			expectVersion:   "1.0.0",
		},
		{
			name: "LibraryToolWithNoVersionArgs",
			tool: &ToolDefinition{
				Id:            "simple-lib",
				Category:      ToolCategoryLibrary,
				DetectCommand: "simple",
			},
			setup: func(runner *mockexec.MockCommandRunner) {
				runner.MockToolInPath("simple", nil)
			},
			expectInstalled: true,
			expectVersion:   "",
		},
		{
			name: "NoDetectCommandReturnsNotInstalled",
			tool: &ToolDefinition{
				Id:       "no-cmd",
				Category: ToolCategoryServer,
			},
			setup:           func(_ *mockexec.MockCommandRunner) {},
			expectInstalled: false,
		},
		{
			name: "ContextCancelledReturnsError",
			tool: &ToolDefinition{
				Id:            "slow-server",
				Category:      ToolCategoryServer,
				DetectCommand: "slow",
				VersionArgs:   []string{"--version"},
			},
			setup: func(runner *mockexec.MockCommandRunner) {
				runner.MockToolInPath("slow", nil)
				runner.When(func(args exec.RunArgs, _ string) bool {
					return args.Cmd == "slow"
				}).SetError(context.DeadlineExceeded)
			},
			expectInstalled:  false,
			expectError:      true,
			expectErrContain: "running slow",
		},
		{
			name: "NonErrNotFoundPathError",
			tool: &ToolDefinition{
				Id:            "perm-denied",
				Category:      ToolCategoryServer,
				DetectCommand: "restricted",
				VersionArgs:   []string{"--version"},
			},
			setup: func(runner *mockexec.MockCommandRunner) {
				runner.MockToolInPath("restricted",
					errors.New("permission denied"))
			},
			expectInstalled:  false,
			expectError:      true,
			expectErrContain: "checking PATH",
		},
		{
			name: "NotFoundOnRunReturnsNotInstalled",
			tool: &ToolDefinition{
				Id:            "transient",
				Category:      ToolCategoryLibrary,
				DetectCommand: "transient",
				VersionArgs:   []string{"--version"},
			},
			setup: func(runner *mockexec.MockCommandRunner) {
				runner.MockToolInPath("transient", nil)
				runner.When(func(args exec.RunArgs, _ string) bool {
					return args.Cmd == "transient"
				}).SetError(osexec.ErrNotFound)
			},
			expectInstalled: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			runner := mockexec.NewMockCommandRunner()
			tt.setup(runner)

			d := NewDetector(runner)
			status, err := d.DetectTool(t.Context(), tt.tool)

			require.NoError(t, err)
			require.NotNil(t, status)
			assert.Equal(t, tt.expectInstalled, status.Installed)
			assert.Equal(t, tt.expectVersion, status.InstalledVersion)

			if tt.expectError {
				require.Error(t, status.Error)
				if tt.expectErrContain != "" {
					assert.Contains(t, status.Error.Error(),
						tt.expectErrContain)
				}
			} else {
				assert.NoError(t, status.Error)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// DetectTool — edge cases
// ---------------------------------------------------------------------------

func TestDetectTool_NilTool(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	d := NewDetector(runner)

	status, err := d.DetectTool(t.Context(), nil)
	require.Error(t, err)
	require.Nil(t, status)
	assert.Contains(t, err.Error(), "nil")
}

func TestDetectTool_UnknownCategory(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	d := NewDetector(runner)

	tool := &ToolDefinition{
		Id:       "weird",
		Category: ToolCategory("unknown-cat"),
	}

	status, err := d.DetectTool(t.Context(), tool)
	require.NoError(t, err)
	require.NotNil(t, status)
	assert.False(t, status.Installed)
}

// ---------------------------------------------------------------------------
// DetectAll
// ---------------------------------------------------------------------------

func TestDetectAll(t *testing.T) {
	t.Parallel()

	t.Run("RunsAllToolsAndReturnsResults", func(t *testing.T) {
		t.Parallel()

		runner := mockexec.NewMockCommandRunner()

		// Tool 1: found
		runner.MockToolInPath("toolA", nil)
		runner.When(func(args exec.RunArgs, _ string) bool {
			return args.Cmd == "toolA"
		}).Respond(exec.RunResult{
			ExitCode: 0,
			Stdout:   "toolA 1.0.0",
		})

		// Tool 2: not found
		runner.MockToolInPath("toolB", osexec.ErrNotFound)

		tools := []*ToolDefinition{
			{
				Id:            "tool-a",
				Category:      ToolCategoryCLI,
				DetectCommand: "toolA",
				VersionArgs:   []string{"--version"},
				VersionRegex:  `(\d+\.\d+\.\d+)`,
			},
			{
				Id:            "tool-b",
				Category:      ToolCategoryCLI,
				DetectCommand: "toolB",
				VersionArgs:   []string{"--version"},
			},
		}

		d := NewDetector(runner)
		results, err := d.DetectAll(t.Context(), tools)

		require.NoError(t, err)
		require.Len(t, results, 2)

		assert.True(t, results[0].Installed)
		assert.Equal(t, "1.0.0", results[0].InstalledVersion)

		assert.False(t, results[1].Installed)
	})

	t.Run("HandlesNilToolInSlice", func(t *testing.T) {
		t.Parallel()

		runner := mockexec.NewMockCommandRunner()
		d := NewDetector(runner)

		tools := []*ToolDefinition{nil}
		results, err := d.DetectAll(t.Context(), tools)

		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.NotNil(t, results[0])
	})

	t.Run("EmptySliceReturnsEmptyResults", func(t *testing.T) {
		t.Parallel()

		runner := mockexec.NewMockCommandRunner()
		d := NewDetector(runner)

		results, err := d.DetectAll(t.Context(), []*ToolDefinition{})

		require.NoError(t, err)
		require.Empty(t, results)
	})
}

// ---------------------------------------------------------------------------
// matchVersion helper
// ---------------------------------------------------------------------------

func TestMatchVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		output  string
		pattern string
		expect  string
	}{
		{
			name:    "MatchesAzureCLI",
			output:  "azure-cli 2.64.0\ncore 2.64.0",
			pattern: `azure-cli\s+(\d+\.\d+\.\d+)`,
			expect:  "2.64.0",
		},
		{
			name:    "MatchesSimpleVersion",
			output:  "v1.2.3",
			pattern: `v(\d+\.\d+\.\d+)`,
			expect:  "1.2.3",
		},
		{
			name:    "EmptyOutputReturnsEmpty",
			output:  "",
			pattern: `(\d+\.\d+\.\d+)`,
			expect:  "",
		},
		{
			name:    "EmptyPatternReturnsEmpty",
			output:  "1.2.3",
			pattern: "",
			expect:  "",
		},
		{
			name:    "NoMatchReturnsEmpty",
			output:  "no version here",
			pattern: `(\d+\.\d+\.\d+)`,
			expect:  "",
		},
		{
			name:    "InvalidRegexReturnsEmpty",
			output:  "1.2.3",
			pattern: `(((invalid`,
			expect:  "",
		},
		{
			name:    "NoCaptureGroupReturnsEmpty",
			output:  "version 1.2.3",
			pattern: `\d+\.\d+\.\d+`, // no capture group
			expect:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := matchVersion(tt.output, tt.pattern)
			assert.Equal(t, tt.expect, result)
		})
	}
}
