// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package local_preflight_test

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/local_preflight"
	"github.com/stretchr/testify/assert"
)

func TestSubscriptionCheck_BothSet(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{
		environment.SubscriptionIdEnvVarName: "sub-id",
		environment.LocationEnvVarName:       "eastus",
	})
	check := local_preflight.NewSubscriptionCheck(env)
	result := check.Run(context.Background())
	assert.Equal(t, local_preflight.StatusPass, result.Status)
	assert.Contains(t, result.Message, "sub-id")
	assert.Contains(t, result.Message, "eastus")
}

func TestSubscriptionCheck_MissingSubscription(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{
		environment.LocationEnvVarName: "eastus",
	})
	check := local_preflight.NewSubscriptionCheck(env)
	result := check.Run(context.Background())
	assert.Equal(t, local_preflight.StatusFail, result.Status)
	assert.Contains(t, result.Message, environment.SubscriptionIdEnvVarName)
	assert.NotEmpty(t, result.Suggestion)
}

func TestSubscriptionCheck_MissingLocation(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{
		environment.SubscriptionIdEnvVarName: "sub-id",
	})
	check := local_preflight.NewSubscriptionCheck(env)
	result := check.Run(context.Background())
	assert.Equal(t, local_preflight.StatusFail, result.Status)
	assert.Contains(t, result.Message, environment.LocationEnvVarName)
	assert.NotEmpty(t, result.Suggestion)
}

func TestSubscriptionCheck_BothMissing(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{})
	check := local_preflight.NewSubscriptionCheck(env)
	result := check.Run(context.Background())
	assert.Equal(t, local_preflight.StatusFail, result.Status)
	assert.Contains(t, result.Message, environment.SubscriptionIdEnvVarName)
	assert.Contains(t, result.Message, environment.LocationEnvVarName)
	assert.NotEmpty(t, result.Suggestion)
}

func TestSubscriptionCheck_Name(t *testing.T) {
	env := environment.NewWithValues("test", map[string]string{})
	check := local_preflight.NewSubscriptionCheck(env)
	assert.Equal(t, "Subscription & Location", check.Name())
}
