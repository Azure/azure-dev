// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	githubRemote "github.com/azure/azure-dev/cli/azd/pkg/github"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
)

type gitHubScmProvider struct {
	newGitHubRepoCreated bool
}

// ***  subareaProvider implementation ******
func (p *gitHubScmProvider) requiredTools() []tools.ExternalTool {
	return []tools.ExternalTool{
		github.NewGitHubCli(),
	}
}

func (p *gitHubScmProvider) preConfigureCheck(ctx context.Context, console input.Console) error {
	return ensureGitHubLogin(ctx, github.GitHubHostName, console)
}

func (p *gitHubScmProvider) name() string {
	return "GitHub"
}

// ***  scmProvider implementation ******
func (p *gitHubScmProvider) configureGitRemote(ctx context.Context, repoPath string, remoteName string, console input.Console) (string, error) {
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
	ghCli := github.NewGitHubCli()

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

func (p *gitHubScmProvider) gitRepoDetails(ctx context.Context, remoteUrl string) (*gitRepositoryDetails, error) {

	return nil, nil
}

func (p *gitHubScmProvider) preventGitPush(
	ctx context.Context,
	gitRepo *gitRepositoryDetails,
	remoteName string,
	branchName string,
	console input.Console) (bool, error) {
	return false, nil
}

type gitHubCiProvider struct {
}

// ***  subareaProvider implementation ******
func (p *gitHubCiProvider) requiredTools() []tools.ExternalTool {
	return []tools.ExternalTool{
		github.NewGitHubCli(),
	}
}

func (p *gitHubCiProvider) preConfigureCheck(ctx context.Context, console input.Console) error {
	return ensureGitHubLogin(ctx, github.GitHubHostName, console)
}
func (p *gitHubCiProvider) name() string {
	return "GitHub"
}

// ***  ciProvider implementation ******
func (p *gitHubCiProvider) configureConnection(
	ctx context.Context,
	repoSlug *gitRepositoryDetails,
	environmentName string,
	location string,
	subscriptionId string,
	credential json.RawMessage) error {
	return nil
}

// ensureGitHubLogin ensures the user is logged into the GitHub CLI. If not, it prompt the user
// if they would like to log in and if so runs `gh auth login` interactively.
func ensureGitHubLogin(ctx context.Context, hostname string, console input.Console) error {
	ghCli := github.NewGitHubCli()
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

		fmt.Println("There was an issue logging into GitHub.")
	}
}

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

func getRemoteUrlFromNewRepository(ctx context.Context, ghCli github.GitHubCli, currentPathName string, console input.Console) (string, error) {
	var repoName string
	currentFolderName := filepath.Base(currentPathName)

	for {
		repoName, err := console.Prompt(ctx, input.ConsoleOptions{
			Message:      "Enter the name for your new repository OR Hit enter to use this name:",
			DefaultValue: currentFolderName,
		})
		if err != nil {
			return "", fmt.Errorf("asking for new repository name: %w", err)
		}

		err = ghCli.CreatePrivateRepository(ctx, repoName)
		if errors.Is(err, github.ErrRepositoryNameInUse) {
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
			fmt.Printf("error: \"%s\" is not a valid GitHub URL.\n", remoteUrl)

			// So we retry from the loop.
			remoteUrl = ""
		}
	}

	return remoteUrl, nil
}
