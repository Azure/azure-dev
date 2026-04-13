// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package language

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/node"
)

// jsExecutor implements [tools.HookExecutor] for JavaScript
// scripts. It manages package.json discovery and dependency
// installation via the shared [prepareNodeProject] helper, then
// executes scripts using `node`.
type jsExecutor struct {
	commandRunner exec.CommandRunner
	nodeCli       nodeTools
}

// NewJavaScriptExecutor creates a JavaScript HookExecutor.
// Takes only IoC-injectable deps.
func NewJavaScriptExecutor(
	commandRunner exec.CommandRunner,
	nodeCli node.Cli,
) tools.HookExecutor {
	return newJSExecutorInternal(commandRunner, nodeCli)
}

// newJSExecutorInternal creates a jsExecutor using the
// nodeTools interface. This allows tests to inject mocks.
func newJSExecutorInternal(
	commandRunner exec.CommandRunner,
	nodeCli nodeTools,
) *jsExecutor {
	return &jsExecutor{
		commandRunner: commandRunner,
		nodeCli:       nodeCli,
	}
}

// Prepare verifies that Node.js is installed and, when a
// package.json is found near the script, installs dependencies
// using npm.
func (e *jsExecutor) Prepare(
	ctx context.Context,
	scriptPath string,
	execCtx tools.ExecutionContext,
) error {
	_, err := prepareNodeProject(
		ctx, e.nodeCli, scriptPath, execCtx,
	)
	return err
}

// Execute runs the JavaScript script at the given path using
// `node scriptPath`.
func (e *jsExecutor) Execute(
	ctx context.Context,
	scriptPath string,
	execCtx tools.ExecutionContext,
) (exec.RunResult, error) {
	runArgs := buildNodeRunArgs(
		"node", nil, scriptPath, execCtx,
	)
	return e.commandRunner.Run(ctx, runArgs)
}

// Cleanup is a no-op — no temporary resources are created.
func (e *jsExecutor) Cleanup(_ context.Context) error {
	return nil
}
