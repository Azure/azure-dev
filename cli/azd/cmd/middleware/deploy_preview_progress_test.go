// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/require"
)

func TestDeployPreviewProgressMiddleware(t *testing.T) {
	setupError := errors.New("preview setup failed")
	for _, tt := range []struct {
		name      string
		json      bool
		cancel    bool
		resultErr error
	}{
		{name: "success"},
		{name: "setup-error", resultErr: setupError},
		{name: "cancellation", cancel: true},
		{name: "json", json: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mockContext := mocks.NewMockContext(t.Context())
			var formatter output.Formatter = &output.NoneFormatter{}
			if tt.json {
				formatter = &output.JsonFormatter{}
			}
			progress := NewDeployPreviewProgressMiddleware(mockContext.Console, formatter)
			ctx, cancel := context.WithCancel(*mockContext.Context)
			defer cancel()
			expected := &actions.ActionResult{}

			result, err := progress.Run(ctx, func(ctx context.Context) (*actions.ActionResult, error) {
				require.Equal(t, !tt.json, mockContext.Console.IsSpinnerRunning(ctx),
					"preparation feedback must start before resolving extensions or action dependencies")
				if tt.cancel {
					cancel()
					return nil, ctx.Err()
				}
				return expected, tt.resultErr
			})

			if tt.cancel {
				require.ErrorIs(t, err, context.Canceled)
				require.Nil(t, result)
			} else {
				require.ErrorIs(t, err, tt.resultErr)
				require.Same(t, expected, result)
			}
			require.False(t, mockContext.Console.IsSpinnerRunning(ctx))
			if tt.json {
				require.Empty(t, mockContext.Console.Output())
				require.Empty(t, mockContext.Console.SpinnerOps())
			} else {
				require.Equal(t, []mockinput.SpinnerOp{
					{Op: mockinput.SpinnerOpShow, Message: "Preparing deployment preview", Format: input.Step},
					{Op: mockinput.SpinnerOpStop, Format: input.Step},
				}, mockContext.Console.SpinnerOps())
				require.Len(t, mockContext.Console.Output(), 1)
				require.Contains(t, mockContext.Console.Output()[0], "Previewing deployment (azd deploy --preview)")
			}
		})
	}
}
