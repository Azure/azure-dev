// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
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
	repoInitializer   *repository.Initializer
	templateManager   *templates.TemplateManager
	featuresManager   *alpha.FeatureManager
	extensionsManager *extensions.Manager
	azd               workflow.AzdCommandRunner
	agentFactory      *agent.CopilotAgentFactory
	consentManager    consent.ConsentManager
	configManager     config.UserConfigManager
}

func newInitAction(
	lazyAzdCtx *lazy.Lazy[*azdcontext.AzdContext],
	lazyEnvManager *lazy.Lazy[environment.Manager],
	cmdRun exec.CommandRunner,
	console input.Console,
	gitCli *git.Cli,
	flags *initFlags,
	repoInitializer *repository.Initializer,
	templateManager *templates.TemplateManager,
	featuresManager *alpha.FeatureManager,
	extensionsManager *extensions.Manager,
	azd workflow.AzdCommandRunner,
	agentFactory *agent.CopilotAgentFactory,
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
		repoInitializer:   repoInitializer,
		templateManager:   templateManager,
		featuresManager:   featuresManager,
		extensionsManager: extensionsManager,
		azd:               azd,
		agentFactory:      agentFactory,
		consentManager:    consentManager,
		configManager:     configManager,
	}
}

func (i *initAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getting cwd: %w", err)
	}

	azdCtx := azdcontext.NewAzdContextWithDirectory(wd)
	i.lazyAzdCtx.SetValue(azdCtx)

	if i.flags.templateBranch != "" && i.flags.templatePath == "" {
		return nil, &internal.ErrorWithSuggestion{
			Err:        internal.ErrBranchRequiresTemplate,
			Suggestion: "Add '--template <repo-url>' when using '--branch'.",
		}
	}

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
	initTypeCount := 0
	if i.flags.templatePath != "" || len(i.flags.templateTags) > 0 {
		initTypeCount++
		initTypeSelect = initAppTemplate
	}
	if i.flags.fromCode {
		initTypeCount++
		initTypeSelect = initFromApp
	}
	if i.flags.minimal {
		initTypeCount++
		initTypeSelect = initFromApp // Minimal now also uses initFromApp path
	}

	if initTypeCount > 1 {
		return nil, &internal.ErrorWithSuggestion{
			Err:        internal.ErrMultipleInitModes,
			Suggestion: "Choose one: 'azd init --template <url>', 'azd init --from-code', or 'azd init --minimal'.",
		}
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

	if i.featuresManager.IsEnabled(agentcopilot.FeatureCopilot) {
		followUp += fmt.Sprintf("\n\n%s Run %s to deploy project to the cloud.",
			color.HiMagentaString("Next steps:"),
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
				i.azd.SetArgs([]string{"up", "--cwd", azdCtx.ProjectDirectory()})
				err := i.azd.ExecuteContext(ctx)
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
		env, err := i.initializeEnv(ctx, azdCtx, templates.Metadata{})
		if err != nil {
			return nil, err
		}

		header = fmt.Sprintf("Initialized environment %s.", env.Name())
		followUp = ""
	case initWithAgent:
		tracing.SetUsageAttributes(fields.InitMethod.String("agent"))
		if err := i.initAppWithAgent(ctx); err != nil {
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

func (i *initAction) initAppWithAgent(ctx context.Context) error {
	// Show alpha warning
	i.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title: fmt.Sprintf("Agentic mode init is in alpha mode. The agent will scan your repository and "+
			"attempt to make an azd-ready template to init. You can always change permissions later "+
			"by running %s. Mistakes may occur in agent mode. "+
			"To learn more, go to %s",
			output.WithHighLightFormat("azd copilot consent"),
			output.WithLinkFormat("https://aka.ms/azd-feature-stages")),
		TitleNote: "CTRL+C to cancel interaction \n? to pull up help text",
	})

	// Create agent
	copilotAgent, err := i.agentFactory.Create(ctx,
		agent.WithMode(agent.AgentModeInteractive),
		agent.WithDebug(i.flags.global.EnableDebugLogging),
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

	// Session picker — resume previous or start fresh
	selected, err := copilotAgent.SelectSession(ctx)
	if err != nil {
		return err
	}

	// Build send options
	opts := []agent.SendOption{}
	if selected != nil {
		opts = append(opts, agent.WithSessionID(selected.SessionID))
		i.console.Message(ctx, output.WithSuccessFormat("Session resumed"))
		i.console.Message(ctx, "")
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

	result, err := copilotAgent.SendMessageWithRetry(ctx, prompt, opts...)
	if err != nil {
		return err
	}

	// Show usage
	if usage := result.Usage.Format(); usage != "" {
		i.console.Message(ctx, "")
		i.console.Message(ctx, usage)
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
		fmt.Sprintf("Use agent mode %s", color.YellowString("(Alpha)")),
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

			console.Message(ctx, "\nThe azd agent feature has been enabled to support this new experience."+
				" To turn off in the future run `azd config unset alpha.llm`.")

			err = azdConfig.Set(agentcopilot.ConfigKeyModelType, "copilot")
			if err != nil {
				return initUnknown, fmt.Errorf("failed to set %s config: %w", agentcopilot.ConfigKeyModelType, err)
			}

			err = configManager.Save(azdConfig)
			if err != nil {
				return initUnknown, fmt.Errorf("failed to save config: %w", err)
			}

			console.Message(ctx, fmt.Sprintf("\nGitHub Copilot has been enabled to support this new experience."+
				" To turn off in the future run `azd config unset %s`.", agentcopilot.ConfigKeyModelType))
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
	return generateCmdHelpDescription("Initialize a new application in your current directory.",
		[]string{
			formatHelpNote(
				fmt.Sprintf("Running %s without flags specified will prompt "+
					"you to initialize using your existing code, or from a template.",
					output.WithHighLightFormat("init"),
				)),
			formatHelpNote(
				"To view all available sample templates, including those submitted by the azd community, visit: " +
					output.WithLinkFormat("https://azure.github.io/awesome-azd") + "."),
		})
}

func getCmdInitHelpFooter(*cobra.Command) string {
	return generateCmdHelpSamplesBlock(map[string]string{
		"Initialize a template to your current local directory from a GitHub repo.": fmt.Sprintf("%s %s",
			output.WithHighLightFormat("azd init --template"),
			output.WithWarningFormat("[GitHub repo URL]"),
		),
		"Initialize a template to your current local directory from a branch other than main.": fmt.Sprintf("%s %s %s %s",
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
