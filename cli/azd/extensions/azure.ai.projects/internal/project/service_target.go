// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package project implements the azd service target for the azure.ai.project host.
package project

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// ProjectHost is the azd service host served by this extension. It must match
// the provider name declared in extension.yaml.
const ProjectHost = "azure.ai.project"

var _ azdext.ServiceTargetProvider = (*ProjectServiceTargetProvider)(nil)

// ProjectServiceTargetProvider is a no-op service target for the
// azure.ai.project host. The Foundry project and its model deployments are
// created by Bicep during `azd provision` (orchestrated by the Foundry agents
// extension), so the deploy-time hooks here intentionally do nothing.
// Registering the host is what lets `azd up`/`azd deploy` succeed for project
// service entries that an agent service references via `uses:`.
type ProjectServiceTargetProvider struct {
	azdClient     *azdext.AzdClient
	serviceConfig *azdext.ServiceConfig
}

// NewProjectServiceTargetProvider creates a no-op project service target.
func NewProjectServiceTargetProvider(azdClient *azdext.AzdClient) azdext.ServiceTargetProvider {
	return &ProjectServiceTargetProvider{azdClient: azdClient}
}

// Initialize stores the service configuration; no other setup is required.
func (p *ProjectServiceTargetProvider) Initialize(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
) error {
	p.serviceConfig = serviceConfig
	return nil
}

// Endpoints returns no endpoints; the project service does not expose any.
func (p *ProjectServiceTargetProvider) Endpoints(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	targetResource *azdext.TargetResource,
) ([]string, error) {
	return nil, nil
}

// GetTargetResource resolves the target resource. It delegates to azd's default
// resolver and falls back to a minimal target so the deploy pipeline can proceed.
func (p *ProjectServiceTargetProvider) GetTargetResource(
	ctx context.Context,
	subscriptionId string,
	serviceConfig *azdext.ServiceConfig,
	defaultResolver func() (*azdext.TargetResource, error),
) (*azdext.TargetResource, error) {
	if defaultResolver != nil {
		if target, err := defaultResolver(); err == nil && target != nil {
			return target, nil
		}
	}

	// Deploy is a no-op and does not use the target; azd only requires a
	// non-nil target to continue the deploy pipeline.
	return &azdext.TargetResource{SubscriptionId: subscriptionId}, nil
}

// Package is a no-op; there is nothing to build or stage for the project service.
func (p *ProjectServiceTargetProvider) Package(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	progress azdext.ProgressReporter,
) (*azdext.ServicePackageResult, error) {
	return &azdext.ServicePackageResult{}, nil
}

// Publish is a no-op; the project service has no artifacts to publish.
func (p *ProjectServiceTargetProvider) Publish(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	targetResource *azdext.TargetResource,
	publishOptions *azdext.PublishOptions,
	progress azdext.ProgressReporter,
) (*azdext.ServicePublishResult, error) {
	return &azdext.ServicePublishResult{}, nil
}

// Deploy is a no-op; the project and its deployments are created at provision time by Bicep.
func (p *ProjectServiceTargetProvider) Deploy(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	targetResource *azdext.TargetResource,
	progress azdext.ProgressReporter,
) (*azdext.ServiceDeployResult, error) {
	return &azdext.ServiceDeployResult{}, nil
}
