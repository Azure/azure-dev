// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

// projectCommandHookDeps bundles the dependencies required to invoke a project
// command hook (the shell scripts declared under a project's top-level
// `hooks:` section in azure.yaml, e.g. `preprovision`, `postdeploy`).
//
// The fields mirror what [ext.NewHooksRunner] needs. The same set of values
// that the cobra hooks middleware uses when it wraps a command with its
// `pre<cmd>`/`post<cmd>` hooks — see cmd/middleware/hooks.go.
type projectCommandHookDeps struct {
	projectConfig  *project.ProjectConfig
	env            *environment.Environment
	envManager     environment.Manager
	console        input.Console
	commandRunner  exec.CommandRunner
	serviceLocator ioc.ServiceLocator
}

// runProjectCommandHook fires the pre- or post- shell hook (scope=project)
// registered under azure.yaml's top-level `hooks:` section for `commandName`
// (e.g., "provision" or "deploy").
//
// It is a no-op — no error, no side effects — when the project defines no
// hooks at all or no hook entry matches. Validation warnings are emitted by
// the cobra hooks middleware at command entry, so this helper deliberately
// does not repeat that work.
//
// The helper exists so the unified up execution graph can fire the same
// project command hooks that the cobra hooks middleware fires for stand-alone
// `azd provision` / `azd deploy` invocations, preserving feature parity with
// the previous workflow-runner-based `azd up` path.
func runProjectCommandHook(
	ctx context.Context,
	deps *projectCommandHookDeps,
	hookType ext.HookType,
	commandName string,
) error {
	if deps == nil || deps.projectConfig == nil || len(deps.projectConfig.Hooks) == 0 {
		return nil
	}

	hooksManager := ext.NewHooksManager(ext.HooksManagerOptions{
		Cwd:        deps.projectConfig.Path,
		ProjectDir: deps.projectConfig.Path,
	}, deps.commandRunner)

	hooksRunner := ext.NewHooksRunner(
		hooksManager,
		deps.commandRunner,
		deps.envManager,
		deps.console,
		deps.projectConfig.Path,
		deps.projectConfig.Hooks,
		deps.env,
		deps.serviceLocator,
	)

	return hooksRunner.RunHooks(ctx, hookType, "project", nil, commandName)
}
