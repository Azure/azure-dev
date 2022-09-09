// Package action provides interfaces for the main entry-point for CLI applications.
//
// An action is simply a request handler for a command invocation. Each action implements Run, which looks like:
//
//	Run(ctx context.Context, flags F, args []string)
package action

import (
	"context"
)

// ActionFunc is an Action implementation.
type ActionFunc[F any] func(context.Context, F, []string) error

// Run implements the Action interface
func (a ActionFunc[F]) Run(ctx context.Context, flags F, args []string) error {
	return a(ctx, flags, args)
}

// Action is the representation of the business logic of a
// command.
type Action[F any] interface {
	Run(ctx context.Context, flags F, args []string) error
}
