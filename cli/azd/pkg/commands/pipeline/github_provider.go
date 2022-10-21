// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	githubRemote "github.com/azure/azure-dev/cli/azd/pkg/github"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
)

// GitHubScmProvider implements ScmProvider using GitHub as the provider
// for source control manager.
type GitHubScmProvider struct {
	newGitHubRepoCreated bool
}

// ***  subareaProvider implementation ******

// requiredTools return the list of external tools required by
// GitHub provider during its execution.
func (p *GitHubScmProvider) requiredTools(ctx context.Context) []tools.ExternalTool {
	return []tools.ExternalTool{
		github.NewGitHubCli(ctx),
	}
}

// preConfigureCheck check the current state of external tools and any
// other dependency to be as expected for execution.
func (p *GitHubScmProvider) preConfigureCheck(ctx context.Context, console input.Console) error {
	return ensureGitHubLogin(ctx, github.GitHubHostName, console)
}

// name returns the name of the provider
func (p *GitHubScmProvider) name() string {
	return "GitHub"
}

// ***  scmProvider implementation ******

// configureGitRemote uses GitHub cli to guide user on setting a remote url
// for the local git project
func (p *GitHubScmProvider) configureGitRemote(
	ctx context.Context,
	repoPath string,
	remoteName string,
	console input.Console,
) (string, error) {
	// used to detect when the GitHub has created a new repo
	p.newGitHubRepoCreated = false

	// There are a few ways to configure the remote so offer a choice to the user.
	idx, err := console.Select(ctx, input.ConsoleOptions{
		Message: "How would you like to configure your remote?",
		Options: []string{
			"Select an existing GitHub project",
			"Create a new private GitHub repository",
			"Enter a remote URL directly",
		},
		DefaultValue: "Create a new private GitHub repository",
	})

	if err != nil {
		return "", fmt.Errorf("prompting for remote configuration type: %w", err)
	}

	var remoteUrl string
	ghCli := github.NewGitHubCli(ctx)

	switch idx {
	// Select from an existing GitHub project
	case 0:
		remoteUrl, err = getRemoteUrlFromExisting(ctx, ghCli, console)
		if err != nil {
			return "", fmt.Errorf("getting remote from existing repository: %w", err)
		}
	// Create a new project
	case 1:
		remoteUrl, err = getRemoteUrlFromNewRepository(ctx, ghCli, repoPath, console)
		if err != nil {
			return "", fmt.Errorf("getting remote from new repository: %w", err)
		}
		p.newGitHubRepoCreated = true
	// Enter a URL directly.
	case 2:
		remoteUrl, err = getRemoteUrlFromPrompt(ctx, remoteName, console)
		if err != nil {
			return "", fmt.Errorf("getting remote from prompt: %w", err)
		}
	default:
		panic(fmt.Sprintf("unexpected selection index %d", idx))
	}

	return remoteUrl, nil
}

// defines the structure of an ssl git remote
var gitHubRemoteGitUrlRegex = regexp.MustCompile(`^git@github\.com:(.*?)(?:\.git)?$`)

// defines the structure of an HTTPS git remote
var gitHubRemoteHttpsUrlRegex = regexp.MustCompile(`^https://(?:www\.)?github\.com/(.*?)(?:\.git)?$`)

// ErrRemoteHostIsNotGitHub the error used when a non GitHub remote is found
var ErrRemoteHostIsNotGitHub = errors.New("not a github host")

// gitRepoDetails extracts the information from a GitHub remote url into general scm concepts
// like owner, name and path
func (p *GitHubScmProvider) gitRepoDetails(ctx context.Context, remoteUrl string) (*gitRepositoryDetails, error) {
	slug := ""
	for _, r := range []*regexp.Regexp{gitHubRemoteGitUrlRegex, gitHubRemoteHttpsUrlRegex} {
		captures := r.FindStringSubmatch(remoteUrl)
		if captures != nil {
			slug = captures[1]
		}
	}
	if slug == "" {
		return nil, ErrRemoteHostIsNotGitHub
	}
	slugParts := strings.Split(slug, "/")
	return &gitRepositoryDetails{
		owner:    slugParts[0],
		repoName: slugParts[1],
	}, nil
}

// preventGitPush validate if GitHub actions are disabled and won't work before pushing
// changes to upstream.
func (p *GitHubScmProvider) preventGitPush(
	ctx context.Context,
	gitRepo *gitRepositoryDetails,
	remoteName string,
	branchName string,
	console input.Console) (bool, error) {
	// Don't need to check for preventing push on new created repos
	// Only check when using an existing repo in case github actions are disabled
	if !p.newGitHubRepoCreated {
		slug := gitRepo.owner + "/" + gitRepo.repoName
		return notifyWhenGitHubActionsAreDisabled(ctx, gitRepo.gitProjectPath, slug, remoteName, branchName, console)
	}
	return false, nil
}

func (p *GitHubScmProvider) postGitPush(
	ctx context.Context,
	gitRepo *gitRepositoryDetails,
	remoteName string,
	branchName string,
	console input.Console) error {
	return nil
}

// enum type for taking a choice after finding GitHub actions disabled.
type gitHubActionsEnablingChoice int

// defines the options upon detecting GitHub actions disabled.
const (
	manualChoice gitHubActionsEnablingChoice = iota
	cancelChoice
)

// enables gitHubActionsEnablingChoice to produce a string value.
func (selection gitHubActionsEnablingChoice) String() string {
	switch selection {
	case manualChoice:
		return "I have manually enabled GitHub Actions. Continue with pushing my changes."
	case cancelChoice:
		return "Exit without pushing my changes. I don't need to run GitHub actions right now."
	}
	panic("Tried to convert invalid input gitHubActionsEnablingChoice to string")
}

// notifyWhenGitHubActionsAreDisabled uses GitHub cli to check if actions are disabled
// or if at least one workflow is not listed. Returns true after interacting with user
// and if user decides to stop a current petition to push changes to upstream.
func notifyWhenGitHubActionsAreDisabled(
	ctx context.Context,
	gitProjectPath,
	repoSlug string,
	origin string,
	branch string,
	console input.Console) (bool, error) {

	ghCli := github.NewGitHubCli(ctx)
	gitCli := git.NewGitCli(ctx)
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
		gitProjectPath,
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
				newFile, err := gitCli.IsUntrackedFile(ctx, gitProjectPath, folderName)
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
		message := fmt.Sprintf("\n%s\n"+
			" - If you forked and cloned a template, please enable actions here: %s.\n"+
			" - Otherwise, check the GitHub Actions permissions here: %s.\n",
			output.WithHighLightFormat("GitHub actions are currently disabled for your repository."),
			output.WithHighLightFormat("https://github.com/%s/actions", repoSlug),
			output.WithHighLightFormat("https://github.com/%s/settings/actions", repoSlug))

		console.Message(ctx, message)

		rawSelection, err := console.Select(ctx, input.ConsoleOptions{
			Message: "What would you like to do now?",
			Options: []string{
				manualChoice.String(),
				cancelChoice.String(),
			},
			DefaultValue: manualChoice.String(),
		})

		if err != nil {
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

// GitHubCiProvider implements a CiProvider using GitHub to manage CI pipelines as
// GitHub actions.
type GitHubCiProvider struct {
}

// ***  subareaProvider implementation ******

// requiredTools defines the requires tools for GitHub to be used as CI manager
func (p *GitHubCiProvider) requiredTools(ctx context.Context) []tools.ExternalTool {
	return []tools.ExternalTool{
		github.NewGitHubCli(ctx),
	}
}

// preConfigureCheck validates that current state of tools and GitHub is as expected to
// execute.
func (p *GitHubCiProvider) preConfigureCheck(ctx context.Context, console input.Console) error {
	return ensureGitHubLogin(ctx, github.GitHubHostName, console)
}

// name returns the name of the provider.
func (p *GitHubCiProvider) name() string {
	return "GitHub"
}

// ***  ciProvider implementation ******

// configureConnection set up GitHub account with Azure Credentials for
// GitHub actions to use a service principal account to log in to Azure
// and make changes on behalf of a user.
func (p *GitHubCiProvider) configureConnection(
	ctx context.Context,
	azdEnvironment *environment.Environment,
	repoDetails *gitRepositoryDetails,
	infraOptions provisioning.Options,
	credentials json.RawMessage,
	console input.Console) error {

	repoSlug := repoDetails.owner + "/" + repoDetails.repoName
	console.Message(ctx, fmt.Sprintf("Configuring repository %s.\n", repoSlug))
	console.Message(ctx, "Setting AZURE_CREDENTIALS GitHub repo secret.\n")

	ghCli := github.NewGitHubCli(ctx)
	// set azure credential for pipelines can log in to Azure
	if err := ghCli.SetSecret(ctx, repoSlug, "AZURE_CREDENTIALS", string(credentials)); err != nil {
		return fmt.Errorf("failed setting AZURE_CREDENTIALS secret: %w", err)
	}

	if infraOptions.Provider == provisioning.Terraform {
		// terraform expect the credential info to be set in the env individually
		type credentialParse struct {
			Tenant       string `json:"tenantId"`
			ClientId     string `json:"clientId"`
			ClientSecret string `json:"clientSecret"`
		}
		values := credentialParse{}
		if e := json.Unmarshal(credentials, &values); e != nil {
			return fmt.Errorf("setting terraform env var credentials: %w", e)
		}
		if err := ghCli.SetSecret(ctx, repoSlug, "ARM_TENANT_ID", values.Tenant); err != nil {
			return fmt.Errorf("setting terraform env var credentials:: %w", err)
		}
		if err := ghCli.SetSecret(ctx, repoSlug, "ARM_CLIENT_ID", values.ClientId); err != nil {
			return fmt.Errorf("setting terraform env var credentials:: %w", err)
		}
		if err := ghCli.SetSecret(ctx, repoSlug, "ARM_CLIENT_SECRET", values.ClientSecret); err != nil {
			return fmt.Errorf("setting terraform env var credentials:: %w", err)
		}

		// Sets the terraform remote state environment variables in github
		remoteStateKeys := []string{"RS_RESOURCE_GROUP", "RS_STORAGE_ACCOUNT", "RS_CONTAINER_NAME"}
		for _, key := range remoteStateKeys {
			value, ok := azdEnvironment.Values[key]
			if !ok || strings.TrimSpace(value) == "" {
				console.Message(ctx, output.WithWarningFormat("WARNING: Terraform Remote State configuration is invalid!"))
				console.Message(
					ctx,
					fmt.Sprintf(
						"Visit %s for more information on configuring Terraform remote state",
						output.WithLinkFormat("https://aka.ms/azure-dev/terraform"),
					),
				)
				console.Message(ctx, "")
				return errors.New("terraform remote state is not correctly configured")
			}
			// env var was found
			if err := ghCli.SetSecret(ctx, repoSlug, key, value); err != nil {
				return fmt.Errorf("setting terraform remote state variables: %w", err)
			}
		}
	}

	console.Message(ctx, "Configuring repository environment.\n")

	for _, envName := range []string{
		environment.EnvNameEnvVarName,
		environment.LocationEnvVarName,
		environment.SubscriptionIdEnvVarName} {
		console.Message(ctx, fmt.Sprintf("Setting %s GitHub repo secret.\n", envName))

		if err := ghCli.SetSecret(ctx, repoSlug, envName, azdEnvironment.Values[envName]); err != nil {
			return fmt.Errorf("failed setting %s secret: %w", envName, err)
		}
	}

	console.Message(ctx, fmt.Sprintf(
		`GitHub Action secrets are now configured.
		See your .github/workflows folder for details on which actions will be enabled.
		You can view the GitHub Actions here: https://github.com/%s/actions`, repoSlug))

	return nil
}

// configurePipeline is a no-op for GitHub, as the pipeline is automatically
// created by creating the workflow files in .github folder.
func (p *GitHubCiProvider) configurePipeline(
	ctx context.Context,
	repoDetails *gitRepositoryDetails,
	provisioningProvider provisioning.Options,
) error {
	return nil
}

// ensureGitHubLogin ensures the user is logged into the GitHub CLI. If not, it prompt the user
// if they would like to log in and if so runs `gh auth login` interactively.
func ensureGitHubLogin(ctx context.Context, hostname string, console input.Console) error {
	ghCli := github.NewGitHubCli(ctx)
	loggedIn, err := ghCli.CheckAuth(ctx, hostname)
	if err != nil {
		return err
	}

	if loggedIn {
		return nil
	}

	for {
		var accept bool
		accept, err := console.Confirm(ctx, input.ConsoleOptions{
			Message:      "This command requires you to be logged into GitHub. Log in using the GitHub CLI?",
			DefaultValue: true,
		})
		if err != nil {
			return fmt.Errorf("prompting to log in to github: %w", err)
		}

		if !accept {
			return errors.New("interactive GitHub login declined; use `gh auth login` to log into GitHub")
		}

		if err := ghCli.Login(ctx, hostname); err == nil {
			return nil
		}

		fmt.Fprintln(console.Handles().Stdout, "There was an issue logging into GitHub.")
	}
}

// getRemoteUrlFromExisting let user to select an existing repository from his/her account and
// returns the remote url for that repository.
func getRemoteUrlFromExisting(ctx context.Context, ghCli github.GitHubCli, console input.Console) (string, error) {
	repos, err := ghCli.ListRepositories(ctx)
	if err != nil {
		return "", fmt.Errorf("listing existing repositories: %w", err)
	}

	options := make([]string, len(repos))
	for idx, repo := range repos {
		options[idx] = repo.NameWithOwner
	}

	repoIdx, err := console.Select(ctx, input.ConsoleOptions{
		Message: "Please choose an existing GitHub repository",
		Options: options,
	})

	if err != nil {
		return "", fmt.Errorf("prompting for repository: %w", err)
	}

	return selectRemoteUrl(ctx, ghCli, repos[repoIdx])
}

// selectRemoteUrl let user to type and enter the url from an existing GitHub repo.
// If the url is valid, the remote url is returned. Otherwise an error is returned.
func selectRemoteUrl(ctx context.Context, ghCli github.GitHubCli, repo github.GhCliRepository) (string, error) {
	protocolType, err := ghCli.GetGitProtocolType(ctx)
	if err != nil {
		return "", fmt.Errorf("detecting default protocol: %w", err)
	}

	switch protocolType {
	case github.GitHttpsProtocolType:
		return repo.HttpsUrl, nil
	case github.GitSshProtocolType:
		return repo.SshUrl, nil
	default:
		panic(fmt.Sprintf("unexpected protocol type: %s", protocolType))
	}
}

// getRemoteUrlFromNewRepository creates a new repository on GitHub and returns its remote url
func getRemoteUrlFromNewRepository(
	ctx context.Context,
	ghCli github.GitHubCli,
	currentPathName string,
	console input.Console,
) (string, error) {
	var repoName string
	currentFolderName := filepath.Base(currentPathName)

	for {
		name, err := console.Prompt(ctx, input.ConsoleOptions{
			Message:      "Enter the name for your new repository OR Hit enter to use this name:",
			DefaultValue: currentFolderName,
		})
		if err != nil {
			return "", fmt.Errorf("asking for new repository name: %w", err)
		}

		err = ghCli.CreatePrivateRepository(ctx, name)
		if errors.Is(err, github.ErrRepositoryNameInUse) {
			console.Message(ctx, fmt.Sprintf("error: the repository name '%s' is already in use\n", name))
			continue // try again
		} else if err != nil {
			return "", fmt.Errorf("creating repository: %w", err)
		} else {
			repoName = name
			break
		}
	}

	repo, err := ghCli.ViewRepository(ctx, repoName)
	if err != nil {
		return "", fmt.Errorf("fetching repository info: %w", err)
	}

	return selectRemoteUrl(ctx, ghCli, repo)
}

// getRemoteUrlFromPrompt interactively prompts the user for a URL for a GitHub repository. It validates
// that the URL is well formed and is in the correct format for a GitHub repository.
func getRemoteUrlFromPrompt(ctx context.Context, remoteName string, console input.Console) (string, error) {
	remoteUrl := ""

	for remoteUrl == "" {
		promptValue, err := console.Prompt(ctx, input.ConsoleOptions{
			Message: fmt.Sprintf("Please enter the url to use for remote %s:", remoteName),
		})

		if err != nil {
			return "", fmt.Errorf("prompting for remote url: %w", err)
		}

		remoteUrl = promptValue

		if _, err := githubRemote.GetSlugForRemote(remoteUrl); errors.Is(err, githubRemote.ErrRemoteHostIsNotGitHub) {
			fmt.Fprintf(console.Handles().Stdout, "error: \"%s\" is not a valid GitHub URL.\n", remoteUrl)

			// So we retry from the loop.
			remoteUrl = ""
		}
	}

	return remoteUrl, nil
}
