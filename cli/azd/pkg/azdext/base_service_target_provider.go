// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import "context"

// BaseServiceTargetProvider provides no-op default implementations for all ServiceTargetProvider methods.
// Extensions should embed this struct and override only the methods they need.
//
// Example:
//
//	type MyProvider struct {
//	    azdext.BaseServiceTargetProvider
//	}
//
//	func (p *MyProvider) Deploy(
//	    ctx context.Context,
//	    serviceConfig *azdext.ServiceConfig,
//	    serviceContext *azdext.ServiceContext,
//	    targetResource *azdext.TargetResource,
//	    progress azdext.ProgressReporter,
//	) (*azdext.ServiceDeployResult, error) {
//	    // custom deploy logic
//	}
type BaseServiceTargetProvider struct{}

func (b *BaseServiceTargetProvider) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return nil
}

func (b *BaseServiceTargetProvider) Endpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *TargetResource,
) ([]string, error) {
	return nil, nil
}

func (b *BaseServiceTargetProvider) GetTargetResource(
	ctx context.Context,
	subscriptionId string,
	serviceConfig *ServiceConfig,
	defaultResolver func() (*TargetResource, error),
) (*TargetResource, error) {
	return nil, nil
}

func (b *BaseServiceTargetProvider) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress ProgressReporter,
) (*ServicePackageResult, error) {
	return nil, nil
}

func (b *BaseServiceTargetProvider) Publish(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	targetResource *TargetResource,
	publishOptions *PublishOptions,
	progress ProgressReporter,
) (*ServicePublishResult, error) {
	return nil, nil
}

func (b *BaseServiceTargetProvider) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	targetResource *TargetResource,
	progress ProgressReporter,
) (*ServiceDeployResult, error) {
	return nil, nil
}
