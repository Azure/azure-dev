// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azureutil"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

type Asker func(p survey.Prompt, response interface{}) error

const (
	manualSubscriptionEntryOption = "Other (enter manually)"
)

func invalidEnvironmentNameMsg(environmentName string) string {
	return fmt.Sprintf(
		"environment name '%s' is invalid (it should contain only alphanumeric characters and hyphens)\n",
		environmentName,
	)
}

// ensureValidEnvironmentName ensures the environment name is valid, if it is not, an error is printed
// and the user is prompted for a new name.
func ensureValidEnvironmentName(ctx context.Context, environmentName *string, console input.Console) error {
	for !environment.IsValidEnvironmentName(*environmentName) {
		userInput, err := console.Prompt(ctx, input.ConsoleOptions{
			Message: "Please enter a new environment name:",
		})

		if err != nil {
			return fmt.Errorf("reading environment name: %w", err)
		}

		*environmentName = userInput

		if !environment.IsValidEnvironmentName(*environmentName) {
			fmt.Fprint(console.Handles().Stdout, invalidEnvironmentNameMsg(*environmentName))
		}
	}

	return nil
}

type environmentSpec struct {
	environmentName string
	subscription    string
	location        string
}

// createEnvironment creates a new named environment. If an environment with this name already
// exists, and error is return.
func createAndInitEnvironment(
	ctx context.Context,
	envSpec *environmentSpec,
	azdCtx *azdcontext.AzdContext,
	console input.Console,
) (*environment.Environment, context.Context, error) {
	if envSpec.environmentName != "" && !environment.IsValidEnvironmentName(envSpec.environmentName) {
		errMsg := invalidEnvironmentNameMsg(envSpec.environmentName)
		fmt.Fprint(console.Handles().Stdout, errMsg)
		return nil, nil, fmt.Errorf(errMsg)
	}

	if err := ensureValidEnvironmentName(ctx, &envSpec.environmentName, console); err != nil {
		return nil, nil, err
	}

	// Ensure the environment does not already exist:
	env, err := environment.GetEnvironment(azdCtx, envSpec.environmentName)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, nil, fmt.Errorf("checking for existing environment: %w", err)
	case err == nil:
		return nil, nil, fmt.Errorf("environment '%s' already exists", envSpec.environmentName)
	}

	if err := ensureEnvironmentInitialized(ctx, *envSpec, env, console); err != nil {
		return nil, nil, fmt.Errorf("initializing environment: %w", err)
	}

	return env, telemetry.ContextWithEnvironment(ctx, env), nil
}

func loadOrInitEnvironment(
	ctx context.Context,
	environmentName *string,
	azdCtx *azdcontext.AzdContext,
	console input.Console,
) (*environment.Environment, context.Context, error) {
	loadOrCreateEnvironment := func() (*environment.Environment, bool, error) {
		// If there's a default environment, use that
		if *environmentName == "" {
			var err error
			*environmentName, err = azdCtx.GetDefaultEnvironmentName()
			if err != nil {
				return nil, false, fmt.Errorf("getting default environment: %w", err)
			}
		}

		if *environmentName != "" {
			env, err := environment.GetEnvironment(azdCtx, *environmentName)
			switch {
			case errors.Is(err, os.ErrNotExist):
				msg := fmt.Sprintf("Environment '%s' does not exist, would you like to create it?", *environmentName)
				shouldCreate, promptErr := console.Confirm(ctx, input.ConsoleOptions{
					Message:      msg,
					DefaultValue: true,
				})
				if promptErr != nil {
					return nil, false, fmt.Errorf("prompting to create environment '%s': %w", *environmentName, promptErr)
				}
				if !shouldCreate {
					return nil, false, fmt.Errorf("environment '%s' not found: %w", *environmentName, err)
				}
			case err != nil:
				return nil, false, fmt.Errorf("loading environment '%s': %w", *environmentName, err)
			case err == nil:
				return env, false, nil
			}
		}

		// Two cases if we get to here:
		// - The user has not specified an environment name (and there was no default environment set)
		// - The user has specified an environment name, but the named environment didn't exist and they told us they would
		//   like us to create it.
		if *environmentName != "" && !environment.IsValidEnvironmentName(*environmentName) {
			fmt.Fprintf(
				console.Handles().Stdout,
				"environment name '%s' is invalid (it should contain only alphanumeric characters and hyphens)\n",
				*environmentName)
			return nil, false, fmt.Errorf(
				"environment name '%s' is invalid (it should contain only alphanumeric characters and hyphens)",
				*environmentName)
		}

		if err := ensureValidEnvironmentName(ctx, environmentName, console); err != nil {
			return nil, false, err
		}

		return environment.EmptyWithFile(azdCtx.GetEnvironmentFilePath(*environmentName)), true, nil
	}

	env, isNew, err := loadOrCreateEnvironment()
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, nil, fmt.Errorf("environment %s does not exist", *environmentName)
	case err != nil:
		return nil, nil, err
	}

	if err := ensureEnvironmentInitialized(
		ctx,
		environmentSpec{environmentName: *environmentName},
		env,
		console); err != nil {
		return nil, nil, fmt.Errorf("initializing environment: %w", err)
	}

	if isNew {
		if err := azdCtx.SetDefaultEnvironmentName(*environmentName); err != nil {
			return nil, nil, fmt.Errorf("saving default environment name: %w", err)
		}
	}

	return env, telemetry.ContextWithEnvironment(ctx, env), nil
}

// ensureEnvironmentInitialized ensures the environment is initialized, i.e. it contains values for `AZURE_ENV_NAME`,
// `AZURE_LOCATION`, `AZURE_SUBSCRIPTION_ID` and `AZURE_PRINCIPAL_ID`.
// It will use the values from the "environment spec" passed in, and prompt for any missing values as necessary.
// Existing environment value are left unchanged, even if the "spec" has different values.
func ensureEnvironmentInitialized(
	ctx context.Context,
	envSpec environmentSpec,
	env *environment.Environment,
	console input.Console,
) error {
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

	if !hasEnvName && envSpec.environmentName != "" {
		env.SetEnvName(envSpec.environmentName)
	}

	needAzureInteraction := !hasSubID || !hasLocation || !hasPrincipalID
	if needAzureInteraction {
		if err := ensureLoggedIn(ctx); err != nil {
			return fmt.Errorf("logging in: %w", err)
		}
	}

	if !hasSubID && envSpec.subscription != "" {
		env.SetSubscriptionId(envSpec.subscription)
	} else {
		subscriptionOptions, defaultSubscription, err := getSubscriptionOptions(ctx)
		if err != nil {
			return err
		}

		var subscriptionId = ""
		for subscriptionId == "" {
			subscriptionSelectionIndex, err := console.Select(ctx, input.ConsoleOptions{
				Message:      "Please select an Azure Subscription to use:",
				Options:      subscriptionOptions,
				DefaultValue: defaultSubscription,
			})

			if err != nil {
				return fmt.Errorf("reading subscription id: %w", err)
			}

			subscriptionSelection := subscriptionOptions[subscriptionSelectionIndex]

			if subscriptionSelection == manualSubscriptionEntryOption {
				subscriptionId, err = console.Prompt(ctx, input.ConsoleOptions{
					Message: "Enter an Azure Subscription to use:",
				})

				if err != nil {
					return fmt.Errorf("reading subscription id: %w", err)
				}
			} else {
				subscriptionId = subscriptionSelection[len(subscriptionSelection)-
					len("(00000000-0000-0000-0000-000000000000)")+1 : len(subscriptionSelection)-1]
			}
		}

		env.SetSubscriptionId(strings.TrimSpace(subscriptionId))
	}

	if !hasLocation && envSpec.location != "" {
		env.SetLocation(envSpec.location)
	} else {
		location, err := azureutil.PromptLocation(ctx, env, "Please select an Azure location to use:")
		if err != nil {
			return fmt.Errorf("prompting for location: %w", err)
		}
		env.SetLocation(strings.TrimSpace(location))
	}

	if !hasPrincipalID {
		principalID, err := azureutil.GetCurrentPrincipalId(ctx)
		if err != nil {
			return fmt.Errorf("fetching current user information: %w", err)
		}
		env.SetPrincipalId(*principalID)
	}

	if err := env.Save(); err != nil {
		return fmt.Errorf("saving environment: %w", err)
	}

	return nil
}

func getSubscriptionOptions(ctx context.Context) ([]string, string, error) {
	accountManager, err := account.NewManager(config.NewManager(), azcli.GetAzCli(ctx))
	if err != nil {
		return nil, "", fmt.Errorf("failed creating account manager: %w", err)
	}

	subscriptionInfos, err := accountManager.GetSubscriptions(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("listing accounts: %w", err)
	}

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
	var defaultSubscription string = ""

	for index, info := range subscriptionInfos {
		subscriptionOptions[index] = fmt.Sprintf("%2d. %s (%s)", index+1, info.Name, info.Id)

		if info.Id == defaultSubscriptionId {
			defaultSubscription = subscriptionOptions[index]
		}
	}

	subscriptionOptions[len(subscriptionOptions)-1] = manualSubscriptionEntryOption
	return subscriptionOptions, defaultSubscription, nil
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
