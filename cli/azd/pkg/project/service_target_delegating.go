// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"maps"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

// delegatingServiceTarget initializes an extension provider, then routes target operations to a built-in target.
type delegatingServiceTarget struct {
	host        ServiceTargetKind
	baseKind    ServiceTargetKind
	initializer ServiceTarget
	base        ServiceTarget
}

// NewDelegatingServiceTarget creates a service target that decorates a built-in target.
func NewDelegatingServiceTarget(
	host ServiceTargetKind,
	baseKind ServiceTargetKind,
	initializer ServiceTarget,
	base ServiceTarget,
) ServiceTarget {
	return &delegatingServiceTarget{
		host:        host,
		baseKind:    baseKind,
		initializer: initializer,
		base:        base,
	}
}

func (t *delegatingServiceTarget) effectiveConfig(serviceConfig *ServiceConfig) (*ServiceConfig, error) {
	if serviceConfig == nil {
		return nil, fmt.Errorf("service configuration is required")
	}
	if serviceConfig.Host != t.host {
		return nil, fmt.Errorf(
			"delegating target %q cannot process service host %q",
			t.host,
			serviceConfig.Host,
		)
	}

	effective := *serviceConfig
	effective.Host = t.baseKind
	return &effective, nil
}

func (t *delegatingServiceTarget) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	if err := t.initializer.Initialize(ctx, serviceConfig); err != nil {
		return err
	}

	effective, err := t.effectiveConfig(serviceConfig)
	if err != nil {
		return err
	}
	return t.base.Initialize(ctx, effective)
}

func (t *delegatingServiceTarget) RequiredExternalTools(
	ctx context.Context,
	serviceConfig *ServiceConfig,
) []tools.ExternalTool {
	effective, err := t.effectiveConfig(serviceConfig)
	if err != nil {
		return nil
	}
	return t.base.RequiredExternalTools(ctx, effective)
}

func (t *delegatingServiceTarget) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	effective, err := t.effectiveConfig(serviceConfig)
	if err != nil {
		return nil, err
	}
	return t.base.Package(ctx, effective, serviceContext, progress)
}

func (t *delegatingServiceTarget) Publish(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	targetResource *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
	publishOptions *PublishOptions,
) (*ServicePublishResult, error) {
	effective, err := t.effectiveConfig(serviceConfig)
	if err != nil {
		return nil, err
	}
	return t.base.Publish(ctx, effective, serviceContext, targetResource, progress, publishOptions)
}

func (t *delegatingServiceTarget) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	targetResource *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
) (*ServiceDeployResult, error) {
	effective, err := t.effectiveConfig(serviceConfig)
	if err != nil {
		return nil, err
	}
	return t.base.Deploy(ctx, effective, serviceContext, targetResource, progress)
}

func (t *delegatingServiceTarget) Endpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) ([]string, error) {
	effective, err := t.effectiveConfig(serviceConfig)
	if err != nil {
		return nil, err
	}
	return t.base.Endpoints(ctx, effective, targetResource)
}

func (t *delegatingServiceTarget) ResolveTargetResource(
	ctx context.Context,
	subscriptionID string,
	serviceConfig *ServiceConfig,
	defaultResolver func() (*environment.TargetResource, error),
) (*environment.TargetResource, error) {
	resolver, ok := t.base.(TargetResourceResolver)
	if !ok {
		return defaultResolver()
	}

	effective, err := t.effectiveConfig(serviceConfig)
	if err != nil {
		return nil, err
	}
	return resolver.ResolveTargetResource(ctx, subscriptionID, effective, defaultResolver)
}

func (t *delegatingServiceTarget) PreviewWithTarget(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) (*ServiceDeployPreviewResult, error) {
	previewer, ok := t.base.(ServiceTargetResourcePreviewer)
	if !ok {
		return nil, ErrDeployPreviewNotSupported
	}

	effective, err := t.effectiveConfig(serviceConfig)
	if err != nil {
		return nil, err
	}
	result, err := previewer.PreviewWithTarget(ctx, effective, targetResource)
	if err != nil || result == nil {
		return result, err
	}
	copy := *result
	if result.Data != nil {
		copy.Data = maps.Clone(result.Data)
		copy.Data["host"] = serviceConfig.Host
	}
	return &copy, nil
}
