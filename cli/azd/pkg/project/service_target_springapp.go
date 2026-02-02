// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

var errSpringAppDeprecated = errors.New(
	"Azure Spring Apps is no longer supported. Recommend using Azure Container Apps." +
		" For more information, please refer to https://aka.ms/asaretirement",
)

type springAppTarget struct {
	env        *environment.Environment
	envManager environment.Manager
}

// NewSpringAppTarget creates the spring app service target.
func NewSpringAppTarget(
	env *environment.Environment,
	envManager environment.Manager,
) ServiceTarget {
	return &springAppTarget{
		env:        env,
		envManager: envManager,
	}
}

func (st *springAppTarget) RequiredExternalTools(
	ctx context.Context,
	serviceConfig *ServiceConfig,
) []tools.ExternalTool {
	return []tools.ExternalTool{}
}

func (st *springAppTarget) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return errSpringAppDeprecated
}

func (st *springAppTarget) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	return nil, errSpringAppDeprecated
}

func (st *springAppTarget) Publish(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	targetResource *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
	publishOptions *PublishOptions,
) (*ServicePublishResult, error) {
	return nil, errSpringAppDeprecated
}

func (st *springAppTarget) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	targetResource *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
) (*ServiceDeployResult, error) {
	return nil, errSpringAppDeprecated
}

func (st *springAppTarget) Endpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) ([]string, error) {
	return nil, errSpringAppDeprecated
}

// Tasks returns the list of available tasks for this service target.
func (st *springAppTarget) Tasks(ctx context.Context, serviceConfig *ServiceConfig) []ServiceTask {
	return []ServiceTask{}
}

// Task executes a specific task for this service target.
func (st *springAppTarget) Task(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
	task ServiceTask,
	taskArgs string,
) error {
	return nil
}
