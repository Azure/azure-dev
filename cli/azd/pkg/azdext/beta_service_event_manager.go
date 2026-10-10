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
	"time"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/errorchain"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

const betaServiceEventAckTimeout = 5 * time.Second

// BetaServiceEventMessageKind identifies message presentation.
type BetaServiceEventMessageKind string

const (
	// BetaServiceEventMessageInfo is informational and non-blocking.
	BetaServiceEventMessageInfo BetaServiceEventMessageKind = "info"
	// BetaServiceEventMessageWarning is a non-blocking warning.
	BetaServiceEventMessageWarning BetaServiceEventMessageKind = "warning"
)

// BetaServiceEventMessage is a non-blocking deploy message.
type BetaServiceEventMessage struct {
	Kind       BetaServiceEventMessageKind
	Message    string
	Suggestion string
	Links      []errorhandler.ErrorLink
}

// BetaServiceEventResponse contains messages from a deploy handler.
type BetaServiceEventResponse struct {
	Messages []BetaServiceEventMessage
}

// BetaServiceEventHandler handles a beta deploy event.
type BetaServiceEventHandler func(
	ctx context.Context,
	args *ServiceEventArgs,
) (*BetaServiceEventResponse, error)

type betaServiceEventRegistration struct {
	EventName string
	Handler   BetaServiceEventHandler
	Options   *ServiceEventOptions
}

type betaServiceEventManager struct {
	extensionId  string
	client       *AzdClient
	brokerLogger *log.Logger

	mu     sync.RWMutex
	broker *grpcbroker.MessageBroker[v1beta.EventMessage]

	handlersMu    sync.RWMutex
	serviceEvents map[string]BetaServiceEventHandler
}

func newBetaServiceEventManager(
	extensionID string,
	client *AzdClient,
	brokerLogger *log.Logger,
) *betaServiceEventManager {
	return &betaServiceEventManager{
		extensionId:   extensionID,
		client:        client,
		brokerLogger:  brokerLogger,
		serviceEvents: make(map[string]BetaServiceEventHandler),
	}
}

func (m *betaServiceEventManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.broker != nil {
		m.broker.Close()
		m.broker = nil
	}
	return nil
}

func (m *betaServiceEventManager) ensureStream(ctx context.Context) error {
	m.mu.RLock()
	if m.broker != nil {
		m.mu.RUnlock()
		return nil
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.broker != nil {
		return nil
	}
	if m.client == nil {
		return errors.New("azd client is required for beta service events")
	}

	stream, err := m.client.EventsBeta().EventStream(ctx)
	if err != nil {
		return fmt.Errorf("failed to create beta event stream: %w", err)
	}
	broker := grpcbroker.NewMessageBroker(
		stream,
		&betaSDKEventMessageEnvelope{},
		m.extensionId,
		m.brokerLogger,
	)
	if err := broker.On(m.onInvokeServiceHandler); err != nil {
		return fmt.Errorf("failed to register beta service event handler: %w", err)
	}
	m.broker = broker
	return nil
}

func (m *betaServiceEventManager) Receive(ctx context.Context) error {
	if err := m.ensureStream(ctx); err != nil {
		return err
	}
	return m.broker.Run(ctx)
}

func (m *betaServiceEventManager) Ready(ctx context.Context) error {
	if err := m.ensureStream(ctx); err != nil {
		return err
	}
	return m.broker.Ready(ctx)
}

func (m *betaServiceEventManager) AddBetaServiceEventHandler(
	ctx context.Context,
	eventName string,
	handler BetaServiceEventHandler,
	options *ServiceEventOptions,
) error {
	if err := validateBetaServiceEventName(eventName); err != nil {
		return err
	}
	if handler == nil {
		return errors.New("beta service event handler is required")
	}
	if err := m.ensureStream(ctx); err != nil {
		return err
	}

	m.handlersMu.Lock()
	if _, exists := m.serviceEvents[eventName]; exists {
		m.handlersMu.Unlock()
		return fmt.Errorf("beta service event %q is already registered", eventName)
	}
	m.serviceEvents[eventName] = handler
	m.handlersMu.Unlock()

	registered := false
	defer func() {
		if !registered {
			m.handlersMu.Lock()
			delete(m.serviceEvents, eventName)
			m.handlersMu.Unlock()
		}
	}()

	var host, language string
	if options != nil {
		host = options.Host
		language = options.Language
	}
	requestID := uuid.NewString()
	request := &v1beta.EventMessage{
		RequestId: requestID,
		MessageType: &v1beta.EventMessage_SubscribeServiceEvent{
			SubscribeServiceEvent: &v1beta.SubscribeServiceEvent{
				EventNames: []string{eventName},
				Host:       host,
				Language:   language,
			},
		},
	}

	ackCtx, cancel := context.WithTimeout(ctx, betaServiceEventAckTimeout)
	defer cancel()
	response, err := m.broker.SendAndWait(ackCtx, request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf(
				"timed out waiting for the %s beta service event acknowledgement; "+
					"use an azd build that supports structured deploy messages: %w",
				eventName,
				err,
			)
		}
		return fmt.Errorf("failed to register beta service event %q: %w", eventName, err)
	}
	if response.GetRequestId() != requestID ||
		response.GetSubscribeServiceEventResponse() == nil {
		return fmt.Errorf("azd sent an invalid acknowledgement for %s", eventName)
	}

	registered = true
	return nil
}

func (m *betaServiceEventManager) onInvokeServiceHandler(
	ctx context.Context,
	request *v1beta.InvokeServiceHandler,
) (*v1beta.EventMessage, error) {
	if request == nil {
		return nil, errors.New("beta service invocation is required")
	}

	m.handlersMu.RLock()
	handler := m.serviceEvents[request.GetEventName()]
	m.handlersMu.RUnlock()
	if handler == nil {
		return &v1beta.EventMessage{}, nil
	}

	args, err := betaServiceEventArgs(request)
	if err != nil {
		return nil, err
	}
	response, handlerErr := handler(ctx, args)

	var messages []*v1beta.ServiceEventMessage
	if response != nil {
		for index, message := range response.Messages {
			if err := validateBetaServiceEventMessage(message); err != nil {
				messageErr := fmt.Errorf("message %d: %w", index+1, err)
				if handlerErr == nil {
					handlerErr = messageErr
				} else {
					handlerErr = errors.Join(handlerErr, messageErr)
				}
				continue
			}
			messages = append(messages, wrapBetaServiceEventMessage(message))
		}
	}

	status := "completed"
	message := ""
	var extensionErr *v1beta.ExtensionError
	if handlerErr != nil {
		status = "failed"
		message = handlerErr.Error()
		extensionErr = wrapBetaServiceEventError(handlerErr)
		log.Printf(
			"invokeBetaServiceHandler error for event %s: %v",
			request.GetEventName(),
			handlerErr,
		)
	}

	return &v1beta.EventMessage{
		MessageType: &v1beta.EventMessage_ServiceHandlerStatus{
			ServiceHandlerStatus: &v1beta.ServiceHandlerStatus{
				EventName:   request.GetEventName(),
				ServiceName: request.GetService().GetName(),
				Status:      status,
				Message:     message,
				Error:       extensionErr,
				Messages:    messages,
			},
		},
	}, nil
}

func betaServiceEventArgs(request *v1beta.InvokeServiceHandler) (*ServiceEventArgs, error) {
	if request.GetProject() == nil {
		return nil, errors.New("beta service invocation has no project")
	}
	if request.GetService() == nil {
		return nil, errors.New("beta service invocation has no service")
	}

	args := &ServiceEventArgs{
		Project:        &ProjectConfig{},
		Service:        &ServiceConfig{},
		ServiceContext: &ServiceContext{},
	}
	if request.GetServiceContext() != nil {
		args.ServiceContext = &ServiceContext{}
		if err := transcodeBetaServiceEventMessage(
			request.GetServiceContext(),
			args.ServiceContext,
		); err != nil {
			return nil, fmt.Errorf("convert beta service context: %w", err)
		}
	}
	if err := transcodeBetaServiceEventMessage(request.GetProject(), args.Project); err != nil {
		return nil, fmt.Errorf("convert beta project config: %w", err)
	}
	if err := transcodeBetaServiceEventMessage(request.GetService(), args.Service); err != nil {
		return nil, fmt.Errorf("convert beta service config: %w", err)
	}
	return args, nil
}

func transcodeBetaServiceEventMessage(source, destination proto.Message) error {
	wire, err := proto.Marshal(source)
	if err != nil {
		return err
	}
	return proto.Unmarshal(wire, destination)
}

func validateBetaServiceEventName(eventName string) error {
	if eventName != "predeploy" && eventName != "postdeploy" {
		return fmt.Errorf(
			"beta service event %q is not supported; use predeploy or postdeploy",
			eventName,
		)
	}
	return nil
}

func validateBetaServiceEventMessage(message BetaServiceEventMessage) error {
	if message.Kind != BetaServiceEventMessageInfo &&
		message.Kind != BetaServiceEventMessageWarning {
		return fmt.Errorf("unsupported message kind %q", message.Kind)
	}
	if strings.TrimSpace(message.Message) == "" {
		return errors.New("message text is required")
	}
	return nil
}

func wrapBetaServiceEventMessage(
	message BetaServiceEventMessage,
) *v1beta.ServiceEventMessage {
	kind := v1beta.ServiceEventMessageKind_SERVICE_EVENT_MESSAGE_KIND_UNSPECIFIED
	switch message.Kind {
	case BetaServiceEventMessageInfo:
		kind = v1beta.ServiceEventMessageKind_SERVICE_EVENT_MESSAGE_KIND_INFO
	case BetaServiceEventMessageWarning:
		kind = v1beta.ServiceEventMessageKind_SERVICE_EVENT_MESSAGE_KIND_WARNING
	}
	result := &v1beta.ServiceEventMessage{
		Kind:       kind,
		Message:    message.Message,
		Suggestion: message.Suggestion,
	}
	for _, link := range message.Links {
		result.Links = append(result.Links, &v1beta.ErrorLink{
			Url:   link.URL,
			Title: link.Title,
		})
	}
	return result
}

func wrapBetaServiceEventError(err error) *v1beta.ExtensionError {
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
			if stableError.GetOrigin() == ErrorOrigin_ERROR_ORIGIN_TOOL {
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
			}
			return betaError
		}
	}
	return &v1beta.ExtensionError{Message: err.Error()}
}

type betaSDKEventMessageEnvelope struct{}

var _ grpcbroker.MessageEnvelope[v1beta.EventMessage] = (*betaSDKEventMessageEnvelope)(nil)

func (*betaSDKEventMessageEnvelope) GetRequestId(
	_ context.Context,
	message *v1beta.EventMessage,
) string {
	if message == nil {
		return ""
	}
	return message.GetRequestId()
}

func (*betaSDKEventMessageEnvelope) SetRequestId(
	_ context.Context,
	message *v1beta.EventMessage,
	requestID string,
) {
	message.RequestId = requestID
}

func (*betaSDKEventMessageEnvelope) GetError(message *v1beta.EventMessage) error {
	if message == nil || message.GetError() == nil {
		return nil
	}

	wire, err := proto.Marshal(message.GetError())
	if err != nil {
		return fmt.Errorf("%s", message.GetError().GetMessage())
	}
	stableError := new(ExtensionError)
	if err := proto.Unmarshal(wire, stableError); err != nil {
		return fmt.Errorf("%s", message.GetError().GetMessage())
	}
	return UnwrapError(stableError)
}

func (*betaSDKEventMessageEnvelope) SetError(
	message *v1beta.EventMessage,
	err error,
) {
	message.Error = wrapBetaServiceEventError(err)
}

func (*betaSDKEventMessageEnvelope) GetInnerMessage(message *v1beta.EventMessage) any {
	if message == nil {
		return nil
	}
	switch content := message.GetMessageType().(type) {
	case *v1beta.EventMessage_SubscribeProjectEvent:
		return content.SubscribeProjectEvent
	case *v1beta.EventMessage_InvokeProjectHandler:
		return content.InvokeProjectHandler
	case *v1beta.EventMessage_ProjectHandlerStatus:
		return content.ProjectHandlerStatus
	case *v1beta.EventMessage_SubscribeServiceEvent:
		return content.SubscribeServiceEvent
	case *v1beta.EventMessage_InvokeServiceHandler:
		return content.InvokeServiceHandler
	case *v1beta.EventMessage_ServiceHandlerStatus:
		return content.ServiceHandlerStatus
	case *v1beta.EventMessage_SubscribeProjectEventResponse:
		return content.SubscribeProjectEventResponse
	case *v1beta.EventMessage_SubscribeServiceEventResponse:
		return content.SubscribeServiceEventResponse
	default:
		return nil
	}
}

func (*betaSDKEventMessageEnvelope) IsProgressMessage(*v1beta.EventMessage) bool {
	return false
}

func (*betaSDKEventMessageEnvelope) GetProgressMessage(*v1beta.EventMessage) string {
	return ""
}

func (*betaSDKEventMessageEnvelope) CreateProgressMessage(string, string) *v1beta.EventMessage {
	return nil
}
