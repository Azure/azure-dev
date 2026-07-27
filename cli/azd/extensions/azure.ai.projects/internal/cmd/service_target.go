// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// aiProjectHost is the azure.yaml host owned by this extension.
// A project service carries model deployments.
// It may also carry an endpoint for an existing project.
const aiProjectHost = "azure.ai.project"

var _ azdext.ServiceTargetProvider = (*projectServiceTarget)(nil)

// projectServiceTarget owns the azure.ai.project host.
// The microsoft.foundry provider provisions projects, deployments,
// accounts, and RBAC. This target keeps deploy graph ordering but has
// no package, publish, or deploy work.
//
// When the entry sets `endpoint:`, provisioning reuses that project.
// This target still has nothing to upsert during deploy.
type projectServiceTarget struct {
	azdClient     *azdext.AzdClient
	serviceConfig *azdext.ServiceConfig
}

// newProjectServiceTarget creates the project service target.
func newProjectServiceTarget(azdClient *azdext.AzdClient) azdext.ServiceTargetProvider {
	return &projectServiceTarget{azdClient: azdClient}
}

// Initialize stores the service configuration.
func (p *projectServiceTarget) Initialize(ctx context.Context, serviceConfig *azdext.ServiceConfig) error {
	p.serviceConfig = serviceConfig
	return nil
}

// Endpoints returns no endpoints.
// Provisioning publishes the endpoint through the azd environment.
func (p *projectServiceTarget) Endpoints(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	targetResource *azdext.TargetResource,
) ([]string, error) {
	return nil, nil
}

// GetTargetResource delegates to azd's resolver.
// It returns a minimal target when the resolver cannot find one.
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

// Deploy is a no-op because resources are provisioned earlier.
// Removing the service stops management without deleting resources.
// Teardown continues to run through `azd down`.
func (p *projectServiceTarget) Deploy(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	targetResource *azdext.TargetResource,
	progress azdext.ProgressReporter,
) (*azdext.ServiceDeployResult, error) {
	return &azdext.ServiceDeployResult{}, nil
}
