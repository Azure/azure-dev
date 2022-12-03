package cmd

// Run `go generate ./cmd` or `wire ./cmd` after modifying this file to regenerate `wire_gen.go`.

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/google/wire"
	"github.com/mattn/go-colorable"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

func newOutputWriter(console input.Console) io.Writer {
	writer := console.Handles().Stdout

	if os.Getenv("NO_COLOR") != "" {
		writer = colorable.NewNonColorable(writer)
	}

	return writer
}

func newFormatterFromConsole(console input.Console) output.Formatter {
	return console.GetFormatter()
}

func newConsoleFromOptions(
	rootOptions *internal.GlobalCommandOptions,
	formatter output.Formatter,
	cmd *cobra.Command,
) input.Console {
	writer := cmd.OutOrStdout()
	// When using JSON formatting, we want to ensure we always write messages from the console to stderr.
	if formatter != nil && formatter.Kind() == output.JsonFormat {
		writer = cmd.ErrOrStderr()
	}

	if os.Getenv("NO_COLOR") != "" {
		writer = colorable.NewNonColorable(writer)
	}

	isTerminal := cmd.OutOrStdout() == os.Stdout &&
		cmd.InOrStdin() == os.Stdin && isatty.IsTerminal(os.Stdin.Fd()) &&
		isatty.IsTerminal(os.Stdout.Fd())

	return input.NewConsole(rootOptions.NoPrompt, isTerminal, writer, input.ConsoleHandles{
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

func newAzCliFromOptions(
	rootOptions *internal.GlobalCommandOptions,
	credential azcore.TokenCredential,
) azcli.AzCli {
	return azcli.NewAzCli(credential, azcli.NewAzCliArgs{
		EnableDebug:     rootOptions.EnableDebugLogging,
		EnableTelemetry: rootOptions.EnableTelemetry,
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

func newCredential(ctx context.Context, authManager *auth.Manager) (azcore.TokenCredential, error) {
	credential, err := authManager.CredentialForCurrentUser(ctx)
	if err != nil {
		return nil, err
	}

	if _, err := auth.EnsureLoggedInCredential(ctx, credential); err != nil {
		return nil, err
	}

	return credential, nil
}

// Attempts to retrieve any environment flags available
// If found, return the envFlags otherwise an empty flag set.
func newEnvFlagsFromCmd(flags any, commandOptions *internal.GlobalCommandOptions) *envFlag {
	flagsWithEnv, ok := flags.(flagsWithEnv)
	if !ok {
		return &envFlag{}
	}

	envFlags := flagsWithEnv.Env()
	if envFlags.environmentName != "" {
		log.Printf("Found environment name with value '%s'", envFlags.environmentName)
	}

	return envFlags
}

// Creates a new azd environment from the azd context and command flags/options
func newEnvironmentFromAzdContext(
	azdContext *azdcontext.AzdContext,
	envFlags *envFlag,
	commandOptions *internal.GlobalCommandOptions,
) (*environment.Environment, error) {
	environmentName := envFlags.environmentName
	var err error

	if environmentName == "" {
		defaultEnvName, err := azdContext.GetDefaultEnvironmentName()
		if err != nil {
			return nil, err
		}

		environmentName = defaultEnvName
	}

	env, err := environment.GetEnvironment(azdContext, environmentName)
	if err != nil {
		return nil, err
	}

	return env, nil
}

// Creates a new project config for the specified environment
func newProjectConfigFromEnv(
	azdContext *azdcontext.AzdContext,
	env *environment.Environment,
) (*project.ProjectConfig, error) {
	projectConfig, err := project.LoadProjectConfig(azdContext.ProjectPath(), env)
	if err != nil {
		return nil, err
	}

	return projectConfig, nil
}

// Creates extensibility command hooks from azd context, environment & project configuration
func newCommandHooksFromEnv(
	console input.Console,
	commandRunner exec.CommandRunner,
	azdContext *azdcontext.AzdContext,
	env *environment.Environment,
	projectConfig *project.ProjectConfig) *ext.CommandHooks {
	return ext.NewCommandHooks(
		commandRunner,
		console,
		projectConfig.Scripts,
		azdContext.ProjectDirectory(),
		env.Environ(),
	)
}

var FormattedConsoleSet = wire.NewSet(
	output.GetCommandFormatter,
	newConsoleFromOptions,
)

var CommonSet = wire.NewSet(
	config.NewManager,
	config.NewUserConfigManager,
	account.NewManager,
	newAzdContext,
	newCommandRunnerFromConsole,
	newFormatterFromConsole,
	newOutputWriter,
)

var AzCliSet = wire.NewSet(
	auth.NewManager,
	newCredential,
	newAzCliFromOptions,
)

var InitCmdSet = wire.NewSet(
	CommonSet,
	AzCliSet,
	git.NewGitCli,
	newInitAction,
	wire.Bind(new(actions.Action), new(*initAction)))

var InfraCreateCmdSet = wire.NewSet(
	CommonSet,
	AzCliSet,
	newInfraCreateAction,
	wire.Bind(new(actions.Action), new(*infraCreateAction)))

var InfraDeleteCmdSet = wire.NewSet(
	CommonSet,
	AzCliSet,
	newInfraDeleteAction,
	wire.Bind(new(actions.Action), new(*infraDeleteAction)))

var DeployCmdSet = wire.NewSet(
	CommonSet,
	AzCliSet,
	newDeployAction,
	wire.Bind(new(actions.Action), new(*deployAction)))

var UpCmdSet = wire.NewSet(
	CommonSet,
	AzCliSet,
	git.NewGitCli,
	newInitAction,
	newInfraCreateAction,
	newDeployAction,
	newUpAction,
	wire.FieldsOf(new(upFlags), "initFlags", "infraCreateFlags", "deployFlags"),
	wire.Bind(new(actions.Action), new(*upAction)))

var EnvSetCmdSet = wire.NewSet(
	CommonSet,
	AzCliSet,
	newEnvSetAction,
	wire.Bind(new(actions.Action), new(*envSetAction)))

var EnvSelectCmdSet = wire.NewSet(
	newAzdContext,
	newEnvSelectAction,
	wire.Bind(new(actions.Action), new(*envSelectAction)))

var EnvListCmdSet = wire.NewSet(
	CommonSet,
	newEnvListAction,
	wire.Bind(new(actions.Action), new(*envListAction)))

var EnvNewCmdSet = wire.NewSet(
	CommonSet,
	AzCliSet,
	newEnvNewAction,
	wire.Bind(new(actions.Action), new(*envNewAction)))

var EnvRefreshCmdSet = wire.NewSet(
	CommonSet,
	AzCliSet,
	newEnvRefreshAction,
	wire.Bind(new(actions.Action), new(*envRefreshAction)))

var EnvGetValuesCmdSet = wire.NewSet(
	CommonSet,
	AzCliSet,
	newEnvGetValuesAction,
	wire.Bind(new(actions.Action), new(*envGetValuesAction)))

var LoginCmdSet = wire.NewSet(
	CommonSet,
	auth.NewManager,
	newLoginAction,
	wire.Bind(new(actions.Action), new(*loginAction)))

var LogoutCmdSet = wire.NewSet(
	CommonSet,
	auth.NewManager,
	newLogoutAction,
	wire.Bind(new(actions.Action), new(*logoutAction)))

var MonitorCmdSet = wire.NewSet(
	CommonSet,
	AzCliSet,
	newMonitorAction,
	wire.Bind(new(actions.Action), new(*monitorAction)))

var PipelineConfigCmdSet = wire.NewSet(
	CommonSet,
	AzCliSet,
	newPipelineConfigAction,
	wire.Bind(new(actions.Action), new(*pipelineConfigAction)))

var RestoreCmdSet = wire.NewSet(
	CommonSet,
	AzCliSet,
	newRestoreAction,
	wire.Bind(new(actions.Action), new(*restoreAction)))

var ShowCmdSet = wire.NewSet(
	CommonSet,
	AzCliSet,
	newShowAction,
	wire.Bind(new(actions.Action), new(*showAction)))

var TemplatesListCmdSet = wire.NewSet(
	CommonSet,
	newTemplatesListAction,
	templates.NewTemplateManager,
	wire.Bind(new(actions.Action), new(*templatesListAction)))

var TemplatesShowCmdSet = wire.NewSet(
	CommonSet,
	newTemplatesShowAction,
	templates.NewTemplateManager,
	wire.Bind(new(actions.Action), new(templatesShowAction)))

var VersionCmdSet = wire.NewSet(
	CommonSet,
	newVersionAction,
	wire.Bind(new(actions.Action), new(*versionAction)))

var ConfigListCmdSet = wire.NewSet(
	CommonSet,
	newConfigListAction,
	wire.Bind(new(actions.Action), new(*configListAction)))

var ConfigGetCmdSet = wire.NewSet(
	CommonSet,
	newConfigGetAction,
	wire.Bind(new(actions.Action), new(*configGetAction)))

var ConfigSetCmdSet = wire.NewSet(
	CommonSet,
	newConfigSetAction,
	wire.Bind(new(actions.Action), new(*configSetAction)))

var ConfigUnsetCmdSet = wire.NewSet(
	CommonSet,
	newConfigUnsetAction,
	wire.Bind(new(actions.Action), new(*configUnsetAction)))

var ConfigResetCmdSet = wire.NewSet(
	CommonSet,
	newConfigResetAction,
	wire.Bind(new(actions.Action), new(*configResetAction)))

var DebugMiddlewareSet = wire.NewSet(
	CommonSet,
	middleware.NewDebugMiddleware,
	wire.Bind(new(middleware.Middleware), new(*middleware.DebugMiddleware)))

var TelemetryMiddlewareSet = wire.NewSet(
	CommonSet,
	middleware.NewTelemetryMiddleware,
	wire.Bind(new(middleware.Middleware), new(*middleware.TelemetryMiddleware)))

var CommandHooksMiddlewareSet = wire.NewSet(
	CommonSet,
	newEnvFlagsFromCmd,
	newEnvironmentFromAzdContext,
	newProjectConfigFromEnv,
	newCommandHooksFromEnv,
	middleware.NewCommandHooksMiddleware,
	wire.Bind(new(middleware.Middleware), new(*middleware.CommandHooksMiddleware)))

var AuthTokenCmdSet = wire.NewSet(
	CommonSet,
	auth.NewManager,
	newCredential,
	newAuthTokenAction,
	wire.Bind(new(actions.Action), new(*authTokenAction)))
