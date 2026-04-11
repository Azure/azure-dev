// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec/scripting"
	"github.com/azure/azure-dev/cli/azd/pkg/keyvault"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func execActions(root *actions.ActionDescriptor) *actions.ActionDescriptor {
	root.Add("exec", &actions.ActionDescriptorOptions{
		Command:        newExecCmd(),
		FlagsResolver:  newExecFlags,
		ActionResolver: newExecAction,
		OutputFormats:  []output.Format{output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
		GroupingOptions: actions.CommandGroupOptions{
			RootLevelHelp: actions.CmdGroupManage,
		},
	})
	return root
}

func newExecCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exec [command] [args...] [-- script-args...]",
		Short: "Execute commands and scripts with azd environment context.",
		Long: `Execute commands and scripts with full access to azd environment variables.

Commands are run with the azd environment loaded into the child process.
Multiple arguments use direct process execution (no shell wrapping).
A single quoted argument uses shell inline execution.

Examples:
  azd exec python script.py                     # Direct exec (exact argv)
  azd exec npm run dev                           # Direct exec (no shell)
  azd exec -- python app.py --port 8000          # Direct exec with flags
  azd exec 'echo $AZURE_ENV_NAME'                # Inline via shell
  azd exec ./setup.sh                            # Execute script file
  azd exec --shell pwsh "Write-Host 'Hello'"     # Inline PowerShell
  azd exec ./build.sh -- --verbose               # Script with args
  azd exec -i ./init.sh                          # Interactive mode`,
		Args: cobra.MinimumNArgs(1),
	}
	// Stop cobra from parsing flags after the first positional argument
	// so that `azd exec python --version` passes --version to python.
	cmd.Flags().SetInterspersed(false)
	cmd.FParseErrWhitelist.UnknownFlags = true
	return cmd
}

type execFlags struct {
	internal.EnvFlag
	global      *internal.GlobalCommandOptions
	shell       string
	interactive bool
}

func newExecFlags(
	cmd *cobra.Command, global *internal.GlobalCommandOptions,
) *execFlags {
	flags := &execFlags{}
	flags.Bind(cmd.Flags(), global)
	return flags
}

func (f *execFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.EnvFlag.Bind(local, global)
	f.global = global

	local.StringVarP(&f.shell, "shell", "s", "",
		"Shell to use (bash, sh, zsh, pwsh, powershell, cmd). "+
			"Auto-detected if not specified.")
	local.BoolVarP(&f.interactive, "interactive", "i", false,
		"Run in interactive mode (connect stdin)")
}

type execAction struct {
	env             *environment.Environment
	keyvaultService keyvault.KeyVaultService
	flags           *execFlags
	args            []string
}

func newExecAction(
	env *environment.Environment,
	keyvaultService keyvault.KeyVaultService,
	flags *execFlags,
	args []string,
) actions.Action {
	return &execAction{
		env:             env,
		keyvaultService: keyvaultService,
		flags:           flags,
		args:            args,
	}
}

// buildChildEnv creates a scoped environment for the child process.
// It starts from the current process env, then overlays azd environment
// variables. Key Vault references (akvs:// and @Microsoft.KeyVault(SecretUri=...))
// are resolved transparently. We never call os.Setenv — secrets stay out of
// the parent process.
func (a *execAction) buildChildEnv(ctx context.Context) ([]string, error) {
	childEnv := os.Environ()
	subscriptionId := a.env.GetSubscriptionId()
	for key, value := range a.env.Dotenv() {
		resolved := value
		if keyvault.IsSecretReference(value) {
			secret, err := a.keyvaultService.SecretFromKeyVaultReference(ctx, value, subscriptionId)
			if err != nil {
				return nil, fmt.Errorf(
					"resolving secret for %q: %w", key, err)
			}
			resolved = secret
		}
		childEnv = append(childEnv, key+"="+resolved)
	}
	return childEnv, nil
}

func (a *execAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	childEnv, err := a.buildChildEnv(ctx)
	if err != nil {
		return nil, err
	}

	scriptInput := a.args[0]
	var scriptArgs []string
	if len(a.args) > 1 {
		scriptArgs = a.args[1:]
	}

	exec, err := scripting.New(scripting.Config{
		Shell:       a.flags.shell,
		Interactive: a.flags.interactive,
		Args:        scriptArgs,
		Env:         childEnv,
	})
	if err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	// Try file execution first; fall back based on argument shape.
	if err := exec.Execute(ctx, scriptInput); err != nil {
		if _, ok := errors.AsType[*scripting.ScriptNotFoundError](err); ok {
			if len(scriptArgs) > 0 && a.flags.shell == "" {
				err = exec.ExecuteDirect(ctx, scriptInput, scriptArgs)
			} else {
				err = exec.ExecuteInline(ctx, scriptInput)
			}
		}
		if err != nil {
			if execErr, ok := errors.AsType[*scripting.ExecutionError](err); ok {
				return nil, &internal.ExitCodeError{
					ExitCode: execErr.ExitCode,
					Err:      err,
				}
			}
			return nil, err
		}
	}

	return nil, nil
}
