// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type betaEventStreamMode uint8

const (
	betaEventStreamLegacy betaEventStreamMode = iota
	betaEventStreamRequestIDs
)

type betaEventBidiStream = grpc.BidiStreamingServer[
	v1beta.EventMessage,
	v1beta.EventMessage,
]

type betaEventStream struct {
	betaEventBidiStream
	first *v1beta.EventMessage
	mode  betaEventStreamMode
}

func betaEventStreamModeFor(
	first *v1beta.EventMessage,
) (betaEventStreamMode, error) {
	if first == nil {
		return 0, status.Error(
			codes.InvalidArgument,
			"the first beta event message must be a project or service subscription",
		)
	}

	switch message := first.MessageType.(type) {
	case *v1beta.EventMessage_SubscribeProjectEvent:
		if message.SubscribeProjectEvent == nil {
			return 0, status.Error(
				codes.InvalidArgument,
				"the first beta event message must be a project subscription",
			)
		}
	case *v1beta.EventMessage_SubscribeServiceEvent:
		if message.SubscribeServiceEvent == nil {
			return 0, status.Error(
				codes.InvalidArgument,
				"the first beta event message must be a service subscription",
			)
		}
	default:
		return 0, status.Error(
			codes.InvalidArgument,
			"the first beta event message must be a project or service subscription",
		)
	}

	if first.RequestId == "" {
		return betaEventStreamLegacy, nil
	}
	return betaEventStreamRequestIDs, nil
}

func newBetaEventStream(
	stream betaEventBidiStream,
	first *v1beta.EventMessage,
	mode betaEventStreamMode,
) *betaEventStream {
	return &betaEventStream{
		betaEventBidiStream: stream,
		first:               first,
		mode:                mode,
	}
}

func (s *betaEventStream) Recv() (*v1beta.EventMessage, error) {
	if s.first != nil {
		first := s.first
		s.first = nil
		return first, nil
	}

	message, err := s.betaEventBidiStream.Recv()
	if err != nil {
		return nil, err
	}
	if message == nil {
		return nil, status.Error(codes.InvalidArgument, "beta event message is required")
	}

	switch s.mode {
	case betaEventStreamLegacy:
		if message.RequestId != "" {
			return nil, status.Error(
				codes.InvalidArgument,
				"request_id cannot be added after a legacy beta event subscription",
			)
		}
	case betaEventStreamRequestIDs:
		switch message.MessageType.(type) {
		case *v1beta.EventMessage_SubscribeProjectEvent,
			*v1beta.EventMessage_SubscribeServiceEvent,
			*v1beta.EventMessage_ProjectHandlerStatus:
			if message.RequestId == "" {
				return nil, status.Error(
					codes.InvalidArgument,
					"request_id is required for this beta event message",
				)
			}
		}
	default:
		return nil, fmt.Errorf("unknown beta event stream mode %d", s.mode)
	}

	return message, nil
}

func delegateLegacyBetaEventStream(
	stream *betaEventStream,
	stable azdext.EventServiceServer,
) error {
	return (&betaEventServiceAdapter{stable: stable}).EventStream(stream)
}
