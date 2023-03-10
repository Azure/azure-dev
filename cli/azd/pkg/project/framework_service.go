// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type FrameworkService interface {
	RequiredExternalTools(ctx context.Context) []tools.ExternalTool
	Initialize(ctx context.Context, serviceConfig *ServiceConfig) error
	Restore(
		ctx context.Context,
		serviceConfig *ServiceConfig,
	) *async.TaskWithProgress[*ServiceRestoreResult, ServiceProgress]
	Build(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		restoreOutput *ServiceRestoreResult,
	) *async.TaskWithProgress[*ServiceBuildResult, ServiceProgress]
}
