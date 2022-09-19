package azdo

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/microsoft/azure-devops-go-api/azuredevops"
	"github.com/microsoft/azure-devops-go-api/azuredevops/core"
	"github.com/microsoft/azure-devops-go-api/azuredevops/operations"
)

// returns a process template (basic, agile etc) used in the new project creation flow
func getProcessTemplateId(ctx context.Context, client core.Client) (string, error) {
	processArgs := core.GetProcessesArgs{}
	processes, err := client.GetProcesses(ctx, processArgs)
	if err != nil {
		return "", err
	}
	process := (*processes)[0]
	return process.Id.String(), nil
}

// creates a new Azure Devops project
func createProject(ctx context.Context, connection *azuredevops.Connection, name string, description string, console input.Console) (*core.TeamProjectReference, error) {
	coreClient, err := core.NewClient(ctx, connection)
	if err != nil {
		return nil, err
	}

	processTemplateId, err := getProcessTemplateId(ctx, coreClient)
	if err != nil {
		return nil, fmt.Errorf("error fetching process template id %w", err)
	}

	capabilities := map[string]map[string]string{
		"versioncontrol": {
			"sourceControlType": "git",
		},
		"processTemplate": {
			"templateTypeId": processTemplateId,
		},
	}
	args := core.QueueCreateProjectArgs{
		ProjectToCreate: &core.TeamProject{
			Description:  &description,
			Name:         &name,
			Visibility:   &core.ProjectVisibilityValues.Private,
			Capabilities: &capabilities,
		},
	}
	res, err := coreClient.QueueCreateProject(ctx, args)
	if err != nil {
		return nil, err
	}

	operationsClient := operations.NewClient(ctx, connection)

	getOperationsArgs := operations.GetOperationArgs{
		OperationId: res.Id,
	}

	projectCreated := false
	maxCheck := 10
	count := 0

	for !projectCreated {
		operation, err := operationsClient.GetOperation(ctx, getOperationsArgs)
		if err != nil {
			return nil, err
		}

		if *operation.Status == "succeeded" {
			projectCreated = true
		}

		if count >= maxCheck {
			return nil, fmt.Errorf("error creating azure devops project %s", name)
		}

		count++
		time.Sleep(700 * time.Millisecond)
	}

	project, err := getAzdoProjectByName(ctx, connection, name)
	if err != nil {
		return nil, err
	}
	return project, nil
}

// prompts the user for a new AzDo project name and creates the project
// returns project name, project id, error
func GetAzdoProjectFromNew(ctx context.Context, repoPath string, connection *azuredevops.Connection, env *environment.Environment, console input.Console) (string, string, error) {
	var project *core.TeamProjectReference
	currentFolderName := filepath.Base(repoPath)
	var projectDescription string = AzDoProjectDescription

	for {
		name, err := console.Prompt(ctx, input.ConsoleOptions{
			Message:      "Enter the name for your new Azure Devops Project OR Hit enter to use this name:",
			DefaultValue: currentFolderName,
		})
		if err != nil {
			return "", "", fmt.Errorf("asking for new project name: %w", err)
		}
		var message string = ""
		newProject, err := createProject(ctx, connection, name, projectDescription, console)
		if err != nil {
			message = err.Error()
		}
		if strings.Contains(message, fmt.Sprintf("The following project already exists on the Azure DevOps Server: %s", name)) {
			console.Message(ctx, fmt.Sprintf("error: the project name '%s' is already in use\n", name))
			continue // try again
		} else if strings.Contains(message, "The following name is not valid") {
			console.Message(ctx, fmt.Sprintf("error: the project name '%s' is not a valid Azure DevOps project Name. See https://aka.ms/azure-dev/azdo-project-naming\n", name))
			continue // try again
		} else if err != nil {
			return "", "", fmt.Errorf("creating project: %w", err)
		} else {
			project = newProject
			break
		}
	}

	return *project.Name, project.Id.String(), nil
}

// return an azdo project by name
func getAzdoProjectByName(ctx context.Context, connection *azuredevops.Connection, name string) (*core.TeamProjectReference, error) {
	coreClient, err := core.NewClient(ctx, connection)
	if err != nil {
		return nil, err
	}

	args := core.GetProjectsArgs{}
	getProjectsResponse, err := coreClient.GetProjects(ctx, args)
	if err != nil {
		return nil, err
	}

	projects := getProjectsResponse.Value
	for _, project := range projects {
		if *project.Name == name {
			return &project, nil
		}
	}

	return nil, fmt.Errorf("azure devops project %s not found", name)
}

// prompt the user to select form a list of existing Azure DevOps projects
func GetAzdoProjectFromExisting(ctx context.Context, connection *azuredevops.Connection, console input.Console) (string, string, error) {
	coreClient, err := core.NewClient(ctx, connection)
	if err != nil {
		return "", "", err
	}

	args := core.GetProjectsArgs{}
	getProjectsResponse, err := coreClient.GetProjects(ctx, args)
	if err != nil {
		return "", "", err
	}

	projects := getProjectsResponse.Value
	projectsList := make([]core.TeamProjectReference, len(projects))
	options := make([]string, len(projects))
	for idx, project := range projects {
		options[idx] = *project.Name
		projectsList[idx] = project
	}

	projectIdx, err := console.Select(ctx, input.ConsoleOptions{
		Message: "Please choose an existing Azure DevOps Project",
		Options: options,
	})

	if err != nil {
		return "", "", fmt.Errorf("prompting for azdo project: %w", err)
	}

	return options[projectIdx], projectsList[projectIdx].Id.String(), nil
}
