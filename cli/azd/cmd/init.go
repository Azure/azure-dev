// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/internal/repository"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/exp/maps"
	"golang.org/x/exp/slices"
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
	template       templates.Template
	templateBranch string
	subscription   string
	location       string
	global         *internal.GlobalCommandOptions
	*envFlag
}

func (i *initFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	i.bindNonCommon(local, global)
	i.bindCommon(local, global)
}

func (i *initFlags) bindNonCommon(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.StringVarP(
		&i.template.Name,
		"template",
		"t",
		"",
		//nolint:lll
		"The template to use when you initialize the project. You can use Full URI, <owner>/<repository>, or <repository> if it's part of the azure-samples organization.",
	)
	local.StringVarP(&i.templateBranch, "branch", "b", "", "The template branch to initialize from.")
	local.StringVar(
		&i.subscription,
		"subscription",
		"",
		"Name or ID of an Azure subscription to use for the new environment",
	)
	local.StringVarP(&i.location, "location", "l", "", "Azure location for the new environment")
	i.global = global
}

func (i *initFlags) bindCommon(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	i.envFlag = &envFlag{}
	i.envFlag.Bind(local, global)
}

func (i *initFlags) setCommon(envFlag *envFlag) {
	i.envFlag = envFlag
}

type initAction struct {
	accountManager     account.Manager
	userProfileService *azcli.UserProfileService
	console            input.Console
	cmdRun             exec.CommandRunner
	gitCli             git.GitCli
	flags              *initFlags
	repoInitializer    *repository.Initializer
}

func newInitAction(
	accountManager account.Manager,
	userProfileService *azcli.UserProfileService,
	_ auth.LoggedInGuard,
	cmdRun exec.CommandRunner,
	console input.Console,
	gitCli git.GitCli,
	flags *initFlags,
	repoInitializer *repository.Initializer) actions.Action {
	return &initAction{
		accountManager:     accountManager,
		console:            console,
		cmdRun:             cmdRun,
		gitCli:             gitCli,
		flags:              flags,
		userProfileService: userProfileService,
		repoInitializer:    repoInitializer,
	}
}

func (i *initAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getting cwd: %w", err)
	}

	azdCtx := azdcontext.NewAzdContextWithDirectory(wd)

	if i.flags.templateBranch != "" && i.flags.template.Name == "" {
		return nil, errors.New("template name required when specifying a branch name")
	}

	// init now requires git all the time, even for empty template, azd initializes a local git project
	if err := tools.EnsureInstalled(ctx, []tools.ExternalTool{i.gitCli}...); err != nil {
		return nil, err
	}

	// Command title
	i.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title: "Initializing a new project (azd init)",
	})

	if i.flags.template.Name != "" {
		// Explicit template specified, always initialize
		err = i.initializeFromTemplate(ctx, azdCtx, &i.flags.template, i.flags.templateBranch)
		if err != nil {
			return nil, err
		}
	} else if _, err := os.Stat(azdCtx.ProjectPath()); errors.Is(err, os.ErrNotExist) {
		// Only prompt if azure.yaml is not present
		choice, err := i.console.Select(ctx, input.ConsoleOptions{
			Message: "How would you like to initialize your app?",
			Options: []string{
				"Select an official template",
				"Enter a template URL",
				"Scaffold my existing app",
			},
		})
		if err != nil {
			return nil, err
		}

		if choice == 0 {
			i.flags.template, err = templates.PromptTemplate(ctx, "Select a project template:", i.console)
			if err != nil {
				return nil, err
			}

			if i.flags.template.Name == "" {
				err = i.repoInitializer.InitializeEmpty(ctx, azdCtx)
				if err != nil {
					return nil, fmt.Errorf("init empty repository: %w", err)
				}
			} else {
				err = i.initializeFromTemplate(ctx, azdCtx, &i.flags.template, i.flags.templateBranch)
				if err != nil {
					return nil, err
				}
			}
		} else if choice == 1 {
			i.console.Message(
				ctx,
				"Template URL formats: <owner>/<repository> for a GitHub repository, or a full git URL.")
			answer, err := i.console.Prompt(ctx, input.ConsoleOptions{
				Message: "Enter a template URL",
			})
			if err != nil {
				return nil, err
			}

			i.flags.template.Name = answer
			err = i.initializeFromTemplate(ctx, azdCtx, &i.flags.template, i.flags.templateBranch)
			if err != nil {
				return nil, err
			}
		} else {
			if _, err := os.Stat(filepath.Join(azdCtx.ProjectDirectory(), "azure.draft.yaml")); err == nil {
				i.console.Message(ctx, "Resuming scaffolding from azure.draft.yaml")
				err = os.Rename(filepath.Join(azdCtx.ProjectDirectory(), "azure.draft.yaml"), azdCtx.ProjectPath())
				if err != nil {
					return nil, err
				}
				i.console.MessageUxItem(ctx, &ux.DoneMessage{Message: "Renaming to " + output.WithLinkFormat("azure.yaml")})
			}

			i.console.ShowSpinner(ctx, "Analyzing code under current directory...", input.Step)

			projects, err := appdetect.Detect(azdCtx.ProjectDirectory())
			if err != nil {
				return nil, fmt.Errorf("failed app detection: %w", err)
			}

			i.console.StopSpinner(
				ctx, "Analyzed code under current directory.", input.StepDone)

			useOptions := repository.InfraUseOptions{}
			c := templates.Characteristics{}

			if len(projects) > 0 {
				extractCharacteristics(projects, &c, &useOptions)

				i.console.Message(ctx, "The following languages were detected:")
				msg, err := describe(projects, c.Type, azdCtx)
				if err != nil {
					return nil, err
				}
				i.console.Message(ctx, msg)

				confirm, err := i.console.Confirm(ctx, input.ConsoleOptions{Message: "Is this correct?"})
				if err != nil {
					return nil, err
				}

				if !confirm {
					err = i.repoInitializer.ScaffoldProject(ctx, "azure.draft.yaml", azdCtx, useOptions.Projects)
					if err != nil {
						return nil, err
					}
					i.console.Message(
						ctx,
						//nolint:lll
						"Saved draft as azure.draft.yaml. Make edits to this file, and rerun `azd init`. For more information, visit https://aka.ms/azd")
					return nil, nil
				}
			} else if len(projects) == 0 {
				i.console.Message(ctx, "Could not find any projects under the current directory.")
				i.console.Message(
					ctx,
					//nolint:lll
					"Saved draft as azure.draft.yaml. Make edits to this file, and rerun `azd init`. For more information, visit https://aka.ms/azd")
				err = i.repoInitializer.ScaffoldProject(ctx, "azure.draft.yaml", azdCtx, useOptions.Projects)
				if err != nil {
					return nil, err
				}

				return nil, nil
			}

			err = i.repoInitializer.ScaffoldProject(ctx, "azure.yaml", azdCtx, useOptions.Projects)
			if err != nil {
				return nil, err
			}

			i.flags.template, err = templates.MatchOne(ctx, i.console, c)
			if err != nil {
				return nil, err
			}

			if i.flags.template.RepositoryPath == string(templates.ApiApp) {
				options := repository.DatabaseDisplayOptions()
				display := maps.Keys(options)
				slices.Sort(display)
				sel, err := i.console.Select(ctx, input.ConsoleOptions{
					Message: "Would you like to create a database for your app?",
					Options: display,
				})
				if err != nil {
					return nil, fmt.Errorf("prompting for database: %w", err)
				}

				useOptions.Database = options[display[sel]]

				if useOptions.Database != repository.DatabaseNone {
					ans, err := i.console.Prompt(ctx, input.ConsoleOptions{
						Message: "Enter a name for your database",
					})
					if err != nil {
						return nil, fmt.Errorf("prompting for database name: %w", err)
					}

					useOptions.DatabaseName = ans
				}
			}

			err = i.repoInitializer.InitializeInfra(ctx, azdCtx, i.flags.template.RepositoryPath, "", useOptions)
			if err != nil {
				return nil, fmt.Errorf("generating infrastructure: %w", err)
			}

			return &actions.ActionResult{
				Message: &actions.ResultMessage{
					Header: "New project initialized!",
					FollowUp: heredoc.Docf(`
						Make changes to infrastructure-as-code (IaC) files under the %s folder as needed.
						To deploy your app to Azure, run 'azd up'
					`,
						output.WithLinkFormat("infra")),
				},
			}, nil
		}
	} else if err != nil {
		return nil, err
	}

	envName, err := azdCtx.GetDefaultEnvironmentName()
	if err != nil {
		return nil, fmt.Errorf("retrieving default environment name: %w", err)
	}

	if envName != "" {
		return nil, environment.NewEnvironmentInitError(envName)
	}

	envSpec := environmentSpec{
		environmentName: i.flags.environmentName,
		subscription:    i.flags.subscription,
		location:        i.flags.location,
	}
	env, err := createAndInitEnvironment(ctx, &envSpec, azdCtx, i.console, i.accountManager, i.userProfileService)
	if err != nil {
		return nil, fmt.Errorf("loading environment: %w", err)
	}

	if err := azdCtx.SetDefaultEnvironmentName(envSpec.environmentName); err != nil {
		return nil, fmt.Errorf("saving default environment: %w", err)
	}

	// If the configuration is empty, set default subscription & location
	// This will be the case for first run experience
	if !i.accountManager.HasDefaults() {
		_, err = i.accountManager.SetDefaultSubscription(ctx, env.GetSubscriptionId())
		if err != nil {
			log.Printf("failed setting default subscription. %s\n", err.Error())
		}
		_, err = i.accountManager.SetDefaultLocation(ctx, env.GetSubscriptionId(), env.GetLocation())
		if err != nil {
			log.Printf("failed setting default location. %s\n", err.Error())
		}
	}

	//nolint:lll
	azdTrustNotice := "https://learn.microsoft.com/azure/developer/azure-developer-cli/azd-templates#guidelines-for-using-azd-templates"

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: "New project initialized!",
			FollowUp: heredoc.Docf(`
			You can view the template code in your directory: %s
			Learn more about running 3rd party code on our DevHub: %s`,
				output.WithLinkFormat("%s", wd),
				output.WithLinkFormat("%s", azdTrustNotice)),
		},
	}, nil
}

func extractCharacteristics(
	projects []appdetect.Project,
	character *templates.Characteristics,
	useOptions *repository.InfraUseOptions) {
	hasOneWeb := false
	for _, project := range projects {
		if project.HasWebUIFramework() {
			hasOneWeb = true
			break
		}
	}

	if hasOneWeb && len(projects) == 1 {
		character.Type = templates.WebApp
	} else if hasOneWeb && len(projects) > 1 {
		character.Type = templates.ApiWeb
	} else {
		character.Type = templates.ApiApp
	}

	for _, project := range projects {
		character.LanguageTags = append(character.LanguageTags, string(project.Language))

		if project.HasWebUIFramework() {
			spec := repository.ProjectSpec{
				Language:  string(project.Language),
				Host:      "appservice",
				Path:      project.Path,
				HackIsWeb: true,
			}

			if project.Frameworks[0] == appdetect.React {
				spec.OutputPath = "build"
			} else if project.Frameworks[0] == appdetect.VueJs || project.Frameworks[0] == appdetect.Angular {
				spec.OutputPath = "dist"
			}

			useOptions.Projects = append(useOptions.Projects, spec)
		} else {
			useOptions.Language = string(project.Language)
			useOptions.Projects = append(useOptions.Projects, repository.ProjectSpec{
				Language: string(project.Language),
				Host:     "appservice",
				Path:     project.Path,
			})
		}
	}
}

func (i *initAction) initializeFromTemplate(
	ctx context.Context,
	azdCtx *azdcontext.AzdContext,
	template *templates.Template,
	templateBranch string) error {
	var templateUrl string

	if template.RepositoryPath == "" {
		// using template name directly from command line
		template.RepositoryPath = template.Name
	}

	// treat names that start with http or git as full URLs and don't change them
	if strings.HasPrefix(template.RepositoryPath, "git") ||
		strings.HasPrefix(template.RepositoryPath, "http") {
		templateUrl = template.RepositoryPath
	} else {
		switch strings.Count(template.RepositoryPath, "/") {
		case 0:
			templateUrl = fmt.Sprintf("https://github.com/Azure-Samples/%s", template.RepositoryPath)
		case 1:
			templateUrl = fmt.Sprintf("https://github.com/%s", template.RepositoryPath)
		default:
			return fmt.Errorf(
				"template '%s' should be either <repository> or <repo>/<repository>", template.RepositoryPath)
		}
	}

	err := i.repoInitializer.Initialize(ctx, azdCtx, templateUrl, templateBranch)
	if err != nil {
		return fmt.Errorf("init from template repository: %w", err)
	}

	return nil
}
func describe(
	projects []appdetect.Project,
	appType templates.ApplicationType,
	azdCtx *azdcontext.AzdContext) (string, error) {
	var b strings.Builder
	for _, p := range projects {
		hasWeb := p.HasWebUIFramework()
		lang := p.Language.Display()
		if p.Docker != nil {
			lang += " using containers"
		}

		if hasWeb {
			frameworks := []string{}
			for _, f := range p.Frameworks {
				if f.IsWebUIFramework() {
					frameworks = append(frameworks, f.Display())
				}
			}

			lang = strings.Join(frameworks, ", ")
		}

		b.WriteString(fmt.Sprintf("  - %s", output.WithBold(lang)))

		if appType == templates.ApiWeb {
			if hasWeb {
				b.WriteString(" (Web App)\n")
			} else {
				b.WriteString(" (Web API)\n")
			}
		}
	}

	return b.String(), nil
}

func getCmdInitHelpDescription(c *cobra.Command) string {
	return generateCmdHelpDescription("Initialize a new application in your current directory.",
		getCmdHelpDescriptionNoteForInit(c))
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
