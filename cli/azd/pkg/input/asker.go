package input

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/azureutil"
	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/mattn/go-isatty"
)

type Asker func(p survey.Prompt, response interface{}) error

func NewAsker(noPrompt bool) Asker {
	if noPrompt {
		return askOneNoPrompt
	}

	return askOnePrompt
}

func askOneNoPrompt(p survey.Prompt, response interface{}) error {
	switch v := p.(type) {
	case *survey.Input:
		if v.Default == "" {
			return fmt.Errorf("no default response for prompt '%s'", v.Message)
		}

		*(response.(*string)) = v.Default
	case *survey.Select:
		if v.Default == nil {
			return fmt.Errorf("no default response for prompt '%s'", v.Message)
		}

		switch ptr := response.(type) {
		case *int:
			didSet := false
			for idx, item := range v.Options {
				if v.Default.(string) == item {
					*ptr = idx
					didSet = true
				}
			}

			if !didSet {
				return fmt.Errorf("default response not in list of options for prompt '%s'", v.Message)
			}
		case *string:
			*ptr = v.Default.(string)
		default:
			return fmt.Errorf("bad type %T for result, should be (*int or *string)", response)
		}
	case *survey.Confirm:
		*(response.(*bool)) = v.Default
	default:
		panic(fmt.Sprintf("don't know how to prompt for type %T", p))
	}

	return nil
}

func withShowCursor(o *survey.AskOptions) error {
	o.PromptConfig.ShowCursor = true
	return nil
}

func askOnePrompt(p survey.Prompt, response interface{}) error {
	// Like (*bufio.Reader).ReadString(byte) except that it does not buffer input from the input stream.
	// instead, it reads a byte at a time until a delimiter is found, without consuming any extra characters.
	readStringNoBuffer := func(r io.Reader, delim byte) (string, error) {
		strBuf := bytes.Buffer{}
		readBuf := make([]byte, 1)
		for {
			if _, err := r.Read(readBuf); err != nil {
				return strBuf.String(), err
			}

			// discard err, per documentation, WriteByte always succeeds.
			_ = strBuf.WriteByte(readBuf[0])

			if readBuf[0] == delim {
				return strBuf.String(), nil
			}
		}
	}

	if isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd()) && os.Getenv("AZD_DEBUG_FORCE_NO_TTY") != "1" {
		opts := []survey.AskOpt{}

		// When asking a question which requires a text response, show the cursor, it helps
		// users understand we need some input.
		if _, ok := p.(*survey.Input); ok {
			opts = append(opts, withShowCursor)
		}

		return survey.AskOne(p, response, opts...)
	}

	switch v := p.(type) {
	case *survey.Input:
		var pResponse = response.(*string)
		fmt.Printf("%s", v.Message[0:len(v.Message)-1])
		if v.Default != "" {
			fmt.Printf(" (or hit enter to use the default %s)", v.Default)
		}
		fmt.Printf("%s ", v.Message[len(v.Message)-1:])
		result, err := readStringNoBuffer(os.Stdin, '\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("reading response: %w", err)
		}
		result = strings.TrimSpace(result)
		if result == "" && v.Default != "" {
			result = v.Default
		}
		*pResponse = result
		return nil
	case *survey.Select:
		for {
			fmt.Printf("%s", v.Message[0:len(v.Message)-1])
			if v.Default != nil {
				fmt.Printf(" (or hit enter to use the default %v)", v.Default)
			}
			fmt.Printf("%s ", v.Message[len(v.Message)-1:])
			result, err := readStringNoBuffer(os.Stdin, '\n')
			if err != nil && !errors.Is(err, io.EOF) {
				return fmt.Errorf("reading response: %w", err)
			}
			result = strings.TrimSpace(result)
			if result == "" && v.Default != nil {
				result = v.Default.(string)
			}
			for idx, val := range v.Options {
				if val == result {
					switch ptr := response.(type) {
					case *string:
						*ptr = val
					case *int:
						*ptr = idx
					default:
						return fmt.Errorf("bad type %T for result, should be (*int or *string)", response)
					}

					return nil
				}
			}
			fmt.Printf("error: %s is not an allowed choice\n", result)
		}
	case *survey.Confirm:
		var pResponse = response.(*bool)

		for {
			fmt.Print(v.Message)
			if *pResponse {
				fmt.Print(" (Y/n)")
			} else {
				fmt.Printf(" (y/N)")
			}
			result, err := readStringNoBuffer(os.Stdin, '\n')
			if err != nil && !errors.Is(err, io.EOF) {
				return fmt.Errorf("reading response: %w", err)
			}
			switch strings.TrimSpace(result) {
			case "Y", "y":
				*pResponse = true
				return nil
			case "N", "n":
				*pResponse = false
				return nil
			case "":
				return nil
			}
		}
	default:
		panic(fmt.Sprintf("don't know how to prompt for type %T", p))
	}
}

// PromptTemplate ask the user to select a template.
// An empty string is returned if the user selects 'Empty Template' from the choices
func PromptTemplate(ctx context.Context, message string, askOne Asker) (string, error) {
	templateManager := templates.NewTemplateManager()
	templates, err := templateManager.ListTemplates()

	if err != nil {
		return "", fmt.Errorf("prompting for template: %w", err)
	}

	templateNames := []string{"Empty Template"}

	for _, template := range templates {
		templateNames = append(templateNames, template.Name)
	}

	var selectedTemplateIndex int

	if err := askOne(&survey.Select{
		Message: message,
		Options: templateNames,
		Default: templateNames[0],
	}, &selectedTemplateIndex); err != nil {
		return "", fmt.Errorf("prompting for template: %w", err)
	}

	if selectedTemplateIndex == 0 {
		return "", nil
	}

	log.Printf("Selected template: %s", fmt.Sprint(templateNames[selectedTemplateIndex]))

	return templateNames[selectedTemplateIndex], nil
}

// PromptLocation asks the user to select a location from a list of supported azure location
func PromptLocation(ctx context.Context, message string, askOne Asker) (string, error) {
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
		if errors.Is(err, tools.ErrNoConfigurationValue) {
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

	if err := askOne(&survey.Select{
		Message: message,
		Options: locationOptions,
		Default: defaultOption,
	}, &locationSelectionIndex); err != nil {
		return "", fmt.Errorf("prompting for location: %w", err)
	}

	return locations[locationSelectionIndex].Name, nil
}
