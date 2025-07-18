// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
)

// RegisterTemplateVersionMiddleware registers the template version middleware with the CLI
func RegisterTemplateVersionMiddleware(runner *middleware.MiddlewareRunner) error {
	// Register the middleware with a factory function that uses dependency injection
	return runner.Use("template-version", func(container *ioc.NestedContainer) middleware.Middleware {
		return NewTemplateVersionMiddleware(container)
	})
}
