// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package pipeline

// Functions copied from ** cmd-package **
// Duplicating until refactoring code to move util-functions from cmd (frontend)
// to some package in the backend (pkg/ folder)

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azureutil"
	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

func ensureProject(path string) error {
	_, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return errors.New("no project exists; to create a new project, run `azd init`.")
	} else if err != nil {
		return fmt.Errorf("checking for project: %w", err)
	}

	return nil
}

func invalidEnvironmentNameMsg(environmentName string) string {
	return fmt.Sprintf("environment name '%s' is invalid (it should contain only alphanumeric characters and hyphens)\n", environmentName)
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
			fmt.Print(invalidEnvironmentNameMsg(*environmentName))
		}
	}

	return nil
}

const (
	// CodespacesEnvVarName is the name of the env variable set when you're in a Github codespace. It's
	// just set to 'true'.
	codespacesEnvVarName = "CODESPACES"

	// RemoteContainersEnvVarName is the name of the env variable set when you're in a remote container. It's
	// just set to 'true'.
	remoteContainersEnvVarName = "REMOTE_CONTAINERS"
)

// runLogin runs an interactive login. When running in a Codespace or Remote Container, a device code based is
// preformed since the default browser login needs UI. A device code login can be forced with `forceDeviceCode`.
func runLogin(ctx context.Context, forceDeviceCode bool) error {
	azCli := commands.GetAzCliFromContext(ctx)
	useDeviceCode := forceDeviceCode || os.Getenv(codespacesEnvVarName) == "true" || os.Getenv(remoteContainersEnvVarName) == "true"

	return azCli.Login(ctx, useDeviceCode, os.Stdout)
}

func ensureLoggedIn(ctx context.Context) error {
	azCli := commands.GetAzCliFromContext(ctx)
	_, err := azCli.GetAccessToken(ctx)
	if errors.Is(err, azcli.ErrAzCliNotLoggedIn) || errors.Is(err, azcli.ErrAzCliRefreshTokenExpired) {
		if err := runLogin(ctx, false); err != nil {
			return fmt.Errorf("logging in: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("fetching access token: %w", err)
	}

	return nil
}

const (
	manualSubscriptionEntryOption = "Other (enter manually)"
)

func getSubscriptionOptions(ctx context.Context) ([]string, string, error) {
	azCli := commands.GetAzCliFromContext(ctx)
	subscriptionInfos, err := azCli.ListAccounts(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("listing accounts: %w", err)
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

func ensureEnvironmentInitialized(ctx context.Context, envSpec environmentSpec, env *environment.Environment, console input.Console) error {
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

	if !hasLocation && envSpec.location != "" {
		env.SetLocation(envSpec.location)
	} else {
		location, err := console.PromptLocation(ctx, "Please select an Azure location to use:")
		if err != nil {
			return fmt.Errorf("prompting for location: %w", err)
		}
		env.SetLocation(strings.TrimSpace(location))
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
				subscriptionId = subscriptionSelection[len(subscriptionSelection)-len("(00000000-0000-0000-0000-000000000000)")+1 : len(subscriptionSelection)-1]
			}
		}

		env.SetSubscriptionId(strings.TrimSpace(subscriptionId))
	}

	if !hasPrincipalID {
		principalID, err := azureutil.GetCurrentPrincipalId(ctx)
		if err != nil {
			return fmt.Errorf("fetching current user information: %w", err)
		}
		env.SetPrincipalId(principalID)
	}

	if err := env.Save(); err != nil {
		return fmt.Errorf("saving environment: %w", err)
	}

	return nil
}

type environmentSpec struct {
	environmentName string
	subscription    string
	location        string
}

func loadOrInitEnvironment(ctx context.Context, environmentName *string, azdCtx *azdcontext.AzdContext, console input.Console) (environment.Environment, error) {
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
			env, err := environment.GetEnvironment(azdCtx, *environmentName)
			switch {
			case errors.Is(err, os.ErrNotExist):
				msg := fmt.Sprintf("Environment '%s' does not exist, would you like to create it?", *environmentName)
				shouldCreate, promptErr := console.Confirm(ctx, input.ConsoleOptions{
					Message:      msg,
					DefaultValue: true,
				})
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

		if err := ensureValidEnvironmentName(ctx, environmentName, console); err != nil {
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

	if err := ensureEnvironmentInitialized(ctx, environmentSpec{environmentName: *environmentName}, &env, console); err != nil {
		return environment.Environment{}, fmt.Errorf("initializing environment: %w", err)
	}

	if isNew {
		if err := azdCtx.SetDefaultEnvironmentName(*environmentName); err != nil {
			return environment.Environment{}, fmt.Errorf("saving default environment name: %w", err)
		}
	}

	return env, nil
}
