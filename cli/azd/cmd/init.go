// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
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
	"github.com/azure/azure-dev/cli/azd/internal/agent/feedback"
	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/common"
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
	"github.com/azure/azure-dev/cli/azd/pkg/llm"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/azure/azure-dev/cli/azd/pkg/workflow"
	"github.com/fatih/color"
	"github.com/joho/godotenv"
	"github.com/mark3labs/mcp-go/mcp"
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
	agentFactory      *agent.AgentFactory
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
	agentFactory *agent.AgentFactory,
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
		return nil,
			errors.New(
				"using branch argument (-b or --branch) requires a template argument (--template or -t) to be specified")
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
	if i.flags.global.NoPrompt && i.flags.templatePath != "" && i.flags.EnvironmentName == "" {
		return nil, errors.New(
			"--environment is required when running in non-interactive mode (--no-prompt) with --template. " +
				"Use: azd init --template <url> --environment <name> --no-prompt")
	}

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
		return nil, errors.New("only one of init modes: --template, --from-code, or --minimal should be set")
	}

	if initTypeSelect == initUnknown {
		if existingProject {
			// only initialize environment when no mode is set explicitly
			initTypeSelect = initEnvironment
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

	if i.featuresManager.IsEnabled(llm.FeatureLlm) {
		followUp += fmt.Sprintf("\n%s Run azd up to deploy project to the cloud.`",
			color.HiMagentaString("Next steps:"))
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
	// Warn user that this is an alpha feature
	i.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title: fmt.Sprintf("Agentic mode init is in alpha mode. The agent will scan your repository and "+
			"attempt to make an azd-ready template to init. You can always change permissions later "+
			"by running `azd mcp consent`. Mistakes may occur in agent mode. "+
			"To learn more, go to %s\n", output.WithLinkFormat("https://aka.ms/azd-feature-stages")),
		TitleNote: "CTRL C to cancel interaction \n? to pull up help text",
	})

	// Check read only tool consent
	readOnlyRule, err := i.consentManager.CheckConsent(ctx,
		consent.ConsentRequest{
			ToolID:     "*/*",
			ServerName: "*",
			Operation:  consent.OperationTypeTool,
			Annotations: mcp.ToolAnnotation{
				ReadOnlyHint: common.ToPtr(true),
			},
		},
	)
	if err != nil {
		return err
	}

	if !readOnlyRule.Allowed {
		consentChecker := consent.NewConsentChecker(i.consentManager, "")
		err = consentChecker.PromptAndGrantReadOnlyToolConsent(ctx)
		if err != nil {
			return err
		}
		i.console.Message(ctx, "")
	}

	azdAgent, err := i.agentFactory.Create(
		ctx,
		agent.WithDebug(i.flags.global.EnableDebugLogging),
	)
	if err != nil {
		return err
	}

	defer azdAgent.Stop()

	type initStep struct {
		Name         string
		Description  string
		SummaryTitle string
	}

	taskInput := `Your task: %s

Break this task down into smaller steps if needed.
If new information reveals more work to be done, pursue it.
Do not stop until all tasks are complete and fully resolved.
`

	initSteps := []initStep{
		{
			Name:         "Step 1: Running Discovery & Analysis",
			Description:  "Run a deep discovery and analysis on the current working directory.",
			SummaryTitle: "Step 1 (discovery & analysis)",
		},
		{
			Name:         "Step 2: Generating Architecture Plan",
			Description:  "Create a high-level architecture plan for the application.",
			SummaryTitle: "Step 2 (architecture plan)",
		},
		{
			Name:         "Step 3: Generating Dockerfile(s)",
			Description:  "Generate a Dockerfile for the application components as needed.",
			SummaryTitle: "Step 3 (dockerfile generation)",
		},
		{
			Name:         "Step 4: Generating infrastructure",
			Description:  "Generate infrastructure as code (IaC) for the application.",
			SummaryTitle: "Step 4 (infrastructure generation)",
		},
		{
			Name:         "Step 5: Generating azure.yaml file",
			Description:  "Generate an azure.yaml file for the application.",
			SummaryTitle: "Step 5 (azure.yaml generation)",
		},
		{
			Name:         "Step 6: Validating project",
			Description:  "Validate the project structure and configuration.",
			SummaryTitle: "Step 6 (project validation)",
		},
	}

	var stepSummaries []string

	for idx, step := range initSteps {
		// Collect and apply feedback for next steps
		if idx > 0 {
			if err := i.collectAndApplyFeedback(
				ctx,
				azdAgent,
				"Any changes before moving to the next step?",
			); err != nil {
				return err
			}
		} else if idx == len(initSteps)-1 {
			if err := i.collectAndApplyFeedback(
				ctx,
				azdAgent,
				"Any changes before moving to the next completing interaction?",
			); err != nil {
				return err
			}
		}

		// Run Step
		i.console.Message(ctx, color.MagentaString(step.Name))
		fullTaskInput := fmt.Sprintf(taskInput, strings.Join([]string{
			step.Description,
			"Provide a brief summary in around 6 bullet points format about what was scanned" +
				" or analyzed and key actions performed:\n" +
				"Keep it concise and focus on high-level accomplishments, not implementation details.",
		}, "\n"))

		agentOutput, err := azdAgent.SendMessageWithRetry(ctx, fullTaskInput)
		if err != nil {
			if agentOutput != "" {
				i.console.Message(ctx, output.WithMarkdown(agentOutput))
			}

			return err
		}

		stepSummaries = append(stepSummaries, agentOutput)

		i.console.Message(ctx, "")
		i.console.Message(ctx, color.HiMagentaString(fmt.Sprintf("◆ %s Summary:", step.SummaryTitle)))
		i.console.Message(ctx, output.WithMarkdown(agentOutput))
		i.console.Message(ctx, "")
	}

	// Post-completion summary
	if err := i.postCompletionSummary(ctx, azdAgent, stepSummaries); err != nil {
		return err
	}

	return nil
}

// collectAndApplyFeedback prompts for user feedback and applies it using the agent in a loop
func (i *initAction) collectAndApplyFeedback(
	ctx context.Context,
	azdAgent agent.Agent,
	promptMessage string,
) error {
	AIDisclaimer := output.WithGrayFormat("The following content is AI-generated. AI responses may be incorrect.")
	collector := feedback.NewFeedbackCollector(i.console, feedback.FeedbackCollectorOptions{
		EnableLoop:      true,
		FeedbackPrompt:  promptMessage,
		FeedbackHint:    "Enter to skip",
		RequireFeedback: false,
		AIDisclaimer:    AIDisclaimer,
	})

	return collector.CollectFeedbackAndApply(ctx, azdAgent, AIDisclaimer)
}

// postCompletionSummary provides a final summary after all steps complete
func (i *initAction) postCompletionSummary(
	ctx context.Context,
	azdAgent agent.Agent,
	stepSummaries []string,
) error {
	i.console.Message(ctx, "")
	i.console.Message(ctx, "🎉 All initialization steps completed!")
	i.console.Message(ctx, "")

	// Combine all step summaries into a single prompt
	combinedSummaries := strings.Join(stepSummaries, "\n\n---\n\n")
	summaryPrompt := fmt.Sprintf(`Based on the following summaries of the azd init process, please provide
	a comprehensive overall summary of what was accomplished in bullet point format:\n%s`, combinedSummaries)

	agentOutput, err := azdAgent.SendMessageWithRetry(ctx, summaryPrompt)
	if err != nil {
		if agentOutput != "" {
			i.console.Message(ctx, output.WithMarkdown(agentOutput))
		}

		return err
	}

	i.console.Message(ctx, "")
	i.console.Message(ctx, color.HiMagentaString("◆ Agentic init Summary:"))
	i.console.Message(ctx, output.WithMarkdown(agentOutput))
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
		if !featuresManager.IsEnabled(llm.FeatureLlm) {
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

			err = azdConfig.Set("ai.agent.model.type", "github-copilot")
			if err != nil {
				return initUnknown, fmt.Errorf("failed to set ai.agent.model.type config: %w", err)
			}

			err = configManager.Save(azdConfig)
			if err != nil {
				return initUnknown, fmt.Errorf("failed to save config: %w", err)
			}

			console.Message(ctx, "\nGitHub Copilot has been enabled to support this new experience."+
				" To turn off in the future run `azd config unset ai.agent.model.type`.")
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
