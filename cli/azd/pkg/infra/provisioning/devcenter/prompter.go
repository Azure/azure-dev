package devcenter

import (
	"context"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/devcentersdk"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"golang.org/x/exp/slices"
)

type Prompter struct {
	console         input.Console
	devCenterClient devcentersdk.DevCenterClient
}

func NewPrompter(console input.Console, devCenterClient devcentersdk.DevCenterClient) *Prompter {
	return &Prompter{
		console:         console,
		devCenterClient: devCenterClient,
	}
}

func (p *Prompter) PromptDevCenter(ctx context.Context) (string, error) {
	devCenters := []*devcentersdk.DevCenter{}
	writeableProjects, err := p.devCenterClient.WritableProjects(ctx)
	if err != nil {
		return "", err
	}

	for _, project := range writeableProjects {
		containsDevCenter := slices.ContainsFunc(devCenters, func(dc *devcentersdk.DevCenter) bool {
			return dc.ServiceUri == project.DevCenter.ServiceUri
		})

		if !containsDevCenter {
			devCenters = append(devCenters, project.DevCenter)
		}
	}

	devCenterNames := []string{}
	for _, devCenter := range devCenters {
		devCenterNames = append(devCenterNames, devCenter.Name)
	}

	slices.SortFunc(devCenterNames, func(x, y string) bool {
		return x < y
	})

	if len(devCenterNames) == 1 {
		return devCenterNames[0], nil
	}

	selected, err := p.console.Select(ctx, input.ConsoleOptions{
		Message: "Select a Dev Center:",
		Options: devCenterNames,
	})

	if err != nil {
		return "", err
	}

	return devCenterNames[selected], nil
}

func (p *Prompter) PromptCatalog(ctx context.Context, devCenterName string, projectName string) (string, error) {
	catalogs, err := p.devCenterClient.
		DevCenterByName(devCenterName).
		ProjectByName(projectName).
		Catalogs().
		Get(ctx)

	if err != nil {
		return "", err
	}

	catalogNames := []string{}
	for _, catalog := range catalogs.Value {
		catalogNames = append(catalogNames, catalog.Name)
	}

	slices.SortFunc(catalogNames, func(x, y string) bool {
		return x < y
	})

	if len(catalogNames) == 1 {
		return catalogNames[0], nil
	}

	selected, err := p.console.Select(ctx, input.ConsoleOptions{
		Message: "Select a catalog:",
		Options: catalogNames,
	})

	if err != nil {
		return "", err
	}

	return catalogNames[selected], nil
}

func (p *Prompter) PromptProject(ctx context.Context, devCenterName string) (string, error) {
	writeableProjects, err := p.devCenterClient.WritableProjects(ctx)
	if err != nil {
		return "", err
	}

	projectNames := []string{}
	for _, project := range writeableProjects {
		if strings.EqualFold(devCenterName, project.DevCenter.Name) {
			projectNames = append(projectNames, project.Name)
		}
	}

	slices.SortFunc(projectNames, func(x, y string) bool {
		return x < y
	})

	if len(projectNames) == 1 {
		return projectNames[0], nil
	}

	selected, err := p.console.Select(ctx, input.ConsoleOptions{
		Message: "Select a project:",
		Options: projectNames,
	})

	if err != nil {
		return "", err
	}

	return projectNames[selected], nil
}

func (p *Prompter) PromptEnvironmentType(ctx context.Context, devCenterName string, projectName string) (string, error) {
	envTypes, err := p.devCenterClient.
		DevCenterByName(devCenterName).
		ProjectByName(projectName).
		EnvironmentTypes().
		Get(ctx)

	if err != nil {
		return "", err
	}

	envTypeNames := []string{}
	for _, envType := range envTypes.Value {
		envTypeNames = append(envTypeNames, envType.Name)
	}

	slices.SortFunc(envTypeNames, func(x, y string) bool {
		return x < y
	})

	if len(envTypeNames) == 1 {
		return envTypeNames[0], nil
	}

	selected, err := p.console.Select(ctx, input.ConsoleOptions{
		Message: "Select an environment type:",
		Options: envTypeNames,
	})

	if err != nil {
		return "", err
	}

	return envTypeNames[selected], nil
}

func (p *Prompter) PromptEnvironmentDefinition(ctx context.Context, devCenterName, projectName string) (string, error) {
	envDefinitions, err := p.devCenterClient.
		DevCenterByName(devCenterName).
		ProjectByName(projectName).
		EnvironmentDefinitions().
		Get(ctx)

	if err != nil {
		return "", err
	}

	envDefinitionNames := []string{}
	for _, envDefinition := range envDefinitions.Value {
		envDefinitionNames = append(envDefinitionNames, envDefinition.Name)
	}

	slices.SortFunc(envDefinitionNames, func(x, y string) bool {
		return x < y
	})

	selected, err := p.console.Select(ctx, input.ConsoleOptions{
		Message: "Select an environment definition:",
		Options: envDefinitionNames,
	})

	if err != nil {
		return "", err
	}

	return envDefinitionNames[selected], nil
}

// Prompts the user for values defined within the environment definition parameters
func (p *Prompter) PromptParameters(
	ctx context.Context,
	env *environment.Environment,
	envDef *devcentersdk.EnvironmentDefinition,
) (map[string]any, error) {
	paramValues := map[string]any{}

	for _, param := range envDef.Parameters {
		if param.Name == "environmentName" {
			paramValues[param.Name] = env.GetEnvName()
			continue
		}

		if param.Name == "repoUrl" {
			paramValues[param.Name] = param.Allowed[0]
			continue
		}

		paramValue, exists := env.Config.Get(fmt.Sprintf("provision.%s", param.Name))
		if !exists {
			promptOptions := input.ConsoleOptions{
				DefaultValue: param.Default,
				Options:      param.Allowed,
				Message:      fmt.Sprintf("Enter a value for %s", param.Name),
				Help:         param.Description,
			}

			if param.Allowed != nil && len(param.Allowed) > 0 {
				selectedIndex, err := p.console.Select(ctx, promptOptions)

				if err != nil {
					return nil, err
				}

				paramValue = param.Allowed[selectedIndex]
			} else {
				promptValue, err := p.console.Prompt(ctx, promptOptions)
				if err != nil {
					return nil, err
				}
				paramValue = promptValue
			}
		}

		paramValues[param.Name] = paramValue
	}

	return paramValues, nil
}
