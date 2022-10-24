package commands

import (
	"context"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/identity"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"

	"github.com/mattn/go-colorable"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

// Create the core context for use in all Azd commands
// Registers context values for azCli, formatter, writer, console and more.
func RegisterDependenciesInCtx(
	ctx context.Context,
	cmd *cobra.Command,
	rootOptions *internal.GlobalCommandOptions,
) (context.Context, error) {
	// Set the global options in the go context
	ctx = internal.WithCommandOptions(ctx, *rootOptions)
	ctx = tools.WithInstalledCheckCache(ctx)

	runner := exec.NewCommandRunner(cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
	ctx = exec.WithCommandRunner(ctx, runner)

	// Set default credentials used for operations against azure data/control planes
	credentials, err := azidentity.NewAzureCLICredential(nil)
	if err != nil {
		panic("failed creating azure cli credential")
	}
	ctx = identity.WithCredentials(ctx, credentials)

	azCliArgs := azcli.NewAzCliArgs{
		EnableDebug:     rootOptions.EnableDebugLogging,
		EnableTelemetry: rootOptions.EnableTelemetry,
		CommandRunner:   runner,
	}

	// Create and set the AzCli that will be used for the command
	azCli := azcli.NewAzCli(credentials, azCliArgs)
	ctx = azcli.WithAzCli(ctx, azCli)

	// Attempt to get the user specified formatter from the command args
	formatter, err := output.GetCommandFormatter(cmd)
	if err != nil {
		return ctx, err
	}

	if formatter != nil {
		ctx = output.WithFormatter(ctx, formatter)
	}

	writer := cmd.OutOrStdout()

	if os.Getenv("NO_COLOR") != "" {
		writer = colorable.NewNonColorable(writer)
	}

	// To support color on windows platforms which don't natively support rendering ANSI codes
	// we use colorable.NewColorableStdout() which creates a stream that uses the Win32 APIs to
	// change colors as it interprets the ANSI escape codes in the string it is writing.
	if writer == os.Stdout {
		writer = colorable.NewColorableStdout()
	}

	ctx = output.WithWriter(ctx, writer)

	isTerminal := cmd.OutOrStdout() == os.Stdout &&
		cmd.InOrStdin() == os.Stdin && isatty.IsTerminal(os.Stdin.Fd()) &&
		isatty.IsTerminal(os.Stdout.Fd())

	// When using JSON formatting, we want to ensure we always write messages from the console to stderr.
	if formatter != nil && formatter.Kind() == output.JsonFormat {
		writer = cmd.ErrOrStderr()
	}

	console := input.NewConsole(rootOptions.NoPrompt, isTerminal, writer, input.ConsoleHandles{
		Stdin:  cmd.InOrStdin(),
		Stdout: cmd.OutOrStdout(),
		Stderr: cmd.ErrOrStderr(),
	}, formatter)
	ctx = input.WithConsole(ctx, console)

	return ctx, nil
}
