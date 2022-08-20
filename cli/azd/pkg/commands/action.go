package commands

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// ActionFunc is an Action implementation. Use this when
// you only need to execute a function with no need for
// any other accompanying data or to set up any flags.
type ActionFunc func(context.Context, *cobra.Command, []string, *azdcontext.AzdContext) error

// Run implements the Action interface
func (a ActionFunc) Run(ctx context.Context, cmd *cobra.Command, args []string, azdCtx *azdcontext.AzdContext) error {
	return a(ctx, cmd, args, azdCtx)
}

// SetupFlags implements the Action interface
func (a ActionFunc) SetupFlags(*pflag.FlagSet, *pflag.FlagSet) {
}

// Action is the representation of the business logic of a
// command. It describes a Cobra action which is injected
// with a context and a list of arguments.
type Action interface {
	Run(ctx context.Context, cmd *cobra.Command, args []string, azdCtx *azdcontext.AzdContext) error
	SetupFlags(
		persistent *pflag.FlagSet,
		local *pflag.FlagSet,
	)
}

// CompositeAction returns a new Action that executes
// each of the given actions in the order they were given.
//
// Requires that no two or more actions register the
// same or invalid flags in their SetupFlags functions.
func CompositeAction(actions ...Action) Action {
	return &compositeAction{actions: actions}
}

type compositeAction struct {
	actions []Action
}

func (a *compositeAction) Run(ctx context.Context, cmd *cobra.Command, args []string, azdCtx *azdcontext.AzdContext) error {
	for _, a := range a.actions {
		if err := a.Run(ctx, cmd, args, azdCtx); err != nil {
			return err
		}
	}
	return nil
}

func (a *compositeAction) SetupFlags(
	persistent *pflag.FlagSet,
	local *pflag.FlagSet,
) {
	for _, a := range a.actions {
		a.SetupFlags(persistent, local)
	}
}
