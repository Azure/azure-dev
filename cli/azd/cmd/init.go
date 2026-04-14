// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/agent"
	"github.com/azure/azure-dev/cli/azd/internal/agent/consent"
	agentcopilot "github.com/azure/azure-dev/cli/azd/internal/agent/copilot"
	"github.com/azure/azure-dev/cli/azd/internal/repository"
	"github.com/azure/azure-dev/cli/azd/internal/runcontext/agentdetect"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	uxlib "github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/workflow"
	"github.com/fatih/color"
	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func newInitFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *initFlags {
	flags := &initFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize a new application.",
		Long: `Initialize a new application.

When used with --template, a new directory is created (named after the template)
and the project is initialized inside it — similar to git clone.
Pass "." as the directory to initialize in the current directory instead.`,
		Args: cobra.MaximumNArgs(1),
	}
}

type initFlags struct {
	templatePath   string
	templateBranch string
	templateTags   []string
	subscription   string
	location       string
	global         *internal.GlobalCommandOptions
	fromCode       bool
	minimal        bool
	up             bool
	internal.EnvFlag
}

func (i *initFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.StringVarP(
		&i.templatePath,
		"template",
		"t",
		"",
		//nolint:lll
		"Initializes a new application from a template. You can use a Full URI, <owner>/<repository>, <repository> if it's part of the azure-samples organization, or a local directory path (./dir, ../dir, or absolute path).",
	)
	local.StringVarP(
		&i.templateBranch,
		"branch",
		"b",
		"",
		"The template branch to initialize from. Must be used with a template argument (--template or -t).")
	local.StringSliceVarP(
		&i.templateTags,
		"filter",
		"f",
		[]string{},
		"The tag(s) used to filter template results. Supports comma-separated values.",
	)
	local.StringVarP(
		&i.subscription,
		"subscription",
		"s",
		"",
		"ID of an Azure subscription to use for the new environment",
	)
	local.BoolVarP(
		&i.fromCode,
		"from-code",
		"",
		false,
		"Initializes a new application from your existing code.",
	)
	local.BoolVarP(
		&i.minimal,
		"minimal",
		"m",
		false,
		"Initializes a minimal project.",
	)
	local.BoolVarP(
		&i.up,
		"up",
		"",
		false,
		"Provision and deploy to Azure after initializing the project from a template.",
	)
	local.StringVarP(&i.location, "location", "l", "", "Azure location for the new environment")
	i.EnvFlag.Bind(local, global)

	i.global = global
}

type initAction struct {
	lazyAzdCtx        *lazy.Lazy[*azdcontext.AzdContext]
	lazyEnvManager    *lazy.Lazy[environment.Manager]
	console           input.Console
	cmdRun            exec.CommandRunner
	gitCli            *git.Cli
	flags             *initFlags
	args              []string
	repoInitializer   *repository.Initializer
	templateManager   *templates.TemplateManager
	featuresManager   *alpha.FeatureManager
	extensionsManager *extensions.Manager
	azd               workflow.AzdCommandRunner
	agentFactory      agent.AgentFactory
	consentManager    consent.ConsentManager
	configManager     config.UserConfigManager
	// isRunningInAgent reports whether azd was invoked by an AI agent.
	// Defaults to agentdetect.IsRunningInAgent; overridable in tests.
	isRunningInAgent func() bool
}

func newInitAction(
	lazyAzdCtx *lazy.Lazy[*azdcontext.AzdContext],
	lazyEnvManager *lazy.Lazy[environment.Manager],
	cmdRun exec.CommandRunner,
	console input.Console,
	gitCli *git.Cli,
	flags *initFlags,
	args []string,
	repoInitializer *repository.Initializer,
	templateManager *templates.TemplateManager,
	featuresManager *alpha.FeatureManager,
	extensionsManager *extensions.Manager,
	azd workflow.AzdCommandRunner,
	agentFactory agent.AgentFactory,
	consentManager consent.ConsentManager,
	configManager config.UserConfigManager,
) actions.Action {
	return &initAction{
		lazyAzdCtx:        lazyAzdCtx,
		lazyEnvManager:    lazyEnvManager,
		console:           console,
		cmdRun:            cmdRun,
		gitCli:            gitCli,
		flags:             flags,
		args:              args,
		repoInitializer:   repoInitializer,
		templateManager:   templateManager,
		featuresManager:   featuresManager,
		extensionsManager: extensionsManager,
		azd:               azd,
		agentFactory:      agentFactory,
		consentManager:    consentManager,
		configManager:     configManager,
		isRunningInAgent:  agentdetect.IsRunningInAgent,
	}
}

func (i *initAction) Run(ctx context.Context) (_ *actions.ActionResult, retErr error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getting cwd: %w", err)
	}

	if i.flags.templateBranch != "" && i.flags.templatePath == "" {
		return nil, &internal.ErrorWithSuggestion{
			Err:        internal.ErrBranchRequiresTemplate,
			Suggestion: "Add '--template <repo-url>' when using '--branch'.",
		}
	}

	// Validate init-mode combinations before any filesystem side effects.
	isTemplateInit := i.flags.templatePath != "" || len(i.flags.templateTags) > 0
	initModeCount := 0
	if isTemplateInit {
		initModeCount++
	}
	if i.flags.fromCode {
		initModeCount++
	}
	if i.flags.minimal {
		initModeCount++
	}
	if initModeCount > 1 {
		return nil, &internal.ErrorWithSuggestion{
			Err:        internal.ErrMultipleInitModes,
			Suggestion: "Choose one: 'azd init --template <url>', 'azd init --from-code', or 'azd init --minimal'.",
		}
	}

	// The positional [directory] argument is only valid with --template.
	if len(i.args) > 0 && !isTemplateInit {
		return nil, &internal.ErrorWithSuggestion{
			Err: fmt.Errorf(
				"unexpected argument %q: the [directory] option requires --template: %w",
				i.args[0],
				internal.ErrInvalidFlagCombination,
			),
			Suggestion: "Run 'azd init' to initialize interactively, or " +
				"'azd init --template <url> [directory]' to create a project from a template.",
		}
	}

	// Resolve local template paths to absolute before any chdir so that
	// relative paths like ../my-template resolve against the original CWD.
	if i.flags.templatePath != "" && templates.LooksLikeLocalPath(i.flags.templatePath) {
		absPath, err := filepath.Abs(i.flags.templatePath)
		if err == nil {
			i.flags.templatePath = absPath
		}
	}

	// When a template is specified, auto-create a project directory (like git clone).
	// The user can pass a positional [directory] argument to override the folder name,
	// or pass "." to use the current directory (preserving existing behavior).
	createdProjectDir := ""
	originalWd := wd

	if isTemplateInit {
		targetDir, err := i.resolveTargetDirectory(wd)
		if err != nil {
			return nil, err
		}

		if targetDir != wd {
			// Guard against self-targeting: if a local template path resolves to the
			// same directory we'd create, skip auto-create to avoid conflicts where
			// the template source and target directory overlap.
			if i.flags.templatePath != "" && templates.LooksLikeLocalPath(i.flags.templatePath) {
				absTemplate, absErr := filepath.Abs(i.flags.templatePath)
				if absErr == nil && filepath.Clean(absTemplate) == filepath.Clean(targetDir) {
					// Template source is the target directory — fall back to CWD.
					targetDir = wd
				}
			}
		}

		if targetDir != wd {
			// Check if target already exists and is non-empty
			if err := i.validateTargetDirectory(ctx, targetDir); err != nil {
				return nil, err
			}

			// Track whether the directory existed before we create it so the
			// cleanup defer only removes directories we actually created.
			dirExistedBefore := false
			if _, statErr := os.Stat(targetDir); statErr == nil {
				dirExistedBefore = true
			}

			if err := os.MkdirAll(targetDir, osutil.PermissionDirectory); err != nil {
				return nil, fmt.Errorf("creating project directory '%s': %w",
					filepath.Base(targetDir), err)
			}

			if err := os.Chdir(targetDir); err != nil {
				return nil, fmt.Errorf("changing to project directory '%s': %w",
					filepath.Base(targetDir), err)
			}

			wd = targetDir
			createdProjectDir = targetDir

			// Clean up the created directory and restore the original CWD
			// if any downstream step fails, matching git clone's behavior.
			// Only remove the directory if we created it — don't delete
			// pre-existing directories the user pointed at.
			defer func() {
				if retErr != nil {
					_ = os.Chdir(originalWd)
					if !dirExistedBefore {
						_ = os.RemoveAll(createdProjectDir)
					}
				}
			}()
		}
	}

	azdCtx := azdcontext.NewAzdContextWithDirectory(wd)
	i.lazyAzdCtx.SetValue(azdCtx)

	// ensure that git is available
	if err := tools.EnsureInstalled(ctx, []tools.ExternalTool{i.gitCli}...); err != nil {
		return nil, err
	}

	// Command title
	i.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title: "Initializing an app to run on Azure (azd init)",
	})

	// AZD supports having .env at the root of the project directory as the initial environment file.
	// godotenv.Load() -> add all the values from the .env file in the process environment
	// If AZURE_ENV_NAME is set in the .env file, it will be used to name the environment during env initialize.
	//
	// Note: When auto-creating a project directory, this runs after chdir into the target
	// directory. For new directories this is a no-op (no .env exists). For existing directories
	// passed via positional arg, we intentionally load the target directory's .env — the project
	// directory's configuration should take precedence over the invocation directory's.
	if err := godotenv.Overload(); err != nil {
		// ignore the error if the file does not exist
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("reading .env file: %w", err)
		}
	}
	if i.flags.EnvFlag.EnvironmentName == "" ||
		(i.flags.EnvFlag.EnvironmentName != "" && !i.flags.EnvFlag.FromArg()) {
		// only azd init supports using .env to influence the command. The `-e` flag is linked to the
		// env var AZURE_ENV_NAME, which means it could've be set either from ENV or from arg.
		// re-setting the value here after loading the .env file overrides any value coming from the system env but
		// doest not override the value coming from the arg.
		i.flags.EnvFlag.EnvironmentName = os.Getenv(environment.EnvNameEnvVarName)
	}

	// Fail fast when running non-interactively with --template but without --environment
	// to avoid downloading the template and then failing at the environment name prompt.
	// This check runs after .env loading so that AZURE_ENV_NAME from .env is considered.
	// When --no-prompt is active, the environment manager will auto-generate a name from the
	// working directory if no explicit name is provided, so we no longer require --environment.

	var existingProject bool
	if _, err := os.Stat(azdCtx.ProjectPath()); err == nil {
		existingProject = true
	} else if errors.Is(err, os.ErrNotExist) {
		existingProject = false
	} else {
		return nil, fmt.Errorf("checking if project exists: %w", err)
	}

	var initTypeSelect initType = initUnknown
	if isTemplateInit {
		initTypeSelect = initAppTemplate
	} else if i.flags.fromCode || i.flags.minimal {
		initTypeSelect = initFromApp
	}

	if initTypeSelect == initUnknown {
		if existingProject {
			// only initialize environment when no mode is set explicitly
			initTypeSelect = initEnvironment
		} else if i.console.IsNoPromptMode() {
			return nil, &initModeRequiredError{}
		} else {
			// Prompt for init type for new projects
			initTypeSelect, err = promptInitType(i.console, ctx, i.featuresManager, i.configManager)
			if err != nil {
				return nil, err
			}
		}
	}

	header := "New project initialized!"
	followUp := heredoc.Docf(`
	You can view the template code in your directory: %s
	Learn more about running 3rd party code on our DevHub: %s`,
		output.WithLinkFormat("%s", wd),
		output.WithLinkFormat("%s", "https://aka.ms/azd-third-party-code-notice"))

	if createdProjectDir != "" {
		// Compute a user-friendly cd path relative to where they started
		cdPath, relErr := filepath.Rel(originalWd, createdProjectDir)
		if relErr != nil {
			cdPath = createdProjectDir // Fall back to absolute path
		}
		// Quote the path when it contains whitespace so the hint is copy/paste-safe
		cdPathDisplay := cdPath
		if strings.ContainsAny(cdPath, " \t") {
			cdPathDisplay = fmt.Sprintf("%q", cdPath)
		}
		followUp += fmt.Sprintf("\n\nChange to the project directory:\n  %s",
			output.WithHighLightFormat("cd %s", cdPathDisplay))
	}

	if i.featuresManager.IsEnabled(agentcopilot.FeatureCopilot) {
		followUp += fmt.Sprintf("\n\n%s Run %s to deploy project to the cloud.",
			output.WithHintFormat("(→) NEXT STEPS:"),
			output.WithHighLightFormat("azd up"))
	}

	switch initTypeSelect {
	case initAppTemplate:
		tracing.SetUsageAttributes(fields.InitMethod.String("template"))
		template, err := i.initializeTemplate(ctx, azdCtx)
		if err != nil {
			return nil, err
		}

		if _, err := i.initializeEnv(ctx, azdCtx, template.Metadata); err != nil {
			return nil, err
		}

		if i.flags.up {
			// Prompt to deploy to Azure
			deploy, err := i.console.Confirm(ctx, input.ConsoleOptions{
				Message:      "Do you want to run " + output.WithHighLightFormat("azd up") + " now?",
				DefaultValue: true,
				Help: "Template files have been initialized in your local directory. " +
					"If you want to provision and deploy now without making changes, select Y. If not, select N.",
			})
			if err != nil {
				return nil, err
			}

			if deploy {
				// Call azd up
				startTime := time.Now()
				err := i.azd.ExecuteContext(ctx, []string{"up", "--cwd", azdCtx.ProjectDirectory()})
				header = "New project initialized! Provision and deploy to Azure was completed in " +
					ux.DurationAsText(since(startTime)) + "."
				if err != nil {
					return nil, err
				}
			}
		}

	case initFromApp:
		tracing.SetUsageAttributes(fields.InitMethod.String("app"))
		header = "Your app is ready for the cloud!"
		followUp = "Run " + output.WithHighLightFormat("azd up") + " to provision and deploy your app to Azure.\n" +
			"Run " + output.WithHighLightFormat("azd add") + " to add new Azure components to your project.\n" +
			"Run " + output.WithHighLightFormat("azd infra gen") + " to generate IaC for your project to disk, " +
			"allowing you to manually manage it.\n" +
			"See " + output.WithHighLightFormat("./next-steps.md") + " for more information on configuring your app."

		envSpecified := i.flags.EnvironmentName != ""
		initializeEnv := func() (*environment.Environment, error) {
			return i.initializeEnv(ctx, azdCtx, templates.Metadata{})
		}
		initializeMinimal := func() error {
			tracing.SetUsageAttributes(fields.InitMethod.String("project"))
			err := i.repoInitializer.InitializeMinimal(ctx, azdCtx)
			if err != nil {
				return err
			}

			// Create env upfront only if the environment name is passed in.
			if envSpecified {
				_, err := initializeEnv()
				if err != nil {
					return err
				}
			}

			header = "Generated azure.yaml project file."
			followUp = "Run " + output.WithHighLightFormat("azd add") + " to add new Azure components to your project."
			return nil
		}

		if i.flags.minimal {
			err = initializeMinimal()
		} else {
			err = i.repoInitializer.InitFromApp(
				ctx,
				azdCtx,
				initializeEnv,
				initializeMinimal,
				envSpecified,
			)
		}
		if err != nil {
			return nil, err
		}
	case initEnvironment:
		tracing.SetUsageAttributes(fields.InitMethod.String("environment"))
		env, err := i.initializeEnv(ctx, azdCtx, templates.Metadata{})
		if err != nil {
			return nil, err
		}

		header = fmt.Sprintf("Initialized environment %s.", env.Name())
		followUp = ""
	case initWithAgent:
		tracing.SetUsageAttributes(fields.InitMethod.String("copilot"))
		if err := i.initAppWithAgent(ctx, azdCtx); err != nil {
			return nil, err
		}
	default:
		panic("unhandled init type")
	}

	if err := i.initializeExtensions(ctx, azdCtx); err != nil {
		return nil, fmt.Errorf("initializing project extensions: %w", err)
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header:   header,
			FollowUp: followUp,
		},
	}, nil
}

func (i *initAction) initAppWithAgent(ctx context.Context, azdCtx *azdcontext.AzdContext) error {
	// Warn the user if the working directory has uncommitted git changes
	dirty, err := i.gitCli.IsDirty(ctx, azdCtx.ProjectDirectory())
	if err != nil && !errors.Is(err, git.ErrNotRepository) {
		return fmt.Errorf("checking git status: %w", err)
	}

	if dirty {
		defaultNo := false
		confirm := uxlib.NewConfirm(&uxlib.ConfirmOptions{
			Message: "Your working directory has uncommitted changes. Continue initializing?",
			HelpMessage: fmt.Sprintf(
				"%s may create or modify files in your working directory. "+
					"Consider committing or stashing your changes first to avoid losing work.",
				agentcopilot.DisplayTitle),
			DefaultValue: &defaultNo,
		})
		result, promptErr := confirm.Ask(ctx)
		i.console.Message(ctx, "")
		if promptErr != nil {
			return promptErr
		}
		if result == nil || !*result {
			return errors.New("user declined to continue with uncommitted changes")
		}
	}

	// Show preview notice
	i.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title: fmt.Sprintf(
			"%s will scan your repository and help generate an azd compatible project to get you started. "+
				"This experience is currently in preview.\n\n"+
				"You can always change permissions later by running %s.\n\n"+
				"To learn more, go to %s",
			agentcopilot.DisplayTitle,
			output.WithHighLightFormat("azd copilot consent"),
			output.WithLinkFormat("https://aka.ms/azd-feature-stages")),
	})

	// Prompt for upfront tool access before starting the agent
	if err := i.consentManager.PromptWorkflowConsent(ctx,
		[]string{"copilot", "azure", "azd"},
	); err != nil {
		return err
	}

	// Create agent
	copilotAgent, err := i.agentFactory.Create(ctx,
		agent.WithMode(agent.AgentModeInteractive),
		agent.WithDebug(i.flags.global.EnableDebugLogging),
		agent.OnSessionStarted(func(sessionID string) {
			if azdCtx != nil {
				_ = azdCtx.SetCopilotSession(&azdcontext.CopilotSession{
					SessionID: sessionID,
					Command:   "init",
					StartedAt: time.Now().UTC().Format(time.RFC3339),
				})
			}
		}),
	)
	if err != nil {
		return err
	}
	defer copilotAgent.Stop()

	// Initialize — prompts on first run, returns config on subsequent
	initResult, err := copilotAgent.Initialize(ctx)
	if err != nil {
		return err
	}

	// Show current config
	modelDisplay := initResult.Model
	if modelDisplay == "" {
		modelDisplay = "default"
	}
	i.console.Message(ctx, output.WithGrayFormat("  Agent: model=%s, reasoning=%s",
		modelDisplay, initResult.ReasoningEffort))
	if !initResult.IsFirstRun {
		i.console.Message(ctx, output.WithGrayFormat(
			"  To change, run %s or %s",
			output.WithHighLightFormat("azd config set %s <model>", agentcopilot.ConfigKeyModel),
			output.WithHighLightFormat("azd config set %s <level>", agentcopilot.ConfigKeyReasoningEffort)))
	}
	i.console.Message(ctx, "")

	// Check for an in-progress session to resume
	opts := []agent.SendOption{}
	if azdCtx != nil {
		if session := azdCtx.GetCopilotSession(); session != nil {
			timeDisplay := agent.FormatSessionTime(session.StartedAt)
			defaultYes := true
			confirm := uxlib.NewConfirm(&uxlib.ConfirmOptions{
				Message: fmt.Sprintf("Resume previous session from %s?", timeDisplay),
				HelpMessage: "Resuming continues where you left off. " +
					"Choosing no starts a fresh session.",
				DefaultValue: &defaultYes,
			})
			if result, err := confirm.Ask(ctx); err == nil && result != nil && *result {
				opts = append(opts, agent.WithSessionID(session.SessionID))
				i.console.Message(ctx, output.WithSuccessFormat("Session resumed"))
				i.console.Message(ctx, "")
			}
			i.console.Message(ctx, "")
		}
	}

	// Init prompt
	prompt := `Prepare this application for deployment to Azure.

First, check if the current directory contains application code. If the directory is empty
or has no application code, ask the user what type of Azure application they would like to
build (e.g., web app, API, function app, static site, containerized service) before proceeding.

Then invoke the following skills in order:

1. Invoke the azure-prepare skill using the "azd" recipe to analyze the project,
   generate infrastructure (Bicep or Terraform), Dockerfiles, and azure.yaml configuration.

2. Invoke the azure-validate skill using the "azd" recipe to run pre-deployment
   validation checks and verify everything is ready for deployment.

Always select the "azd" recipe when prompted for recipe selection.

Ask the user for input when you need clarification about architecture choices,
service selection, or configuration options.

When complete, provide a brief summary of what was accomplished.`

	i.console.Message(ctx, color.MagentaString("Preparing application for Azure deployment..."))

	_, err = copilotAgent.SendMessageWithRetry(ctx, prompt, opts...)
	if err != nil {
		return err
	}

	// Clear session on success
	if azdCtx != nil {
		_ = azdCtx.ClearCopilotSession()
	}

	// Show session metrics (usage + file changes)
	if metricsStr := copilotAgent.GetMetrics().String(); metricsStr != "" {
		i.console.Message(ctx, "")
		i.console.Message(ctx, metricsStr)
	}

	i.console.Message(ctx, "")
	return nil
}

type initType int

const (
	initUnknown = iota
	initFromApp
	initAppTemplate
	initEnvironment
	initWithAgent
)

func promptInitType(
	console input.Console,
	ctx context.Context,
	featuresManager *alpha.FeatureManager,
	configManager config.UserConfigManager,
) (initType, error) {
	options := []string{
		"Scan current directory", // This now covers minimal project creation too
		"Select a template",
		fmt.Sprintf("Set up with %s %s", agentcopilot.DisplayTitle, color.YellowString("(Preview)")),
	}

	selection, err := console.Select(ctx, input.ConsoleOptions{
		Message: "How do you want to initialize your app?",
		Options: options,
	})
	if err != nil {
		return initUnknown, err
	}

	switch selection {
	case 0:
		return initFromApp, nil
	case 1:
		return initAppTemplate, nil
	case 2:
		if !featuresManager.IsEnabled(agentcopilot.FeatureCopilot) {
			azdConfig, err := configManager.Load()
			if err != nil {
				return initUnknown, fmt.Errorf("failed to load config: %w", err)
			}

			err = azdConfig.Set("alpha.llm", "on")
			if err != nil {
				return initUnknown, fmt.Errorf("failed to set alpha.llm config: %w", err)
			}

			err = configManager.Save(azdConfig)
			if err != nil {
				return initUnknown, fmt.Errorf("failed to save config: %w", err)
			}

			console.Message(ctx, fmt.Sprintf("\n%s has been enabled to support this new experience."+
				" To turn off in the future run `azd config unset alpha.llm`.", agentcopilot.DisplayTitle))

			err = azdConfig.Set(agentcopilot.ConfigKeyModelType, "copilot")
			if err != nil {
				return initUnknown, fmt.Errorf("failed to set %s config: %w", agentcopilot.ConfigKeyModelType, err)
			}

			err = configManager.Save(azdConfig)
			if err != nil {
				return initUnknown, fmt.Errorf("failed to save config: %w", err)
			}

			console.Message(ctx, fmt.Sprintf(
				"\n%s has been enabled to support this new experience."+
					" To turn off in the future run `azd config unset %s`.",
				agentcopilot.DisplayTitle, agentcopilot.ConfigKeyModelType))
		}

		return initWithAgent, nil
	default:
		panic("unhandled selection")
	}
}

func (i *initAction) initializeTemplate(
	ctx context.Context,
	azdCtx *azdcontext.AzdContext) (templates.Template, error) {
	err := i.repoInitializer.PromptIfNonEmpty(ctx, azdCtx)
	if err != nil {
		return templates.Template{}, err
	}

	var initFromTemplate *templates.Template
	if i.flags.templatePath == "" {
		// prompt for the template explicitly
		template, err := templates.PromptTemplate(
			ctx,
			"Select a project template:",
			i.templateManager,
			i.console,
			&templates.ListOptions{
				Tags: i.flags.templateTags,
			},
		)
		if err != nil {
			return templates.Template{}, err
		}

		initFromTemplate = &template
	} else {
		initFromTemplate = &templates.Template{
			RepositoryPath: i.flags.templatePath,
		}
	}

	err = i.repoInitializer.Initialize(ctx, azdCtx, initFromTemplate, i.flags.templateBranch)
	if err != nil {
		return templates.Template{}, fmt.Errorf("init from template repository: %w", err)
	}

	return *initFromTemplate, nil
}

func (i *initAction) initializeEnv(
	ctx context.Context,
	azdCtx *azdcontext.AzdContext,
	templateMetadata templates.Metadata) (*environment.Environment, error) {
	envName, err := azdCtx.GetDefaultEnvironmentName()
	if err != nil {
		return nil, fmt.Errorf("retrieving default environment name: %w", err)
	}

	if envName != "" {
		return nil, environment.NewEnvironmentInitError(envName)
	}

	base := filepath.Base(azdCtx.ProjectDirectory())
	examples := []string{}
	for _, c := range []string{"dev", "test", "prod"} {
		suggest := environment.CleanName(base + "-" + c)
		if len(suggest) > environment.EnvironmentNameMaxLength {
			suggest = suggest[len(suggest)-environment.EnvironmentNameMaxLength:]
		}

		examples = append(examples, suggest)
	}

	// Environment manager requires azd context
	// Azd context isn't available in init so lazy instantiating
	// it here after the template is hydrated and the context is available
	envManager, err := i.lazyEnvManager.GetValue()
	if err != nil {
		return nil, err
	}

	envSpec := environment.Spec{
		Name:         i.flags.EnvironmentName,
		Subscription: i.flags.subscription,
		Location:     i.flags.location,
		Examples:     examples,
	}

	env, err := envManager.Create(ctx, envSpec)
	if err != nil {
		return nil, fmt.Errorf("loading environment: %w", err)
	}

	if err := azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: env.Name()}); err != nil {
		return nil, fmt.Errorf("saving default environment: %w", err)
	}

	// Copy template metadata into environment values
	for key, value := range templateMetadata.Variables {
		env.DotenvSet(key, value)
	}

	for key, value := range templateMetadata.Config {
		if err := env.Config.Set(key, value); err != nil {
			return nil, fmt.Errorf("setting environment config: %w", err)
		}
	}

	initialValuesFromEnv, err := repository.InitEnvFileValues()
	if err != nil {
		return nil, fmt.Errorf("loading initial env file values: %w", err)
	}
	for key, value := range initialValuesFromEnv {
		env.DotenvSet(key, value)
	}

	if err := envManager.Save(ctx, env); err != nil {
		return nil, fmt.Errorf("saving environment: %w", err)
	}

	return env, nil
}

// initializeExtensions installs extensions specified in the project config
func (i *initAction) initializeExtensions(ctx context.Context, azdCtx *azdcontext.AzdContext) error {
	projectConfig, err := project.Load(ctx, azdCtx.ProjectPath())
	if err != nil {
		return fmt.Errorf("loading project config: %w", err)
	}

	// No extensions required
	if projectConfig.RequiredVersions == nil || len(projectConfig.RequiredVersions.Extensions) == 0 {
		return nil
	}

	installedExtensions, err := i.extensionsManager.ListInstalled()
	if err != nil {
		return fmt.Errorf("listing installed extensions: %w", err)
	}

	i.console.Message(ctx, "\nInstalling required extensions...")

	for extensionId, versionConstraint := range projectConfig.RequiredVersions.Extensions {
		stepMessage := fmt.Sprintf("Installing %s extension", output.WithHighLightFormat(extensionId))
		i.console.ShowSpinner(ctx, stepMessage, input.Step)

		installed, isInstalled := installedExtensions[extensionId]
		if isInstalled {
			stepMessage += output.WithGrayFormat(" (version %s already installed)", installed.Version)
			i.console.StopSpinner(ctx, stepMessage, input.StepSkipped)
			continue
		} else {
			installConstraint := "latest"
			if versionConstraint != nil {
				installConstraint = *versionConstraint
			}

			// Find the extension first
			filterOptions := &extensions.FilterOptions{
				Id: extensionId,
			}

			extensionMatches, err := i.extensionsManager.FindExtensions(ctx, filterOptions)
			if err != nil {
				i.console.StopSpinner(ctx, stepMessage, input.StepFailed)
				return fmt.Errorf("finding extension %s: %w", extensionId, err)
			}

			if len(extensionMatches) == 0 {
				i.console.StopSpinner(ctx, stepMessage, input.StepFailed)
				return fmt.Errorf("extension %s not found", extensionId)
			}

			if len(extensionMatches) > 1 {
				i.console.StopSpinner(ctx, stepMessage, input.StepFailed)
				return fmt.Errorf("extension %s found in multiple sources, specify exact source", extensionId)
			}

			extensionMetadata := extensionMatches[0]

			extensionVersion, err := i.extensionsManager.Install(ctx, extensionMetadata, installConstraint)
			if err != nil {
				i.console.StopSpinner(ctx, stepMessage, input.StepFailed)
				return fmt.Errorf("installing extension %s: %w", extensionId, err)
			}

			stepMessage += output.WithGrayFormat(" (%s)", extensionVersion.Version)
			i.console.StopSpinner(ctx, stepMessage, input.StepDone)
		}
	}

	return nil
}

func getCmdInitHelpDescription(*cobra.Command) string {
	return generateCmdHelpDescription(
		"Initialize a new application. When using --template, creates a project directory automatically.",
		[]string{
			formatHelpNote(
				fmt.Sprintf("Running %s without flags specified will prompt "+
					"you to initialize using your existing code, or from a template.",
					output.WithHighLightFormat("init"),
				)),
			formatHelpNote(
				fmt.Sprintf("When using %s, a new directory is created "+
					"(named after the template) and the project is initialized inside it. "+
					"Pass %s as the directory to use the current directory instead.",
					output.WithHighLightFormat("--template"),
					output.WithHighLightFormat("."),
				)),
			formatHelpNote(
				"To view all available sample templates, including those submitted by the azd community, visit: " +
					output.WithLinkFormat("https://azure.github.io/awesome-azd") + "."),
		})
}

func getCmdInitHelpFooter(*cobra.Command) string {
	return generateCmdHelpSamplesBlock(map[string]string{
		"Initialize a template into a new project directory.": fmt.Sprintf("%s %s",
			output.WithHighLightFormat("azd init --template"),
			output.WithWarningFormat("[GitHub repo URL]"),
		),
		"Initialize a template into the current directory.": fmt.Sprintf("%s %s %s",
			output.WithHighLightFormat("azd init --template"),
			output.WithWarningFormat("[GitHub repo URL]"),
			output.WithHighLightFormat("."),
		),
		"Initialize a template from a branch other than main.": fmt.Sprintf("%s %s %s %s",
			output.WithHighLightFormat("azd init --template"),
			output.WithWarningFormat("[GitHub repo URL]"),
			output.WithHighLightFormat("--branch"),
			output.WithWarningFormat("[Branch name]"),
		),
	})
}

// initModeRequiredError is returned when azd init requires interactive prompts for initialization mode
// but --no-prompt is set.
type initModeRequiredError struct{}

func (e *initModeRequiredError) Error() string {
	return "initialization mode required when --no-prompt is set"
}

func (e *initModeRequiredError) ToString(currentIndentation string) string {
	var buf strings.Builder
	separator := "──────────────────────────────────────────────────────────────"

	buf.WriteString(separator + "\n")
	buf.WriteString("Init cannot continue (interactive prompts disabled)\n")
	buf.WriteString(separator + "\n\n")

	buf.WriteString("Choose one:\n\n")

	buf.WriteString("  • Minimal (no template)\n")
	buf.WriteString("      Creates required azd project files in the current directory.\n")
	buf.WriteString("      azd init --minimal\n\n")

	buf.WriteString("  • From template\n")
	buf.WriteString("      Creates a new project from an azd template.\n")
	buf.WriteString("      azd template list\n")
	buf.WriteString("      azd init --template <template-id> --environment <environment>\n\n")

	buf.WriteString("Environment name must be globally unique (for example: myapp-dev).\n")

	return buf.String()
}

func (e *initModeRequiredError) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details struct {
			Type    string                         `json:"type"`
			Options []initModeRequiredErrorOptions `json:"options"`
		} `json:"details"`
	}{
		Code:    "initModeRequired",
		Message: "Init cannot continue (interactive prompts disabled)",
		Details: struct {
			Type    string                         `json:"type"`
			Options []initModeRequiredErrorOptions `json:"options"`
		}{
			Type: "initModeRequired",
			Options: []initModeRequiredErrorOptions{
				{
					Name:        "minimal",
					Description: "Creates required azd project files in the current directory.",
					Command:     "azd init --minimal",
				},
				{
					Name:        "template",
					Description: "Creates a new project from an azd template.",
					Command:     "azd init --template <template-id> --environment <environment>",
				},
			},
		},
	})
}

type initModeRequiredErrorOptions struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Command     string `json:"command"`
}

// resolveTargetDirectory determines the target directory for template initialization.
// It returns the current working directory when "." is passed or no template is specified,
// otherwise it derives or uses the explicit directory name.
func (i *initAction) resolveTargetDirectory(wd string) (string, error) {
	if len(i.args) > 0 {
		dirArg := i.args[0]
		if dirArg == "." {
			return wd, nil
		}

		// Reject absolute paths to prevent creating directories outside the working tree.
		// With cleanup-on-failure, an absolute path could lead to os.RemoveAll on an
		// unrelated directory.
		if filepath.IsAbs(dirArg) {
			return "", &internal.ErrorWithSuggestion{
				Err:        fmt.Errorf("absolute path %q is not allowed as a directory argument", dirArg),
				Suggestion: "Use a relative directory name (e.g., 'my-project') or '.' for the current directory.",
			}
		}

		// Reject paths that escape the working directory via ".." traversal.
		// filepath.Join + filepath.Rel gives us a cleaned relative path; if it
		// starts with ".." the target is outside the working tree.
		resolved := filepath.Join(wd, dirArg)
		rel, err := filepath.Rel(wd, resolved)
		if err != nil || strings.HasPrefix(rel, "..") {
			return "", &internal.ErrorWithSuggestion{
				Err: fmt.Errorf(
					"directory %q escapes the current working directory", dirArg),
				Suggestion: "Use a directory name without '..' traversal (e.g., 'my-project').",
			}
		}

		return resolved, nil
	}

	// In non-interactive mode, non-TTY environments (CI, piped stdin), or when called by
	// an AI agent, default to CWD to preserve backward compatibility. Existing scripts,
	// CI pipelines, and LLM agents expect `azd init -t <template>` to place files in CWD.
	// The auto-create-directory behavior only activates for interactive terminal users.
	// Users can still pass an explicit positional arg to opt into the new behavior anywhere.
	if i.console.IsNoPromptMode() || !i.console.IsSpinnerInteractive() || i.isRunningInAgent() {
		return wd, nil
	}

	// No positional arg: auto-derive from template path
	if i.flags.templatePath != "" {
		dirName := templates.DeriveDirectoryName(i.flags.templatePath)
		return filepath.Join(wd, dirName), nil
	}

	// Template selected via --filter tags (interactive selection) — use CWD for now.
	// TODO(#7290): Derive directory name from the selected template after interactive
	// selection completes, so --filter users also get git-clone-style behavior.
	return wd, nil
}

// validateTargetDirectory checks that the target directory is safe to use.
// If it already exists and is non-empty, it prompts the user for confirmation
// or returns an error in non-interactive mode.
func (i *initAction) validateTargetDirectory(ctx context.Context, targetDir string) error {
	f, err := os.Open(targetDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil // Directory doesn't exist yet — will be created
	}
	if err != nil {
		return fmt.Errorf("reading directory '%s': %w", filepath.Base(targetDir), err)
	}

	// Read a single entry to check emptiness without loading the full listing.
	names, readErr := f.Readdirnames(1)
	f.Close()

	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return fmt.Errorf("checking directory contents of '%s': %w",
			filepath.Base(targetDir), readErr)
	}

	if len(names) == 0 {
		return nil // Empty directory is fine
	}

	dirName := filepath.Base(targetDir)

	if i.console.IsNoPromptMode() {
		return fmt.Errorf(
			"directory '%s' already exists and is not empty; "+
				"use '.' to initialize in the current directory instead", dirName)
	}

	// Warn the user but don't prompt for confirmation here — the downstream
	// template initialization will prompt when overwriting individual files,
	// avoiding redundant confirmations.
	i.console.MessageUxItem(ctx, &ux.WarningMessage{
		Description: fmt.Sprintf("Directory '%s' already exists and is not empty.", dirName),
	})

	return nil
}
