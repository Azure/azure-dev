// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
)

type deployPreviewProgressMiddleware struct {
	console   input.Console
	formatter output.Formatter
}

// NewDeployPreviewProgressMiddleware starts feedback before extension startup and action construction.
func NewDeployPreviewProgressMiddleware(console input.Console, formatter output.Formatter) Middleware {
	return &deployPreviewProgressMiddleware{console: console, formatter: formatter}
}

func (m *deployPreviewProgressMiddleware) Run(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
	if m.formatter.Kind() == output.JsonFormat {
		return next(ctx)
	}

	m.console.MessageUxItem(ctx, &ux.MessageTitle{Title: "Previewing deployment (azd deploy --preview)"})
	m.console.ShowSpinner(ctx, "Preparing deployment preview", input.Step)
	defer m.console.StopSpinner(ctx, "", input.Step)

	return next(ctx)
}
