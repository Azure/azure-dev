// Package actions contains the application logic that handles azd CLI commands.
package actions

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
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
	Run(ctx context.Context) (*ActionResult, error)
}

func ToUxItem(actionResult *ActionResult, err error) ux.UxItem {
	if err != nil {
		return &ux.ActionResult{
			SuccessMessage: "",
			FollowUp:       "",
			Err:            err,
		}
	}

	if actionResult == nil {
		return &ux.ActionResult{
			SuccessMessage: "",
			FollowUp:       "",
			Err:            nil,
		}
	}
	if actionResult.Message == nil {
		return &ux.ActionResult{
			SuccessMessage: "",
			FollowUp:       "",
			Err:            nil,
		}
	}
	return &ux.ActionResult{
		SuccessMessage: actionResult.Message.Header,
		FollowUp:       actionResult.Message.FollowUp,
		Err:            nil,
	}
}
