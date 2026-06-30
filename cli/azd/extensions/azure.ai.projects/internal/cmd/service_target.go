// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// aiProjectHost is the azure.yaml service host kind owned by this extension. A
// `host: azure.ai.project` service entry represents the Foundry project and carries its
// model `deployments` (and an optional `endpoint:` for reuse).
const aiProjectHost = "azure.ai.project"

var _ azdext.ServiceTargetProvider = (*projectServiceTarget)(nil)

// projectServiceTarget owns the azure.ai.project host. The Foundry project, its model
// deployments, the underlying Account, and RBAC are provisioned at `azd provision` by the
// built-in `microsoft.foundry` Bicep provider (or by the user's own infra), so this
// target has no deploy-time work: Package, Publish, and Deploy are no-ops. It exists so
// the azure.ai.projects extension owns the project host (rather than the shared no-op
// shim in the agents extension) and so `azd deploy`/`azd up` can walk a project entry.
//
// When the project entry sets `endpoint:` (bring-your-own project), provisioning is
// skipped by the provider and azd connects to the existing project; this target still
// has nothing to upsert at deploy.
type projectServiceTarget struct {
	azdClient     *azdext.AzdClient
	serviceConfig *azdext.ServiceConfig
}

// newProjectServiceTarget creates the azure.ai.project service-target provider.
func newProjectServiceTarget(azdClient *azdext.AzdClient) azdext.ServiceTargetProvider {
	return &projectServiceTarget{azdClient: azdClient}
}

// Initialize stores the service configuration; no other setup is required.
func (p *projectServiceTarget) Initialize(ctx context.Context, serviceConfig *azdext.ServiceConfig) error {
	p.serviceConfig = serviceConfig
	return nil
}

// Endpoints returns no endpoints; the project endpoint is surfaced through the azd
// environment (FOUNDRY_PROJECT_ENDPOINT) during provisioning, not here.
func (p *projectServiceTarget) Endpoints(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	targetResource *azdext.TargetResource,
) ([]string, error) {
	return nil, nil
}

// GetTargetResource delegates to azd's default resolver and falls back to a minimal
// target so the deploy pipeline can proceed.
func (p *projectServiceTarget) GetTargetResource(
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

// Package is a no-op; the project has nothing to build or stage.
func (p *projectServiceTarget) Package(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	progress azdext.ProgressReporter,
) (*azdext.ServicePackageResult, error) {
	return &azdext.ServicePackageResult{}, nil
}

// Publish is a no-op; the project has no artifact to publish.
func (p *projectServiceTarget) Publish(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	targetResource *azdext.TargetResource,
	publishOptions *azdext.PublishOptions,
	progress azdext.ProgressReporter,
) (*azdext.ServicePublishResult, error) {
	return &azdext.ServicePublishResult{}, nil
}

// Deploy is a no-op; the project and its model deployments are created at provision time
// by the built-in microsoft.foundry Bicep provider (or the user's infra). Removing the
// service from azure.yaml stops azd managing the project but does not delete it; teardown
// runs through `azd down`.
func (p *projectServiceTarget) Deploy(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	targetResource *azdext.TargetResource,
	progress azdext.ProgressReporter,
) (*azdext.ServiceDeployResult, error) {
	return &azdext.ServiceDeployResult{}, nil
}
