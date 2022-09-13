package pipeline

import (
	"context"
	"fmt"
	"os"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"

	"github.com/microsoft/azure-devops-go-api/azuredevops"
	"github.com/microsoft/azure-devops-go-api/azuredevops/core"
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
