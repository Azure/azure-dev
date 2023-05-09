// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

// LocationPromptFunc prompts the user for an Azure location, from the set of locations that the given subscription has
// access to. This list may be further restricted by [shouldDisplay]. The return value is the short name of the location
// (e.g. eastus2).
type LocationPromptFunc func(
	ctx context.Context,
	subscriptionId string,
	msg string,
	shouldDisplay func(loc account.Location) bool,
) (location string, err error)

// SubscriptionPromptFunc prompts the user for an Azure subscription, from the set of subscriptions the user has access to.
type SubscriptionPromptFunc func(ctx context.Context, msg string) (subscriptionId string, err error)

// Ensures subscription and location are available on the environment by prompting if necessary
type EnsureSubscriptionLocationPromptFunc func(ctx context.Context, env *environment.Environment) error

// Prompters contains prompt functions that can be used for general scenarios.
type Prompters struct {
	Location                   LocationPromptFunc
	Subscription               SubscriptionPromptFunc
	EnsureSubscriptionLocation EnsureSubscriptionLocationPromptFunc
}

type ProviderKind string

type NewProviderFn func(
	ctx context.Context,
	env *environment.Environment,
	projectPath string,
	infraOptions Options,
	console input.Console,
	cli azcli.AzCli,
	commandRunner exec.CommandRunner,
	prompters Prompters,
	principalProvider CurrentPrincipalIdProvider,
) (Provider, error)

var (
	providers map[ProviderKind]NewProviderFn = make(map[ProviderKind]NewProviderFn)
)

const (
	Bicep     ProviderKind = "bicep"
	Arm       ProviderKind = "arm"
	Terraform ProviderKind = "terraform"
	Pulumi    ProviderKind = "pulumi"
	Test      ProviderKind = "test"
)

type Options struct {
	Provider ProviderKind `yaml:"provider"`
	Path     string       `yaml:"path"`
	Module   string       `yaml:"module"`
}

type DeploymentPlan struct {
	Deployment Deployment

	// Additional information about deployment, provider-specific.
	Details interface{}
}

type DeploymentPlanningProgress struct {
	Message   string
	Timestamp time.Time
}

type DeployResult struct {
	Deployment *Deployment
}

type DestroyResult struct {
	Resources []azcli.AzCliResource
	Outputs   map[string]OutputParameter
}

type DeployProgress struct {
	Message   string
	Timestamp time.Time
}

type DestroyProgress struct {
	Message   string
	Timestamp time.Time
}

type StateResult struct {
	State *State
}

type StateProgress struct {
	Message   string
	Timestamp time.Time
}

type Provider interface {
	Name() string
	RequiredExternalTools() []tools.ExternalTool
	// EnsureConfigured ensures that any required configuration for the provider has been loaded, prompting the user for
	// any missing values.
	//
	// EnsureConfigured is called when a provider is constructed.
	EnsureConfigured(ctx context.Context) error
	// State gets the current state of the infrastructure, this contains both the provisioned resources and any outputs from
	// the module.
	State(ctx context.Context) *async.InteractiveTaskWithProgress[*StateResult, *StateProgress]
	Plan(ctx context.Context) *async.InteractiveTaskWithProgress[*DeploymentPlan, *DeploymentPlanningProgress]
	Deploy(
		ctx context.Context,
		plan *DeploymentPlan,
	) *async.InteractiveTaskWithProgress[*DeployResult, *DeployProgress]
	Destroy(
		ctx context.Context,
		deployment *Deployment,
		options DestroyOptions,
	) *async.InteractiveTaskWithProgress[*DestroyResult, *DestroyProgress]
}

// Registers a provider creation function for the specified provider kind
func RegisterProvider(kind ProviderKind, newFn NewProviderFn) error {
	if newFn == nil {
		return errors.New("NewProviderFn is required")
	}

	providers[kind] = newFn
	return nil
}

func NewProvider(
	ctx context.Context,
	console input.Console,
	azCli azcli.AzCli,
	commandRunner exec.CommandRunner,
	env *environment.Environment,
	projectPath string,
	infraOptions Options,
	prompters Prompters,
	principalProvider CurrentPrincipalIdProvider,
	alphaFeatureManager *alpha.FeatureManager,
) (Provider, error) {
	var provider Provider

	if infraOptions.Provider == "" {
		infraOptions.Provider = Bicep
	}

	if alphaFeatureId, isAlphaFeature := alpha.IsFeatureKey(string(infraOptions.Provider)); isAlphaFeature {
		if !alphaFeatureManager.IsEnabled(alphaFeatureId) {
			return nil, fmt.Errorf("provider '%s' is alpha feature and it is not enabled. Run `%s` to enable it.",
				infraOptions.Provider,
				alpha.GetEnableCommand(alphaFeatureId),
			)
		}
		console.MessageUxItem(ctx, alpha.WarningMessage(alphaFeatureId))
	}

	newProviderFn, ok := providers[infraOptions.Provider]

	if !ok {
		return nil, fmt.Errorf("provider '%s' is not supported", infraOptions.Provider)
	}

	provider, err := newProviderFn(
		ctx, env, projectPath, infraOptions, console, azCli, commandRunner, prompters, principalProvider)
	if err != nil {
		return nil, fmt.Errorf("error creating provider for type '%s' : %w", infraOptions.Provider, err)
	}

	return provider, nil
}
