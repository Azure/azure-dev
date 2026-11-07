// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package prompt

import (
	"context"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/inputs"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
)

// ResolveLocationInput tries input providers before the caller's existing location fallback.
// Returned values must identify a supported subscription location and satisfy the caller's filter.
func ResolveLocationInput(
	ctx context.Context,
	locator ioc.ServiceLocator,
	definition *v1beta.InputDefinition,
	subscriptionID string,
	filter LocationFilterPredicate,
	validate func([]inputs.Option) ([]inputs.Option, error),
) (*string, error) {
	var locations []account.Location
	loaded := false
	settings := inputs.ResolveOptions{FilterOptions: validate}
	if definition.EnvironmentVariable != "" {
		settings.Sources = []input.InputSource{{
			Kind: input.InputSourceEnvironment, Name: definition.EnvironmentVariable,
		}}
	}
	settings.Filter = func(value string) (bool, error) {
		if !loaded {
			var manager account.Manager
			if err := locator.Resolve(&manager); err != nil {
				return false, fmt.Errorf("resolving account manager for input validation: %w", err)
			}
			var err error
			locations, err = manager.GetLocations(ctx, subscriptionID)
			if err != nil {
				return false, fmt.Errorf("validating input provider location: %w", err)
			}
			loaded = true
		}
		for _, location := range locations {
			if location.Name == value && !strings.Contains(location.RegionalDisplayName, "STG") &&
				(filter == nil || filter(location)) {
				return true, nil
			}
		}
		return false, nil
	}
	return inputs.Resolve(ctx, locator, definition, settings)
}
