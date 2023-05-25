// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"fmt"
	"io"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/azureutil"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

// Manages the orchestration of infrastructure provisioning
type Manager struct {
	azCli              azcli.AzCli
	env                *environment.Environment
	provider           Provider
	writer             io.Writer
	console            input.Console
	accountManager     account.Manager
	userProfileService *azcli.UserProfileService
	subResolver        account.SubscriptionTenantResolver
	interactive        bool
}

// Prepares for an infrastructure provision operation
func (m *Manager) Plan(ctx context.Context) (*DeploymentPlan, error) {
	deploymentPlan, err := m.plan(ctx)
	if err != nil {
		return nil, err
	}

	return deploymentPlan, nil
}

// Gets the latest deployment details for the specified scope
func (m *Manager) State(ctx context.Context) (*StateResult, error) {
	result, err := m.provider.State(ctx)
	if err != nil {
		return nil, fmt.Errorf("error retrieving state: %w", err)
	}

	return result, nil
}

// Deploys the Azure infrastructure for the specified project
func (m *Manager) Deploy(ctx context.Context, plan *DeploymentPlan) (*DeployResult, error) {
	// Apply the infrastructure deployment
	deployResult, err := m.deploy(ctx, plan)
	if err != nil {
		return nil, err
	}

	if err := UpdateEnvironment(m.env, deployResult.Deployment.Outputs); err != nil {
		return nil, fmt.Errorf("updating environment with deployment outputs: %w", err)
	}

	return deployResult, nil
}

// Destroys the Azure infrastructure for the specified project
func (m *Manager) Destroy(ctx context.Context, options DestroyOptions) (*DestroyResult, error) {
	// Call provisioning provider to destroy the infrastructure
	destroyResult, err := m.destroy(ctx, options)
	if err != nil {
		return nil, err
	}

	// Remove any outputs from the template from the environment since destroying the infrastructure
	// invalidated them all.
	for outputName := range destroyResult.Outputs {
		m.env.DotenvDelete(outputName)
	}

	// Update environment files to remove invalid infrastructure parameters
	if err := m.env.Save(); err != nil {
		return nil, fmt.Errorf("saving environment: %w", err)
	}

	return destroyResult, nil
}

// Plans the infrastructure provisioning and orchestrates interactive terminal operations
func (m *Manager) plan(ctx context.Context) (*DeploymentPlan, error) {
	deploymentPlan, err := m.provider.Plan(ctx)
	if err != nil {
		return nil, fmt.Errorf("planning infrastructure provisioning: %w", err)
	}

	return deploymentPlan, nil
}

// Applies the specified infrastructure provisioning and orchestrates the interactive terminal operations
func (m *Manager) deploy(
	ctx context.Context,
	plan *DeploymentPlan,
) (*DeployResult, error) {
	deployResult, err := m.provider.Deploy(ctx, plan)
	if err != nil {
		return nil, fmt.Errorf("error deploying infrastructure: %w", err)
	}

	return deployResult, nil
}

// Destroys the specified infrastructure provisioning and orchestrates the interactive terminal operations
func (m *Manager) destroy(ctx context.Context, options DestroyOptions) (*DestroyResult, error) {
	result, err := m.provider.Destroy(ctx, options)
	if err != nil {
		return nil, fmt.Errorf("error deleting Azure resources: %w", err)
	}

	return result, nil
}

// Creates a new instance of the Provisioning Manager
func NewManager(
	ctx context.Context,
	env *environment.Environment,
	projectPath string,
	infraOptions Options,
	interactive bool,
	azCli azcli.AzCli,
	console input.Console,
	commandRunner exec.CommandRunner,
	accountManager account.Manager,
	userProfileService *azcli.UserProfileService,
	subResolver account.SubscriptionTenantResolver,
	alphaFeatureManager *alpha.FeatureManager,
) (*Manager, error) {

	principalProvider := &principalIDProvider{
		env:                env,
		userProfileService: userProfileService,
		subResolver:        subResolver,
	}

	m := &Manager{
		azCli:              azCli,
		env:                env,
		writer:             console.GetWriter(),
		console:            console,
		interactive:        interactive,
		accountManager:     accountManager,
		userProfileService: userProfileService,
		subResolver:        subResolver,
	}

	prompters := Prompters{
		Location:                   m.promptLocation,
		Subscription:               m.promptSubscription,
		EnsureSubscriptionLocation: m.ensureSubscriptionLocation,
	}

	infraProvider, err := NewProvider(
		ctx,
		console,
		azCli,
		commandRunner,
		env,
		projectPath,
		infraOptions,
		prompters,
		principalProvider,
		alphaFeatureManager,
	)
	if err != nil {
		return nil, fmt.Errorf("error creating infra provider: %w", err)
	}

	requiredTools := infraProvider.RequiredExternalTools()
	if err := tools.EnsureInstalled(ctx, requiredTools...); err != nil {
		return nil, err
	}

	m.provider = infraProvider
	if err := m.provider.EnsureConfigured(ctx); err != nil {
		return nil, err
	}

	return m, nil
}

func (m *Manager) promptSubscription(ctx context.Context, msg string) (subscriptionId string, err error) {
	return promptSubscription(ctx, msg, m.console, m.env, m.accountManager)
}

func (m *Manager) promptLocation(
	ctx context.Context,
	subId string,
	msg string,
	filter func(loc account.Location) bool,
) (string, error) {
	return promptLocation(ctx, subId, msg, filter, m.console, m.env, m.accountManager)
}

func (m *Manager) ensureSubscriptionLocation(ctx context.Context, env *environment.Environment) error {
	return EnsureEnv(ctx, m.console, m.env, m.accountManager)
}

type CurrentPrincipalIdProvider interface {
	// CurrentPrincipalId returns the object id of the current logged in principal, or an error if it can not be
	// determined.
	CurrentPrincipalId(ctx context.Context) (string, error)
}

type principalIDProvider struct {
	env                *environment.Environment
	userProfileService *azcli.UserProfileService
	subResolver        account.SubscriptionTenantResolver
}

func (p *principalIDProvider) CurrentPrincipalId(ctx context.Context) (string, error) {
	tenantId, err := p.subResolver.LookupTenant(ctx, p.env.GetSubscriptionId())
	if err != nil {
		return "", fmt.Errorf("getting tenant id for subscription %s. Error: %w", p.env.GetSubscriptionId(), err)
	}

	principalId, err := azureutil.GetCurrentPrincipalId(ctx, p.userProfileService, tenantId)
	if err != nil {
		return "", fmt.Errorf("fetching current user information: %w", err)
	}

	return principalId, nil
}
