// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type FrameworkService interface {
	RequiredExternalTools() []tools.ExternalTool
	Package(ctx context.Context, progress chan<- string) (string, error)
	InstallDependencies(ctx context.Context) error
	Initialize(ctx context.Context) error
}

var _ FrameworkService = &dotnetProject{}
var _ FrameworkService = &npmProject{}
var _ FrameworkService = &pythonProject{}
var _ FrameworkService = &dockerProject{}
