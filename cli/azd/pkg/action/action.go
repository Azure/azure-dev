// Package action provides general interfaces for the main application logic for any CLI program.
//
// An action is simply a request handler for a command invocation. Each action implements Run, which looks like:
//
//	Run(ctx context.Context, flags F, args []string)
//
// where `flags` is the parsed flags, and `args` is the remaining positional arguments after flags.
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
