// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package local_preflight

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
)

// SubscriptionCheck verifies that the required Azure targeting environment variables
// (AZURE_SUBSCRIPTION_ID and AZURE_LOCATION) are present in the current environment.
type SubscriptionCheck struct {
	env *environment.Environment
}

// NewSubscriptionCheck creates a new SubscriptionCheck backed by the provided environment.
func NewSubscriptionCheck(env *environment.Environment) *SubscriptionCheck {
	return &SubscriptionCheck{env: env}
}

// Name returns the display name of the check.
func (c *SubscriptionCheck) Name() string {
	return "Subscription & Location"
}

// Run validates that AZURE_SUBSCRIPTION_ID and AZURE_LOCATION are configured.
func (c *SubscriptionCheck) Run(_ context.Context) Result {
	subscriptionId := c.env.GetSubscriptionId()
	location := c.env.GetLocation()

	switch {
	case subscriptionId == "" && location == "":
		return Result{
			Status: StatusFail,
			Message: fmt.Sprintf(
				"%s and %s are not set in the current environment.",
				environment.SubscriptionIdEnvVarName,
				environment.LocationEnvVarName,
			),
			Suggestion: "Run 'azd env select' or 'azd env new' to configure a target environment, " +
				"or set the environment variables directly.",
		}
	case subscriptionId == "":
		return Result{
			Status:  StatusFail,
			Message: fmt.Sprintf("%s is not set in the current environment.", environment.SubscriptionIdEnvVarName),
			Suggestion: "Run 'azd env select' or set the AZURE_SUBSCRIPTION_ID environment variable " +
				"to specify the target Azure subscription.",
		}
	case location == "":
		return Result{
			Status:  StatusFail,
			Message: fmt.Sprintf("%s is not set in the current environment.", environment.LocationEnvVarName),
			Suggestion: "Run 'azd env select' or set the AZURE_LOCATION environment variable " +
				"to specify a target Azure region.",
		}
	default:
		return Result{
			Status:  StatusPass,
			Message: fmt.Sprintf("Targeting subscription %q in region %q.", subscriptionId, location),
		}
	}
}
