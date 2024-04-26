// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azureutil

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
)

// PromptLocation asks the user to select a location from a list of supported azure locations for a given subscription.
// shouldDisplay, when non-nil, filters the location being displayed.
func PromptLocationWithFilter(
	ctx context.Context,
	subscriptionId string,
	message string,
	help string,
	console input.Console,
	accountManager account.Manager,
	shouldDisplay func(account.Location) bool,
) (string, error) {
	allLocations, err := accountManager.GetLocations(ctx, subscriptionId)
	if err != nil {
		return "", fmt.Errorf("listing locations: %w", err)
	}

	locations := make([]account.Location, 0, len(allLocations))

	for _, location := range allLocations {
		if shouldDisplay == nil || shouldDisplay(location) {
			locations = append(locations, location)
		}
	}

	slices.SortFunc(locations, func(a, b account.Location) int {
		return strings.Compare(
			strings.ToLower(a.RegionalDisplayName), strings.ToLower(b.RegionalDisplayName))
	})

	// Allow the environment variable `AZURE_LOCATION` to control the default value for the location
	// selection.
	defaultLocation := os.Getenv(environment.LocationEnvVarName)

	// If no location is set in the process environment, see what the azd config default is.
	if defaultLocation == "" {
		defaultLocation = accountManager.GetDefaultLocationName(ctx)
	}

	var defaultOption any

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
