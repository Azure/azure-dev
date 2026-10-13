// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"io"
	"testing"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestBetaEventStreamModeForFirstSubscription(t *testing.T) {
	tests := []struct {
		name    string
		first   *v1beta.EventMessage
		want    betaEventStreamMode
		wantErr codes.Code
	}{
		{
			name: "legacy project subscription",
			first: &v1beta.EventMessage{
				MessageType: &v1beta.EventMessage_SubscribeProjectEvent{
					SubscribeProjectEvent: &v1beta.SubscribeProjectEvent{
						EventNames: []string{"postdeploy"},
					},
				},
			},
			want: betaEventStreamLegacy,
		},
		{
			name: "request ID service subscription",
			first: &v1beta.EventMessage{
				RequestId: "subscribe-1",
				MessageType: &v1beta.EventMessage_SubscribeServiceEvent{
					SubscribeServiceEvent: &v1beta.SubscribeServiceEvent{
						EventNames: []string{"prepackage"},
					},
				},
			},
			want: betaEventStreamRequestIDs,
		},
		{
			name:    "nil message",
			wantErr: codes.InvalidArgument,
		},
		{
			name: "first message is not a subscription",
			first: &v1beta.EventMessage{
				MessageType: &v1beta.EventMessage_ProjectHandlerStatus{
					ProjectHandlerStatus: &v1beta.ProjectHandlerStatus{
						EventName: "postdeploy",
						Status:    "completed",
					},
				},
			},
			wantErr: codes.InvalidArgument,
		},
		{
			name: "nil subscription",
			first: &v1beta.EventMessage{
				MessageType: &v1beta.EventMessage_SubscribeProjectEvent{},
			},
			wantErr: codes.InvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mode, err := betaEventStreamModeFor(tt.first)
			if tt.wantErr != codes.OK {
				require.Equal(t, tt.wantErr, status.Code(err))
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.want, mode)
		})
	}
}

func TestBetaEventStreamReplaysFirstMessageAndRejectsModeChanges(t *testing.T) {
	first := &v1beta.EventMessage{
		MessageType: &v1beta.EventMessage_SubscribeProjectEvent{
			SubscribeProjectEvent: &v1beta.SubscribeProjectEvent{
				EventNames: []string{"postdeploy"},
			},
		},
	}
	second := &v1beta.EventMessage{
		MessageType: &v1beta.EventMessage_ProjectHandlerStatus{
			ProjectHandlerStatus: &v1beta.ProjectHandlerStatus{
				EventName: "postdeploy",
				Status:    "completed",
			},
		},
	}
	source := &scriptedBetaEventStream{
		ctx:    t.Context(),
		recvCh: make(chan *v1beta.EventMessage, 2),
	}
	source.recvCh <- second

	stream := newBetaEventStream(source, first, betaEventStreamLegacy)
	message, err := stream.Recv()
	require.NoError(t, err)
	require.Same(t, first, message)

	message, err = stream.Recv()
	require.NoError(t, err)
	require.Same(t, second, message)

	source.recvCh <- &v1beta.EventMessage{
		RequestId: "request-1",
		MessageType: &v1beta.EventMessage_ProjectHandlerStatus{
			ProjectHandlerStatus: &v1beta.ProjectHandlerStatus{
				EventName: "postdeploy",
				Status:    "completed",
			},
		},
	}
	message, err = stream.Recv()
	require.Nil(t, message)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestBetaEventStreamRequestIDModeValidation(t *testing.T) {
	tests := []struct {
		name    string
		message *v1beta.EventMessage
		wantErr bool
	}{
		{
			name: "project status with request ID",
			message: &v1beta.EventMessage{
				RequestId: "invocation-1",
				MessageType: &v1beta.EventMessage_ProjectHandlerStatus{
					ProjectHandlerStatus: &v1beta.ProjectHandlerStatus{
						EventName: "postdeploy",
						Status:    "completed",
					},
				},
			},
		},
		{
			name: "service status with request ID",
			message: &v1beta.EventMessage{
				RequestId: "request-1",
				MessageType: &v1beta.EventMessage_ServiceHandlerStatus{
					ServiceHandlerStatus: &v1beta.ServiceHandlerStatus{
						EventName:   "prepackage",
						ServiceName: "api",
						Status:      "completed",
					},
				},
			},
		},
		{
			name: "service status without request ID",
			message: &v1beta.EventMessage{
				MessageType: &v1beta.EventMessage_ServiceHandlerStatus{
					ServiceHandlerStatus: &v1beta.ServiceHandlerStatus{
						EventName:   "prepackage",
						ServiceName: "api",
						Status:      "completed",
					},
				},
			},
		},
		{
			name: "structured service message requires request ID",
			message: &v1beta.EventMessage{
				MessageType: &v1beta.EventMessage_ServiceHandlerStatus{
					ServiceHandlerStatus: &v1beta.ServiceHandlerStatus{
						EventName:   "predeploy",
						ServiceName: "api",
						Status:      "completed",
						Messages: []*v1beta.ServiceEventMessage{{
							Kind:    v1beta.ServiceEventMessageKind_SERVICE_EVENT_MESSAGE_KIND_WARNING,
							Message: "service warning",
						}},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "structured service message with request ID",
			message: &v1beta.EventMessage{
				RequestId: "invocation-1",
				MessageType: &v1beta.EventMessage_ServiceHandlerStatus{
					ServiceHandlerStatus: &v1beta.ServiceHandlerStatus{
						EventName:   "predeploy",
						ServiceName: "api",
						Status:      "completed",
						Messages: []*v1beta.ServiceEventMessage{{
							Kind:    v1beta.ServiceEventMessageKind_SERVICE_EVENT_MESSAGE_KIND_WARNING,
							Message: "service warning",
						}},
					},
				},
			},
		},
		{
			name: "project status without request ID",
			message: &v1beta.EventMessage{
				MessageType: &v1beta.EventMessage_ProjectHandlerStatus{
					ProjectHandlerStatus: &v1beta.ProjectHandlerStatus{
						EventName: "postdeploy",
						Status:    "completed",
					},
				},
			},
			wantErr: true,
		},
		{
			name: "subscription without request ID",
			message: &v1beta.EventMessage{
				MessageType: &v1beta.EventMessage_SubscribeServiceEvent{
					SubscribeServiceEvent: &v1beta.SubscribeServiceEvent{
						EventNames: []string{"prepackage"},
					},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &scriptedBetaEventStream{
				ctx:    t.Context(),
				recvCh: make(chan *v1beta.EventMessage, 1),
			}
			source.recvCh <- tt.message

			stream := newBetaEventStream(
				source,
				&v1beta.EventMessage{
					RequestId: "subscribe-1",
					MessageType: &v1beta.EventMessage_SubscribeProjectEvent{
						SubscribeProjectEvent: &v1beta.SubscribeProjectEvent{
							EventNames: []string{"postdeploy"},
						},
					},
				},
				betaEventStreamRequestIDs,
			)
			_, err := stream.Recv()
			require.NoError(t, err)

			_, err = stream.Recv()
			if tt.wantErr {
				require.Equal(t, codes.InvalidArgument, status.Code(err))
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestBetaEventStreamCorrelatesUnambiguousIDlessServiceStatuses(t *testing.T) {
	tests := []struct {
		name          string
		registerIDs   []string
		abandonIDs    []string
		wantRequestID string
		wantError     codes.Code
	}{
		{
			name:          "single outstanding invocation",
			registerIDs:   []string{"request-1"},
			wantRequestID: "request-1",
		},
		{
			name: "no outstanding invocation",
		},
		{
			name:        "multiple outstanding invocations",
			registerIDs: []string{"request-1", "request-2"},
			wantError:   codes.InvalidArgument,
		},
		{
			name:        "canceled invocation makes fallback ambiguous",
			registerIDs: []string{"canceled", "request-2"},
			abandonIDs:  []string{"canceled"},
			wantError:   codes.InvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &scriptedBetaEventStream{
				ctx:    t.Context(),
				recvCh: make(chan *v1beta.EventMessage, 1),
			}
			source.recvCh <- &v1beta.EventMessage{
				MessageType: &v1beta.EventMessage_ServiceHandlerStatus{
					ServiceHandlerStatus: &v1beta.ServiceHandlerStatus{
						EventName:   "predeploy",
						ServiceName: "api",
						Status:      "completed",
					},
				},
			}
			stream := newBetaEventStream(
				source,
				&v1beta.EventMessage{
					RequestId: "subscribe-1",
					MessageType: &v1beta.EventMessage_SubscribeServiceEvent{
						SubscribeServiceEvent: &v1beta.SubscribeServiceEvent{
							EventNames: []string{"predeploy"},
						},
					},
				},
				betaEventStreamRequestIDs,
			)

			for _, requestID := range tt.registerIDs {
				require.NoError(t, stream.serviceCorrelations.register(
					"api",
					"predeploy",
					requestID,
				))
			}
			for _, requestID := range tt.abandonIDs {
				stream.serviceCorrelations.abandon(requestID)
			}

			_, err := stream.Recv()
			require.NoError(t, err)
			message, err := stream.Recv()
			if tt.wantError != codes.OK {
				require.Equal(t, tt.wantError, status.Code(err))
				require.Empty(t, message)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.wantRequestID, message.GetRequestId())
		})
	}
}

func TestBetaEventStreamDoesNotReuseCanceledRequestIDForLateResponse(t *testing.T) {
	source := &scriptedBetaEventStream{
		ctx:    t.Context(),
		recvCh: make(chan *v1beta.EventMessage, 2),
	}
	first := &v1beta.EventMessage{
		RequestId: "subscribe-1",
		MessageType: &v1beta.EventMessage_SubscribeServiceEvent{
			SubscribeServiceEvent: &v1beta.SubscribeServiceEvent{
				EventNames: []string{"predeploy"},
			},
		},
	}
	stream := newBetaEventStream(source, first, betaEventStreamRequestIDs)
	require.NoError(t, stream.serviceCorrelations.register("api", "predeploy", "canceled"))
	stream.serviceCorrelations.abandon("canceled")
	require.NoError(t, stream.serviceCorrelations.register("api", "predeploy", "current"))

	for _, requestID := range []string{"canceled", "current"} {
		source.recvCh <- &v1beta.EventMessage{
			RequestId: requestID,
			MessageType: &v1beta.EventMessage_ServiceHandlerStatus{
				ServiceHandlerStatus: &v1beta.ServiceHandlerStatus{
					EventName:   "predeploy",
					ServiceName: "api",
					Status:      "completed",
				},
			},
		}
	}

	_, err := stream.Recv()
	require.NoError(t, err)
	lateResponse, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "canceled", lateResponse.GetRequestId())
	currentResponse, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "current", currentResponse.GetRequestId())
}

type initialErrorBetaEventStream struct {
	*scriptedBetaEventStream
	err error
}

func (s *initialErrorBetaEventStream) Recv() (*v1beta.EventMessage, error) {
	return nil, s.err
}

func TestBetaEventServiceInitialStreamTermination(t *testing.T) {
	tests := []struct {
		name      string
		streamErr error
		wantErr   codes.Code
	}{
		{name: "end of stream", streamErr: io.EOF},
		{name: "context cancellation", streamErr: context.Canceled},
		{
			name:      "receive error",
			streamErr: status.Error(codes.Unavailable, "connection closed"),
			wantErr:   codes.Unavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, _ := createTestEventService()
			extension := createTestExtension()
			extension.Capabilities = []extensions.CapabilityType{
				extensions.LifecycleEventsCapability,
			}
			service.extensionManager = testExtensionLookup{extension: extension}

			stream := &initialErrorBetaEventStream{
				scriptedBetaEventStream: &scriptedBetaEventStream{
					ctx: extensionClaimsContext(t.Context(), extension.Id),
				},
				err: tt.streamErr,
			}
			err := (&betaEventService{service: service}).EventStream(stream)
			if tt.wantErr == codes.OK {
				require.NoError(t, err)
				return
			}
			require.Equal(t, tt.wantErr, status.Code(err))
		})
	}
}

func TestBetaEventServiceRejectsNonSubscriptionAsFirstMessage(t *testing.T) {
	service, _ := createTestEventService()
	extension := createTestExtension()
	extension.Capabilities = []extensions.CapabilityType{
		extensions.LifecycleEventsCapability,
	}
	service.extensionManager = testExtensionLookup{extension: extension}
	stream := &scriptedBetaEventStream{
		ctx:    extensionClaimsContext(t.Context(), extension.Id),
		recvCh: make(chan *v1beta.EventMessage, 1),
	}
	stream.recvCh <- &v1beta.EventMessage{
		MessageType: &v1beta.EventMessage_ProjectHandlerStatus{
			ProjectHandlerStatus: &v1beta.ProjectHandlerStatus{
				EventName: "postdeploy",
				Status:    "completed",
			},
		},
	}

	err := (&betaEventService{service: service}).EventStream(stream)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

var _ grpc.BidiStreamingServer[v1beta.EventMessage, v1beta.EventMessage] = (*scriptedBetaEventStream)(nil)
