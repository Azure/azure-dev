// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

var _ azdext.ServiceTargetProvider = (*ResourceServiceTargetProvider)(nil)

// ResourceServiceTargetProvider is a no-op service target shared by the Foundry
// resource hosts that `azd ai agent init` writes as sibling service entries:
// azure.ai.project, azure.ai.connection, and azure.ai.toolbox. The agents
// extension registers all three so `azd up`/`azd deploy` can walk the service
// entries the agent references via uses:, without requiring a separate
// extension per host. The resources themselves are created by Bicep during
// `azd provision` (orchestrated by this extension), so every deploy-time hook
// here intentionally does nothing.
//
// These hosts share one provider type because none of them has deploy-time
// behavior yet. When a host gains real backend functionality it can move to its
// own dedicated extension, at which point that extension registers the host
// instead of this one.
type ResourceServiceTargetProvider struct {
	azdClient     *azdext.AzdClient
	serviceConfig *azdext.ServiceConfig
}

// NewResourceServiceTargetProvider creates a no-op service target for a Foundry
// resource host.
func NewResourceServiceTargetProvider(azdClient *azdext.AzdClient) azdext.ServiceTargetProvider {
	return &ResourceServiceTargetProvider{azdClient: azdClient}
}

// Initialize stores the service configuration; no other setup is required.
func (p *ResourceServiceTargetProvider) Initialize(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
) error {
	p.serviceConfig = serviceConfig
	return nil
}

// Endpoints returns no endpoints; Foundry resource services do not expose any.
func (p *ResourceServiceTargetProvider) Endpoints(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	targetResource *azdext.TargetResource,
) ([]string, error) {
	return nil, nil
}

// GetTargetResource resolves the target resource. It delegates to azd's default
// resolver and falls back to a minimal target so the deploy pipeline can proceed.
func (p *ResourceServiceTargetProvider) GetTargetResource(
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

// Package is a no-op; there is nothing to build or stage for a resource service.
func (p *ResourceServiceTargetProvider) Package(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	progress azdext.ProgressReporter,
) (*azdext.ServicePackageResult, error) {
	return &azdext.ServicePackageResult{}, nil
}

// Publish is a no-op; resource services have no artifacts to publish.
func (p *ResourceServiceTargetProvider) Publish(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	targetResource *azdext.TargetResource,
	publishOptions *azdext.PublishOptions,
	progress azdext.ProgressReporter,
) (*azdext.ServicePublishResult, error) {
	return &azdext.ServicePublishResult{}, nil
}

// Deploy is a no-op; the resources are created at provision time by Bicep.
func (p *ResourceServiceTargetProvider) Deploy(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	targetResource *azdext.TargetResource,
	progress azdext.ProgressReporter,
) (*azdext.ServiceDeployResult, error) {
	return &azdext.ServiceDeployResult{}, nil
}
