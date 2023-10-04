// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/repository"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/fatih/color"
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
	subscription   string
	location       string
	global         *internal.GlobalCommandOptions
	envFlag
}

func (i *initFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.StringVarP(
		&i.templatePath,
		"template",
		"t",
		"",
		//nolint:lll
		"The template to use when you initialize the project. You can use Full URI, <owner>/<repository>, or <repository> if it's part of the azure-samples organization.",
	)
	local.StringVarP(
		&i.templateBranch,
		"branch",
		"b",
		"",
		"The template branch to initialize from. Must be used with a template argument (--template or -t).")
	local.StringVarP(
		&i.subscription,
		"subscription",
		"s",
		"",
		"Name or ID of an Azure subscription to use for the new environment",
	)
	local.StringVarP(&i.location, "location", "l", "", "Azure location for the new environment")
	i.envFlag.Bind(local, global)

	i.global = global
}

type initAction struct {
	lazyAzdCtx      *lazy.Lazy[*azdcontext.AzdContext]
	lazyEnvManager  *lazy.Lazy[environment.Manager]
	console         input.Console
	cmdRun          exec.CommandRunner
	gitCli          git.GitCli
	flags           *initFlags
	repoInitializer *repository.Initializer
	templateManager *templates.TemplateManager
	featuresManager *alpha.FeatureManager
}

func newInitAction(
	lazyAzdCtx *lazy.Lazy[*azdcontext.AzdContext],
	lazyEnvManager *lazy.Lazy[environment.Manager],
	cmdRun exec.CommandRunner,
	console input.Console,
	gitCli git.GitCli,
	flags *initFlags,
	repoInitializer *repository.Initializer,
	templateManager *templates.TemplateManager,
	featuresManager *alpha.FeatureManager) actions.Action {
	return &initAction{
		lazyAzdCtx:      lazyAzdCtx,
		lazyEnvManager:  lazyEnvManager,
		console:         console,
		cmdRun:          cmdRun,
		gitCli:          gitCli,
		flags:           flags,
		repoInitializer: repoInitializer,
		templateManager: templateManager,
		featuresManager: featuresManager,
	}
}

func (i *initAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getting cwd: %w", err)
	}

	azdCtx, err := i.lazyAzdCtx.GetValue()
	if err != nil {
		azdCtx = azdcontext.NewAzdContextWithDirectory(wd)
		i.lazyAzdCtx.SetValue(azdCtx)
	}

	if i.flags.templateBranch != "" && i.flags.templatePath == "" {
		return nil,
			errors.New(
				"Using branch argument (-b or --branch) requires a template argument (--template or -t) to be specified.")
	}

	// ensure that git is available
	if err := tools.EnsureInstalled(ctx, []tools.ExternalTool{i.gitCli}...); err != nil {
		return nil, err
	}

	// Command title
	i.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title: "Initializing an app to run on Azure (azd init)",
	})

	var existingProject bool
	if _, err := os.Stat(azdCtx.ProjectPath()); err == nil {
		existingProject = true
	} else if errors.Is(err, os.ErrNotExist) {
		existingProject = false
	} else {
		return nil, fmt.Errorf("checking if project exists: %w", err)
	}

	var initTypeSelect initType
	if i.flags.templatePath != "" {
		// an explicit --template passed, always initialize from app template
		initTypeSelect = initAppTemplate
	}

	if i.flags.templatePath == "" && existingProject {
		// no explicit --template, and azure.yaml exists, only initialize environment
		initTypeSelect = initEnvironment
	}

	if initTypeSelect == initUnknown {
		if i.featuresManager.IsEnabled(alpha.EasyInit) {
			initTypeSelect, err = promptInitType(i.console, ctx)
			if err != nil {
				return nil, err
			}
		} else {
			initTypeSelect = initAppTemplate
		}
	}

	header := "New project initialized!"
	followUp := heredoc.Docf(`
	You can view the template code in your directory: %s
	Learn more about running 3rd party code on our DevHub: %s`,
		output.WithLinkFormat("%s", wd),
		output.WithLinkFormat("%s", "https://aka.ms/azd-third-party-code-notice"))

	switch initTypeSelect {
	case initAppTemplate:
		tracing.SetUsageAttributes(fields.InitMethod.String("template"))
		template, err := i.initializeTemplate(ctx, azdCtx)
		if err != nil {
			return nil, err
		}

		var templateMetadata *templates.Metadata
		if template != nil {
			templateMetadata = &template.Metadata
		}

		if err := i.initializeEnv(ctx, azdCtx, templateMetadata); err != nil {
			return nil, err
		}

		if err := i.initializeProject(ctx, azdCtx, templateMetadata); err != nil {
			return nil, err
		}
	case initFromApp:
		tracing.SetUsageAttributes(fields.InitMethod.String("app"))

		header = "Your app is ready for the cloud!"
		followUp = "You can provision and deploy your app to Azure by running the " + color.BlueString("azd up") +
			" command in this directory. For more information on configuring your app, see " +
			output.WithHighLightFormat("./next-steps.md")
		entries, err := os.ReadDir(azdCtx.ProjectDirectory())
		if err != nil {
			return nil, fmt.Errorf("reading current directory: %w", err)
		}

		if len(entries) == 0 {
			return nil, &azcli.ErrorWithSuggestion{
				Err: errors.New("no files found in the current directory"),
				Suggestion: "Ensure you're in the directory where your app code is located and try again." +
					" If you do not have code and would like to start with an app template, run '" +
					color.BlueString("azd init") + "' and select the option to " +
					color.MagentaString("Use a template") + ".",
			}
		}

		err = i.repoInitializer.InitFromApp(ctx, azdCtx, func() error {
			return i.initializeEnv(ctx, azdCtx, nil)
		})
		if err != nil {
			return nil, err
		}
	case initEnvironment:
		err = i.initializeEnv(ctx, azdCtx, nil)
		if err != nil {
			return nil, err
		}
	default:
		panic("unhandled init type")
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header:   header,
			FollowUp: followUp,
		},
	}, nil
}

type initType int

const (
	initUnknown = iota
	initFromApp
	initAppTemplate
	initEnvironment
)

func promptInitType(console input.Console, ctx context.Context) (initType, error) {
	selection, err := console.Select(ctx, input.ConsoleOptions{
		Message: "How do you want to initialize your app?",
		Options: []string{
			"Use code in the current directory",
			"Select a template",
		},
	})
	if err != nil {
		return initUnknown, err
	}

	switch selection {
	case 0:
		return initFromApp, nil
	case 1:
		return initAppTemplate, nil
	default:
		panic("unhandled selection")
	}
}

func (i *initAction) initializeTemplate(
	ctx context.Context,
	azdCtx *azdcontext.AzdContext) (*templates.Template, error) {
	err := i.repoInitializer.PromptIfNonEmpty(ctx, azdCtx)
	if err != nil {
		return nil, err
	}

	var template *templates.Template

	if i.flags.templatePath == "" {
		template, err = templates.PromptTemplate(ctx, "Select a project template:", i.templateManager, i.console)
		if err != nil {
			return nil, err
		}

		if template != nil {
			i.flags.templatePath = template.RepositoryPath
		}
	}

	if i.flags.templatePath != "" {
		if template == nil {
			template = &templates.Template{
				RepositoryPath: i.flags.templatePath,
			}
		}

		gitUri, err := templates.Absolute(i.flags.templatePath)
		if err != nil {
			return nil, err
		}

		err = i.repoInitializer.Initialize(ctx, azdCtx, gitUri, i.flags.templateBranch)
		if err != nil {
			return nil, fmt.Errorf("init from template repository: %w", err)
		}
	} else {
		err := i.repoInitializer.InitializeMinimal(ctx, azdCtx)
		if err != nil {
			return nil, fmt.Errorf("init empty repository: %w", err)
		}
	}

	return template, nil
}

// Initialize the project with any metadata values from the template
func (i *initAction) initializeProject(
	ctx context.Context,
	azdCtx *azdcontext.AzdContext,
	templateMetaData *templates.Metadata,
) error {
	if templateMetaData == nil {
		return nil
	}

	projectPath := azdCtx.ProjectPath()
	projectConfig, err := project.LoadConfig(ctx, projectPath)
	if err != nil {
		return fmt.Errorf("loading project config: %w", err)
	}

	for key, value := range templateMetaData.Project {
		if err := projectConfig.Set(key, value); err != nil {
			return fmt.Errorf("setting project config: %w", err)
		}
	}

	return project.SaveConfig(ctx, projectConfig, projectPath)
}

func (i *initAction) initializeEnv(
	ctx context.Context,
	azdCtx *azdcontext.AzdContext,
	templateMetadata *templates.Metadata) error {
	envName, err := azdCtx.GetDefaultEnvironmentName()
	if err != nil {
		return fmt.Errorf("retrieving default environment name: %w", err)
	}

	if envName != "" {
		return environment.NewEnvironmentInitError(envName)
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
		return err
	}

	envSpec := environment.Spec{
		Name:         i.flags.environmentName,
		Subscription: i.flags.subscription,
		Location:     i.flags.location,
		Examples:     examples,
	}

	env, err := envManager.Create(ctx, envSpec)
	if err != nil {
		return fmt.Errorf("loading environment: %w", err)
	}

	if err := azdCtx.SetDefaultEnvironmentName(env.GetEnvName()); err != nil {
		return fmt.Errorf("saving default environment: %w", err)
	}

	// If the template includes any metadata values, set them in the environment
	if templateMetadata != nil {
		for key, value := range templateMetadata.Variables {
			env.DotenvSet(key, value)
		}

		for key, value := range templateMetadata.Config {
			if err := env.Config.Set(key, value); err != nil {
				return fmt.Errorf("setting environment config: %w", err)
			}
		}

		if err := envManager.Save(ctx, env); err != nil {
			return fmt.Errorf("saving environment: %w", err)
		}
	}

	return nil
}

func getCmdInitHelpDescription(*cobra.Command) string {
	return generateCmdHelpDescription("Initialize a new application in your current directory.",
		[]string{
			formatHelpNote(
				fmt.Sprintf("Running %s without a template will prompt "+
					"you to start with a minimal template or select from a curated list of presets.",
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
