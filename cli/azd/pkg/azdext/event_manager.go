// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"fmt"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/google/uuid"
)

type EventManager struct {
	extensionId   string
	client        *AzdClient
	broker        *grpcbroker.MessageBroker[EventMessage]
	projectEvents map[string]ProjectEventHandler
	serviceEvents map[string]ServiceEventHandler
	eventsMutex   sync.RWMutex // Protects both projectEvents and serviceEvents maps

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

func NewEventManager(extensionId string, azdClient *AzdClient) *EventManager {
	return &EventManager{
		extensionId:   extensionId,
		client:        azdClient,
		projectEvents: make(map[string]ProjectEventHandler),
		serviceEvents: make(map[string]ServiceEventHandler),
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
	envelope := &EventMessageEnvelope{}
	// Use client as name since we're on the client side (extension process)
	em.broker = grpcbroker.NewMessageBroker(stream, envelope, em.extensionId)

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
		RequestId: uuid.NewString(),
		MessageType: &EventMessage_SubscribeProjectEventRequest{
			SubscribeProjectEventRequest: &SubscribeProjectEventRequest{
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
		RequestId: uuid.NewString(),
		MessageType: &EventMessage_SubscribeServiceEventRequest{
			SubscribeServiceEventRequest: &SubscribeServiceEventRequest{
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

func (em *EventManager) RemoveProjectEventHandler(eventName string) {
	em.eventsMutex.Lock()
	defer em.eventsMutex.Unlock()
	delete(em.projectEvents, eventName)
}

func (em *EventManager) RemoveServiceEventHandler(eventName string) {
	em.eventsMutex.Lock()
	defer em.eventsMutex.Unlock()
	delete(em.serviceEvents, eventName)
}

// Handler methods - these are registered with the broker to handle incoming requests

// onInvokeProjectHandler handles project event invocations from the server
func (em *EventManager) onInvokeProjectHandler(
	ctx context.Context,
	req *InvokeProjectHandlerRequest,
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

	// Call the project event handler
	err := handler(ctx, args)
	if err != nil {
		return nil, err
	}

	// Return status message
	return &EventMessage{
		MessageType: &EventMessage_InvokeProjectHandlerResponse{
			InvokeProjectHandlerResponse: &InvokeProjectHandlerResponse{},
		},
	}, err
}

// onInvokeServiceHandler handles service event invocations from the server
func (em *EventManager) onInvokeServiceHandler(
	ctx context.Context,
	req *InvokeServiceHandlerRequest,
) (*EventMessage, error) {
	em.eventsMutex.RLock()
	defer em.eventsMutex.RUnlock()
	handler, exists := em.serviceEvents[req.EventName]

	if !exists {
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

	// Call the service event handler
	err := handler(ctx, args)
	if err != nil {
		return nil, err
	}

	// Return status message
	return &EventMessage{
		MessageType: &EventMessage_InvokeServiceHandlerResponse{
			InvokeServiceHandlerResponse: &InvokeServiceHandlerResponse{},
		},
	}, err
}
