package pipeline

import (
	"context"
	"fmt"
	"os"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"

	"github.com/microsoft/azure-devops-go-api/azuredevops"
	"github.com/microsoft/azure-devops-go-api/azuredevops/core"
	"github.com/microsoft/azure-devops-go-api/azuredevops/git"
)

var (
	// The hostname of the AzDo PaaS service.
	AzDoHostName           = "dev.azure.com"
	AzDoPatName            = "AZURE_DEVOPS_EXT_PAT"
	AzDoEnvironmentOrgName = "AZURE_DEVOPS_ORG_NAME"
)

type AzDoClient struct {
	core.Client
}

func ensureAzdoConfigExists(ctx context.Context, env *environment.Environment, key string, label string) (string, error) {
	value := env.Values[key]
	if value != "" {
		return value, nil
	}

	value, exists := os.LookupEnv(key)
	if !exists || value == "" {
		return value, fmt.Errorf("%s not found in environment variable %s", label, key)
	}
	return value, nil
}

func ensureAzdoPatExists(ctx context.Context, env *environment.Environment) (string, error) {
	return ensureAzdoConfigExists(ctx, env, AzDoPatName, "azure devops personal access token")
}

func ensureAzdoOrgNameExists(ctx context.Context, env *environment.Environment) (string, error) {
	return ensureAzdoConfigExists(ctx, env, AzDoEnvironmentOrgName, "azure devops organization name")

}

func getAzdoConnection(ctx context.Context, organization string, personalAccessToken string) *azuredevops.Connection {
	organizationUrl := fmt.Sprintf("https://dev.azure.com/%s", organization)
	connection := azuredevops.NewPatConnection(organizationUrl, personalAccessToken)
	return connection
}

func getAzDoGitRepositoriesInProject(ctx context.Context, projectName string, orgName string, connection *azuredevops.Connection, console input.Console) (*git.GitRepository, error) {
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

	// If there is only one repo in the project, skip asking the user to select a repo
	if len(repos) == 1 {
		return &repos[0], nil
	}

	options := make([]string, len(repos))
	for idx, repo := range repos {
		options[idx] = *repo.Name
	}
	repoIdx, err := console.Select(ctx, input.ConsoleOptions{
		Message: "Please choose an existing Azure DevOps Repository",
		Options: options,
	})

	if err != nil {
		return nil, fmt.Errorf("prompting for azdo project: %w", err)
	}
	selectedRepoName := options[repoIdx]
	for _, repo := range repos {
		if selectedRepoName == *repo.Name {
			return &repo, nil
		}
	}

	return nil, fmt.Errorf("Error finding git repository %s in organization %s", selectedRepoName, orgName)
}

func getAzdoProjectFromExisting(ctx context.Context, connection *azuredevops.Connection, console input.Console) (string, error) {
	coreClient, err := core.NewClient(ctx, connection)
	if err != nil {
		return "", err
	}

	args := core.GetProjectsArgs{}
	getProjectsResponse, err := coreClient.GetProjects(ctx, args)
	if err != nil {
		return "", err
	}

	projects := getProjectsResponse.Value
	options := make([]string, len(projects))
	for idx, project := range projects {
		options[idx] = *project.Name
	}

	projectIdx, err := console.Select(ctx, input.ConsoleOptions{
		Message: "Please choose an existing Azure DevOps Project",
		Options: options,
	})

	if err != nil {
		return "", fmt.Errorf("prompting for azdo project: %w", err)
	}

	return options[projectIdx], nil
}
