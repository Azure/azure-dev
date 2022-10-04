// Package action provides the general interface for the application logic for azd CLI commands.
package action

import (
	"context"
)

// ActionFunc is an Action implementation for regular functions.
type ActionFunc func(context.Context) error

// Run implements the Action interface
func (a ActionFunc) Run(ctx context.Context) error {
	return a(ctx)
}

// Action is the representation of the application logic of a CLI command.
type Action interface {
	// Run executes the CLI command.
	Run(ctx context.Context) error
}
