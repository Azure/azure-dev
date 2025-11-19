// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import "context"

// ProjectEventArgs contains the project context for project-level events
type ProjectEventArgs struct {
	Project *ProjectConfig
}

// ServiceEventArgs contains the project and service context for service-level events
type ServiceEventArgs struct {
	Project        *ProjectConfig
	Service        *ServiceConfig
	ServiceContext *ServiceContext
}

// ProjectEventHandler is a function that handles project-level extension events
type ProjectEventHandler func(ctx context.Context, args *ProjectEventArgs) error

// ServiceEventHandler is a function that handles service-level extension events
type ServiceEventHandler func(ctx context.Context, args *ServiceEventArgs) error

// ServiceEventOptions contains options for service event handlers
type ServiceEventOptions struct {
	// Filters to apply - service events will only be triggered for services matching all filters
	Filters map[string]string

	// Host filter - deprecated, use Filters["host"] instead
	Host string

	// Language filter - deprecated, use Filters["language"] instead
	Language string
}
