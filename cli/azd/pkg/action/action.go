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
type ActionFunc[F any] func(context.Context, F, []string) error

// Run implements the Action interface
func (a ActionFunc[F]) Run(ctx context.Context, flags F, args []string) error {
	return a(ctx, flags, args)
}

// Action is the representation of the application logic of a CLI command.
type Action[F any] interface {
	// Run executes the CLI command.
	Run(ctx context.Context, flags F, args []string) error
}
