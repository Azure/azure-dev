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
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
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
	local.StringVar(
		&i.subscription,
		"subscription",
		"",
		"Name or ID of an Azure subscription to use for the new environment",
	)
	local.StringVarP(&i.location, "location", "l", "", "Azure location for the new environment")
	i.envFlag.Bind(local, global)

	i.global = global
}

type initAction struct {
	console         input.Console
	cmdRun          exec.CommandRunner
	gitCli          git.GitCli
	flags           *initFlags
	repoInitializer *repository.Initializer
	templateManager *templates.TemplateManager
}

func newInitAction(
	cmdRun exec.CommandRunner,
	console input.Console,
	gitCli git.GitCli,
	flags *initFlags,
	repoInitializer *repository.Initializer,
	templateManager *templates.TemplateManager) actions.Action {
	return &initAction{
		console:         console,
		cmdRun:          cmdRun,
		gitCli:          gitCli,
		flags:           flags,
		repoInitializer: repoInitializer,
		templateManager: templateManager,
	}
}

func (i *initAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getting cwd: %w", err)
	}

	azdCtx := azdcontext.NewAzdContextWithDirectory(wd)

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
		Title: "Initializing an app to run on Azure",
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
		initTypeSelect, err = promptInitType(i.console, ctx)
		if err != nil {
			return nil, err
		}
	}

	initializeEnv := func() (*actions.ActionResult, error) {
		envName, err := azdCtx.GetDefaultEnvironmentName()
		if err != nil {
			return nil, fmt.Errorf("retrieving default environment name: %w", err)
		}

		if envName != "" {
			return nil, environment.NewEnvironmentInitError(envName)
		}

		base := filepath.Base(wd)
		examples := []string{}
		for _, c := range []string{"dev", "test", "prod"} {
			suggest := environment.CleanName(base + "-" + c)
			if len(suggest) > environment.EnvironmentNameMaxLength {
				suggest = suggest[len(suggest)-environment.EnvironmentNameMaxLength:]
			}

			examples = append(examples, suggest)
		}

		envSpec := environmentSpec{
			environmentName: i.flags.environmentName,
			subscription:    i.flags.subscription,
			location:        i.flags.location,
			examples:        examples,
		}

		env, err := createEnvironment(ctx, envSpec, azdCtx, i.console)
		if err != nil {
			return nil, fmt.Errorf("loading environment: %w", err)
		}

		if err := azdCtx.SetDefaultEnvironmentName(env.GetEnvName()); err != nil {
			return nil, fmt.Errorf("saving default environment: %w", err)
		}

		return nil, nil
	}

	header := "New project initialized!"
	followUp := heredoc.Docf(`
	You can view the template code in your directory: %s
	Learn more about running 3rd party code on our DevHub: %s`,
		output.WithLinkFormat("%s", wd),
		output.WithLinkFormat("%s", "https://aka.ms/azd-third-party-code-notice"))
	switch initTypeSelect {
	case initInfra:
		header = "Your app is ready for the cloud!"
		followUp = "You can provision and deploy your app to Azure by running the " + output.WithBlueFormat("azd up") +
			" command in this directory. For more information on configuring your app, see " +
			output.WithHighLightFormat("./next-steps.md")
		err := i.repoInitializer.InitializeInfra(ctx, azdCtx, func() error {
			_, err := initializeEnv()
			return err
		})
		if err != nil {
			return nil, err
		}
	case initAppTemplate:
		err := i.InitializeTemplate(ctx, azdCtx)
		if err != nil {
			return nil, err
		}

		_, err = initializeEnv()
		if err != nil {
			return nil, err
		}
	case initEnvironment:
		_, err = initializeEnv()
		if err != nil {
			return nil, err
		}
	// no-opt
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
	initInfra
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
		return initInfra, nil
	case 1:
		return initAppTemplate, nil
	default:
		panic("unhandled selection")
	}
}

func (i *initAction) InitializeTemplate(
	ctx context.Context,
	azdCtx *azdcontext.AzdContext) error {
	err := i.repoInitializer.PromptIfNonEmpty(ctx, azdCtx)
	if err != nil {
		return err
	}

	if i.flags.templatePath == "" {
		template, err := templates.PromptTemplate(ctx, "Select a project template:", i.templateManager, i.console)
		if template != nil {
			i.flags.templatePath = template.RepositoryPath
		}

		if err != nil {
			return err
		}
	}

	if i.flags.templatePath != "" {
		gitUri, err := templates.Absolute(i.flags.templatePath)
		if err != nil {
			return err
		}

		err = i.repoInitializer.Initialize(ctx, azdCtx, gitUri, i.flags.templateBranch)
		if err != nil {
			return fmt.Errorf("init from template repository: %w", err)
		}
	} else {
		err := i.repoInitializer.InitializeMinimal(ctx, azdCtx)
		if err != nil {
			return fmt.Errorf("init empty repository: %w", err)
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
