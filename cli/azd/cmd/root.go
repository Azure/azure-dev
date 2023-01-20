// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"os"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/cmd/middleware"

	// Importing for infrastructure provider plugin registrations

	_ "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/bicep"
	_ "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/terraform"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
)

// Creates the root Cobra command for AZD.
// staticHelp - False, except for running for doc generation
// middlewareChain - nil, except for running unit tests
func NewRootCmd(staticHelp bool, middlewareChain []*actions.MiddlewareRegistration) *cobra.Command {
	prevDir := ""
	opts := &internal.GlobalCommandOptions{GenerateStaticHelp: staticHelp}
	opts.EnableTelemetry = telemetry.IsTelemetryEnabled()

	productName := "Azure Developer CLI"
	if opts.GenerateStaticHelp {
		productName = "Azure Developer CLI (`azd`)"
	}

	shortDescription := heredoc.Docf(`%s is a command-line interface for developers who build Azure solutions.`, productName)

	synopsisHeading := shortDescription + "\n\n"
	if opts.GenerateStaticHelp {
		synopsisHeading = ""
	}
	//nolint:lll
	longDescription := heredoc.Docf(`%sTo begin working with Azure Developer CLI, run the `+output.WithBackticks("azd up")+` command by supplying a sample template in an empty directory:

		$ azd up –-template todo-nodejs-mongo

	You can pick a template by running `+output.WithBackticks("azd template list")+` and then supplying the repo name as a value to `+output.WithBackticks("--template")+`.

	The most common next commands are:

		$ azd pipeline config
		$ azd deploy
		$ azd monitor --overview

	For more information, visit the Azure Developer CLI Dev Hub: https://aka.ms/azure-dev/devhub.`, synopsisHeading)

	rootCmd := &cobra.Command{
		Use:   "azd",
		Short: shortDescription,
		//nolint:lll
		Long: longDescription,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if opts.Cwd != "" {
				current, err := os.Getwd()

				if err != nil {
					return err
				}

				prevDir = current

				if err := os.Chdir(opts.Cwd); err != nil {
					return fmt.Errorf("failed to change directory to %s: %w", opts.Cwd, err)
				}
			}

			return nil
		},
		PersistentPostRunE: func(cmd *cobra.Command, args []string) error {
			// This is just for cleanliness and making writing tests simpler since
			// we can just remove the entire project folder afterwards.
			// In practical execution, this wouldn't affect much, since the CLI is exiting.
			if prevDir != "" {
				return os.Chdir(prevDir)
			}

			return nil
		},
		SilenceUsage:      true,
		DisableAutoGenTag: true,
	}

	rootCmd.CompletionOptions.HiddenDefaultCmd = true
	rootCmd.SetHelpTemplate(
		fmt.Sprintf("%s\nPlease let us know how we are doing: https://aka.ms/azure-dev/hats\n", rootCmd.HelpTemplate()),
	)

	root := actions.NewActionDescriptor("azd", &actions.ActionDescriptorOptions{
		Command: rootCmd,
		FlagsResolver: func(cmd *cobra.Command) *internal.GlobalCommandOptions {
			rootCmd.PersistentFlags().StringVarP(&opts.Cwd, "cwd", "C", "", "Sets the current working directory.")
			rootCmd.PersistentFlags().
				BoolVar(&opts.EnableDebugLogging, "debug", false, "Enables debugging and diagnostics logging.")
			rootCmd.PersistentFlags().
				BoolVar(
					&opts.NoPrompt,
					"no-prompt",
					false,
					"Accepts the default value instead of prompting, or it fails if there is no default.")

			// The telemetry system is responsible for reading these flags value and using it to configure the telemetry
			// system, but we still need to add it to our flag set so that when we parse the command line with Cobra we
			// don't error due to an "unknown flag".
			var traceLogFile string
			var traceLogEndpoint string

			rootCmd.PersistentFlags().StringVar(&traceLogFile, "trace-log-file", "", "Write a diagnostics trace to a file.")
			_ = rootCmd.PersistentFlags().MarkHidden("trace-log-file")

			rootCmd.PersistentFlags().StringVar(
				&traceLogEndpoint, "trace-log-url", "", "Send traces to an Open Telemetry compatible endpoint.")
			_ = rootCmd.PersistentFlags().MarkHidden("trace-log-url")

			return opts
		},
	})

	configActions(root, opts)
	envActions(root)
	infraActions(root)
	pipelineActions(root)
	telemetryActions(root)
	templatesActions(root)
	authActions(root)

	root.Add("version", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Short: "Print the version number of Azure Developer CLI.",
		},
		ActionResolver:   newVersionAction,
		FlagsResolver:    newVersionFlags,
		DisableTelemetry: true,
		OutputFormats:    []output.Format{output.JsonFormat, output.NoneFormat},
		DefaultFormat:    output.NoneFormat,
	})

	root.Add("show", &actions.ActionDescriptorOptions{
		Command:        newShowCmd(),
		FlagsResolver:  newShowFlags,
		ActionResolver: newShowAction,
		OutputFormats:  []output.Format{output.JsonFormat},
		DefaultFormat:  output.NoneFormat,
	})

	root.Add("restore", &actions.ActionDescriptorOptions{
		Command:        restoreCmdDesign(),
		FlagsResolver:  newRestoreFlags,
		ActionResolver: newRestoreAction,
	})

	root.Add("login", &actions.ActionDescriptorOptions{
		Command:        newLoginCmd(),
		FlagsResolver:  newLoginFlags,
		ActionResolver: newLoginAction,
		OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
	})

	root.Add("logout", &actions.ActionDescriptorOptions{
		Command:        newLogoutCmd(),
		ActionResolver: newLogoutAction,
	})

	root.Add("monitor", &actions.ActionDescriptorOptions{
		Command:        newMonitorCmd(),
		FlagsResolver:  newMonitorFlags,
		ActionResolver: newMonitorAction,
	})

	root.Add("down", &actions.ActionDescriptorOptions{
		Command:        newDownCmd(),
		FlagsResolver:  newDownFlags,
		ActionResolver: newDownAction,
		OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
	})

	root.Add("init", &actions.ActionDescriptorOptions{
		Command:        newInitCmd(),
		FlagsResolver:  newInitFlags,
		ActionResolver: newInitAction,
	}).AddFlagCompletion("template", templateNameCompletion)

	root.Add("up", &actions.ActionDescriptorOptions{
		Command:        newUpCmd(),
		FlagsResolver:  newUpFlags,
		ActionResolver: newUpAction,
		OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
	}).AddFlagCompletion("template", templateNameCompletion)

	root.Add("provision", &actions.ActionDescriptorOptions{
		Command:        newProvisionCmd(),
		FlagsResolver:  newProvisionFlags,
		ActionResolver: newInfraCreateAction,
		OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
	})

	root.Add("deploy", &actions.ActionDescriptorOptions{
		Command:        newDeployCmd(),
		FlagsResolver:  newDeployFlags,
		ActionResolver: newDeployAction,
		OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
	})

	// Register any global middleware defined by the caller
	if len(middlewareChain) > 0 {
		for _, registration := range middlewareChain {
			root.UseMiddlewareWhen(registration.Name, registration.Resolver, registration.Predicate)
		}
	}

	// Global middleware registration
	root.
		UseMiddleware("debug", middleware.NewDebugMiddleware).
		UseMiddlewareWhen("telemetry", middleware.NewTelemetryMiddleware, func(descriptor *actions.ActionDescriptor) bool {
			return !descriptor.Options.DisableTelemetry
		})

	registerCommonDependencies(ioc.Global)
	cobraBuilder := NewCobraBuilder(ioc.Global)

	// Compose the hierarchy of action descriptions into cobra commands
	cmd, err := cobraBuilder.BuildCommand(root)
	if err != nil {
		// If their is a container registration issue or similar we'll get an error at this point
		// Error descriptions should be clear enough to resolve the issue
		panic(err)
	}

	return cmd
}
