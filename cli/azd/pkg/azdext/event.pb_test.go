// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"errors"
	"testing"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func TestEventMessageEnvelope_GetInnerMessage(t *testing.T) {
	env := NewEventMessageEnvelope()

	tests := []struct {
		name    string
		msg     *EventMessage
		wantNil bool
	}{
		{
			name: "SubscribeProjectEvent",
			msg: &EventMessage{
				MessageType: &EventMessage_SubscribeProjectEvent{
					SubscribeProjectEvent: &SubscribeProjectEvent{
						EventNames: []string{"provision"},
					},
				},
			},
		},
		{
			name: "InvokeProjectHandler",
			msg: &EventMessage{
				MessageType: &EventMessage_InvokeProjectHandler{
					InvokeProjectHandler: &InvokeProjectHandler{
						EventName: "provision",
					},
				},
			},
		},
		{
			name: "ProjectHandlerStatus",
			msg: &EventMessage{
				MessageType: &EventMessage_ProjectHandlerStatus{
					ProjectHandlerStatus: &ProjectHandlerStatus{
						EventName: "provision",
					},
				},
			},
		},
		{
			name: "SubscribeServiceEvent",
			msg: &EventMessage{
				MessageType: &EventMessage_SubscribeServiceEvent{
					SubscribeServiceEvent: &SubscribeServiceEvent{
						EventNames: []string{"deploy"},
					},
				},
			},
		},
		{
			name: "InvokeServiceHandler",
			msg: &EventMessage{
				MessageType: &EventMessage_InvokeServiceHandler{
					InvokeServiceHandler: &InvokeServiceHandler{
						EventName: "deploy",
					},
				},
			},
		},
		{
			name: "ServiceHandlerStatus",
			msg: &EventMessage{
				MessageType: &EventMessage_ServiceHandlerStatus{
					ServiceHandlerStatus: &ServiceHandlerStatus{
						EventName:   "deploy",
						ServiceName: "api",
					},
				},
			},
		},
		{
			name:    "NilMessageType",
			msg:     &EventMessage{},
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inner := env.GetInnerMessage(tt.msg)
			if tt.wantNil {
				require.Nil(t, inner)
			} else {
				require.NotNil(t, inner)
			}
		})
	}
}

func TestEventMessageEnvelope_GetRequestId_NoContext(t *testing.T) {
	env := NewEventMessageEnvelope()

	// Without extension ID in context, should return ""
	msg := &EventMessage{
		MessageType: &EventMessage_SubscribeProjectEvent{
			SubscribeProjectEvent: &SubscribeProjectEvent{
				EventNames: []string{"provision"},
			},
		},
	}

	id := env.GetRequestId(t.Context(), msg)
	require.Empty(t, id)
}

func TestEventMessageEnvelope_GetRequestId(t *testing.T) {
	env := NewEventMessageEnvelope()
	ctx := extensions.WithClaimsContext(t.Context(), &extensions.ExtensionClaims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: "test-ext"},
	})

	tests := []struct {
		name string
		msg  *EventMessage
		want string
	}{
		{
			name: "SubscribeProjectEvent",
			msg: &EventMessage{
				MessageType: &EventMessage_SubscribeProjectEvent{
					SubscribeProjectEvent: &SubscribeProjectEvent{
						EventNames: []string{"provision"},
					},
				},
			},
			want: "test-ext.provision",
		},
		{
			name: "ProjectHandlerStatus",
			msg: &EventMessage{
				MessageType: &EventMessage_ProjectHandlerStatus{
					ProjectHandlerStatus: &ProjectHandlerStatus{
						EventName: "provision",
					},
				},
			},
			want: "test-ext.provision",
		},
		{
			name: "InvokeProjectHandler",
			msg: &EventMessage{
				MessageType: &EventMessage_InvokeProjectHandler{
					InvokeProjectHandler: &InvokeProjectHandler{
						EventName: "provision",
					},
				},
			},
			want: "test-ext.provision",
		},
		{
			name: "SubscribeServiceEvent",
			msg: &EventMessage{
				MessageType: &EventMessage_SubscribeServiceEvent{
					SubscribeServiceEvent: &SubscribeServiceEvent{
						EventNames: []string{"deploy"},
					},
				},
			},
			want: "test-ext.deploy",
		},
		{
			name: "ServiceHandlerStatus",
			msg: &EventMessage{
				MessageType: &EventMessage_ServiceHandlerStatus{
					ServiceHandlerStatus: &ServiceHandlerStatus{
						EventName:   "deploy",
						ServiceName: "api",
					},
				},
			},
			want: "test-ext.api.deploy",
		},
		{
			name: "InvokeServiceHandler",
			msg: &EventMessage{
				MessageType: &EventMessage_InvokeServiceHandler{
					InvokeServiceHandler: &InvokeServiceHandler{
						EventName: "deploy",
						Service:   &ServiceConfig{Name: "api"},
					},
				},
			},
			want: "test-ext.api.deploy",
		},
		{
			name: "EmptyEventNames",
			msg: &EventMessage{
				MessageType: &EventMessage_SubscribeProjectEvent{
					SubscribeProjectEvent: &SubscribeProjectEvent{},
				},
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, env.GetRequestId(ctx, tt.msg))
		})
	}
}

func TestBetaEventMessageEnvelope_GetRequestId(t *testing.T) {
	env := NewBetaEventMessageEnvelope()
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
			msg: &v1beta.EventMessage{
				MessageType: &v1beta.EventMessage_SubscribeProjectEvent{
					SubscribeProjectEvent: &v1beta.SubscribeProjectEvent{
						EventNames: []string{"provision"},
					},
				},
			},
			want: "test-ext.provision",
		},
		{
			name: "ProjectHandlerStatus",
			msg: &v1beta.EventMessage{
				MessageType: &v1beta.EventMessage_ProjectHandlerStatus{
					ProjectHandlerStatus: &v1beta.ProjectHandlerStatus{
						EventName: "provision",
					},
				},
			},
			want: "test-ext.provision",
		},
		{
			name: "InvokeProjectHandler",
			msg: &v1beta.EventMessage{
				MessageType: &v1beta.EventMessage_InvokeProjectHandler{
					InvokeProjectHandler: &v1beta.InvokeProjectHandler{
						EventName: "provision",
					},
				},
			},
			want: "test-ext.provision",
		},
		{
			name: "SubscribeServiceEvent",
			msg: &v1beta.EventMessage{
				MessageType: &v1beta.EventMessage_SubscribeServiceEvent{
					SubscribeServiceEvent: &v1beta.SubscribeServiceEvent{
						EventNames: []string{"deploy"},
					},
				},
			},
			want: "test-ext.deploy",
		},
		{
			name: "ServiceHandlerStatus",
			msg: &v1beta.EventMessage{
				MessageType: &v1beta.EventMessage_ServiceHandlerStatus{
					ServiceHandlerStatus: &v1beta.ServiceHandlerStatus{
						EventName:   "deploy",
						ServiceName: "api",
					},
				},
			},
			want: "test-ext.api.deploy",
		},
		{
			name: "InvokeServiceHandler",
			msg: &v1beta.EventMessage{
				MessageType: &v1beta.EventMessage_InvokeServiceHandler{
					InvokeServiceHandler: &v1beta.InvokeServiceHandler{
						EventName: "deploy",
						Service:   &v1beta.ServiceConfig{Name: "api"},
					},
				},
			},
			want: "test-ext.api.deploy",
		},
		{
			name: "NilMessage",
			want: "",
		},
		{
			name: "NilService",
			msg: &v1beta.EventMessage{
				MessageType: &v1beta.EventMessage_InvokeServiceHandler{
					InvokeServiceHandler: &v1beta.InvokeServiceHandler{
						EventName: "deploy",
					},
				},
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, env.GetRequestId(ctx, tt.msg))
		})
	}
}

func TestBetaEventMessageEnvelope_RequestResponseFields(t *testing.T) {
	env := NewBetaEventMessageEnvelope()
	msg := &v1beta.EventMessage{
		MessageType: &v1beta.EventMessage_SubscribeProjectEventResponse{
			SubscribeProjectEventResponse: &v1beta.SubscribeProjectEventResponse{},
		},
	}

	env.SetRequestId(t.Context(), msg, "request-1")
	require.Equal(t, "request-1", env.GetRequestId(t.Context(), msg))
	require.NotNil(t, env.GetInnerMessage(msg))

	env.SetError(msg, errors.New("subscription failed"))
	require.ErrorContains(t, env.GetError(msg), "subscription failed")
}
