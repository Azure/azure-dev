// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"fmt"
	"log"

	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
)

type EventManager struct {
	client        *AzdClient
	broker        *grpcbroker.MessageBroker[EventMessage]
	projectEvents map[string]ProjectEventHandler
	serviceEvents map[string]ServiceEventHandler
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

func NewEventManager(azdClient *AzdClient) *EventManager {
	return &EventManager{
		client:        azdClient,
		projectEvents: make(map[string]ProjectEventHandler),
		serviceEvents: make(map[string]ServiceEventHandler),
	}
}

func (em *EventManager) Close() error {
	if em.broker != nil {
		em.broker.Close()
	}

	return nil
}

// ensureStream initializes the broker and stream if they haven't been created yet.
func (em *EventManager) ensureStream(ctx context.Context) error {
	if em.broker == nil {
		stream, err := em.client.Events().EventStream(ctx)
		if err != nil {
			return fmt.Errorf("failed to create event stream: %w", err)
		}

		// Create broker with client stream
		envelope := &EventMessageEnvelope{}
		em.broker = grpcbroker.NewMessageBroker(stream, envelope)

		// Register handlers for incoming requests
		if err := em.broker.On(em.onInvokeProjectHandler); err != nil {
			return fmt.Errorf("failed to register invoke project handler: %w", err)
		}
		if err := em.broker.On(em.onInvokeServiceHandler); err != nil {
			return fmt.Errorf("failed to register invoke service handler: %w", err)
		}
	}

	return nil
}

// Receive starts the broker's message dispatcher and blocks until the stream completes.
// Returns nil on graceful shutdown, or an error if the stream fails.
func (em *EventManager) Receive(ctx context.Context) error {
	if err := em.ensureStream(ctx); err != nil {
		return err
	}

	return em.broker.Run(ctx)
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

	em.serviceEvents[eventName] = handler

	return nil
}

func (em *EventManager) RemoveProjectEventHandler(eventName string) {
	delete(em.projectEvents, eventName)
}

func (em *EventManager) RemoveServiceEventHandler(eventName string) {
	delete(em.serviceEvents, eventName)
}

// Handler methods - these are registered with the broker to handle incoming requests

// onInvokeProjectHandler handles project event invocations from the server
func (em *EventManager) onInvokeProjectHandler(
	ctx context.Context,
	req *InvokeProjectHandler,
) (*EventMessage, error) {
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

	// Call the project event handler
	err := handler(ctx, args)
	if err != nil {
		handlerStatus = "failed"
		handlerMessage = err.Error()
		log.Printf("invokeProjectHandler error for event %s: %v", req.EventName, err)
	}

	// Return status message
	return &EventMessage{
		MessageType: &EventMessage_ProjectHandlerStatus{
			ProjectHandlerStatus: &ProjectHandlerStatus{
				EventName: req.EventName,
				Status:    handlerStatus,
				Message:   handlerMessage,
			},
		},
	}, nil
}

// onInvokeServiceHandler handles service event invocations from the server
func (em *EventManager) onInvokeServiceHandler(
	ctx context.Context,
	req *InvokeServiceHandler,
) (*EventMessage, error) {
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

	handlerStatus := "completed"
	handlerMessage := ""

	// Call the service event handler
	err := handler(ctx, args)
	if err != nil {
		handlerStatus = "failed"
		handlerMessage = err.Error()
		log.Printf("invokeServiceHandler error for event %s: %v", req.EventName, err)
	}

	// Return status message
	return &EventMessage{
		MessageType: &EventMessage_ServiceHandlerStatus{
			ServiceHandlerStatus: &ServiceHandlerStatus{
				EventName:   req.EventName,
				ServiceName: req.Service.Name,
				Status:      handlerStatus,
				Message:     handlerMessage,
			},
		},
	}, nil
}
