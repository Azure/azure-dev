package middleware

import (
	"context"
	"errors"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/messaging"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/progress"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"golang.org/x/exp/slices"
)

// Adds support to easily debug and attach a debugger to AZD for development purposes
type UxMiddleware struct {
	options         *Options
	console         input.Console
	descriptor      *actions.ActionDescriptor
	progressPrinter *progress.Printer
}

// Creates a new instance of the Debug middleware
func NewUxMiddleware(
	options *Options,
	console input.Console,
	descriptor *actions.ActionDescriptor,
	progressPrinter *progress.Printer,
) Middleware {
	return &UxMiddleware{
		options:         options,
		console:         console,
		descriptor:      descriptor,
		progressPrinter: progressPrinter,
	}
}

// Invokes the debug middleware. When AZD_DEBUG is set will prompt the user to attach
// a debugger before continuing invocation of the action
func (m *UxMiddleware) Run(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
	if m.options.IsChildAction() {
		return next(ctx)
	}

	if m.descriptor.Options.TitleOptions != nil {
		// Command title
		m.console.MessageUxItem(ctx, &ux.MessageTitle{
			Title:     m.descriptor.Options.TitleOptions.Title,
			TitleNote: m.descriptor.Options.TitleOptions.Description,
		})
	}

	subscription := m.progressPrinter.Register(ctx, func(ctx context.Context, msg *messaging.Message) bool {
		kinds := []messaging.MessageKind{progress.MessageKind, ext.HookMessageKind}
		return slices.Contains(kinds, msg.Type)
	})
	defer subscription.Close(ctx)

	actionResult, err := next(ctx)

	var displayResult *ux.ActionResult
	if actionResult != nil && actionResult.Message != nil {
		displayResult = &ux.ActionResult{
			SuccessMessage: actionResult.Message.Header,
			FollowUp:       actionResult.Message.FollowUp,
		}
	} else if err != nil {
		displayResult = &ux.ActionResult{
			Err: err,
		}
	}

	if displayResult != nil {
		m.console.MessageUxItem(ctx, displayResult)
	}

	if err != nil {
		var respErr *azcore.ResponseError
		var azureErr *azcli.AzureDeploymentError

		// We only want to show trace ID for server-related errors,
		// where we have full server logs to troubleshoot from.
		//
		// For client errors, we don't want to show the trace ID, as it is not useful to the user currently.
		if errors.As(err, &respErr) || errors.As(err, &azureErr) {
			if actionResult != nil && actionResult.TraceID != "" {
				m.console.Message(
					ctx,
					output.WithErrorFormat(fmt.Sprintf("TraceID: %s", actionResult.TraceID)))
			}
		}
	}
	return actionResult, err
}
