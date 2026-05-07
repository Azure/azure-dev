// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azureutil

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/require"
)

// errAccountManager wraps MockAccountManager to inject a GetLocations error.
type errAccountManager struct {
	*mockaccount.MockAccountManager
	err error
}

func (e *errAccountManager) GetLocations(ctx context.Context, subscriptionId string) ([]account.Location, error) {
	return nil, e.err
}

func sampleLocations() []account.Location {
	return []account.Location{
		{Name: "eastus", DisplayName: "East US", RegionalDisplayName: "(US) East US"},
		{Name: "westus", DisplayName: "West US", RegionalDisplayName: "(US) West US"},
		{Name: "centralusstg", DisplayName: "Central US STG", RegionalDisplayName: "(US) Central US STG"},
		{Name: "westeurope", DisplayName: "West Europe", RegionalDisplayName: "(Europe) West Europe"},
	}
}

func TestPromptLocationWithFilter_FiltersStgAndSortsAndReturnsSelection(t *testing.T) {
	t.Setenv(environment.LocationEnvVarName, "")

	mgr := &mockaccount.MockAccountManager{Locations: sampleLocations()}
	console := mockinput.NewMockConsole()

	var seenOptions []string
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Pick a location"
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		seenOptions = options.Options
		// select the second option (sorted: Europe/West Europe then US/East US, US/West US)
		return 0, nil
	})

	selected, err := PromptLocationWithFilter(
		t.Context(), "sub-id", "Pick a location", "", console, mgr, nil, nil,
	)
	require.NoError(t, err)
	// STG entry must be filtered out.
	require.Len(t, seenOptions, 3)
	for _, opt := range seenOptions {
		require.NotContains(t, opt, "STG")
	}
	// First alphabetically (lowercased RegionalDisplayName) should be "(Europe) West Europe".
	require.Contains(t, seenOptions[0], "West Europe")
	require.Equal(t, "westeurope", selected)
}

func TestPromptLocationWithFilter_ShouldDisplayFilter(t *testing.T) {
	t.Setenv(environment.LocationEnvVarName, "")

	mgr := &mockaccount.MockAccountManager{Locations: sampleLocations()}
	console := mockinput.NewMockConsole()

	var captured []string
	console.WhenSelect(func(options input.ConsoleOptions) bool { return true }).
		RespondFn(func(options input.ConsoleOptions) (any, error) {
			captured = options.Options
			return 0, nil
		})

	shouldDisplay := func(l account.Location) bool {
		return strings.HasPrefix(l.Name, "east")
	}

	selected, err := PromptLocationWithFilter(
		t.Context(), "sub", "m", "h", console, mgr, shouldDisplay, nil,
	)
	require.NoError(t, err)
	require.Len(t, captured, 1)
	require.Equal(t, "eastus", selected)
}

func TestPromptLocationWithFilter_DefaultFromEnvVar(t *testing.T) {
	t.Setenv(environment.LocationEnvVarName, "westus")

	mgr := &mockaccount.MockAccountManager{
		DefaultLocation: "eastus", // should be overridden by env var
		Locations:       sampleLocations(),
	}
	console := mockinput.NewMockConsole()

	var defaultOption any
	console.WhenSelect(func(options input.ConsoleOptions) bool { return true }).
		RespondFn(func(options input.ConsoleOptions) (any, error) {
			defaultOption = options.DefaultValue
			return 0, nil
		})

	_, err := PromptLocationWithFilter(t.Context(), "sub", "m", "h", console, mgr, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, defaultOption)
	require.Contains(t, defaultOption.(string), "West US")
	require.Contains(t, defaultOption.(string), "westus")
}

func TestPromptLocationWithFilter_DefaultFromParameter(t *testing.T) {
	t.Setenv(environment.LocationEnvVarName, "")

	mgr := &mockaccount.MockAccountManager{Locations: sampleLocations()}
	console := mockinput.NewMockConsole()

	var defaultOption any
	console.WhenSelect(func(options input.ConsoleOptions) bool { return true }).
		RespondFn(func(options input.ConsoleOptions) (any, error) {
			defaultOption = options.DefaultValue
			return 0, nil
		})

	def := "East US" // match on DisplayName (case-insensitive)
	_, err := PromptLocationWithFilter(t.Context(), "sub", "m", "h", console, mgr, nil, &def)
	require.NoError(t, err)
	require.NotNil(t, defaultOption)
	require.Contains(t, defaultOption.(string), "eastus")
}

func TestPromptLocationWithFilter_DefaultFromAccountManager(t *testing.T) {
	t.Setenv(environment.LocationEnvVarName, "")

	mgr := &mockaccount.MockAccountManager{
		DefaultLocation: "westeurope",
		Locations:       sampleLocations(),
	}
	console := mockinput.NewMockConsole()

	var defaultOption any
	console.WhenSelect(func(options input.ConsoleOptions) bool { return true }).
		RespondFn(func(options input.ConsoleOptions) (any, error) {
			defaultOption = options.DefaultValue
			return 0, nil
		})

	_, err := PromptLocationWithFilter(t.Context(), "sub", "m", "h", console, mgr, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, defaultOption)
	require.Contains(t, defaultOption.(string), "westeurope")
}

func TestPromptLocationWithFilter_GetLocationsError(t *testing.T) {
	t.Setenv(environment.LocationEnvVarName, "")

	boom := errors.New("boom")
	mgr := &errAccountManager{MockAccountManager: &mockaccount.MockAccountManager{}, err: boom}
	console := mockinput.NewMockConsole()

	selected, err := PromptLocationWithFilter(t.Context(), "sub", "m", "h", console, mgr, nil, nil)
	require.Error(t, err)
	require.Empty(t, selected)
	require.ErrorIs(t, err, boom)
	require.ErrorContains(t, err, "listing locations")
}

func TestPromptLocationWithFilter_SelectError(t *testing.T) {
	t.Setenv(environment.LocationEnvVarName, "")

	mgr := &mockaccount.MockAccountManager{Locations: sampleLocations()}
	console := mockinput.NewMockConsole()

	selectErr := errors.New("user cancelled")
	console.WhenSelect(func(options input.ConsoleOptions) bool { return true }).
		RespondFn(func(options input.ConsoleOptions) (any, error) {
			return 0, selectErr
		})

	selected, err := PromptLocationWithFilter(t.Context(), "sub", "m", "h", console, mgr, nil, nil)
	require.Error(t, err)
	require.Empty(t, selected)
	require.ErrorIs(t, err, selectErr)
	require.ErrorContains(t, err, "prompting for location")
}
