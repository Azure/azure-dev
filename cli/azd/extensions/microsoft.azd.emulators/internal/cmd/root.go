// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.emulators/internal/emulator"
	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.emulators/internal/emulators"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
)

const extensionID = "microsoft.azd.emulators"

const (
	defaultTimeout     = 5 * time.Minute
	defaultDirectory   = "."
	defaultUpstreamURL = "http://host.docker.internal:7071/runtime/webhooks/signalr"
)

type launcher interface {
	CheckDocker(context.Context) (emulator.DockerHost, error)
	CheckRequirements(context.Context, emulator.Definition) error
	Run(context.Context, emulator.Definition, emulator.Request) (emulator.Result, error)
}

// NewRootCommand creates the emulators extension command tree for the supplied version.
func NewRootCommand(version string) *cobra.Command {
	container := ioc.NewNestedContainer(nil)
	container.MustRegisterSingleton(func() exec.CommandRunner {
		return exec.NewCommandRunner(&exec.RunnerOptions{
			Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard,
		})
	})
	container.MustRegisterSingleton(func(runner exec.CommandRunner) (*emulator.Manager, error) {
		directory, err := config.GetUserConfigDir()
		if err != nil {
			return nil, fmt.Errorf("resolve azd configuration directory: %w", err)
		}
		directory, err = filepath.Abs(filepath.Join(directory, "emulators"))
		if err != nil {
			return nil, err
		}
		return emulator.NewManager(runner, directory), nil
	})
	resolveManager := func() (*emulator.Manager, error) {
		var manager *emulator.Manager
		if err := container.Resolve(&manager); err != nil {
			return nil, err
		}
		return manager, nil
	}
	return newRootCommand(version, func() (launcher, error) { return resolveManager() },
		func(projectDir, environment string) (launcher, error) {
			manager, err := resolveManager()
			if err != nil {
				return nil, err
			}
			return manager.ForProject(projectDir, environment), nil
		})
}

func newRootCommand(
	version string, resolve func() (launcher, error), resolveProject func(string, string) (launcher, error),
) *cobra.Command {
	root, extCtx := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{
		Name: "emulators", Version: version,
		Short: "Launch local Azure emulators and display connection information.",
		Long: "Launch local Azure service emulators using Docker Compose. Commands return after the emulator is ready.\n" +
			"Run the same command again to reuse its saved settings, or add --stop to remove the instance and its data.",
	})
	root.SilenceUsage = true
	root.SilenceErrors = true
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetHelpCommand(&cobra.Command{Hidden: true})
	preRun := root.PersistentPreRunE
	root.PersistentPreRunE = func(command *cobra.Command, args []string) error {
		if preRun != nil {
			if err := preRun(command, args); err != nil {
				return err
			}
		}
		var writer io.Writer = io.Discard
		if extCtx.Debug && extCtx.OutputFormat != "none" {
			writer = command.ErrOrStderr()
		}
		azdext.SetupLogging(azdext.LoggerOptions{Debug: extCtx.Debug, Writer: writer})
		return nil
	}
	for _, definition := range emulators.All() {
		root.AddCommand(newEmulatorCommand(definition, extCtx, resolve))
	}
	root.AddCommand(newProviderListener(resolve, resolveProject))
	root.AddCommand(&cobra.Command{
		Use: "metadata", Hidden: true, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(
				azdext.GenerateExtensionMetadata("1.0", extensionID, root))
		},
	})
	return root
}

func newEmulatorCommand(
	definition emulator.Definition, extCtx *azdext.ExtensionContext, resolve func() (launcher, error),
) *cobra.Command {
	request := emulator.Request{Options: emulator.Options{Ports: map[string]int{}}}
	command := &cobra.Command{
		Use: definition.Name, Short: definition.Description, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			request.Changed = map[string]bool{}
			for _, port := range definition.Ports {
				value, err := cmd.Flags().GetInt(port.Flag)
				if err != nil {
					return err
				}
				request.Options.Ports[port.Flag] = value
				request.Changed[port.Flag] = cmd.Flags().Changed(port.Flag)
			}
			for _, flag := range []string{"stop", "timeout", "accept-license", "directory", "upstream-url", "environment"} {
				request.Changed[flag] = cmd.Flags().Changed(flag)
			}
			return runEmulator(cmd.Context(), cmd, extCtx.OutputFormat, resolve, definition, request)
		},
	}
	for _, port := range definition.Ports {
		command.Flags().Int(port.Flag, port.Default, port.Description)
	}
	command.Flags().BoolVar(&request.Stop, "stop", false, "Stop and remove this emulator, its dependencies and local data")
	command.Flags().DurationVar(&request.Timeout, "timeout", defaultTimeout, "Maximum time to launch or stop the emulator")
	if len(definition.LicenseURLs) > 0 {
		command.Flags().BoolVar(&request.AcceptLicense, "accept-license", false,
			"Accept the emulator and dependency license terms")
	}
	if definition.HasDirectory {
		command.Flags().StringVar(&request.Options.Directory, "directory", defaultDirectory,
			"Static-content directory to serve read-only")
	}
	if definition.HasUpstream {
		command.Flags().StringVar(&request.Options.UpstreamURL, "upstream-url",
			defaultUpstreamURL, "Upstream URL reachable from inside the container")
	}
	return azdext.RegisterFlagOptions(command, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{"default", "table", "json", "none"},
	})
}

func runEmulator(
	ctx context.Context,
	cmd *cobra.Command,
	format string,
	resolve func() (launcher, error),
	definition emulator.Definition,
	request emulator.Request,
) error {
	if err := emulator.ValidateRequest(definition, request); err != nil {
		return err
	}
	if definition.HasDirectory && !request.Stop {
		absolute, err := filepath.Abs(request.Options.Directory)
		if err != nil {
			return fmt.Errorf("resolve --directory: %w", err)
		}
		request.Options.Directory = absolute
	}
	service, err := resolve()
	if err != nil {
		return err
	}
	if format != "none" {
		action := "Starting"
		if request.Stop {
			action = "Stopping"
		}
		if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "%s %s...\n", action, definition.DisplayName); err != nil {
			return err
		}
	}
	result, err := service.Run(ctx, definition, request)
	if err != nil {
		return err
	}
	return writeResult(cmd.OutOrStdout(), format, result)
}

func writeResult(writer io.Writer, format string, result emulator.Result) error {
	switch format {
	case "none":
		return nil
	case "json":
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	default:
		if _, err := fmt.Fprintf(writer, "%s: %s\n", result.Emulator, result.Status); err != nil {
			return err
		}
		for _, connection := range result.Connections {
			if _, err := fmt.Fprintf(writer, "  %s: %s\n", connection.Name, connection.Value); err != nil {
				return err
			}
		}
		for _, note := range result.Notes {
			if _, err := fmt.Fprintf(writer, "\n%s\n", note); err != nil {
				return err
			}
		}
		if result.Status == "running" {
			_, err := fmt.Fprintf(writer, "\nStop and remove data: azd emulators %s --stop\n", result.Emulator)
			return err
		}
		return nil
	}
}
