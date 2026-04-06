// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package mocktools

import (
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bash"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/language"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/powershell"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/python"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
)

// RegisterHookExecutors registers all hook executors as named
// transients in the mock container so that IoC resolution works
// in tests.
func RegisterHookExecutors(mockCtx *mocks.MockContext) {
	mockCtx.Container.MustRegisterNamedTransient(
		string(language.ScriptLanguageBash), bash.NewExecutor,
	)
	mockCtx.Container.MustRegisterNamedTransient(
		string(language.ScriptLanguagePowerShell), powershell.NewExecutor,
	)
	mockCtx.Container.MustRegisterSingleton(python.NewCli)
	mockCtx.Container.MustRegisterNamedTransient(
		string(language.ScriptLanguagePython), language.NewPythonExecutor,
	)
}
