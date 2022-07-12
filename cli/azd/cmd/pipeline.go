// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/AlecAivazis/survey/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/github"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func pipelineCmd(rootOptions *commands.GlobalCommandOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pipeline",
		Short: "Manage GitHub Actions pipelines",
		Long: `Manage GitHub Actions pipelines

azd template includes a GitHub Actions pipeline configuration file (find within folder .github/workflows) that will deploy your application whenever code is pushed to the main branch.
		
For more information, please visit: https://aka.ms/azure-dev/pipeline`,
	}
	cmd.Flags().BoolP("help", "h", false, "Help for "+cmd.Name())
	cmd.AddCommand(pipelineConfigCmd(rootOptions))
	return cmd
}

func pipelineConfigCmd(rootOptions *commands.GlobalCommandOptions) *cobra.Command {
	cmd := commands.Build(
		&pipelineConfigAction{rootOptions: rootOptions},
		rootOptions,
		"config",
		"Create and configure your deployment pipeline using GitHub Actions",
		`Create and configure your deployment pipeline using GitHub Actions
		
For more information, please visit: https://aka.ms/azure-dev/pipeline`,
	)
	cmd.Flags().BoolP("help", "h", false, "Help for "+cmd.Name())
	return cmd
}

type pipelineConfigAction struct {
	pipelineServicePrincipalName string
	pipelineRemoteName           string
	pipelineRoleName             string
	rootOptions                  *commands.GlobalCommandOptions
}

func (p *pipelineConfigAction) SetupFlags(
	persis *pflag.FlagSet,
	local *pflag.FlagSet,
) {
	local.StringVar(&p.pipelineServicePrincipalName, "principal-name", "", "The name of the service principal to use to grant access to Azure resources as part of the pipeline")
	local.StringVar(&p.pipelineRemoteName, "remote-name", "origin", "The name of the git remote to configure the pipeline to run on")
	local.StringVar(&p.pipelineRoleName, "principal-role", "Contributor", "Role to assign to the service principal")
}

func (p *pipelineConfigAction) Run(ctx context.Context, _ *cobra.Command, args []string, azdCtx *environment.AzdContext) error {
	askOne := makeAskOne(p.rootOptions.NoPrompt)
	azCli := commands.GetAzCliFromContext(ctx)

	if err := ensureProject(azdCtx.ProjectPath()); err != nil {
		return err
	}

	env, err := loadOrInitEnvironment(ctx, &p.rootOptions.EnvironmentName, azdCtx, askOne)
	if err != nil {
		return fmt.Errorf("loading environment: %w", err)
	}

	if err != nil {
		return fmt.Errorf("loading environment: %w", err)
	}

	gitCli := tools.NewGitCli()
	ghCli := tools.NewGitHubCli()

	if err := tools.EnsureInstalled(ctx, azCli, gitCli, ghCli); err != nil {
		return err
	}

	if err := ensureLoggedIn(ctx); err != nil {
		return fmt.Errorf("failed to ensure login: %w", err)
	}

	if err := ensureGitHubLogin(ctx, ghCli, tools.GitHubHostName, askOne); err != nil {
		return fmt.Errorf("failed to ensure login to GitHub: %w", err)
	}

	// This flag is used later to skip checking GitHub Actions.
	// For new repositories, there's no need to check
	newGitHubRepoCreated := false

	getSlugOrInit := func() (string, error) {
		for {
			repoSlug, err := github.EnsureRemote(ctx, azdCtx.ProjectDirectory(), p.pipelineRemoteName, gitCli)
			switch {
			case errors.Is(err, tools.ErrNotRepository):
				// Offer the user a chance to init a new repository if one does not exist.
				initRepo := false
				if askErr := askOne(&survey.Confirm{
					Message: "Initialize a new git repository?",
					Default: true,
				}, &initRepo); askErr != nil {
					return "", fmt.Errorf("prompting for git init: %w", err)
				}

				if !initRepo {
					return "", err
				}

				if err := gitCli.InitRepo(ctx, azdCtx.ProjectDirectory()); err != nil {
					return "", fmt.Errorf("initializing repository: %w", err)
				}

				// Recovered from this error, try again
				continue
			case errors.Is(err, tools.ErrNoSuchRemote):
				// Offer the user a chance to create the remote if one does not exist.
				addRemote := false
				if err := askOne(&survey.Confirm{
					Message: fmt.Sprintf("A remote named \"%s\" was not found. Would you like to configure one?", p.pipelineRemoteName),
					Default: true,
				}, &addRemote); err != nil {
					return "", fmt.Errorf("prompting for remote init: %w", err)
				}

				if !addRemote {
					return "", errors.New("confirmation declined")
				}

				// There are a few ways to configure the remote so offer a choice to the user.
				var idx int

				if err := askOne(&survey.Select{
					Message: "How would you like to configure your remote?",
					Options: []string{
						"Select an existing GitHub project",
						"Create a new private GitHub repository",
						"Enter a remote URL directly",
					},
					Default: "Create a new private GitHub repository",
				}, &idx); err != nil {
					return "", fmt.Errorf("prompting for remote configuration type: %w", err)
				}

				var remoteUrl string

				switch idx {
				// Select from an existing GitHub project
				case 0:
					url, err := getRemoteUrlFromExisting(ctx, ghCli, askOne)
					if err != nil {
						return "", fmt.Errorf("getting remote from existing repository: %w", err)
					}
					remoteUrl = url
				// Create a new project
				case 1:
					url, err := getRemoteUrlFromNewRepository(ctx, ghCli, azdCtx, askOne)
					if err != nil {
						return "", fmt.Errorf("getting remote from new repository: %w", err)
					}
					newGitHubRepoCreated = true
					remoteUrl = url
				// Enter a URL directly.
				case 2:
					url, err := p.getRemoteUrlFromPrompt(askOne)
					if err != nil {
						return "", fmt.Errorf("getting remote from prompt: %w", err)
					}
					remoteUrl = url
				default:
					panic(fmt.Sprintf("unexpected selection index %d", idx))
				}

				if err := gitCli.AddRemote(ctx, azdCtx.ProjectDirectory(), p.pipelineRemoteName, remoteUrl); err != nil {
					return "", fmt.Errorf("initializing repository: %w", err)
				}

				// Recovered from this error, try again
				continue
			case err != nil:
				return "", err
			default:
				return repoSlug, nil
			}
		}
	}

	repoSlug, err := getSlugOrInit()
	if err != nil {
		return fmt.Errorf("ensuring github remote: %w", err)
	}

	currentBranch, err := gitCli.GetCurrentBranch(ctx, azdCtx.ProjectDirectory())
	if err != nil {
		return fmt.Errorf("getting current branch: %w", err)
	}

	if p.pipelineServicePrincipalName == "" {
		// This format matches what the `az` cli uses when a name is not provided, with the prefix
		// changed from "az-cli" to "az-dev"
		p.pipelineServicePrincipalName = fmt.Sprintf("az-dev-%s", time.Now().UTC().Format("01-02-2006-15-04-05"))
	}

	fmt.Printf("Creating or updating service principal %s.\n", p.pipelineServicePrincipalName)

	credentials, err := azCli.CreateOrUpdateServicePrincipal(ctx, env.GetSubscriptionId(), p.pipelineServicePrincipalName, p.pipelineRoleName)
	if err != nil {
		return fmt.Errorf("failed to create or update service principal: %w", err)
	}

	fmt.Printf("Configuring repository %s to use credentials for %s.\n", repoSlug, p.pipelineServicePrincipalName)

	fmt.Printf("Setting AZURE_CREDENTIALS GitHub repo secret.\n")

	if err := ghCli.SetSecret(ctx, repoSlug, "AZURE_CREDENTIALS", string(credentials)); err != nil {
		return fmt.Errorf("failed setting AZURE_CREDENTIALS secret: %w", err)
	}

	fmt.Printf("Configuring repository environment.\n")

	for _, envName := range []string{environment.EnvNameEnvVarName, environment.LocationEnvVarName, environment.SubscriptionIdEnvVarName} {
		fmt.Printf("Setting %s GitHub repo secret.\n", envName)

		if err := ghCli.SetSecret(ctx, repoSlug, envName, env.Values[envName]); err != nil {
			return fmt.Errorf("failed setting %s secret: %w", envName, err)
		}
	}

	fmt.Println()
	fmt.Printf(`GitHub Action secrets are now configured. See your .github/workflows folder for details on which actions will be enabled.
You can view the GitHub Actions here: https://github.com/%s/actions
`, repoSlug)

	var doPush bool

	if err := askOne(&survey.Confirm{
		Message: "Would you like to commit and push your local changes to start a new GitHub Actions run?",
		Default: true,
	}, &doPush); err != nil {
		return fmt.Errorf("prompting to push: %w", err)
	}

	// Check if GitHub actions are disabled *Only* when user requested to push changes AND this is NOT a just-created repo
	//
	// A repo that is just created would return zero GitHub Actions and might be confused by azd
	// as a repo where Actions are disabled. Sadly, there's not GitHub API to fetch exact information
	// to distinguish between disabled-after-fork v/s repo-disabled-actions v/s similar scenarios).
	if doPush && !newGitHubRepoCreated {
		cancelPushing, err := notifyWhenGitHubActionsAreDisabled(ctx, gitCli, ghCli, azdCtx, repoSlug, p.pipelineRemoteName, currentBranch, askOne)
		if err != nil {
			return fmt.Errorf("ensure github actions: %w", err)
		}
		// Abort doing push on user request
		doPush = !cancelPushing
	}

	if doPush {
		if err := gitCli.AddFile(ctx, azdCtx.ProjectDirectory(), "."); err != nil {
			return fmt.Errorf("adding files: %w", err)
		}

		if err := gitCli.Commit(ctx, azdCtx.ProjectDirectory(), "Configure GitHub Actions"); err != nil {
			return fmt.Errorf("commit changes: %w", err)
		}

		fmt.Println("Pushing changes")

		if err := gitCli.PushUpstream(ctx, azdCtx.ProjectDirectory(), p.pipelineRemoteName, currentBranch); err != nil {
			return fmt.Errorf("pushing changes: %w", err)
		}
	} else {
		fmt.Printf("To fully enable GitHub Actions you need to push this repo to GitHub using 'git push --set-upstream %s %s'.\n", p.pipelineRemoteName, currentBranch)
	}

	return nil
}

// getRemoteUrlFromPrompt interactively prompts the user for a URL for a GitHub repository. It validates
// that the URL is well formed and is in the correct format for a GitHub repository.
func (p *pipelineConfigAction) getRemoteUrlFromPrompt(askOne Asker) (string, error) {
	remoteUrl := ""

	for remoteUrl == "" {
		if err := askOne(&survey.Input{
			Message: fmt.Sprintf("Please enter the url to use for remote %s:", p.pipelineRemoteName),
		}, &remoteUrl); err != nil {
			return "", fmt.Errorf("prompting for remote url: %w", err)
		}

		if _, err := github.GetSlugForRemote(remoteUrl); errors.Is(err, github.ErrRemoteHostIsNotGitHub) {
			fmt.Printf("error: \"%s\" is not a valid GitHub URL.\n", remoteUrl)

			// So we retry from the loop.
			remoteUrl = ""
		}
	}

	return remoteUrl, nil
}

func getRemoteUrlFromNewRepository(ctx context.Context, ghCli tools.GitHubCli, azdCtx *environment.AzdContext, askOne Asker) (string, error) {
	var repoName string

	currentPathName := azdCtx.ProjectDirectory()
	currentFolderName := filepath.Base(currentPathName)

	for {
		if err := askOne(&survey.Input{
			Message: "Enter the name for your new repository OR Hit enter to use this name:",
			Default: currentFolderName,
		}, &repoName); err != nil {
			return "", fmt.Errorf("asking for new repository name: %w", err)
		}

		err := ghCli.CreatePrivateRepository(ctx, repoName)
		if errors.Is(err, tools.ErrRepositoryNameInUse) {
			fmt.Printf("error: the repository name '%s' is already in use\n", repoName)
			continue // try again
		} else if err != nil {
			return "", fmt.Errorf("creating repository: %w", err)
		} else {
			break
		}
	}

	repo, err := ghCli.ViewRepository(ctx, repoName)
	if err != nil {
		return "", fmt.Errorf("fetching repository info: %w", err)
	}

	return selectRemoteUrl(ctx, ghCli, repo)

}

func getRemoteUrlFromExisting(ctx context.Context, ghCli tools.GitHubCli, askOne Asker) (string, error) {
	repos, err := ghCli.ListRepositories(ctx)
	if err != nil {
		return "", fmt.Errorf("listing existing repositories: %w", err)
	}

	options := make([]string, len(repos))
	for idx, repo := range repos {
		options[idx] = repo.NameWithOwner
	}

	var repoIdx int
	if err := askOne(&survey.Select{
		Message: "Please choose an existing GitHub repository",
		Options: options,
	}, &repoIdx); err != nil {
		return "", fmt.Errorf("prompting for repository: %w", err)
	}

	return selectRemoteUrl(ctx, ghCli, repos[repoIdx])
}

func selectRemoteUrl(ctx context.Context, ghCli tools.GitHubCli, repo tools.GhCliRepository) (string, error) {
	protocolType, err := ghCli.GetGitProtocolType(ctx)
	if err != nil {
		return "", fmt.Errorf("detecting default protocol: %w", err)
	}

	switch protocolType {
	case tools.GitHttpsProtocolType:
		return repo.HttpsUrl, nil
	case tools.GitSshProtocolType:
		return repo.SshUrl, nil
	default:
		panic(fmt.Sprintf("unexpected protocol type: %s", protocolType))
	}
}

// ensureGitHubLogin ensures the user is logged into the GitHub CLI. If not, it prompt the user
// if they would like to log in and if so runs `gh auth login` interactively.
func ensureGitHubLogin(ctx context.Context, ghCli tools.GitHubCli, hostname string, askOne Asker) error {
	loggedIn, err := ghCli.CheckAuth(ctx, hostname)
	if err != nil {
		return err
	}

	if loggedIn {
		return nil
	}

	for {
		var accept bool
		if err := askOne(&survey.Confirm{
			Message: "This command requires you to be logged into GitHub. Log in using the GitHub CLI?",
			Default: true,
		}, &accept); err != nil {
			return fmt.Errorf("prompting to log in to github: %w", err)
		}

		if !accept {
			return errors.New("interactive GitHub login declined; use `gh login` to log into GitHub")
		}

		if err := ghCli.Login(ctx, hostname); err == nil {
			return nil
		}

		fmt.Println("There was an issue logging into GitHub.")
	}
}

type gitHubActionsEnablingChoice int

const (
	manualChoice gitHubActionsEnablingChoice = iota
	cancelChoice
)

func (selection gitHubActionsEnablingChoice) String() string {
	switch selection {
	case manualChoice:
		return "I have manually enabled GitHub Actions. Continue with pushing my changes."
	case cancelChoice:
		return "Exit without pushing my changes. I don't need to run GitHub actions right now."
	}
	panic("Tried to convert invalid input gitHubActionsEnablingChoice to string")
}

// notifyWhenGitHubActionsAreDisabled checks if gh-actions are disabled on the repo
// This can happen when a template is first forked and user calls `pipeline config`
// GitHub disables actions by default when a repo is forked.
//
// A user can also disable Actions from /settings/actions, which is different from
// what GitHub does after a template is forked. However, for both cases, calling API
// /repos/<repoSlug>/actions/workflows would return the same.
//
// Returns true, nil if user decides to cancel pushing changes.
func notifyWhenGitHubActionsAreDisabled(
	ctx context.Context,
	gitCli tools.GitCli,
	ghCli tools.GitHubCli,
	azdCtx *environment.AzdContext,
	repoSlug string,
	origin string,
	branch string,
	askOne Asker) (bool, error) {

	ghActionsInUpstreamRepo, err := ghCli.GitHubActionsExists(ctx, repoSlug)
	if err != nil {
		return false, err
	}

	if ghActionsInUpstreamRepo {
		// upstream is already listing GitHub actions.
		// There's no need to check if there are local workflows
		return false, nil
	}

	// Upstream has no GitHub actions listed.
	// See if there's at least one workflow file within .github/workflows
	ghLocalWorkflowFiles := false
	defaultGitHubWorkflowPathLocation := filepath.Join(
		azdCtx.ProjectDirectory(),
		".github",
		"workflows")
	err = filepath.WalkDir(defaultGitHubWorkflowPathLocation,
		func(folderName string, file fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			fileName := file.Name()
			fileExtension := filepath.Ext(fileName)
			if fileExtension == ".yml" || fileExtension == ".yaml" {
				// ** workflow file found.
				// Now check if this file is already tracked by git.
				// If the file is not tracked, it means this is a new file (never pushed to mainstream)
				// A git untracked file should not be considered as GitHub workflow until it is pushed.
				newFile, err := gitCli.IsUntrackedFile(ctx, azdCtx.ProjectDirectory(), folderName)
				if err != nil {
					return fmt.Errorf("checking workflow file %w", err)
				}
				if !newFile {
					ghLocalWorkflowFiles = true
				}
			}

			return nil
		})

	if err != nil {
		return false, fmt.Errorf("Getting GitHub local workflow files %w", err)
	}

	if ghLocalWorkflowFiles {
		printWithStyling("\n%s\n"+
			" - If you forked and cloned a template, please enable actions here: %s.\n"+
			" - Otherwise, check the GitHub Actions permissions here: %s.\n",
			withHighLightFormat("GitHub actions are currently disabled for your repository."),
			withHighLightFormat("https://github.com/%s/actions", repoSlug),
			withHighLightFormat("https://github.com/%s/settings/actions", repoSlug))

		var rawSelection int
		if err := askOne(&survey.Select{
			Message: "What would you like to do now?",
			Options: []string{
				manualChoice.String(),
				cancelChoice.String(),
			},
			Default: manualChoice,
		}, &rawSelection); err != nil {
			return false, fmt.Errorf("prompting to enable github actions: %w", err)
		}
		choice := gitHubActionsEnablingChoice(rawSelection)

		if choice == manualChoice {
			return false, nil
		}

		if choice == cancelChoice {
			return true, nil
		}
	}

	return false, nil
}
