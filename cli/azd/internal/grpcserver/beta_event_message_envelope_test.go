// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func TestBetaEventMessageEnvelope_GetRequestId(t *testing.T) {
	envelope := newBetaEventMessageEnvelope()
	ctx := extensions.WithClaimsContext(t.Context(), &extensions.ExtensionClaims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: "test-ext"},
	})

	tests := []struct {
		name string
		msg  *v1beta.EventMessage
		want string
	}{
		{
			name: "SubscribeProjectEvent",
			msg: &v1beta.EventMessage{MessageType: &v1beta.EventMessage_SubscribeProjectEvent{
				SubscribeProjectEvent: &v1beta.SubscribeProjectEvent{
					EventNames: []string{"provision"},
				},
			}},
			want: "test-ext.provision",
		},
		{
			name: "ProjectHandlerStatus",
			msg: &v1beta.EventMessage{MessageType: &v1beta.EventMessage_ProjectHandlerStatus{
				ProjectHandlerStatus: &v1beta.ProjectHandlerStatus{
					EventName: "provision",
				},
			}},
			want: "test-ext.provision",
		},
		{
			name: "InvokeProjectHandler",
			msg: &v1beta.EventMessage{MessageType: &v1beta.EventMessage_InvokeProjectHandler{
				InvokeProjectHandler: &v1beta.InvokeProjectHandler{
					EventName: "provision",
				},
			}},
			want: "test-ext.provision",
		},
		{
			name: "SubscribeServiceEvent",
			msg: &v1beta.EventMessage{MessageType: &v1beta.EventMessage_SubscribeServiceEvent{
				SubscribeServiceEvent: &v1beta.SubscribeServiceEvent{
					EventNames: []string{"deploy"},
				},
			}},
			want: "test-ext.deploy",
		},
		{
			name: "ServiceHandlerStatus",
			msg: &v1beta.EventMessage{MessageType: &v1beta.EventMessage_ServiceHandlerStatus{
				ServiceHandlerStatus: &v1beta.ServiceHandlerStatus{
					EventName:   "deploy",
					ServiceName: "api",
				},
			}},
			want: "test-ext.api.deploy",
		},
		{
			name: "InvokeServiceHandler",
			msg: &v1beta.EventMessage{MessageType: &v1beta.EventMessage_InvokeServiceHandler{
				InvokeServiceHandler: &v1beta.InvokeServiceHandler{
					EventName: "deploy",
					Service:   &v1beta.ServiceConfig{Name: "api"},
				},
			}},
			want: "test-ext.api.deploy",
		},
		{
			name: "TopLevelRequestID",
			msg:  &v1beta.EventMessage{RequestId: "request-1"},
			want: "request-1",
		},
		{name: "NilMessage"},
		{
			name: "NilService",
			msg: &v1beta.EventMessage{MessageType: &v1beta.EventMessage_InvokeServiceHandler{
				InvokeServiceHandler: &v1beta.InvokeServiceHandler{
					EventName: "deploy",
				},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, envelope.GetRequestId(ctx, tt.msg))
		})
	}
}

func TestBetaEventMessageEnvelope_RequestResponseFields(t *testing.T) {
	envelope := newBetaEventMessageEnvelope()
	message := &v1beta.EventMessage{
		MessageType: &v1beta.EventMessage_SubscribeProjectEventResponse{
			SubscribeProjectEventResponse: &v1beta.SubscribeProjectEventResponse{},
		},
	}
	envelope.SetRequestId(t.Context(), message, "request-1")
	require.Equal(t, "request-1", envelope.GetRequestId(t.Context(), message))
	require.NotNil(t, envelope.GetInnerMessage(message))

	envelope.SetError(message, errors.New("subscription failed"))
	require.ErrorContains(t, envelope.GetError(message), "subscription failed")
}

func TestBetaEventMessageEnvelope_NoProgressMessages(t *testing.T) {
	envelope := betaEventMessageEnvelope{}
	message := &v1beta.EventMessage{
		RequestId: "request-1",
		MessageType: &v1beta.EventMessage_ServiceHandlerStatus{
			ServiceHandlerStatus: &v1beta.ServiceHandlerStatus{
				EventName:   "predeploy",
				ServiceName: "api",
				Status:      "completed",
			},
		},
	}
	require.Equal(t, "request-1", envelope.GetRequestId(t.Context(), message))
	require.False(t, envelope.IsProgressMessage(message))
	require.Empty(t, envelope.GetProgressMessage(message))
	require.Nil(t, envelope.CreateProgressMessage("request-1", "warning"))
}

func TestWrapBetaError_PreservesStructuredDetails(t *testing.T) {
	t.Run("local cause types", func(t *testing.T) {
		err := &azdext.LocalError{
			Message:    "handler failed",
			Code:       "handler_failed",
			Category:   azdext.LocalErrorCategoryValidation,
			CauseTypes: []string{"*demo.TransportError"},
			Suggestion: "Check the extension configuration",
		}

		message := wrapBetaError(err)
		localErr, ok := errors.AsType[*azdext.LocalError](
			unwrapBetaExtensionError(message),
		)
		require.True(t, ok)
		require.Equal(t, []string{"*demo.TransportError"}, localErr.CauseTypes)
		require.Equal(t, "Check the extension configuration", localErr.Suggestion)
	})

	t.Run("service detail", func(t *testing.T) {
		err := &azdext.ServiceError{
			Message:     "service failed",
			ErrorCode:   "Conflict",
			StatusCode:  409,
			ServiceName: "example.service",
		}

		message := wrapBetaError(err)
		serviceErr, ok := errors.AsType[*azdext.ServiceError](
			unwrapBetaExtensionError(message),
		)
		require.True(t, ok)
		require.Equal(t, "Conflict", serviceErr.ErrorCode)
		require.Equal(t, 409, serviceErr.StatusCode)
		require.Equal(t, "example.service", serviceErr.ServiceName)
	})

	t.Run("tool detail", func(t *testing.T) {
		exitCode := 42
		err := &azdext.ToolError{
			Message:    "tool failed",
			ToolName:   "docker",
			Kind:       azdext.ToolErrorKindFailed,
			ExitCode:   &exitCode,
			Suggestion: "Check the tool output",
		}

		message := wrapBetaError(err)
		require.Equal(t, v1beta.ErrorOrigin_ERROR_ORIGIN_TOOL, message.GetOrigin())
		require.Equal(t, "docker", message.GetToolError().GetToolName())
		require.Equal(t, "failed", message.GetToolError().GetFailureKind())
		require.Equal(t, int64(42), message.GetToolError().GetExitCode())
	})
}

func TestWrapBetaError_PreservesErrorChainPrecedence(t *testing.T) {
	for _, tt := range []struct {
		name  string
		cause error
	}{
		{
			name: "local error inside tool error",
			cause: &azdext.LocalError{
				Message:    "invalid configuration",
				Code:       "invalid_config",
				Category:   azdext.LocalErrorCategoryValidation,
				CauseTypes: []string{"*demo.ConfigError"},
				Suggestion: "Check the configuration",
				Links:      []errorhandler.ErrorLink{{URL: "https://example.com/config", Title: "Configuration"}},
			},
		},
		{
			name: "service error inside tool error",
			cause: &azdext.ServiceError{
				Message:     "service unavailable",
				ErrorCode:   "Unavailable",
				StatusCode:  503,
				ServiceName: "example.com",
				Suggestion:  "Try again later",
				Links:       []errorhandler.ErrorLink{{URL: "https://example.com/status", Title: "Service status"}},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := &azdext.ToolError{
				Message:    "tool failed",
				Err:        tt.cause,
				ToolName:   "docker",
				Kind:       azdext.ToolErrorKindFailed,
				ExitCode:   new(42),
				Suggestion: "Check the tool output",
			}
			message := wrapBetaError(err)
			expected := wrapBetaError(tt.cause)
			require.Equal(t, expected.GetOrigin(), message.GetOrigin())
			require.Equal(t, expected.GetSource(), message.GetSource())
			require.Equal(t, expected.GetMessage(), message.GetMessage())
			require.Equal(t, expected.GetSuggestion(), message.GetSuggestion())
			require.Equal(t, expected.GetLinks(), message.GetLinks())
			require.Nil(t, message.GetToolError())
		})
	}
}

func TestValidateBetaEventMessageModes(t *testing.T) {
	t.Run("modern requires a request ID", func(t *testing.T) {
		err := validateBetaEventMessage(&v1beta.EventMessage{
			MessageType: &v1beta.EventMessage_ProjectHandlerStatus{
				ProjectHandlerStatus: &v1beta.ProjectHandlerStatus{
					EventName: "predeploy",
					Status:    "completed",
				},
			},
		}, betaEventStreamRequestIDs)
		require.ErrorContains(t, err, "request_id is required")
	})

	t.Run("legacy rejects structured service messages", func(t *testing.T) {
		err := validateBetaEventMessage(&v1beta.EventMessage{
			MessageType: &v1beta.EventMessage_ServiceHandlerStatus{
				ServiceHandlerStatus: &v1beta.ServiceHandlerStatus{
					EventName:   "predeploy",
					ServiceName: "api",
					Status:      "completed",
					Messages: []*v1beta.ServiceEventMessage{{
						Kind:    v1beta.ServiceEventMessageKind_SERVICE_EVENT_MESSAGE_KIND_WARNING,
						Message: "warning",
					}},
				},
			},
		}, betaEventStreamLegacy)
		require.ErrorContains(t, err, "require a new beta event stream")
	})

	t.Run("modern rejects server message types", func(t *testing.T) {
		err := validateBetaEventMessage(&v1beta.EventMessage{
			RequestId: "request-1",
			MessageType: &v1beta.EventMessage_InvokeProjectHandler{
				InvokeProjectHandler: &v1beta.InvokeProjectHandler{
					EventName: "predeploy",
				},
			},
		}, betaEventStreamRequestIDs)
		require.ErrorContains(t, err, "invalid message for a beta event client")
	})
}
