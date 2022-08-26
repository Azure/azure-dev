package input

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/azureutil"
	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

type Console interface {
	Message(ctx context.Context, message string) error
	Prompt(ctx context.Context, options ConsoleOptions) (string, error)
	Select(ctx context.Context, options ConsoleOptions) (int, error)
	Confirm(ctx context.Context, options ConsoleOptions) (bool, error)
	PromptLocation(ctx context.Context, message string) (string, error)
	PromptTemplate(ctx context.Context, message string) (templates.Template, error)
}

type AskerConsole struct {
	asker Asker
}

type ConsoleOptions struct {
	Message      string
	Options      []string
	DefaultValue any
}

func (c *AskerConsole) Message(ctx context.Context, message string) error {
	_, err := fmt.Println(message)
	if err != nil {
		return fmt.Errorf("error printing line: %w", err)
	}

	return nil
}

func (c *AskerConsole) Prompt(ctx context.Context, options ConsoleOptions) (string, error) {
	var defaultValue string
	if value, ok := options.DefaultValue.(string); ok {
		defaultValue = value
	}

	survey := &survey.Input{
		Message: options.Message,
		Default: defaultValue,
	}

	var response string

	if err := c.asker(survey, &response); err != nil {
		return "", err
	}

	return response, nil
}

func (c *AskerConsole) Select(ctx context.Context, options ConsoleOptions) (int, error) {
	survey := &survey.Select{
		Message: options.Message,
		Options: options.Options,
		Default: options.DefaultValue,
	}

	var response int

	if err := c.asker(survey, &response); err != nil {
		return -1, err
	}

	return response, nil
}

func (c *AskerConsole) Confirm(ctx context.Context, options ConsoleOptions) (bool, error) {
	var defaultValue bool
	if value, ok := options.DefaultValue.(bool); ok {
		defaultValue = value
	}

	survey := &survey.Confirm{
		Message: options.Message,
		Default: defaultValue,
	}

	var response bool

	if err := c.asker(survey, &response); err != nil {
		return false, err
	}

	return response, nil
}

// PromptTemplate ask the user to select a template.
// An empty Template with default values is returned if the user selects 'Empty Template' from the choices
func (c *AskerConsole) PromptTemplate(ctx context.Context, message string) (templates.Template, error) {
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

	var selectedTemplateIndex int

	if err := c.asker(&survey.Select{
		Message: message,
		Options: templateNames,
		Default: templateNames[0],
	}, &selectedTemplateIndex); err != nil {
		return result, fmt.Errorf("prompting for template: %w", err)
	}

	if selectedTemplateIndex == 0 {
		return result, nil
	}

	selectedTemplateName := templateNames[selectedTemplateIndex]
	log.Printf("Selected template: %s", fmt.Sprint(selectedTemplateName))

	return templatesSet[selectedTemplateName], nil
}

// PromptLocation asks the user to select a location from a list of supported azure location
func (c *AskerConsole) PromptLocation(ctx context.Context, message string) (string, error) {
	azCli := commands.GetAzCliFromContext(ctx)

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

	var locationSelectionIndex int

	if err := c.asker(&survey.Select{
		Message: message,
		Options: locationOptions,
		Default: defaultOption,
	}, &locationSelectionIndex); err != nil {
		return "", fmt.Errorf("prompting for location: %w", err)
	}

	return locations[locationSelectionIndex].Name, nil
}

func NewConsole(interactive bool) Console {
	asker := NewAsker(!interactive)

	return &AskerConsole{
		asker: asker,
	}
}
