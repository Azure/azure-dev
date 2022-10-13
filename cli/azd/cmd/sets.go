package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/azure/azure-dev/cli/azd/cmd/action"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/google/wire"
	"github.com/mattn/go-colorable"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

func newWriter(cmd *cobra.Command) io.Writer {
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

	return writer
}

func newConsoleFromOptions(
	rootOptions *internal.GlobalCommandOptions,
	formatter output.Formatter,
	writer io.Writer,
	cmd *cobra.Command,
) input.Console {
	isTerminal := cmd.OutOrStdout() == os.Stdout &&
		cmd.InOrStdin() == os.Stdin && isatty.IsTerminal(os.Stdin.Fd()) &&
		isatty.IsTerminal(os.Stdout.Fd())

	return input.NewConsole(!rootOptions.NoPrompt, isTerminal, writer, input.ConsoleHandles{
		Stdin:  cmd.InOrStdin(),
		Stdout: cmd.OutOrStdout(),
		Stderr: cmd.ErrOrStderr(),
	}, formatter)
}

func newCommandRunnerFromConsole(console input.Console) exec.CommandRunner {
	return exec.NewCommandRunner(
		console.Handles().Stdin,
		console.Handles().Stdout,
		console.Handles().Stderr,
	)
}

func newAzCliFromOptions(rootOptions *internal.GlobalCommandOptions, cmdRun exec.CommandRunner) azcli.AzCli {
	return azcli.NewAzCli(azcli.NewAzCliArgs{
		EnableDebug:     rootOptions.EnableDebugLogging,
		EnableTelemetry: rootOptions.EnableTelemetry,
		CommandRunner:   cmdRun,
		HttpClient:      nil,
	})
}

func newAzdContext() (*azdcontext.AzdContext, error) {
	azdCtx, err := azdcontext.NewAzdContext()
	if err != nil {
		return nil, fmt.Errorf("creating context: %w", err)
	}

	return azdCtx, nil
}

var FormattedConsoleSet = wire.NewSet(
	output.GetCommandFormatter,
	newWriter,
	newConsoleFromOptions,
)

var CommonSet = wire.NewSet(
	newAzdContext,
	FormattedConsoleSet,
	newCommandRunnerFromConsole,
)

var InitCmdSet = wire.NewSet(
	CommonSet,
	newAzCliFromOptions,
	git.NewGitCliFromRunner,
	newInitAction,
	wire.Bind(new(action.Action), new(*initAction)))

var InfraCreateCmdSet = wire.NewSet(
	CommonSet,
	newAzCliFromOptions,
	newInfraCreateAction,
	wire.Bind(new(action.Action), new(*infraCreateAction)))

var InfraDeleteCmdSet = wire.NewSet(
	CommonSet,
	newAzCliFromOptions,
	newInfraDeleteAction,
	wire.Bind(new(action.Action), new(*infraDeleteAction)))

var DeployCmdSet = wire.NewSet(
	CommonSet,
	newAzCliFromOptions,
	newDeployAction,
	wire.Bind(new(action.Action), new(*deployAction)))

var UpCmdSet = wire.NewSet(
	CommonSet,
	newAzCliFromOptions,
	git.NewGitCliFromRunner,
	newInitAction,
	newInfraCreateAction,
	newDeployAction,
	newUpAction,
	wire.FieldsOf(new(upFlags), "initFlags", "infraCreateFlags", "deployFlags"),
	wire.Bind(new(action.Action), new(*upAction)))

var EnvSetCmdSet = wire.NewSet(
	CommonSet,
	newAzCliFromOptions,
	newEnvSetAction,
	wire.Bind(new(action.Action), new(*envSetAction)))

var EnvSelectCmdSet = wire.NewSet(
	newAzdContext,
	newEnvSelectAction,
	wire.Bind(new(action.Action), new(*envSelectAction)))

var EnvListCmdSet = wire.NewSet(
	CommonSet,
	newEnvListAction,
	wire.Bind(new(action.Action), new(*envListAction)))

var EnvNewCmdSet = wire.NewSet(
	CommonSet,
	newAzCliFromOptions,
	newEnvNewAction,
	wire.Bind(new(action.Action), new(*envNewAction)))

var EnvRefreshCmdSet = wire.NewSet(
	CommonSet,
	newAzCliFromOptions,
	newEnvRefreshAction,
	wire.Bind(new(action.Action), new(*envRefreshAction)))

var EnvGetValuesCmdSet = wire.NewSet(
	CommonSet,
	newAzCliFromOptions,
	newEnvGetValuesAction,
	wire.Bind(new(action.Action), new(*envGetValuesAction)))

var LoginCmdSet = wire.NewSet(
	CommonSet,
	newAzCliFromOptions,
	newLoginAction,
	wire.Bind(new(action.Action), new(*loginAction)))

var MonitorCmdSet = wire.NewSet(
	CommonSet,
	newAzCliFromOptions,
	newMonitorAction,
	wire.Bind(new(action.Action), new(*monitorAction)))

var PipelineConfigCmdSet = wire.NewSet(
	CommonSet,
	newAzCliFromOptions,
	newPipelineConfigAction,
	wire.Bind(new(action.Action), new(*pipelineConfigAction)))

var RestoreCmdSet = wire.NewSet(
	CommonSet,
	newRestoreAction,
	wire.Bind(new(action.Action), new(*restoreAction)))

var ShowCmdSet = wire.NewSet(
	CommonSet,
	newShowAction,
	wire.Bind(new(action.Action), new(*showAction)))

var TemplatesListCmdSet = wire.NewSet(
	CommonSet,
	newTemplatesListAction,
	templates.NewTemplateManager,
	wire.Bind(new(action.Action), new(*templatesListAction)))

var TemplatesShowCmdSet = wire.NewSet(
	CommonSet,
	newTemplatesShowAction,
	templates.NewTemplateManager,
	wire.Bind(new(action.Action), new(templatesShowAction)))

var VersionCmdSet = wire.NewSet(
	CommonSet,
	newVersionAction,
	wire.Bind(new(action.Action), new(*versionAction)))
