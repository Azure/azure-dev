// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import "context"

// ProgressFunc is a callback function for reporting progress messages
type ProgressFunc func(message string)

// FrameworkServiceProvider defines the contract for framework service implementations
type FrameworkServiceProvider interface {
	// Initialize initializes the framework service with the specified configuration
	Initialize(ctx context.Context, serviceConfig *ServiceConfig) error

	// RequiredExternalTools returns the set of external tools required by this framework service
	RequiredExternalTools(ctx context.Context, serviceConfig *ServiceConfig) ([]*ExternalTool, error)

	// Requirements returns the requirements for this framework service
	Requirements() (*FrameworkRequirements, error)

	// Restore restores dependencies for the framework service
	Restore(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		serviceContext *ServiceContext,
		progress ProgressFunc,
	) (*ServiceRestoreResult, error)

	// Build builds the framework service
	Build(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		serviceContext *ServiceContext,
		progress ProgressFunc,
	) (*ServiceBuildResult, error)

	// Package packages the framework service
	Package(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		serviceContext *ServiceContext,
		progress ProgressFunc,
	) (*ServicePackageResult, error)
}

// ServiceTargetProvider defines the contract for service target implementations
type ServiceTargetProvider interface {
	// Initialize initializes the service target with the specified configuration
	Initialize(ctx context.Context, serviceConfig *ServiceConfig) error

	// GetTargetResource gets the target resource for the service, with optional default resolution fallback
	GetTargetResource(
		ctx context.Context,
		subscriptionId string,
		serviceConfig *ServiceConfig,
		defaultResolver func() (*TargetResource, error),
	) (*TargetResource, error)

	// Package packages the service for deployment
	Package(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		serviceContext *ServiceContext,
		progress ProgressFunc,
	) (*ServicePackageResult, error)

	// Publish publishes the packaged service to the target
	Publish(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		serviceContext *ServiceContext,
		targetResource *TargetResource,
		publishOptions *PublishOptions,
		progress ProgressFunc,
	) (*ServicePublishResult, error)

	// Deploy deploys the service to the target
	Deploy(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		serviceContext *ServiceContext,
		targetResource *TargetResource,
		progress ProgressFunc,
	) (*ServiceDeployResult, error)

	// Endpoints returns the endpoints for the service
	Endpoints(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		targetResource *TargetResource,
	) ([]string, error)
}
