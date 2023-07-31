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
}

func newInitAction(
	cmdRun exec.CommandRunner,
	console input.Console,
	gitCli git.GitCli,
	flags *initFlags,
	repoInitializer *repository.Initializer) actions.Action {
	return &initAction{
		console:         console,
		cmdRun:          cmdRun,
		gitCli:          gitCli,
		flags:           flags,
		repoInitializer: repoInitializer,
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
		Title: "Initializing a new project (azd init)",
	})

	// If azure.yaml project already exists, we should do the following:
	//   - Not prompt for template selection (user can specify --template if needed to refresh from an existing template)
	//   - Not overwrite azure.yaml (unless --template is explicitly specified)
	//   - Allow for environment initialization
	var existingProject bool
	if _, err := os.Stat(azdCtx.ProjectPath()); err == nil {
		existingProject = true
	} else if errors.Is(err, os.ErrNotExist) {
		existingProject = false
	} else {
		return nil, fmt.Errorf("checking if project exists: %w", err)
	}

	if !existingProject {
		err = i.repoInitializer.PromptIfNonEmpty(ctx, azdCtx)
		if err != nil {
			return nil, err
		}

		if i.flags.templatePath == "" {
			template, err := templates.PromptTemplate(ctx, "Select a project template:", i.console)
			i.flags.templatePath = template.RepositoryPath

			if err != nil {
				return nil, err
			}
		}
	}

	if i.flags.templatePath != "" {
		gitUri, err := templates.Absolute(i.flags.templatePath)
		if err != nil {
			return nil, err
		}

		err = i.repoInitializer.Initialize(ctx, azdCtx, gitUri, i.flags.templateBranch)
		if err != nil {
			return nil, fmt.Errorf("init from template repository: %w", err)
		}
	} else if !existingProject { // do not initialize for empty if azure.yaml is present
		err = i.repoInitializer.InitializeMinimal(ctx, azdCtx)
		if err != nil {
			return nil, fmt.Errorf("init empty repository: %w", err)
		}
	}

	envName, err := azdCtx.GetDefaultEnvironmentName()
	if err != nil {
		return nil, fmt.Errorf("retrieving default environment name: %w", err)
	}

	if envName != "" {
		return nil, environment.NewEnvironmentInitError(envName)
	}

	suggest := environment.CleanName(filepath.Base(wd) + "-dev")
	if len(suggest) > environment.EnvironmentNameMaxLength {
		suggest = suggest[len(suggest)-environment.EnvironmentNameMaxLength:]
	}

	envSpec := environmentSpec{
		environmentName: i.flags.environmentName,
		subscription:    i.flags.subscription,
		location:        i.flags.location,
		suggest:         suggest,
	}

	env, err := createEnvironment(ctx, envSpec, azdCtx, i.console)
	if err != nil {
		return nil, fmt.Errorf("loading environment: %w", err)
	}

	if err := azdCtx.SetDefaultEnvironmentName(env.GetEnvName()); err != nil {
		return nil, fmt.Errorf("saving default environment: %w", err)
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: "New project initialized!",
			FollowUp: heredoc.Docf(`
			You can view the template code in your directory: %s
			Learn more about running 3rd party code on our DevHub: %s`,
				output.WithLinkFormat("%s", wd),
				output.WithLinkFormat("%s", "https://aka.ms/azd-third-party-code-notice")),
		},
	}, nil
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
