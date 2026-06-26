// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"testing"

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
