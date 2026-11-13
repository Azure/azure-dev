// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
)

type EventManager struct {
	extensionId   string
	client        *AzdClient
	broker        *grpcbroker.MessageBroker[EventMessage]
	projectEvents map[string]ProjectEventHandler
	serviceEvents map[string]ServiceEventHandler
	// serviceResponseEvents holds handlers that can return structured messages.
	serviceResponseEvents map[string]ServiceEventResponseHandler
	eventsMutex           sync.RWMutex // Protects the project and service event handler maps
	brokerLogger          *log.Logger

	// Synchronization for concurrent access
	mu sync.RWMutex
}

type ProjectEventArgs struct {
	Project *ProjectConfig
}

type ServiceEventArgs struct {
	Project        *ProjectConfig
	Service        *ServiceConfig
	ServiceContext *ServiceContext
}

type ProjectEventHandler func(ctx context.Context, args *ProjectEventArgs) error

type ServiceEventHandler func(ctx context.Context, args *ServiceEventArgs) error

// ServiceEventResponse contains non-blocking messages returned by a service predeploy or postdeploy handler.
// Supported kinds are ServiceEventMessageKind_SERVICE_EVENT_MESSAGE_KIND_INFO and
// ServiceEventMessageKind_SERVICE_EVENT_MESSAGE_KIND_WARNING. Each message requires non-empty text.
type ServiceEventResponse struct {
	Messages []*ServiceEventMessage
}

// ServiceEventResponseHandler handles a service predeploy or postdeploy event and can return structured messages.
// azd 1.38.0 or later displays the messages after deployment and includes them in JSON output, even when the
// handler also returns an error. Older hosts ignore the messages.
type ServiceEventResponseHandler func(ctx context.Context, args *ServiceEventArgs) (*ServiceEventResponse, error)

func NewEventManager(extensionId string, azdClient *AzdClient, brokerLogger *log.Logger) *EventManager {
	return &EventManager{
		extensionId:   extensionId,
		client:        azdClient,
		projectEvents: make(map[string]ProjectEventHandler),
		serviceEvents: make(map[string]ServiceEventHandler),
		brokerLogger:  brokerLogger,

		serviceResponseEvents: make(map[string]ServiceEventResponseHandler),
	}
}

func (em *EventManager) Close() error {
	em.mu.Lock()
	defer em.mu.Unlock()

	if em.broker != nil {
		em.broker.Close()
		em.broker = nil
	}

	return nil
}

// ensureStream initializes the broker and stream if they haven't been created yet.
// This method is thread-safe for concurrent access.
func (em *EventManager) ensureStream(ctx context.Context) error {
	// Fast path with read lock
	em.mu.RLock()
	if em.broker != nil {
		em.mu.RUnlock()
		return nil
	}
	em.mu.RUnlock()

	// Slow path with write lock
	em.mu.Lock()
	defer em.mu.Unlock()

	// Double-check after acquiring write lock
	if em.broker != nil {
		return nil
	}
	stream, err := em.client.Events().EventStream(ctx)
	if err != nil {
		return fmt.Errorf("failed to create event stream: %w", err)
	}

	// Create broker with client stream
	envelope := NewEventMessageEnvelopeWithExtensionId(em.extensionId)
	// Use client as name since we're on the client side (extension process)
	em.broker = grpcbroker.NewMessageBroker(stream, envelope, em.extensionId, em.brokerLogger)

	// Register handlers for incoming requests
	if err := em.broker.On(em.onInvokeProjectHandler); err != nil {
		return fmt.Errorf("failed to register invoke project handler: %w", err)
	}
	if err := em.broker.On(em.onInvokeServiceHandler); err != nil {
		return fmt.Errorf("failed to register invoke service handler: %w", err)
	}

	return nil
}

// Receive starts the broker's message dispatcher and blocks until the stream completes.
// Returns nil on graceful shutdown, or an error if the stream fails.
// This method is safe for concurrent access but only allows one active Run() at a time.
// Receive starts the broker's message dispatcher and blocks until the stream completes.
// This method ensures the stream is initialized then runs the broker.
func (em *EventManager) Receive(ctx context.Context) error {
	// Ensure stream is initialized (this handles all locking internally)
	if err := em.ensureStream(ctx); err != nil {
		return err
	}

	// Run the broker (this blocks until context is canceled or error)
	return em.broker.Run(ctx)
}

// Ready blocks until the message broker starts receiving messages or the context is cancelled.
// This ensures the stream is initialized and then waits for the broker to be ready.
// Returns nil when ready, or context error if the context is cancelled before ready.
func (em *EventManager) Ready(ctx context.Context) error {
	// Ensure stream is initialized (this handles all locking internally)
	if err := em.ensureStream(ctx); err != nil {
		return err
	}

	// Now that broker is guaranteed to exist, wait for it to be ready
	return em.broker.Ready(ctx)
}

func (em *EventManager) AddProjectEventHandler(ctx context.Context, eventName string, handler ProjectEventHandler) error {
	if err := em.ensureStream(ctx); err != nil {
		return err
	}

	msg := &EventMessage{
		MessageType: &EventMessage_SubscribeProjectEvent{
			SubscribeProjectEvent: &SubscribeProjectEvent{
				EventNames: []string{eventName},
			},
		},
	}

	err := em.broker.Send(ctx, msg)
	if err != nil {
		return err
	}

	em.eventsMutex.Lock()
	defer em.eventsMutex.Unlock()
	em.projectEvents[eventName] = handler

	return nil
}

type ServiceEventOptions struct {
	Host     string
	Language string
}

func (em *EventManager) AddServiceEventHandler(
	ctx context.Context,
	eventName string,
	handler ServiceEventHandler,
	options *ServiceEventOptions,
) error {
	if err := em.ensureStream(ctx); err != nil {
		return err
	}

	if options == nil {
		options = &ServiceEventOptions{}
	}

	msg := &EventMessage{
		MessageType: &EventMessage_SubscribeServiceEvent{
			SubscribeServiceEvent: &SubscribeServiceEvent{
				EventNames: []string{eventName},
				Host:       options.Host,
				Language:   options.Language,
			},
		},
	}

	err := em.broker.Send(ctx, msg)
	if err != nil {
		return err
	}

	em.eventsMutex.Lock()
	defer em.eventsMutex.Unlock()
	em.serviceEvents[eventName] = handler

	return nil
}

// AddServiceEventResponseHandler subscribes to a service predeploy or postdeploy event with a handler that can
// return structured messages. Each event can have only one service handler.
func (em *EventManager) AddServiceEventResponseHandler(
	ctx context.Context,
	eventName string,
	handler ServiceEventResponseHandler,
	options *ServiceEventOptions,
) error {
	if err := validateServiceEventResponseName(eventName); err != nil {
		return err
	}
	if handler == nil {
		return fmt.Errorf("service event response handler for %q is nil", eventName)
	}

	em.eventsMutex.RLock()
	_, hasHandler := em.serviceEvents[eventName]
	_, hasResponseHandler := em.serviceResponseEvents[eventName]
	em.eventsMutex.RUnlock()
	if hasHandler || hasResponseHandler {
		return fmt.Errorf("service event %q is already registered", eventName)
	}

	if err := em.ensureStream(ctx); err != nil {
		return err
	}

	if options == nil {
		options = &ServiceEventOptions{}
	}

	msg := &EventMessage{
		MessageType: &EventMessage_SubscribeServiceEvent{
			SubscribeServiceEvent: &SubscribeServiceEvent{
				EventNames: []string{eventName},
				Host:       options.Host,
				Language:   options.Language,
			},
		},
	}

	if err := em.broker.Send(ctx, msg); err != nil {
		return err
	}

	em.eventsMutex.Lock()
	defer em.eventsMutex.Unlock()
	em.serviceResponseEvents[eventName] = handler

	return nil
}

func (em *EventManager) RemoveProjectEventHandler(eventName string) {
	em.eventsMutex.Lock()
	defer em.eventsMutex.Unlock()
	delete(em.projectEvents, eventName)
}

func (em *EventManager) RemoveServiceEventHandler(eventName string) {
	em.eventsMutex.Lock()
	defer em.eventsMutex.Unlock()
	delete(em.serviceEvents, eventName)
	delete(em.serviceResponseEvents, eventName)
}

// Handler methods - these are registered with the broker to handle incoming requests

// onInvokeProjectHandler handles project event invocations from the server
func (em *EventManager) onInvokeProjectHandler(
	ctx context.Context,
	req *InvokeProjectHandler,
) (*EventMessage, error) {
	em.eventsMutex.RLock()
	defer em.eventsMutex.RUnlock()
	handler, exists := em.projectEvents[req.EventName]

	if !exists {
		// No handler registered, return empty response (not an error)
		return &EventMessage{}, nil
	}

	args := &ProjectEventArgs{
		Project: req.Project,
	}

	handlerStatus := "completed"
	handlerMessage := ""
	var handlerError *ExtensionError

	// Call the project event handler
	handlerErr := handlerErrorWithContextCause(ctx, handler(ctx, args))
	if handlerErr != nil {
		handlerStatus = "failed"
		handlerMessage = handlerErr.Error()
		handlerError = WrapError(handlerErr)
		log.Printf("invokeProjectHandler error for event %s: %v", req.EventName, handlerErr)
	}

	// Return status message
	return &EventMessage{
		MessageType: &EventMessage_ProjectHandlerStatus{
			ProjectHandlerStatus: &ProjectHandlerStatus{
				EventName: req.EventName,
				Status:    handlerStatus,
				Message:   handlerMessage,
				Error:     handlerError,
			},
		},
	}, nil
}

// onInvokeServiceHandler handles service event invocations from the server
func (em *EventManager) onInvokeServiceHandler(
	ctx context.Context,
	req *InvokeServiceHandler,
) (*EventMessage, error) {
	em.eventsMutex.RLock()
	defer em.eventsMutex.RUnlock()
	handler, exists := em.serviceEvents[req.EventName]
	responseHandler, hasResponseHandler := em.serviceResponseEvents[req.EventName]

	if !exists && !hasResponseHandler {
		// No handler registered, return empty response (not an error)
		return &EventMessage{}, nil
	}

	// Extract ServiceContext from the message, default to empty instance if nil
	serviceContext := req.ServiceContext
	if serviceContext == nil {
		serviceContext = &ServiceContext{}
	}

	args := &ServiceEventArgs{
		Project:        req.Project,
		Service:        req.Service,
		ServiceContext: serviceContext,
	}

	handlerStatus := "completed"
	handlerMessage := ""
	var handlerError *ExtensionError

	var messages []*ServiceEventMessage
	var handlerErr error
	if hasResponseHandler {
		var response *ServiceEventResponse
		response, handlerErr = responseHandler(ctx, args)
		messages, handlerErr = validServiceEventMessages(response, handlerErr)
	} else {
		handlerErr = handler(ctx, args)
	}

	// Call the service event handler
	handlerErr = handlerErrorWithContextCause(ctx, handlerErr)
	if handlerErr != nil {
		handlerStatus = "failed"
		handlerMessage = handlerErr.Error()
		handlerError = WrapError(handlerErr)
		log.Printf("invokeServiceHandler error for event %s: %v", req.EventName, handlerErr)
	}

	// Return status message
	return &EventMessage{
		MessageType: &EventMessage_ServiceHandlerStatus{
			ServiceHandlerStatus: &ServiceHandlerStatus{
				EventName:   req.EventName,
				ServiceName: req.Service.Name,
				Status:      handlerStatus,
				Message:     handlerMessage,
				Error:       handlerError,
				Messages:    messages,
			},
		},
	}, nil
}

func validateServiceEventResponseName(eventName string) error {
	if eventName != "predeploy" && eventName != "postdeploy" {
		return fmt.Errorf(
			"service event response handler %q is not supported; use predeploy or postdeploy",
			eventName,
		)
	}
	return nil
}

// validServiceEventMessages returns the valid messages and joins validation failures to the handler error.
func validServiceEventMessages(
	response *ServiceEventResponse,
	handlerErr error,
) ([]*ServiceEventMessage, error) {
	if response == nil {
		return nil, handlerErr
	}

	var messages []*ServiceEventMessage
	for index, message := range response.Messages {
		if err := validateServiceEventMessage(message); err != nil {
			handlerErr = errors.Join(handlerErr, fmt.Errorf("message %d: %w", index+1, err))
			continue
		}
		messages = append(messages, message)
	}
	return messages, handlerErr
}

func validateServiceEventMessage(message *ServiceEventMessage) error {
	if message == nil {
		return errors.New("message is nil")
	}
	switch message.GetKind() {
	case ServiceEventMessageKind_SERVICE_EVENT_MESSAGE_KIND_INFO,
		ServiceEventMessageKind_SERVICE_EVENT_MESSAGE_KIND_WARNING:
	default:
		return fmt.Errorf("unsupported message kind %s", message.GetKind())
	}
	if strings.TrimSpace(message.GetMessage()) == "" {
		return errors.New("message text is required")
	}
	for index, link := range message.GetLinks() {
		if link == nil || strings.TrimSpace(link.GetUrl()) == "" {
			return fmt.Errorf("link %d URL is required", index+1)
		}
	}
	return nil
}

func handlerErrorWithContextCause(ctx context.Context, handlerErr error) error {
	ctxErr := ctx.Err()
	if ctxErr == nil {
		return handlerErr
	}

	cause := context.Cause(ctx)
	if cause == nil {
		cause = ctxErr
	}
	if handlerErr == nil || errors.Is(handlerErr, ctxErr) {
		return cause
	}

	return handlerErr
}
