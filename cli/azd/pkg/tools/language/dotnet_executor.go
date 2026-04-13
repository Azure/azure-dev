// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package language

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
	"github.com/blang/semver/v4"
)

// dotnetTools abstracts the .NET CLI operations needed by
// dotnetExecutor, decoupling it from the concrete [dotnet.Cli]
// for testability. [dotnet.Cli] satisfies this interface.
type dotnetTools interface {
	CheckInstalled(ctx context.Context) error
	SdkVersion(ctx context.Context) (semver.Version, error)
	Restore(ctx context.Context, project string, env []string) error
	Build(
		ctx context.Context,
		project string, configuration string,
		output string, env []string,
	) error
}

// minSingleFileVersion is the minimum .NET SDK version that
// supports running single .cs files without a project file.
var minSingleFileVersion = semver.Version{
	Major: 10, Minor: 0, Patch: 0,
}

// dotnetHookConfig holds the typed configuration that users can
// specify in azure.yaml under a .NET hook's config section.
type dotnetHookConfig struct {
	// Configuration is the MSBuild configuration (Debug, Release,
	// or a custom name). Passed as `-c` to dotnet build and
	// dotnet run. Empty means use the SDK default (Debug).
	Configuration string `json:"configuration"`

	// Framework is the target framework moniker (e.g. net8.0,
	// net10.0). Passed as `--framework` to dotnet run so the
	// correct TFM is selected in multi-target projects.
	Framework string `json:"framework"`
}

// dotnetExecutor implements [tools.HookExecutor] for .NET (C#)
// scripts. It supports two execution modes:
//   - Project mode: when a .csproj/.fsproj/.vbproj is discovered
//     near the script, runs dotnet restore → build → run --project.
//   - Single-file mode: when no project file is found and the SDK
//     is .NET 10+, runs dotnet run script.cs directly.
type dotnetExecutor struct {
	commandRunner exec.CommandRunner
	dotnetCli     dotnetTools

	// projectPath is set by Prepare when a .NET project file is
	// discovered. Empty means single-file mode.
	projectPath string

	// config holds parsed hook configuration from azure.yaml.
	config dotnetHookConfig
}

// NewDotNetExecutor creates a .NET HookExecutor.
// Takes only IoC-injectable deps.
func NewDotNetExecutor(
	commandRunner exec.CommandRunner,
	dotnetCli *dotnet.Cli,
) tools.HookExecutor {
	return newDotNetExecutorInternal(commandRunner, dotnetCli)
}

// newDotNetExecutorInternal creates a dotnetExecutor using the
// dotnetTools interface. This allows tests to inject mocks.
func newDotNetExecutorInternal(
	commandRunner exec.CommandRunner,
	dotnetCli dotnetTools,
) *dotnetExecutor {
	return &dotnetExecutor{
		commandRunner: commandRunner,
		dotnetCli:     dotnetCli,
	}
}

// Prepare verifies that the .NET SDK is installed and, when a
// project file (.csproj/.fsproj/.vbproj) is found near the script,
// runs dotnet restore and dotnet build.
//
// For single-file .cs scripts (no project file), Prepare validates
// that the installed SDK supports single-file execution (.NET 10+).
func (e *dotnetExecutor) Prepare(
	ctx context.Context,
	scriptPath string,
	execCtx tools.ExecutionContext,
) error {
	// 1. Verify .NET SDK is installed.
	if err := e.dotnetCli.CheckInstalled(ctx); err != nil {
		return &errorhandler.ErrorWithSuggestion{
			Err: err,
			Message: ".NET SDK is required to run " +
				".NET hooks.",
			Suggestion: "Install the .NET SDK from " +
				"https://dotnet.microsoft.com/download",
			Links: []errorhandler.ErrorLink{{
				Title: "Download .NET SDK",
				URL:   "https://dotnet.microsoft.com/download",
			}},
		}
	}

	// 2. Parse executor-specific config (configuration, framework).
	cfg, err := tools.UnmarshalHookConfig[dotnetHookConfig](
		execCtx.Config,
	)
	if err != nil {
		return fmt.Errorf(
			"parsing .NET hook config: %w", err,
		)
	}
	e.config = cfg

	// 3. Discover .NET project context (.csproj/.fsproj/.vbproj).
	// Uses DiscoverDotNetProject instead of the generic
	// DiscoverProjectFile to avoid Python/Node.js project files
	// shadowing the .NET project file in mixed-language directories.
	projCtx, err := DiscoverDotNetProject(
		scriptPath, execCtx.BoundaryDir,
	)
	if err != nil {
		return fmt.Errorf(
			"discovering .NET project file: %w", err,
		)
	}

	// 4a. Project mode: restore and build.
	if projCtx != nil {
		if err := e.dotnetCli.Restore(
			ctx, projCtx.DependencyFile, execCtx.EnvVars,
		); err != nil {
			return fmt.Errorf(
				"dotnet restore failed for %q: %w",
				projCtx.DependencyFile, err,
			)
		}

		if err := e.dotnetCli.Build(
			ctx, projCtx.DependencyFile,
			e.config.Configuration, "",
			execCtx.EnvVars,
		); err != nil {
			return fmt.Errorf(
				"dotnet build failed for %q: %w",
				projCtx.DependencyFile, err,
			)
		}

		e.projectPath = projCtx.DependencyFile
		return nil
	}

	// 4b. Single-file mode: validate SDK version >= 10.
	sdkVer, err := e.dotnetCli.SdkVersion(ctx)
	if err != nil {
		return fmt.Errorf(
			"detecting .NET SDK version: %w", err,
		)
	}

	if sdkVer.LT(minSingleFileVersion) {
		return &errorhandler.ErrorWithSuggestion{
			Err: fmt.Errorf(
				".NET SDK %s does not support single-file "+
					"C# execution (requires .NET 10+)",
				sdkVer,
			),
			Message: fmt.Sprintf(
				"Single-file .cs hooks require .NET SDK "+
					"10.0.0 or later (installed: %s).",
				sdkVer,
			),
			Suggestion: "Create a .csproj project file " +
				"alongside your script, or upgrade to " +
				".NET 10 or later.",
			Links: []errorhandler.ErrorLink{{
				Title: "Download .NET 10",
				URL: "https://dotnet.microsoft.com/" +
					"download/dotnet/10.0",
			}},
		}
	}

	return nil
}

// Execute runs the .NET hook at the given path.
//
// In project mode (Prepare found a project file):
//
//	dotnet run --project <project_path>
//
// In single-file mode:
//
//	dotnet run <script.cs>
func (e *dotnetExecutor) Execute(
	ctx context.Context,
	scriptPath string,
	execCtx tools.ExecutionContext,
) (exec.RunResult, error) {
	var runArgs exec.RunArgs

	if e.projectPath != "" {
		// Project mode — skip restore/build since Prepare
		// already ran them.
		args := []string{
			"run",
			"--project", e.projectPath,
			"--no-build",
		}

		if e.config.Configuration != "" {
			args = append(args, "-c", e.config.Configuration)
		}
		if e.config.Framework != "" {
			args = append(
				args, "--framework", e.config.Framework,
			)
		}

		runArgs = exec.NewRunArgs("dotnet", args...)
	} else {
		// Single-file mode.
		runArgs = exec.NewRunArgs(
			"dotnet", "run", scriptPath,
		)
	}

	// Set standard dotnet env vars to suppress noisy output,
	// then append user-provided env vars.
	runArgs = runArgs.WithEnv(append(
		[]string{
			"DOTNET_CLI_WORKLOAD_UPDATE_NOTIFY_DISABLE=1",
			"DOTNET_NOLOGO=1",
		},
		execCtx.EnvVars...,
	))

	// Prefer configured cwd; fall back to script's directory.
	cwd := execCtx.Cwd
	if cwd == "" {
		cwd = filepath.Dir(scriptPath)
	}
	runArgs = runArgs.WithCwd(cwd)

	if execCtx.Interactive != nil {
		runArgs = runArgs.WithInteractive(
			*execCtx.Interactive,
		)
	}
	if execCtx.StdOut != nil {
		runArgs = runArgs.WithStdOut(execCtx.StdOut)
	}

	return e.commandRunner.Run(ctx, runArgs)
}

// Cleanup is a no-op for the .NET executor — no temporary
// resources are created during Prepare.
func (e *dotnetExecutor) Cleanup(_ context.Context) error {
	return nil
}
