// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type FrameworkService interface {
	Initialize(ctx context.Context) error
	RequiredExternalTools() []tools.ExternalTool
	Restore(ctx context.Context) error
	Build(ctx context.Context, progress chan<- string) (string, error)
}
