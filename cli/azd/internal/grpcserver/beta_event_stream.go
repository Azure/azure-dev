// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"fmt"
	"sync"

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
	first               *v1beta.EventMessage
	mode                betaEventStreamMode
	serviceCorrelations *betaServiceEventCorrelations
}

type betaServiceEventKey struct {
	serviceName string
	eventName   string
}

type betaServiceEventCorrelations struct {
	mu         sync.Mutex
	pending    map[betaServiceEventKey]map[string]struct{}
	requestIDs map[string]betaServiceEventKey
	ambiguous  map[betaServiceEventKey]struct{}
}

func newBetaServiceEventCorrelations() *betaServiceEventCorrelations {
	return &betaServiceEventCorrelations{
		pending:    make(map[betaServiceEventKey]map[string]struct{}),
		requestIDs: make(map[string]betaServiceEventKey),
		ambiguous:  make(map[betaServiceEventKey]struct{}),
	}
}

func (c *betaServiceEventCorrelations) register(
	serviceName string,
	eventName string,
	requestID string,
) error {
	if requestID == "" {
		return fmt.Errorf("service event request_id is required")
	}

	key := betaServiceEventKey{serviceName: serviceName, eventName: eventName}
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.requestIDs[requestID]; exists {
		return fmt.Errorf("duplicate service event request_id %q", requestID)
	}
	if c.pending[key] == nil {
		c.pending[key] = make(map[string]struct{})
	}
	c.pending[key][requestID] = struct{}{}
	c.requestIDs[requestID] = key
	return nil
}

func (c *betaServiceEventCorrelations) finish(requestID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.removeLocked(requestID)
}

func (c *betaServiceEventCorrelations) abandon(requestID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key, exists := c.requestIDs[requestID]
	if !exists {
		return
	}
	c.removeLocked(requestID)
	c.ambiguous[key] = struct{}{}
}

func (c *betaServiceEventCorrelations) removeLocked(requestID string) {
	key, exists := c.requestIDs[requestID]
	if !exists {
		return
	}
	delete(c.requestIDs, requestID)
	delete(c.pending[key], requestID)
	if len(c.pending[key]) == 0 {
		delete(c.pending, key)
	}
}

func (c *betaServiceEventCorrelations) correlate(
	message *v1beta.EventMessage,
) error {
	serviceStatus := message.GetServiceHandlerStatus()
	if serviceStatus == nil {
		return nil
	}
	if message.GetRequestId() != "" {
		c.finish(message.GetRequestId())
		return nil
	}

	key := betaServiceEventKey{
		serviceName: serviceStatus.GetServiceName(),
		eventName:   serviceStatus.GetEventName(),
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	pending := c.pending[key]
	if len(pending) == 0 {
		return nil
	}
	if _, ambiguous := c.ambiguous[key]; ambiguous {
		return status.Errorf(
			codes.InvalidArgument,
			"service handler status for %s.%s must include request_id after an invocation was canceled",
			key.serviceName,
			key.eventName,
		)
	}
	if len(pending) != 1 {
		return status.Errorf(
			codes.InvalidArgument,
			"service handler status for %s.%s must include request_id when multiple invocations are outstanding",
			key.serviceName,
			key.eventName,
		)
	}

	for requestID := range pending {
		message.RequestId = requestID
		c.removeLocked(requestID)
	}
	return nil
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
		serviceCorrelations: newBetaServiceEventCorrelations(),
	}
}

func (s *betaEventStream) Recv() (*v1beta.EventMessage, error) {
	if s.first != nil {
		first := s.first
		s.first = nil
		if err := s.validateIncoming(first); err != nil {
			return nil, err
		}
		return first, nil
	}

	message, err := s.betaEventBidiStream.Recv()
	if err != nil {
		return nil, err
	}
	if err := s.validateIncoming(message); err != nil {
		return nil, err
	}
	return message, nil
}

func (s *betaEventStream) validateIncoming(message *v1beta.EventMessage) error {
	if err := validateBetaEventMessage(message, s.mode); err != nil {
		return err
	}
	if s.mode == betaEventStreamRequestIDs {
		return s.serviceCorrelations.correlate(message)
	}
	return nil
}

func validateBetaEventMessage(message *v1beta.EventMessage, mode betaEventStreamMode) error {
	if message == nil {
		return status.Error(codes.InvalidArgument, "beta event message is required")
	}

	switch mode {
	case betaEventStreamLegacy:
		if message.RequestId != "" {
			return status.Error(
				codes.InvalidArgument,
				"request_id cannot be added after a legacy beta event subscription",
			)
		}
		if message.GetError() != nil {
			return status.Error(codes.InvalidArgument, "beta protocol fields require a new stream")
		}
		if message.GetServiceHandlerStatus() != nil &&
			len(message.GetServiceHandlerStatus().GetMessages()) > 0 {
			return status.Error(
				codes.InvalidArgument,
				"structured service event messages require a new beta event stream",
			)
		}
	case betaEventStreamRequestIDs:
		switch message.MessageType.(type) {
		case *v1beta.EventMessage_SubscribeProjectEvent,
			*v1beta.EventMessage_SubscribeServiceEvent,
			*v1beta.EventMessage_ProjectHandlerStatus:
			if message.RequestId == "" {
				return status.Error(
					codes.InvalidArgument,
					"request_id is required for this beta event message",
				)
			}
		}
		if message.GetServiceHandlerStatus() != nil &&
			len(message.GetServiceHandlerStatus().GetMessages()) > 0 &&
			message.RequestId == "" {
			return status.Error(
				codes.InvalidArgument,
				"request_id is required for structured service event messages",
			)
		}
		if message.GetError() != nil {
			return status.Error(codes.InvalidArgument, "extensions cannot send top-level event errors")
		}
	default:
		return fmt.Errorf("unknown beta event stream mode %d", mode)
	}

	switch content := message.GetMessageType().(type) {
	case *v1beta.EventMessage_SubscribeProjectEvent:
		if content.SubscribeProjectEvent != nil {
			return nil
		}
	case *v1beta.EventMessage_SubscribeServiceEvent:
		if content.SubscribeServiceEvent != nil {
			return nil
		}
	case *v1beta.EventMessage_ProjectHandlerStatus:
		if content.ProjectHandlerStatus != nil {
			return nil
		}
	case *v1beta.EventMessage_ServiceHandlerStatus:
		if content.ServiceHandlerStatus != nil {
			return nil
		}
	default:
		return status.Error(codes.InvalidArgument, "invalid message for a beta event client")
	}
	return status.Error(codes.InvalidArgument, "event message payload is required")
}

func delegateLegacyBetaEventStream(
	stream *betaEventStream,
	stable azdext.EventServiceServer,
) error {
	return (&betaEventServiceAdapter{stable: stable}).EventStream(stream)
}
