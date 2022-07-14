// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

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
	"github.com/fatih/color"
	"github.com/mattn/go-isatty"
	"github.com/mgutz/ansi"
)

type Asker func(p survey.Prompt, response interface{}) error

// ensureValidEnvironmentName ensures the environment name is valid, if it is not, an error is printed
// and the user is prompted for a new name.
func ensureValidEnvironmentName(environmentName *string, askOneFn Asker) error {
	for !environment.IsValidEnvironmentName(*environmentName) {
		err := askOneFn(&survey.Input{
			Message: "Please enter a new environment name:",
		}, environmentName)

		if err != nil {
			return fmt.Errorf("reading environment name: %w", err)
		}

		if !environment.IsValidEnvironmentName(*environmentName) {
			fmt.Printf("environment name '%s' is invalid (it should contain only alphanumeric characters and hyphens)\n", *environmentName)
		}
	}

	return nil
}

// createEnvironment creates a new named environment. If an environment with this name already
// exists, and error is return.
func createAndInitEnvironment(ctx context.Context, environmentName *string, azdCtx *environment.AzdContext, askOne Asker) (environment.Environment, error) {
	if *environmentName != "" && !environment.IsValidEnvironmentName(*environmentName) {
		fmt.Printf("environment name '%s' is invalid (it should contain only alphanumeric characters and hyphens)\n", *environmentName)
		return environment.Environment{}, fmt.Errorf("environment name '%s' is invalid (it should contain only alphanumeric characters and hyphens)", *environmentName)
	}

	if err := ensureValidEnvironmentName(environmentName, askOne); err != nil {
		return environment.Environment{}, err
	}

	// Ensure the environment does not already exist:
	env, err := azdCtx.GetEnvironment(*environmentName)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return environment.Environment{}, fmt.Errorf("checking for existing environment: %w", err)
	case err == nil:
		return environment.Environment{}, fmt.Errorf("environment '%s' already exists", *environmentName)
	}

	if err := ensureEnvironmentInitialized(ctx, *environmentName, &env, askOne); err != nil {
		return environment.Environment{}, fmt.Errorf("initializing environment: %w", err)
	}

	return env, nil
}

func loadOrInitEnvironment(ctx context.Context, environmentName *string, azdCtx *environment.AzdContext, askOne Asker) (environment.Environment, error) {
	loadOrCreateEnvironment := func() (environment.Environment, bool, error) {
		// If there's a default environment, use that
		if *environmentName == "" {
			var err error
			*environmentName, err = azdCtx.GetDefaultEnvironmentName()
			if err != nil {
				return environment.Environment{}, false, fmt.Errorf("getting default environment: %w", err)
			}
		}

		if *environmentName != "" {
			env, err := azdCtx.GetEnvironment(*environmentName)
			switch {
			case errors.Is(err, os.ErrNotExist):
				var shouldCreate bool
				msg := fmt.Sprintf("Environment '%s' does not exist, would you like to create it?", *environmentName)
				promptErr := askOne(&survey.Confirm{
					Message: msg,
					Default: true,
				}, &shouldCreate)
				if promptErr != nil {
					return environment.Environment{}, false, fmt.Errorf("prompting to create environment '%s': %w", *environmentName, promptErr)
				}
				if !shouldCreate {
					return environment.Environment{}, false, fmt.Errorf("environment '%s' not found: %w", *environmentName, err)
				}
			case err != nil:
				return environment.Environment{}, false, fmt.Errorf("loading environment '%s': %w", *environmentName, err)
			case err == nil:
				return env, false, nil
			}
		}

		// Two cases if we get to here:
		// - The user has not specified an environment name (and there was no default environment set)
		// - The user has specified an environment name, but the named environment didn't exist and they told us they would
		//   like us to create it.
		if *environmentName != "" && !environment.IsValidEnvironmentName(*environmentName) {
			fmt.Printf("environment name '%s' is invalid (it should contain only alphanumeric characters and hyphens)\n", *environmentName)
			return environment.Environment{}, false, fmt.Errorf("environment name '%s' is invalid (it should contain only alphanumeric characters and hyphens)", *environmentName)
		}

		if err := ensureValidEnvironmentName(environmentName, askOne); err != nil {
			return environment.Environment{}, false, err
		}

		return environment.Empty(azdCtx.GetEnvironmentFilePath(*environmentName)), true, nil
	}

	env, isNew, err := loadOrCreateEnvironment()
	switch {
	case errors.Is(err, os.ErrNotExist):
		return environment.Environment{}, fmt.Errorf("environment %s does not exist", *environmentName)
	case err != nil:
		return environment.Environment{}, err
	}

	if err := ensureEnvironmentInitialized(ctx, *environmentName, &env, askOne); err != nil {
		return environment.Environment{}, fmt.Errorf("initializing environment: %w", err)
	}

	if isNew {
		if err := azdCtx.SetDefaultEnvironmentName(*environmentName); err != nil {
			return environment.Environment{}, fmt.Errorf("saving default environment name: %w", err)
		}
	}

	return env, nil
}

// ensureEnvironmentInitialized ensures the environment is initialized, i.e. it contains values for `AZURE_ENV_NAME`, `AZURE_LOCATION`, `AZURE_SUBSCRIPTION_ID` and `AZURE_PRINCIPAL_ID`.
// prompts for any missing values
func ensureEnvironmentInitialized(ctx context.Context, environmentName string, env *environment.Environment, askOne Asker) error {
	if env.Values == nil {
		env.Values = make(map[string]string)
	}

	hasValue := func(key string) bool {
		val, has := env.Values[key]
		return has && val != ""
	}

	hasEnvName := hasValue(environment.EnvNameEnvVarName)
	hasLocation := hasValue(environment.LocationEnvVarName)
	hasSubID := hasValue(environment.SubscriptionIdEnvVarName)
	hasPrincipalID := hasValue(environment.PrincipalIdEnvVarName)

	if hasEnvName && hasLocation && hasSubID && hasPrincipalID {
		return nil
	}

	if !hasEnvName {
		env.SetEnvName(environmentName)
	}

	if !hasSubID || !hasPrincipalID || !hasLocation {
		if err := ensureLoggedIn(ctx); err != nil {
			return fmt.Errorf("logging in: %w", err)
		}
	}

	if !hasLocation {
		var location string
		location, err := promptLocation(ctx, "Please select an Azure location to use:", askOne)
		if err != nil {
			return fmt.Errorf("prompting for location: %w", err)
		}

		env.Values[environment.LocationEnvVarName] = strings.TrimSpace(location)
	}

	azCli := commands.GetAzCliFromContext(ctx)

	if !hasSubID {
		subscriptionInfos, err := azCli.ListAccounts(ctx)
		if err != nil {
			return fmt.Errorf("listing accounts: %w", err)
		}

		sort.Sort(azureutil.Subs(subscriptionInfos))

		// If `AZURE_SUBSCRIPTION_ID` is set in the environment, use it to influence
		// the default option in our prompt. Fall back to the what the `az` CLI is
		// configured to use if the environment variable is unset.
		defaultSubscriptionId := os.Getenv(environment.SubscriptionIdEnvVarName)
		if defaultSubscriptionId == "" {
			for _, info := range subscriptionInfos {
				if info.IsDefault {
					defaultSubscriptionId = info.Id
				}
			}
		}

		var subscriptionOptions = make([]string, len(subscriptionInfos)+1)
		var defaultSubscription string

		for index, info := range subscriptionInfos {
			subscriptionOptions[index] = fmt.Sprintf("%2d. %s (%s)", index+1, info.Name, info.Id)

			if info.Id == defaultSubscriptionId {
				defaultSubscription = subscriptionOptions[index]
			}
		}

		subscriptionOptions[len(subscriptionOptions)-1] = "Other (enter manually)"

		var subscriptionId string

		for env.GetSubscriptionId() == "" {
			var subscriptionSelection string
			err := askOne(&survey.Select{
				Message: "Please select an Azure Subscription to use:",
				Options: subscriptionOptions,
				Default: defaultSubscription,
			}, &subscriptionSelection)
			if err != nil {
				return fmt.Errorf("reading subscription id: %w", err)
			}
			if subscriptionSelection == "Other (enter manually)" {
				err = askOne(&survey.Input{
					Message: "Enter an Azure Subscription to use:",
				}, &subscriptionId)
				if err != nil {
					return fmt.Errorf("reading subscription id: %w", err)
				}
			} else {
				subscriptionId = subscriptionSelection[len(subscriptionSelection)-len("(059cdffa-0e5b-47d8-ad4b-f13fd9099f21)")+1 : len(subscriptionSelection)-1]
			}

			env.Values[environment.SubscriptionIdEnvVarName] = strings.TrimSpace(subscriptionId)
		}
	}

	if !hasPrincipalID {
		principalId, err := azureutil.GetCurrentPrincipalId(ctx)
		if err != nil {
			return fmt.Errorf("fetching current user information: %w", err)
		}

		env.Values[environment.PrincipalIdEnvVarName] = principalId
	}

	if err := env.Save(); err != nil {
		return fmt.Errorf("saving environment: %w", err)
	}

	return nil
}

func makeAskOne(noPrompt bool) Asker {
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

// promptTemplate ask the user to select a template.
// An empty string is returned if the user selects 'Empty Template' from the choices
func promptTemplate(ctx context.Context, message string, askOne Asker) (string, error) {
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

// promptLocation asks the user to select a location from a list of supported azure location
func promptLocation(ctx context.Context, message string, askOne Asker) (string, error) {
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

func saveEnvironmentValues(res tools.AzCliDeployment, env environment.Environment) error {
	if len(res.Properties.Outputs) > 0 {
		for name, o := range res.Properties.Outputs {
			env.Values[name] = fmt.Sprintf("%v", o.Value)
		}

		if err := env.Save(); err != nil {
			return fmt.Errorf("writing environment: %w", err)
		}
	}

	return nil
}

var (
	errNoProject = errors.New("no project exists; to create a new project, run `azd init`.")
)

// ensureProject ensures that a project file exists, using the given
// context. If a project is missing, errNoProject is returned.
func ensureProject(path string) error {
	_, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return errNoProject
	} else if err != nil {
		return fmt.Errorf("checking for project: %w", err)
	}

	return nil
}

// withLinkFormat creates string with hyperlink-looking color
func withLinkFormat(link string, a ...interface{}) string {
	// See ansi colors: https://en.wikipedia.org/wiki/ANSI_escape_code#Colors
	// ansi code `30` is the one that matches the survey selection
	return ansi.Color(fmt.Sprintf(link, a...), "30")
}

// withHighLightFormat creates string with highlight-looking color
func withHighLightFormat(text string, a ...interface{}) string {
	return color.CyanString(text, a...)
}

// printWithStyling prints text to stdout and handles Windows terminals to support
// escape chars from the text for adding style (color, font, etc)
func printWithStyling(text string, a ...interface{}) {
	colorTerminal := color.New()
	colorTerminal.Printf(text, a...)
}
