// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package language

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/node"
)

// tsExecutor implements [tools.HookExecutor] for TypeScript
// scripts. It uses `npx tsx` for zero-config TypeScript
// execution — tsx handles ESM/CJS and TypeScript natively
// without requiring a separate compilation step.
type tsExecutor struct {
	commandRunner exec.CommandRunner
	nodeCli       nodeTools
}

// NewTypeScriptExecutor creates a TypeScript HookExecutor.
// Takes only IoC-injectable deps.
func NewTypeScriptExecutor(
	commandRunner exec.CommandRunner,
	nodeCli node.Cli,
) tools.HookExecutor {
	return newTSExecutorInternal(commandRunner, nodeCli)
}

// newTSExecutorInternal creates a tsExecutor using the
// nodeTools interface. This allows tests to inject mocks.
func newTSExecutorInternal(
	commandRunner exec.CommandRunner,
	nodeCli nodeTools,
) *tsExecutor {
	return &tsExecutor{
		commandRunner: commandRunner,
		nodeCli:       nodeCli,
	}
}

// Prepare verifies that Node.js is installed and, when a
// package.json is found near the script, installs dependencies
// using npm. tsx is resolved via npx at execution time, so no
// separate installation check is needed here.
func (e *tsExecutor) Prepare(
	ctx context.Context,
	scriptPath string,
	execCtx tools.ExecutionContext,
) error {
	_, err := prepareNodeProject(
		ctx, e.nodeCli, scriptPath, execCtx,
	)
	return err
}

// Execute runs the TypeScript script using `npx tsx scriptPath`.
// tsx is a zero-config TypeScript executor that handles TS
// natively without a separate compile step. When the project
// has tsx as a dependency, npx uses that version; otherwise it
// downloads tsx on demand.
func (e *tsExecutor) Execute(
	ctx context.Context,
	scriptPath string,
	execCtx tools.ExecutionContext,
) (exec.RunResult, error) {
	// npx --yes tsx -- scriptPath
	// --yes: auto-confirm download if tsx is not installed
	runArgs := buildNodeRunArgs(
		"npx", []string{"--yes", "tsx", "--"}, scriptPath, execCtx,
	)
	return e.commandRunner.Run(ctx, runArgs)
}

// Cleanup is a no-op — no temporary resources are created.
func (e *tsExecutor) Cleanup(_ context.Context) error {
	return nil
}
