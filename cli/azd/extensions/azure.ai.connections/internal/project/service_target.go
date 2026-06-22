// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package project implements the azd service target for the azure.ai.connection host.
package project

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// ConnectionHost is the azd service host served by this extension. It must
// match the provider name declared in extension.yaml.
const ConnectionHost = "azure.ai.connection"

var _ azdext.ServiceTargetProvider = (*ConnectionServiceTargetProvider)(nil)

// ConnectionServiceTargetProvider is a no-op service target for the
// azure.ai.connection host. Foundry connections are created by Bicep during
// `azd provision` (orchestrated by the Foundry agents extension), so the
// deploy-time hooks here intentionally do nothing. Registering the host is
// what lets `azd up`/`azd deploy` succeed for connection service entries that
// an agent service references via `uses:`.
type ConnectionServiceTargetProvider struct {
	azdClient     *azdext.AzdClient
	serviceConfig *azdext.ServiceConfig
}

// NewConnectionServiceTargetProvider creates a no-op connection service target.
func NewConnectionServiceTargetProvider(azdClient *azdext.AzdClient) azdext.ServiceTargetProvider {
	return &ConnectionServiceTargetProvider{azdClient: azdClient}
}

// Initialize stores the service configuration; no other setup is required.
func (p *ConnectionServiceTargetProvider) Initialize(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
) error {
	p.serviceConfig = serviceConfig
	return nil
}

// Endpoints returns no endpoints; connections do not expose any.
func (p *ConnectionServiceTargetProvider) Endpoints(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	targetResource *azdext.TargetResource,
) ([]string, error) {
	return nil, nil
}

// GetTargetResource resolves the target resource. Connections have no
// standalone ARM resource, so it delegates to azd's default resolver and
// falls back to a minimal target so the deploy pipeline can proceed.
func (p *ConnectionServiceTargetProvider) GetTargetResource(
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

// Package is a no-op; there is nothing to build or stage for a connection.
func (p *ConnectionServiceTargetProvider) Package(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	progress azdext.ProgressReporter,
) (*azdext.ServicePackageResult, error) {
	return &azdext.ServicePackageResult{}, nil
}

// Publish is a no-op; connections have no artifacts to publish.
func (p *ConnectionServiceTargetProvider) Publish(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	targetResource *azdext.TargetResource,
	publishOptions *azdext.PublishOptions,
	progress azdext.ProgressReporter,
) (*azdext.ServicePublishResult, error) {
	return &azdext.ServicePublishResult{}, nil
}

// Deploy is a no-op; the connection is created at provision time by Bicep.
func (p *ConnectionServiceTargetProvider) Deploy(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	targetResource *azdext.TargetResource,
	progress azdext.ProgressReporter,
) (*azdext.ServiceDeployResult, error) {
	return &azdext.ServiceDeployResult{}, nil
}
