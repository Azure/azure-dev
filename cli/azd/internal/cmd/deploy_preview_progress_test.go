// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type previewProgressWriter struct {
	*bytes.Buffer
	t       *testing.T
	console input.Console
}

func (w *previewProgressWriter) Write(data []byte) (int, error) {
	require.False(w.t, w.console.IsSpinnerRunning(w.t.Context()), "stop progress before writing the result")
	return w.Buffer.Write(data)
}

func TestDeployPreviewProgressDuringProviderCall(t *testing.T) {
	providerError := errors.New("remote comparison failed")
	for _, tt := range []struct {
		name      string
		json      bool
		cancel    bool
		resultErr error
	}{
		{name: "success"},
		{name: "provider-error", resultErr: providerError},
		{name: "cancellation", cancel: true},
		{name: "json", json: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			action, manager, writer := newDeployPreviewAction(t, "api")
			console, ok := action.console.(*mockinput.MockConsole)
			require.True(t, ok)
			console.SetNoPromptMode(true)
			console.SetTerminal(true)
			action.writer = &previewProgressWriter{Buffer: writer, t: t, console: console}
			if tt.json {
				action.formatter = &output.JsonFormatter{}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			service := action.projectConfig.Services["api"]
			target := &deployPreviewTarget{}
			manager.On("GetServiceTarget", mock.Anything, service).Return(target, nil).Once()
			target.On("Preview", mock.Anything, service).
				Run(func(mock.Arguments) {
					require.Equal(t, !tt.json, console.IsSpinnerRunning(ctx),
						"feedback must be visible while awaiting the provider, even with --no-prompt")
					require.Empty(t, writer.String(), "the final result is not available yet")
					if !tt.json {
						require.Equal(t, "Comparing configuration for service: api", console.SpinnerOps()[0].Message)
					}
					if tt.cancel {
						cancel()
					}
				}).
				Return(&project.ServiceDeployPreviewResult{
					Message: "Configuration changes\n", Data: map[string]any{"hasChanges": true},
				}, tt.resultErr).Once()

			_, err := action.Run(ctx)
			switch {
			case tt.cancel:
				require.ErrorIs(t, err, context.Canceled)
				require.Empty(t, writer.String())
			case tt.resultErr != nil:
				require.ErrorIs(t, err, tt.resultErr)
				require.Empty(t, writer.String())
			default:
				require.NoError(t, err)
				if tt.json {
					require.True(t, json.Valid(writer.Bytes()))
				} else {
					require.Equal(t, "Configuration changes\n", writer.String())
				}
			}
			require.False(t, console.IsSpinnerRunning(ctx))
			if tt.json {
				require.Empty(t, console.SpinnerOps())
			} else {
				require.Equal(t, []mockinput.SpinnerOp{
					{Op: mockinput.SpinnerOpShow, Message: "Comparing configuration for service: api", Format: input.Step},
					{Op: mockinput.SpinnerOpStop, Format: input.Step},
				}, console.SpinnerOps())
			}
			target.AssertExpectations(t)
		})
	}
}

func TestDeployPreviewProgressStopsOnTimeout(t *testing.T) {
	action, manager, _ := newDeployPreviewAction(t, "api")
	service := action.projectConfig.Services["api"]
	target := &deployPreviewTarget{}
	manager.On("GetServiceTarget", mock.Anything, service).Return(target, nil).Once()
	target.On("Preview", mock.Anything, service).Run(func(args mock.Arguments) {
		require.True(t, action.console.IsSpinnerRunning(t.Context()))
		ctx, ok := args.Get(0).(context.Context)
		require.True(t, ok)
		<-ctx.Done()
	}).Return(nil, nil).Once()

	_, err := action.previewService(t.Context(), service, time.Second)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.False(t, action.console.IsSpinnerRunning(t.Context()))
	target.AssertExpectations(t)
}

func TestDeployPreviewProgressIgnoresUnsupportedServices(t *testing.T) {
	action, manager, _ := newDeployPreviewAction(t, "--all")
	manager.On("GetServiceTarget", mock.Anything, action.projectConfig.Services["api"]).
		Return(&unadvertisedPreviewTarget{}, nil).Once()
	expectDeployPreview(t, action, manager, "web", &project.ServiceDeployPreviewResult{Message: "Agent changes"})
	_, err := action.Run(t.Context())
	require.NoError(t, err)
	console, ok := action.console.(*mockinput.MockConsole)
	require.True(t, ok)
	require.Equal(t, []mockinput.SpinnerOp{
		{Op: mockinput.SpinnerOpShow, Message: "Comparing configuration for service: web", Format: input.Step},
		{Op: mockinput.SpinnerOpStop, Format: input.Step},
	}, console.SpinnerOps())
}

func TestDeployPreviewProgressStopsBeforeWriterError(t *testing.T) {
	action, manager, _ := newDeployPreviewAction(t, "api")
	action.writer = previewErrorWriter{}
	expectDeployPreview(t, action, manager, "api", &project.ServiceDeployPreviewResult{Message: "Agent changes"})

	_, err := action.Run(t.Context())
	require.ErrorContains(t, err, "deployment preview could not be displayed")
	require.False(t, action.console.IsSpinnerRunning(t.Context()))
}

func TestDeployPreviewProgressSanitizesServiceName(t *testing.T) {
	action, manager, _ := newDeployPreviewAction(t, "api")
	service := action.projectConfig.Services["api"]
	service.Name = "\x1b[31mapi\x1b[0m\r\n"
	expectDeployPreview(t, action, manager, "api", &project.ServiceDeployPreviewResult{Message: "Agent changes"})

	_, err := action.previewService(t.Context(), service, time.Second)
	require.NoError(t, err)
	console, ok := action.console.(*mockinput.MockConsole)
	require.True(t, ok)
	require.Equal(t, "Comparing configuration for service: api", console.SpinnerOps()[0].Message)
	require.Equal(t, "\x1b[31mapi\x1b[0m\r\n", service.Name, "sanitize only the displayed name")
}
