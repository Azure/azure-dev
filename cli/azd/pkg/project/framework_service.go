// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type FrameworkService interface {
	Initialize(ctx context.Context, serviceConfig *ServiceConfig) error
	RequiredExternalTools(ctx context.Context, serviceConfig *ServiceConfig) []tools.ExternalTool
	Restore(ctx context.Context, serviceConfig *ServiceConfig) error
	Build(ctx context.Context, serviceConfig *ServiceConfig) *async.TaskWithProgress[*ServiceBuildResult, ServiceProgress]
}
