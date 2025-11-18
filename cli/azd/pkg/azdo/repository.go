// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdo

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
)

// create a new repository in the current project
func CreateRepository(
	ctx context.Context,
	projectId string,
	repoName string,
	connection *azuredevops.Connection,
) (*git.GitRepository, error) {
	gitClient, err := git.NewClient(ctx, connection)
	if err != nil {
		return nil, err
	}

	gitRepositoryCreateOptions := git.GitRepositoryCreateOptions{
		Name: &repoName,
	}

	createRepositoryArgs := git.CreateRepositoryArgs{
		Project:               &projectId,
		GitRepositoryToCreate: &gitRepositoryCreateOptions,
	}
	repo, err := gitClient.CreateRepository(ctx, createRepositoryArgs)
	if err != nil {
		return nil, err
	}
	return repo, nil
}

// returns a default repo from a newly created AzDo project.
// this relies on the fact that new projects automatically get a repo named the same as the project
func GetDefaultGitRepositoriesInProject(
	ctx context.Context,
	projectName string,
	connection *azuredevops.Connection,
) (*git.GitRepository, error) {
	gitClient, err := git.NewClient(ctx, connection)
	if err != nil {
		return nil, err
	}

	includeLinks := true
	includeAllUrls := true
	repoArgs := git.GetRepositoriesArgs{
		Project:        &projectName,
		IncludeLinks:   &includeLinks,
		IncludeAllUrls: &includeAllUrls,
	}

	getRepositoriesResult, err := gitClient.GetRepositories(ctx, repoArgs)
	if err != nil {
		return nil, err
	}
	repos := *getRepositoriesResult

	for _, repo := range repos {
		if *repo.Name == projectName {
			return &repo, nil
		}
	}

	return nil, fmt.Errorf("error finding default git repository in project %s", projectName)
}

// prompt the user to select a repo and return a repository object
func GetGitRepositoriesInProject(
	ctx context.Context,
	projectName string,
	orgName string,
	connection *azuredevops.Connection,
	console input.Console,
) (*git.GitRepository, error) {
	gitClient, err := git.NewClient(ctx, connection)
	if err != nil {
		return nil, err
	}

	includeLinks := true
	includeAllUrls := true
	repoArgs := git.GetRepositoriesArgs{
		Project:        &projectName,
		IncludeLinks:   &includeLinks,
		IncludeAllUrls: &includeAllUrls,
	}

	getRepositoriesResult, err := gitClient.GetRepositories(ctx, repoArgs)
	if err != nil {
		return nil, err
	}
	repos := *getRepositoriesResult

	options := make([]string, 0, len(repos))
	for _, repo := range repos {
		options = append(options, *repo.Name)
	}

	if len(options) == 0 {
		return nil, fmt.Errorf("no repositories found in project %s", projectName)
	}

	repoIdx, err := console.Select(ctx, input.ConsoleOptions{
		Message: "Choose an existing Azure DevOps Repository",
		Options: options,
	})

	if err != nil {
		return nil, fmt.Errorf("prompting for azdo repository: %w", err)
	}
	selectedRepoName := options[repoIdx]
	for _, repo := range repos {
		if selectedRepoName == *repo.Name {
			return &repo, nil
		}
	}

	return nil, fmt.Errorf("error finding git repository %s in organization %s", selectedRepoName, orgName)
}

// GetGitRepository find the repository by its name
func GetGitRepository(
	ctx context.Context,
	projectName string,
	repoName string,
	connection *azuredevops.Connection,
) (*git.GitRepository, error) {
	gitClient, err := git.NewClient(ctx, connection)
	if err != nil {
		return nil, err
	}

	return gitClient.GetRepository(ctx, git.GetRepositoryArgs{
		RepositoryId: &repoName,
		Project:      &projectName,
	})
}
