// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azureutil

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

type Locs []azcli.AzCliLocation

func (s Locs) Len() int { return len(s) }
func (s Locs) Less(i, j int) bool {
	return strings.Compare(strings.ToLower(s[i].RegionalDisplayName), strings.ToLower(s[j].RegionalDisplayName)) < 0
}
func (s Locs) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

// PromptLocation asks the user to select a location from a list of supported azure location
func PromptLocation(
	ctx context.Context, env *environment.Environment, message string, help string, console input.Console, azCli azcli.AzCli,
) (string, error) {
	return PromptLocationWithFilter(ctx, env, message, help, console, azCli, func(acl azcli.AzCliLocation) bool {
		return true
	})
}

func PromptLocationWithFilter(
	ctx context.Context,
	env *environment.Environment,
	message string,
	help string,
	console input.Console,
	azCli azcli.AzCli,
	filter func(azcli.AzCliLocation) bool,
) (string, error) {
	accountManager, err := account.NewManager(config.NewManager(), azCli)
	if err != nil {
		return "", fmt.Errorf("failed creating account manager: %w", err)
	}

	allLocations, err := accountManager.GetLocations(ctx, env.GetSubscriptionId())
	if err != nil {
		return "", fmt.Errorf("listing locations: %w", err)
	}

	locations := make([]azcli.AzCliLocation, 0, len(allLocations))
	for _, location := range allLocations {
		if filter(location) {
			locations = append(locations, location)
		}
	}

	sort.Sort(Locs(locations))

	// Allow the environment variable `AZURE_LOCATION` to control the default value for the location
	// selection.
	defaultLocation := os.Getenv(environment.LocationEnvVarName)

	// If no location is set in the process environment, see what the azd config default is.
	if defaultLocation == "" {
		defaultConfig, err := accountManager.GetAccountDefaults(ctx)
		if err != nil {
			return "", fmt.Errorf("failed retrieving azd defaults. %w", err)
		}

		defaultLocation = defaultConfig.DefaultLocation.Name
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
		Help:         help,
		Options:      locationOptions,
		DefaultValue: defaultOption,
	})

	if err != nil {
		return "", fmt.Errorf("prompting for location: %w", err)
	}

	return locations[selectedIndex].Name, nil
}
