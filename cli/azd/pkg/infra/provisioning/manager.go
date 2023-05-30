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
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

// Manages the orchestration of infrastructure provisioning
type Manager struct {
	serviceLocator      ioc.ServiceLocator
	projectPath         string
	options             *Options
	azCli               azcli.AzCli
	env                 *environment.Environment
	provider            Provider
	writer              io.Writer
	console             input.Console
	accountManager      account.Manager
	userProfileService  *azcli.UserProfileService
	subResolver         account.SubscriptionTenantResolver
	alphaFeatureManager *alpha.FeatureManager
}

func (m *Manager) Init(ctx context.Context, projectPath string, options Options) error {
	m.projectPath = projectPath
	m.options = &options

	provider, err := m.newProvider(ctx)
	if err != nil {
		return fmt.Errorf("initializing infrastructure provider: %w", err)
	}

	m.provider = provider
	return m.provider.Init(ctx, projectPath, options)
}

// Prepares for an infrastructure provision operation
func (m *Manager) Plan(ctx context.Context) (*DeploymentPlan, error) {
	deploymentPlan, err := m.provider.Plan(ctx)
	if err != nil {
		return nil, fmt.Errorf("planning infrastructure provisioning: %w", err)
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
	deployResult, err := m.provider.Deploy(ctx, plan)
	if err != nil {
		return nil, fmt.Errorf("error deploying infrastructure: %w", err)
	}

	if err := UpdateEnvironment(m.env, deployResult.Deployment.Outputs); err != nil {
		return nil, fmt.Errorf("updating environment with deployment outputs: %w", err)
	}

	return deployResult, nil
}

// Destroys the Azure infrastructure for the specified project
func (m *Manager) Destroy(ctx context.Context, options DestroyOptions) (*DestroyResult, error) {
	destroyResult, err := m.provider.Destroy(ctx, options)
	if err != nil {
		return nil, fmt.Errorf("error deleting Azure resources: %w", err)
	}

	// Remove any outputs from the template from the environment since destroying the infrastructure
	// invalidated them all.
	for _, key := range destroyResult.InvalidatedEnvKeys {
		m.env.DotenvDelete(key)
	}

	// Update environment files to remove invalid infrastructure parameters
	if err := m.env.Save(); err != nil {
		return nil, fmt.Errorf("saving environment: %w", err)
	}

	return destroyResult, nil
}

// Creates a new instance of the Provisioning Manager
func NewManager(
	serviceLocator ioc.ServiceLocator,
	env *environment.Environment,
	azCli azcli.AzCli,
	console input.Console,
	accountManager account.Manager,
	userProfileService *azcli.UserProfileService,
	subResolver account.SubscriptionTenantResolver,
	alphaFeatureManager *alpha.FeatureManager,
) *Manager {
	return &Manager{
		serviceLocator:      serviceLocator,
		azCli:               azCli,
		env:                 env,
		writer:              console.GetWriter(),
		accountManager:      accountManager,
		userProfileService:  userProfileService,
		subResolver:         subResolver,
		alphaFeatureManager: alphaFeatureManager,
	}
}

func (m *Manager) newProvider(ctx context.Context) (Provider, error) {
	if m.options.Provider == "" {
		m.options.Provider = Bicep
	}

	if alphaFeatureId, isAlphaFeature := alpha.IsFeatureKey(string(m.options.Provider)); isAlphaFeature {
		if !m.alphaFeatureManager.IsEnabled(alphaFeatureId) {
			return nil, fmt.Errorf("provider '%s' is alpha feature and it is not enabled. Run `%s` to enable it.",
				m.options.Provider,
				alpha.GetEnableCommand(alphaFeatureId),
			)
		}

		m.console.WarnForFeature(ctx, alphaFeatureId)
	}

	var provider Provider
	err := m.serviceLocator.ResolveNamed(string(m.options.Provider), &provider)
	if err != nil {
		return nil, fmt.Errorf("resolving provider '%s': %w", m.options.Provider, err)
	}

	return provider, nil
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
