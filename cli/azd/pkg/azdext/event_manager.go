// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"io"
	"log"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type EventManager struct {
	azdClient     *AzdClient
	stream        grpc.BidiStreamingClient[EventMessage, EventMessage]
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
		azdClient:     azdClient,
		projectEvents: make(map[string]ProjectEventHandler),
		serviceEvents: make(map[string]ServiceEventHandler),
	}
}

func (em *EventManager) Close() error {
	if em.stream != nil {
		return em.stream.CloseSend()
	}

	return nil
}

func (em *EventManager) init(ctx context.Context) error {
	if em.stream == nil {
		eventStream, err := em.azdClient.Events().EventStream(ctx)
		if err != nil {
			return err
		}

		em.stream = eventStream
	}

	return nil
}

func (em *EventManager) Receive(ctx context.Context) error {
	if err := em.init(ctx); err != nil {
		return err
	}

	if err := em.sendReadyEvent(); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			log.Println("Context cancelled by caller, exiting receiveEvents")
			return nil
		default:
			msg, err := em.stream.Recv()
			if err != nil {
				if errors.Is(err, io.EOF) {
					log.Println("Stream closed by server (EOF), treating as expected")
					return nil
				}

				if st, ok := status.FromError(err); ok {
					if st.Code() == codes.Unavailable {
						log.Println("Stream closed by server (unavailable), treating as expected")
						return nil
					}
				}

				return err
			}

			switch msg.MessageType.(type) {
			case *EventMessage_InvokeProjectHandler:
				invokeMsg := msg.GetInvokeProjectHandler()
				if err := em.invokeProjectHandler(ctx, invokeMsg); err != nil {
					log.Printf("invokeProjectHandler error for event %s: %v", invokeMsg.EventName, err)
				}
			case *EventMessage_InvokeServiceHandler:
				invokeMsg := msg.GetInvokeServiceHandler()
				if err := em.invokeServiceHandler(ctx, invokeMsg); err != nil {
					log.Printf("invokeServiceHandler error for event %s: %v", invokeMsg.EventName, err)
				}
			default:
				log.Printf("receiveEvents: unhandled message type %T", msg.MessageType)
			}
		}
	}
}

func (em *EventManager) AddProjectEventHandler(ctx context.Context, eventName string, handler ProjectEventHandler) error {
	if err := em.init(ctx); err != nil {
		return err
	}

	err := em.stream.Send(&EventMessage{
		MessageType: &EventMessage_SubscribeProjectEvent{
			SubscribeProjectEvent: &SubscribeProjectEvent{
				EventNames: []string{eventName},
			},
		},
	})

	em.projectEvents[eventName] = handler

	return err
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
	if err := em.init(ctx); err != nil {
		return err
	}

	if options == nil {
		options = &ServiceEventOptions{}
	}

	err := em.stream.Send(&EventMessage{
		MessageType: &EventMessage_SubscribeServiceEvent{
			SubscribeServiceEvent: &SubscribeServiceEvent{
				EventNames: []string{eventName},
				Host:       options.Host,
				Language:   options.Language,
			},
		},
	})

	em.serviceEvents[eventName] = handler

	return err
}

func (em *EventManager) RemoveProjectEventHandler(eventName string) {
	delete(em.projectEvents, eventName)
}

func (em *EventManager) RemoveServiceEventHandler(eventName string) {
	delete(em.serviceEvents, eventName)
}

func (em EventManager) sendReadyEvent() error {
	return em.stream.Send(&EventMessage{
		MessageType: &EventMessage_ExtensionReadyEvent{
			ExtensionReadyEvent: &ExtensionReadyEvent{
				Status: "ready",
			},
		},
	})
}

// New helper to send project handler status.
func (em *EventManager) sendProjectHandlerStatus(eventName, status, message string) error {
	return em.stream.Send(&EventMessage{
		MessageType: &EventMessage_ProjectHandlerStatus{
			ProjectHandlerStatus: &ProjectHandlerStatus{
				EventName: eventName,
				Status:    status,
				Message:   message,
			},
		},
	})
}

// New helper to send service handler status.
func (em *EventManager) sendServiceHandlerStatus(eventName, serviceName, status, message string) error {
	return em.stream.Send(&EventMessage{
		MessageType: &EventMessage_ServiceHandlerStatus{
			ServiceHandlerStatus: &ServiceHandlerStatus{
				EventName:   eventName,
				ServiceName: serviceName,
				Status:      status,
				Message:     message,
			},
		},
	})
}

func (em *EventManager) invokeProjectHandler(ctx context.Context, invokeMsg *InvokeProjectHandler) error {
	handler, exists := em.projectEvents[invokeMsg.EventName]
	if !exists {
		return nil
	}

	args := &ProjectEventArgs{
		Project: invokeMsg.Project,
	}

	status := "completed"
	message := ""

	// Call the project event handler.
	err := handler(ctx, args)
	if err != nil {
		status = "failed"
		message = err.Error()
		log.Printf("invokeProjectHandler error for event %s: %v", invokeMsg.EventName, err)
	}

	// Use helper to send completion status.
	return em.sendProjectHandlerStatus(invokeMsg.EventName, status, message)
}

func (em *EventManager) invokeServiceHandler(ctx context.Context, invokeMsg *InvokeServiceHandler) error {
	handler, exists := em.serviceEvents[invokeMsg.EventName]
	if !exists {
		return nil
	}

	// Extract ServiceContext from the message, default to empty instance if nil
	serviceContext := invokeMsg.ServiceContext
	if serviceContext == nil {
		serviceContext = &ServiceContext{}
	}

	args := &ServiceEventArgs{
		Project:        invokeMsg.Project,
		Service:        invokeMsg.Service,
		ServiceContext: serviceContext,
	}

	status := "completed"
	message := ""

	// Call the service event handler.
	err := handler(ctx, args)
	if err != nil {
		status = "failed"
		message = err.Error()
		log.Printf("invokeServiceHandler error for event %s: %v", invokeMsg.EventName, err)
	}

	// Use helper to send completion status.
	return em.sendServiceHandlerStatus(invokeMsg.EventName, invokeMsg.Service.Name, status, message)
}
