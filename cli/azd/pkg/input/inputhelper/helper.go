package inputhelper

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azureutil"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

// PromptLocation asks the user to select a location from a list of supported azure location
func PromptLocation(ctx context.Context, message string) (string, error) {
	azCli := azcli.GetAzCli(ctx)
	console := input.GetConsole(ctx)

	locations, err := azCli.ListAccountLocations(ctx)
	if err != nil {
		return "", fmt.Errorf("listing locations: %w", err)
	}

	sort.Sort(azureutil.Locs(locations))

	// Allow the environment variable `AZURE_LOCATION` to control the default value for the location
	// selection.
	defaultLocation := os.Getenv(environment.LocationEnvVarName)

	// If no location is set in the process environment, see what the CLI default is.
	if defaultLocation == "" {
		defaultLocationConfig, err := azCli.GetCliConfigValue(ctx, "defaults.location")
		if errors.Is(err, azcli.ErrNoConfigurationValue) {
			// If no value has been configured, that's okay we just won't have a default
			// in our list.
		} else if err != nil {
			return "", fmt.Errorf("detecting default location: %w", err)
		} else {
			defaultLocation = defaultLocationConfig.Value
		}
	}

	// If we still couldn't figure out a default location, offer eastus2 as a default
	if defaultLocation == "" {
		defaultLocation = "eastus2"
	}

	var defaultOption string

	locationOptions := make([]string, len(locations))
	for index, location := range locations {
		locationOptions[index] = fmt.Sprintf("%2d. %s (%s)", index+1, location.RegionalDisplayName, location.Name)

		if strings.EqualFold(defaultLocation, location.Name) ||
			strings.EqualFold(defaultLocation, location.DisplayName) {
			defaultOption = locationOptions[index]
		}
	}

	selectedIndex, err := console.Select(ctx, input.ConsoleOptions{
		Message:      message,
		Options:      locationOptions,
		DefaultValue: defaultOption,
	})

	if err != nil {
		return "", fmt.Errorf("prompting for location: %w", err)
	}

	return locations[selectedIndex].Name, nil
}

// PromptTemplate ask the user to select a template.
// An empty Template with default values is returned if the user selects 'Empty Template' from the choices
func PromptTemplate(ctx context.Context, message string) (templates.Template, error) {
	console := input.GetConsole(ctx)

	var result templates.Template
	templateManager := templates.NewTemplateManager()
	templatesSet, err := templateManager.ListTemplates()

	if err != nil {
		return result, fmt.Errorf("prompting for template: %w", err)
	}

	templateNames := []string{"Empty Template"}

	for name := range templatesSet {
		templateNames = append(templateNames, name)
	}

	selectedIndex, err := console.Select(ctx, input.ConsoleOptions{
		Message:      message,
		Options:      templateNames,
		DefaultValue: templateNames[0],
	})

	if err != nil {
		return result, fmt.Errorf("prompting for template: %w", err)
	}

	if selectedIndex == 0 {
		return result, nil
	}

	selectedTemplateName := templateNames[selectedIndex]
	log.Printf("Selected template: %s", fmt.Sprint(selectedTemplateName))

	return templatesSet[selectedTemplateName], nil
}
