package devcenter

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/devcentersdk"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"golang.org/x/exp/slices"
)

// Prompter provides a common set of methods for prompting the user for devcenter configuration values
type Prompter struct {
	console         input.Console
	manager         Manager
	devCenterClient devcentersdk.DevCenterClient
}

// NewPrompter creates a new devcenter prompter
func NewPrompter(
	console input.Console,
	manager Manager,
	devCenterClient devcentersdk.DevCenterClient,
) *Prompter {
	return &Prompter{
		console:         console,
		manager:         manager,
		devCenterClient: devCenterClient,
	}
}

// PromptForConfig prompts the user for devcenter configuration values that have not been previously set
func (p *Prompter) PromptForConfig(ctx context.Context, config *Config) error {
	if config.Project == "" {
		project, err := p.PromptProject(ctx, config.Name)
		if err != nil {
			return err
		}
		config.Name = project.DevCenter.Name
		config.Project = project.Name
	}

	if config.EnvironmentDefinition == "" {
		envDefinition, err := p.PromptEnvironmentDefinition(ctx, config.Name, config.Project)
		if err != nil {
			return err
		}
		config.Catalog = envDefinition.CatalogName
		config.EnvironmentDefinition = envDefinition.Name
	}

	return nil
}

// PromptProject prompts the user to select a project for the specified devcenter
// If the user only has access to a single project, then that project will be returned
func (p *Prompter) PromptProject(ctx context.Context, devCenterName string) (*devcentersdk.Project, error) {
	writeableProjects, err := p.manager.WritableProjects(ctx)
	if err != nil {
		return nil, err
	}

	if len(writeableProjects) == 0 {
		return nil, fmt.Errorf("no dev center projects found")
	}

	slices.SortFunc(writeableProjects, func(x, y *devcentersdk.Project) bool {
		return x.Name < y.Name
	})

	// Filter to only projects that match the specified devcenter
	filteredProjects := []*devcentersdk.Project{}
	for _, project := range writeableProjects {
		if devCenterName == "" || strings.EqualFold(devCenterName, project.DevCenter.Name) {
			filteredProjects = append(filteredProjects, project)
		}
	}

	duplicateNames := []string{}
	projectNames := []string{}
	for _, project := range filteredProjects {
		if slices.Contains(projectNames, project.Name) {
			duplicateNames = append(duplicateNames, project.Name)
		}

		projectNames = append(projectNames, project.Name)
	}

	// Update display name of any duplicate project names
	if len(duplicateNames) > 0 {
		for index, project := range filteredProjects {
			if slices.Contains(duplicateNames, project.Name) {
				projectNames[index] = fmt.Sprintf("%s (%s)", project.Name, project.DevCenter.Name)
			}
		}
	}

	if len(projectNames) == 1 {
		return filteredProjects[0], nil
	}

	selected, err := p.console.Select(ctx, input.ConsoleOptions{
		Message: "Select a project:",
		Options: projectNames,
	})

	if err != nil {
		return nil, err
	}

	return filteredProjects[selected], nil
}

// PromptEnvironmentType prompts the user to select an environment type for the specified devcenter and project
// If the user only has access to a single environment type, then that environment type will be returned
func (p *Prompter) PromptEnvironmentType(
	ctx context.Context,
	devCenterName string,
	projectName string,
) (*devcentersdk.EnvironmentType, error) {
	envTypesResponse, err := p.devCenterClient.
		DevCenterByName(devCenterName).
		ProjectByName(projectName).
		EnvironmentTypes().
		Get(ctx)

	if err != nil {
		return nil, err
	}

	envTypes := envTypesResponse.Value
	slices.SortFunc(envTypes, func(x, y *devcentersdk.EnvironmentType) bool {
		return x.Name < y.Name
	})

	if len(envTypes) == 0 {
		return nil, fmt.Errorf("no environment types found for '%s'", projectName)
	}

	envTypeNames := []string{}
	for _, envType := range envTypesResponse.Value {
		envTypeNames = append(envTypeNames, envType.Name)
	}

	if len(envTypeNames) == 1 {
		return envTypes[0], nil
	}

	selected, err := p.console.Select(ctx, input.ConsoleOptions{
		Message: "Select an environment type:",
		Options: envTypeNames,
	})

	if err != nil {
		return nil, err
	}

	return envTypes[selected], nil
}

// PromptEnvironmentDefinition prompts the user to select an environment definition for the specified devcenter and project
func (p *Prompter) PromptEnvironmentDefinition(
	ctx context.Context,
	devCenterName, projectName string,
) (*devcentersdk.EnvironmentDefinition, error) {
	envDefinitionsResponse, err := p.devCenterClient.
		DevCenterByName(devCenterName).
		ProjectByName(projectName).
		EnvironmentDefinitions().
		Get(ctx)

	if err != nil {
		return nil, err
	}

	environmentDefinitions := envDefinitionsResponse.Value
	slices.SortFunc(environmentDefinitions, func(x, y *devcentersdk.EnvironmentDefinition) bool {
		return x.Name < y.Name
	})

	if len(environmentDefinitions) == 0 {
		return nil, fmt.Errorf("no environment definitions found for '%s'", projectName)
	}

	duplicateNames := []string{}
	envDefinitionNames := []string{}
	for _, envDefinition := range environmentDefinitions {
		if slices.Contains(envDefinitionNames, envDefinition.Name) {
			duplicateNames = append(duplicateNames, envDefinition.Name)
		}

		envDefinitionNames = append(envDefinitionNames, envDefinition.Name)
	}

	// Update display name of any duplicate environment definition names
	if len(duplicateNames) > 0 {
		for index, envDefinition := range environmentDefinitions {
			if slices.Contains(duplicateNames, envDefinition.Name) {
				envDefinitionNames[index] = fmt.Sprintf("%s (%s)", envDefinition.Name, envDefinition.CatalogName)
			}
		}
	}

	selected, err := p.console.Select(ctx, input.ConsoleOptions{
		Message: "Select an environment definition:",
		Options: envDefinitionNames,
	})

	if err != nil {
		return nil, err
	}

	return environmentDefinitions[selected], nil
}

// Prompts the user for values defined within the environment definition parameters
// Responses for prompt are stored in azd environment configuration and used for future provisioning operations
func (p *Prompter) PromptParameters(
	ctx context.Context,
	env *environment.Environment,
	envDef *devcentersdk.EnvironmentDefinition,
) (map[string]any, error) {
	paramValues := map[string]any{}

	for _, param := range envDef.Parameters {
		paramPath := fmt.Sprintf("%s.%s", ProvisionParametersConfigPath, param.Id)
		paramValue, exists := env.Config.Get(paramPath)

		if exists {
			paramValues[param.Id] = paramValue
			continue
		}

		// Only prompt for parameter values when it has not already been set in the environment configuration
		if param.Id == "environmentName" {
			paramValues[param.Id] = env.Name()
			continue
		}

		// Process repoUrl parameter from defaults and allowed values
		if param.Id == "repoUrl" {
			var repoUrlValue string
			if len(param.Allowed) > 0 {
				repoUrlValue = param.Allowed[0]
			} else {
				value, ok := param.Default.(string)
				if ok {
					repoUrlValue = value
				}
			}

			if repoUrlValue != "" {
				paramValues[param.Id] = repoUrlValue
				continue
			}
		}

		promptOptions := input.ConsoleOptions{
			DefaultValue: param.Default,
			Options:      param.Allowed,
			Message:      fmt.Sprintf("Enter a value for %s", param.Name),
			Help:         param.Description,
		}

		switch param.Type {
		case devcentersdk.ParameterTypeBool:
			confirmValue, err := p.console.Confirm(ctx, promptOptions)
			if err != nil {
				return nil, fmt.Errorf("failed to prompt for %s: %w", param.Name, err)
			}
			paramValue = confirmValue
		case devcentersdk.ParameterTypeString:
			if param.Allowed != nil && len(param.Allowed) > 0 {
				selectedIndex, err := p.console.Select(ctx, promptOptions)
				if err != nil {
					return nil, fmt.Errorf("failed to prompt for %s: %w", param.Name, err)
				}

				paramValue = param.Allowed[selectedIndex]
			} else {
				promptValue, err := p.console.Prompt(ctx, promptOptions)
				if err != nil {
					return nil, err
				}

				paramValue = promptValue
			}

		case devcentersdk.ParameterTypeInt:
			promptValue, err := p.console.Prompt(ctx, promptOptions)
			if err != nil {
				return nil, fmt.Errorf("failed to prompt for %s: %w", param.Name, err)
			}

			numValue, err := strconv.Atoi(promptValue)
			if err != nil {
				return nil, fmt.Errorf("failed to convert %s to int: %w", param.Name, err)
			}
			paramValue = numValue
		default:
			return nil, fmt.Errorf("failed to prompt for %s, unsupported parameter type: %s", param.Name, param.Type)
		}

		paramValues[param.Id] = paramValue
	}

	return paramValues, nil
}
