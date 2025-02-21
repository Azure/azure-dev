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
	Project *ProjectConfig
	Service *ServiceConfig
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

func (em *EventManager) Receive(ctx context.Context) <-chan error {
	errChan := make(chan error, 1)

	eventStream, err := em.azdClient.Events().EventStream(ctx)
	if err != nil {
		errChan <- err
		return errChan
	}

	em.stream = eventStream

	go func() {
		defer close(errChan)

		for {
			select {
			case <-ctx.Done():
				log.Println("Context cancelled by caller, exiting receiveEvents")
				errChan <- nil
				return
			default:
				msg, err := em.stream.Recv()
				if err != nil {
					if errors.Is(err, io.EOF) {
						log.Println("Stream closed by server (EOF), treating as expected")
						errChan <- nil
						return
					}

					if st, ok := status.FromError(err); ok {
						if st.Code() == codes.Unavailable {
							log.Println("Stream closed by server (unavailable), treating as expected")
							errChan <- nil
							return
						}
					}

					errChan <- err
					return
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
	}()

	return errChan
}

func (em *EventManager) AddProjectEventHandler(eventName string, handler ProjectEventHandler) error {
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

type ServerEventOptions struct {
	Host     string
	Language string
}

func (em *EventManager) AddServiceEventHandler(
	eventName string,
	handler ServiceEventHandler,
	options *ServerEventOptions,
) error {
	if options == nil {
		options = &ServerEventOptions{}
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

	err := handler(ctx, args)
	if err != nil {
		status = "failed"
		message = err.Error()
	}

	// Send completion status
	return em.stream.Send(&EventMessage{
		MessageType: &EventMessage_ProjectHandlerStatus{
			ProjectHandlerStatus: &ProjectHandlerStatus{
				EventName: invokeMsg.EventName,
				Status:    status,
				Message:   message,
			},
		},
	})
}

func (em *EventManager) invokeServiceHandler(ctx context.Context, invokeMsg *InvokeServiceHandler) error {
	handler, exists := em.serviceEvents[invokeMsg.EventName]
	if !exists {
		return nil
	}

	args := &ServiceEventArgs{
		Project: invokeMsg.Project,
		Service: invokeMsg.Service,
	}

	status := "completed"
	message := ""

	err := handler(ctx, args)
	if err != nil {
		status = "failed"
		message = err.Error()
	}

	// Send completion status
	return em.stream.Send(&EventMessage{
		MessageType: &EventMessage_ServiceHandlerStatus{
			ServiceHandlerStatus: &ServiceHandlerStatus{
				EventName: invokeMsg.EventName,
				Status:    status,
				Message:   message,
			},
		},
	})
}
