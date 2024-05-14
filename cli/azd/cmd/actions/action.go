// Package actions contains the application logic that handles azd CLI commands.
package actions

import (
	"context"
)

// ActionFunc is an Action implementation for regular functions.
type ActionFunc func(context.Context) (*ActionResult, error)

// Run implements the Action interface
func (a ActionFunc) Run(ctx context.Context) (*ActionResult, error) {
	return a(ctx)
}

// Define a message as the completion of an Action.
type ResultMessage struct {
	Header   string
	FollowUp string
}

// Define the Action outputs.
type ActionResult struct {
	Message *ResultMessage
}

// Action is the representation of the application logic of a CLI command.
type Action interface {
	// Run executes the CLI command.
	//
	// It is currently valid to both return an error and a non-nil ActionResult.
	Run(ctx context.Context) (*ActionResult, error)
}
