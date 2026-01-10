// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"time"
)

// ServiceContext defines the shared pipeline state across all phases of the Azure Developer CLI (azd) service lifecycle.
// It captures the results of each phase (Restore, Build, Package, Publish, Deploy) in a consistent, extensible format
// that is accessible to both core and external extension providers.
type ServiceContext struct {
	Restore ArtifactCollection `json:"restore"`
	Build   ArtifactCollection `json:"build"`
	Package ArtifactCollection `json:"package"`
	Publish ArtifactCollection `json:"publish"`
	Deploy  ArtifactCollection `json:"deploy"`
}

// NewServiceContext creates a new ServiceContext with initialized maps and slices
func NewServiceContext() *ServiceContext {
	return &ServiceContext{
		Restore: make(ArtifactCollection, 0),
		Build:   make(ArtifactCollection, 0),
		Package: make(ArtifactCollection, 0),
		Publish: make(ArtifactCollection, 0),
		Deploy:  make(ArtifactCollection, 0),
	}
}

// ServiceLifecycleEventArgs are the event arguments available when
// any service lifecycle event has been triggered
type ServiceLifecycleEventArgs struct {
	Project        *ProjectConfig
	Service        *ServiceConfig
	ServiceContext *ServiceContext
	Args           map[string]any
}

// ServiceProgress represents an incremental progress message
// during a service operation such as restore, build, package & deploy
type ServiceProgress struct {
	Message   string
	Timestamp time.Time
}

// NewServiceProgress is a helper method to create a new
// progress message with a current timestamp
func NewServiceProgress(message string) ServiceProgress {
	return ServiceProgress{
		Message:   message,
		Timestamp: time.Now(),
	}
}

// ServiceRestoreResult is the result of a successful Restore operation
type ServiceRestoreResult struct {
	Artifacts ArtifactCollection `json:"artifacts"`
}

// ServiceBuildResult is the result of a successful Build operation
type ServiceBuildResult struct {
	Artifacts ArtifactCollection `json:"artifacts"`
}

type PackageOptions struct {
	OutputPath string
}

// ServicePackageResult is the result of a successful Package operation
type ServicePackageResult struct {
	Artifacts ArtifactCollection `json:"artifacts"`
}

// ServicePublishResult is the result of a successful Publish operation for services.
type ServicePublishResult struct {
	Artifacts ArtifactCollection `json:"artifacts"`
}

// ServiceDeployResult is the result of a successful Deploy operation
type ServiceDeployResult struct {
	Artifacts ArtifactCollection `json:"artifacts"`
}
