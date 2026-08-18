// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"azure.ai.rle/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type rleInitFlags struct {
	force bool
}

type initAction struct {
	cmd             *cobra.Command
	flags           *rleInitFlags
	envNameOverride string
}

var checkoutOpenEnvEchoSampleFunc = project.CheckoutOpenEnvEchoSample

func newInitCommand() *cobra.Command {
	flags := &rleInitFlags{}

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a local RLE environment",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			envNameOverride := ""
			if len(args) == 1 {
				envNameOverride = args[0]
			}
			return (&initAction{cmd: cmd, flags: flags, envNameOverride: envNameOverride}).Run()
		},
	}

	cmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		var help strings.Builder
		help.WriteString("Initialize a local RLE environment\n")
		help.WriteString("Usage:\n")
		help.WriteString("  rle init [environment-name] [flags]\n")
		help.WriteString("Flags:\n")
		help.WriteString("      --force     Overwrite generated files in an existing non-empty session directory\n")
		help.WriteString("  -h, --help      help for init\n")
		if cmd.InheritedFlags().HasAvailableFlags() {
			help.WriteString("Global Flags:\n")
			help.WriteString(cmd.InheritedFlags().FlagUsages())
		}
		_, _ = fmt.Fprint(cmd.OutOrStdout(), help.String())
	})
	cmd.Flags().BoolVar(&flags.force, "force", false, "Overwrite generated files in an existing non-empty session directory")
	return cmd
}

func (a *initAction) Run() error {
	envName := firstNonEmpty(a.envNameOverride, "echo_env")
	var err error
	envName, err = project.ValidateEnvironmentName(envName)
	if err != nil {
		return &azdext.LocalError{
			Message:    err.Error(),
			Code:       "rle_invalid_environment_name",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Use snake_case starting with a letter, for example code_rl.",
		}
	}

	sessionDir, err := checkoutOpenEnvEchoSampleFunc(envName, ".", a.flags.force)
	if err != nil {
		return err
	}

	if err := saveRleStateIn(sessionDir, defaultRleState(envName)); err != nil {
		return err
	}

	displayDir := "." + string(os.PathSeparator) + sessionDir
	_, err = fmt.Fprint(a.cmd.OutOrStdout(), initNextSteps(displayDir, runtime.GOOS, os.Getenv("SHELL")))
	return err
}

func initNextSteps(displayDir string, goos string, shell string) string {
	projectEndpoint := `https://<account>.services.ai.azure.com/api/projects/<project>`
	registryEndpoint := `<registry>.azurecr.io`
	setEnvironment := fmt.Sprintf(
		"  $env:FOUNDRY_PROJECT_ENDPOINT = %q\n  $env:AZURE_CONTAINER_REGISTRY_ENDPOINT = %q\n",
		projectEndpoint,
		registryEndpoint,
	)
	if goos != "windows" || strings.Contains(strings.ToLower(shell), "sh") {
		setEnvironment = fmt.Sprintf(
			"  export FOUNDRY_PROJECT_ENDPOINT=%q\n  export AZURE_CONTAINER_REGISTRY_ENDPOINT=%q\n",
			projectEndpoint,
			registryEndpoint,
		)
	}

	return fmt.Sprintf(
		"Created OpenEnv-style environment at: %s\n"+
			"\nRun locally:\n"+
			"  cd \"%s\"\n"+
			"  azd ai rle run\n"+
			"\nPublish to RLE when ready (optional):\n"+
			"%s"+
			"  azd ai rle publish\n",
		displayDir,
		displayDir,
		setEnvironment,
	)
}
