// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// aiConnectionHost is the azure.yaml service host kind owned by this extension.
// A `host: azure.ai.connection` service entry carries one Foundry project
// connection, keyed by the connection name.
const aiConnectionHost = "azure.ai.connection"

var _ azdext.ServiceTargetProvider = (*connectionServiceTarget)(nil)

// connectionServiceTarget owns the azure.ai.connection host so azd can walk a
// connection entry in the deploy graph. All lifecycle methods are no-ops; see
// Deploy for why.
type connectionServiceTarget struct {
	azdClient     *azdext.AzdClient
	serviceConfig *azdext.ServiceConfig
}

// newConnectionServiceTarget creates the azure.ai.connection service-target provider.
func newConnectionServiceTarget(azdClient *azdext.AzdClient) azdext.ServiceTargetProvider {
	return &connectionServiceTarget{azdClient: azdClient}
}

// Initialize stores the service configuration; no other setup is required.
func (p *connectionServiceTarget) Initialize(ctx context.Context, serviceConfig *azdext.ServiceConfig) error {
	p.serviceConfig = serviceConfig
	return nil
}

// Endpoints returns no endpoints; a connection service exposes none.
func (p *connectionServiceTarget) Endpoints(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	targetResource *azdext.TargetResource,
) ([]string, error) {
	return nil, nil
}

// GetTargetResource delegates to azd's default resolver and falls back to a minimal
// target so the deploy pipeline can proceed; the connection upsert targets the Foundry
// project, not an ARM resource azd tracks.
func (p *connectionServiceTarget) GetTargetResource(
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
	return &azdext.TargetResource{SubscriptionId: subscriptionId}, nil
}

// Package is a no-op; a connection has nothing to build or stage.
func (p *connectionServiceTarget) Package(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	progress azdext.ProgressReporter,
) (*azdext.ServicePackageResult, error) {
	return &azdext.ServicePackageResult{}, nil
}

// Publish is a no-op; a connection has no artifact to publish.
func (p *connectionServiceTarget) Publish(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	targetResource *azdext.TargetResource,
	publishOptions *azdext.PublishOptions,
	progress azdext.ProgressReporter,
) (*azdext.ServicePublishResult, error) {
	return &azdext.ServicePublishResult{}, nil
}

// Deploy is a no-op. Connections declared as host: azure.ai.connection
// services are created at provision time by the microsoft.foundry provider
// (for both greenfield and brownfield projects), so creating them again here
// would be a redundant ARM write. This mirrors azure.ai.project's Deploy,
// which is a no-op for the same reason.
//
// The target still exists so azd can order a connection's deploy step via
// `uses:` (toolboxes/agents that depend on it). Removing a connection from
// azure.yaml stops azd managing it but does not delete it (use
// `azd ai connection delete`).
func (p *connectionServiceTarget) Deploy(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	targetResource *azdext.TargetResource,
	progress azdext.ProgressReporter,
) (*azdext.ServiceDeployResult, error) {
	if progress != nil {
		progress(fmt.Sprintf(
			"Connection %q is provisioned by infrastructure; nothing to deploy", serviceConfig.GetName()))
	}
	return &azdext.ServiceDeployResult{}, nil
}
