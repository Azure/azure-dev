// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"testing"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/stretchr/testify/require"
)

func TestBetaServiceEventManagerReturnsStructuredMessages(t *testing.T) {
	tests := []struct {
		name        string
		handlerErr  error
		wantStatus  string
		wantMessage string
	}{
		{
			name:       "completed handler",
			wantStatus: "completed",
		},
		{
			name:        "failed handler retains messages",
			handlerErr:  errors.New("deployment policy check failed"),
			wantStatus:  "failed",
			wantMessage: "deployment policy check failed",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := newBetaServiceEventManager("test.extension", nil, nil)
			manager.serviceEvents["postdeploy"] = func(
				context.Context,
				*ServiceEventArgs,
			) (*BetaServiceEventResponse, error) {
				return &BetaServiceEventResponse{
					Messages: []BetaServiceEventMessage{{
						Kind:       BetaServiceEventMessageWarning,
						Message:    "Review the access policy.",
						Suggestion: "Update the policy before the next deployment.",
					}},
				}, test.handlerErr
			}

			response, err := manager.onInvokeServiceHandler(
				t.Context(),
				&v1beta.InvokeServiceHandler{
					EventName: "postdeploy",
					Project:   &v1beta.ProjectConfig{},
					Service:   &v1beta.ServiceConfig{Name: "api"},
				},
			)
			require.NoError(t, err)
			status := response.GetServiceHandlerStatus()
			require.NotNil(t, status)
			require.Equal(t, "postdeploy", status.GetEventName())
			require.Equal(t, "api", status.GetServiceName())
			require.Equal(t, test.wantStatus, status.GetStatus())
			require.Equal(t, test.wantMessage, status.GetMessage())
			if test.handlerErr == nil {
				require.Nil(t, status.GetError())
			} else {
				require.Equal(t, test.handlerErr.Error(), status.GetError().GetMessage())
			}
			require.Equal(t, []*v1beta.ServiceEventMessage{{
				Kind:       v1beta.ServiceEventMessageKind_SERVICE_EVENT_MESSAGE_KIND_WARNING,
				Message:    "Review the access policy.",
				Suggestion: "Update the policy before the next deployment.",
			}}, status.GetMessages())
		})
	}
}
