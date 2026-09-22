// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/errorchain"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

type EventManager struct {
	extensionId   string
	client        *AzdClient
	broker        *grpcbroker.MessageBroker[EventMessage]
	projectEvents map[string]ProjectEventHandler
	serviceEvents map[string]ServiceEventHandler
	eventsMutex   sync.RWMutex // Protects both projectEvents and serviceEvents maps
	brokerLogger  *log.Logger

	// Synchronization for concurrent access
	mu sync.RWMutex
}

type previewEventManager struct {
	extensionId    string
	client         *AzdClient
	broker         *grpcbroker.MessageBroker[v1beta.EventMessage]
	handlers       map[string]PreviewProjectEventHandler
	brokerLogger   *log.Logger
	mu             sync.Mutex
	registrationMu sync.Mutex
}

func newPreviewEventManager(
	extensionId string,
	client *AzdClient,
	brokerLogger *log.Logger,
) *previewEventManager {
	return &previewEventManager{
		extensionId:  extensionId,
		client:       client,
		handlers:     make(map[string]PreviewProjectEventHandler),
		brokerLogger: brokerLogger,
	}
}

func (em *previewEventManager) Close() error {
	em.mu.Lock()
	defer em.mu.Unlock()
	if em.broker != nil {
		em.broker.Close()
		em.broker = nil
	}
	clear(em.handlers)
	return nil
}

func (em *previewEventManager) ensureStream(ctx context.Context) error {
	em.mu.Lock()
	defer em.mu.Unlock()
	if em.broker != nil {
		return nil
	}
	stream, err := em.client.betaEvents().EventStream(ctx)
	if err != nil {
		return fmt.Errorf("failed to create preview event stream: %w", err)
	}
	broker := grpcbroker.NewMessageBroker(
		stream,
		newBetaEventMessageEnvelope(),
		em.extensionId,
		em.brokerLogger,
	)
	if err := broker.On(em.onInvokeProjectHandler); err != nil {
		broker.Close()
		return fmt.Errorf("failed to register preview project handler: %w", err)
	}
	em.broker = broker
	return nil
}

func (em *previewEventManager) Receive(ctx context.Context) error {
	if err := em.ensureStream(ctx); err != nil {
		return err
	}
	em.mu.Lock()
	broker := em.broker
	em.mu.Unlock()
	if broker == nil {
		return fmt.Errorf("preview event manager is closed")
	}
	return broker.Run(ctx)
}

func (em *previewEventManager) Ready(ctx context.Context) error {
	if err := em.ensureStream(ctx); err != nil {
		return err
	}
	em.mu.Lock()
	broker := em.broker
	em.mu.Unlock()
	if broker == nil {
		return fmt.Errorf("preview event manager is closed")
	}
	return broker.Ready(ctx)
}

func (em *previewEventManager) AddProjectEventHandler(
	ctx context.Context,
	eventName string,
	handler PreviewProjectEventHandler,
) error {
	em.registrationMu.Lock()
	defer em.registrationMu.Unlock()

	if err := em.ensureStream(ctx); err != nil {
		return err
	}

	em.mu.Lock()
	broker := em.broker
	if broker == nil {
		em.mu.Unlock()
		return fmt.Errorf("preview event manager is closed")
	}

	// Register before sending so the broker sees the handler first.
	previousHandler, hadPreviousHandler := em.handlers[eventName]
	em.handlers[eventName] = handler
	em.mu.Unlock()

	msg := &v1beta.EventMessage{
		RequestId: uuid.NewString(),
		MessageType: &v1beta.EventMessage_SubscribeProjectEvent{
			SubscribeProjectEvent: &v1beta.SubscribeProjectEvent{
				EventNames: []string{eventName},
			},
		},
	}
	resp, err := broker.SendAndWait(ctx, msg)
	if err == nil && resp.GetSubscribeProjectEventResponse() == nil {
		err = fmt.Errorf(
			"expected SubscribeProjectEventResponse, got %T",
			resp.GetMessageType(),
		)
	}
	if err != nil {
		em.mu.Lock()
		if hadPreviousHandler {
			em.handlers[eventName] = previousHandler
		} else {
			delete(em.handlers, eventName)
		}
		em.mu.Unlock()
		return fmt.Errorf("preview event subscription failed: %w", err)
	}

	return nil
}

func (em *previewEventManager) onInvokeProjectHandler(
	ctx context.Context,
	req *v1beta.InvokeProjectHandler,
) (*v1beta.EventMessage, error) {
	if req == nil {
		return &v1beta.EventMessage{}, nil
	}
	em.mu.Lock()
	handler, exists := em.handlers[req.EventName]
	em.mu.Unlock()
	if !exists {
		return &v1beta.EventMessage{}, nil
	}
	args := &PreviewProjectEventArgs{
		Project: req.Project,
		FollowUp: &FollowUpContribution{
			client:       em.client,
			ctx:          ctx,
			invocationID: req.InvocationId,
		},
	}
	handlerStatus := "completed"
	var handlerError *v1beta.ExtensionError
	if err := handler(ctx, args); err != nil {
		handlerStatus = "failed"
		handlerError = wrapBetaError(err)
	}
	return &v1beta.EventMessage{
		MessageType: &v1beta.EventMessage_ProjectHandlerStatus{
			ProjectHandlerStatus: &v1beta.ProjectHandlerStatus{
				EventName: eventNameOrEmpty(req),
				Status:    handlerStatus,
				Message:   errorMessage(handlerError),
				Error:     handlerError,
			},
		},
	}, nil
}

func eventNameOrEmpty(req *v1beta.InvokeProjectHandler) string {
	if req == nil {
		return ""
	}
	return req.EventName
}

func wrapBetaError(err error) *v1beta.ExtensionError {
	if err == nil {
		return nil
	}

	stableError := WrapError(err)
	wire, marshalErr := proto.Marshal(stableError)
	if marshalErr == nil {
		betaError := new(v1beta.ExtensionError)
		if unmarshalErr := proto.Unmarshal(wire, betaError); unmarshalErr == nil {
			if localErr, ok := errors.AsType[*LocalError](err); ok {
				if betaLocalErr := betaError.GetLocalError(); betaLocalErr != nil {
					betaLocalErr.CauseTypes = errorchain.NormalizeCauseTypes(localErr.CauseTypes)
				}
			}
			if toolErr, ok := errors.AsType[*ToolError](err); ok {
				var exitCode *int64
				if toolErr.ExitCode != nil {
					exitCode = new(int64(*toolErr.ExitCode))
				}
				betaError.Source = &v1beta.ExtensionError_ToolError{
					ToolError: &v1beta.ToolErrorDetail{
						ToolName:    toolErr.ToolName,
						FailureKind: string(toolErr.Kind),
						ExitCode:    exitCode,
					},
				}
			}
			return betaError
		}
	}

	return &v1beta.ExtensionError{Message: err.Error()}
}

func errorMessage(err *v1beta.ExtensionError) string {
	if err == nil {
		return ""
	}
	return err.Message
}

type ProjectEventArgs struct {
	Project *ProjectConfig
}

// PreviewProjectEventArgs holds beta event data and preview services.
type PreviewProjectEventArgs struct {
	Project  *v1beta.ProjectConfig
	FollowUp *FollowUpContribution
}

// FollowUpContribution contributes text for one handler invocation.
type FollowUpContribution struct {
	client       *AzdClient
	ctx          context.Context
	invocationID string
}

// Set replaces the contribution for the current invocation.
func (f *FollowUpContribution) Set(text string) error {
	if f == nil {
		return fmt.Errorf("follow-up contribution is unavailable")
	}
	if f.invocationID == "" {
		return fmt.Errorf("follow-up invocation is unavailable")
	}

	_, err := f.client.FollowUp().SetFollowUp(
		WithAccessToken(f.ctx),
		&v1beta.SetFollowUpRequest{
			InvocationId: f.invocationID,
			Text:         text,
		},
	)
	return err
}

// Clear removes the contribution for the current invocation.
func (f *FollowUpContribution) Clear() error {
	return f.Set("")
}

type ServiceEventArgs struct {
	Project        *ProjectConfig
	Service        *ServiceConfig
	ServiceContext *ServiceContext
}

type ProjectEventHandler func(ctx context.Context, args *ProjectEventArgs) error

// PreviewProjectEventHandler handles a beta project lifecycle event.
type PreviewProjectEventHandler func(ctx context.Context, args *PreviewProjectEventArgs) error

type ServiceEventHandler func(ctx context.Context, args *ServiceEventArgs) error

func NewEventManager(extensionId string, azdClient *AzdClient, brokerLogger *log.Logger) *EventManager {
	return &EventManager{
		extensionId:   extensionId,
		client:        azdClient,
		projectEvents: make(map[string]ProjectEventHandler),
		serviceEvents: make(map[string]ServiceEventHandler),
		brokerLogger:  brokerLogger,
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
	req *InvokeProjectHandler,
) (*EventMessage, error) {
	em.eventsMutex.RLock()
	defer em.eventsMutex.RUnlock()
	handler, exists := em.projectEvents[req.EventName]

	if !exists {
		// No handler registered, return empty response (not an error)
		return &EventMessage{}, nil
	}

	args := &ProjectEventArgs{Project: req.Project}

	handlerStatus := "completed"
	handlerMessage := ""
	var handlerError *ExtensionError

	// Call the project event handler
	err := handler(ctx, args)
	if err != nil {
		handlerStatus = "failed"
		handlerMessage = err.Error()
		handlerError = WrapError(err)
		log.Printf("invokeProjectHandler error for event %s: %v", req.EventName, err)
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
	var handlerError *ExtensionError

	// Call the service event handler
	err := handler(ctx, args)
	if err != nil {
		handlerStatus = "failed"
		handlerMessage = err.Error()
		handlerError = WrapError(err)
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
				Error:       handlerError,
			},
		},
	}, nil
}
