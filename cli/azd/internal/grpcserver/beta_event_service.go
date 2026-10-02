// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"

	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type betaEventService struct {
	service *eventService
}

var _ BetaEventServiceEventStreamOverride = (*betaEventService)(nil)

func (s *betaEventService) EventStream(
	stream grpc.BidiStreamingServer[v1beta.EventMessage, v1beta.EventMessage],
) error {
	extension, err := s.authorize(stream.Context())
	if err != nil {
		return err
	}

	first, err := stream.Recv()
	if err != nil {
		return err
	}
	if first == nil {
		return status.Error(codes.InvalidArgument, "first event message is required")
	}

	switch {
	case first.GetRequestId() == "" && isEventSubscription(first):
		if err := validateLegacyBetaEventMessage(first); err != nil {
			return err
		}
		legacyAdapter := &betaEventServiceAdapter{stable: s.service}
		return legacyAdapter.EventStream(&legacyBetaEventStream{
			BidiStreamingServer: stream,
			first:               first,
		})
	case first.GetRequestId() != "" && isEventSubscription(first):
		eventStream := &modernBetaEventStream{
			BidiStreamingServer: stream,
			first:               first,
		}
		return s.runModern(stream.Context(), extension, eventStream)
	default:
		return status.Error(
			codes.InvalidArgument,
			"event stream must start with a project or service subscription",
		)
	}
}

func (s *betaEventService) authorize(ctx context.Context) (*extensions.Extension, error) {
	claims, err := extensions.GetClaimsFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get extension claims: %w", err)
	}

	extension, err := s.service.extensionManager.GetInstalled(extensions.FilterOptions{
		Id: claims.Subject,
	})
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "failed to get extension: %s", err.Error())
	}
	if !extension.HasCapability(extensions.LifecycleEventsCapability) {
		return nil, status.Error(codes.PermissionDenied, "extension does not support lifecycle events")
	}

	return extension, nil
}

func (s *betaEventService) runModern(
	ctx context.Context,
	extension *extensions.Extension,
	stream grpc.BidiStreamingServer[v1beta.EventMessage, v1beta.EventMessage],
) error {
	broker := grpcbroker.NewMessageBroker(
		stream,
		betaEventMessageEnvelope{},
		extension.Id,
		log.Default(),
	)
	var subscriptionMu sync.Mutex
	var subscriptionCancels []context.CancelFunc
	subscriptionsClosed := false
	trackSubscriptionCancel := func(cancel context.CancelFunc) {
		subscriptionMu.Lock()
		if subscriptionsClosed {
			subscriptionMu.Unlock()
			cancel()
			return
		}
		subscriptionCancels = append(subscriptionCancels, cancel)
		subscriptionMu.Unlock()
	}
	defer func() {
		subscriptionMu.Lock()
		subscriptionsClosed = true
		cancels := subscriptionCancels
		subscriptionMu.Unlock()
		for _, cancel := range cancels {
			cancel()
		}
	}()

	if err := broker.On(func(
		ctx context.Context,
		request *v1beta.SubscribeProjectEvent,
	) (*v1beta.EventMessage, error) {
		return s.onSubscribeProjectEvent(ctx, extension, request, broker, trackSubscriptionCancel)
	}); err != nil {
		return fmt.Errorf("register beta project event subscription handler: %w", err)
	}
	if err := broker.On(func(
		ctx context.Context,
		request *v1beta.SubscribeServiceEvent,
	) (*v1beta.EventMessage, error) {
		return s.onSubscribeServiceEvent(ctx, extension, request, broker, trackSubscriptionCancel)
	}); err != nil {
		return fmt.Errorf("register beta service event subscription handler: %w", err)
	}

	if err := broker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("beta event broker error: %w", err)
	}
	return nil
}

func (s *betaEventService) onSubscribeProjectEvent(
	ctx context.Context,
	extension *extensions.Extension,
	request *v1beta.SubscribeProjectEvent,
	broker *grpcbroker.MessageBroker[v1beta.EventMessage],
	trackCancel func(context.CancelFunc),
) (*v1beta.EventMessage, error) {
	if err := validateEventNames(request.GetEventNames()); err != nil {
		return nil, err
	}

	projectConfig, err := s.service.lazyProject.GetValue()
	if err != nil {
		return nil, err
	}

	registrationCtx, cancel := context.WithCancel(ctx)
	trackCancel(cancel)
	for _, eventName := range request.GetEventNames() {
		handler := s.createProjectEventHandler(ctx, extension, eventName, broker)
		if err := projectConfig.AddHandler(registrationCtx, ext.Event(eventName), handler); err != nil {
			cancel()
			return nil, fmt.Errorf("failed to add handler for event %s: %w", eventName, err)
		}
	}

	return &v1beta.EventMessage{
		MessageType: &v1beta.EventMessage_SubscribeProjectEventResponse{
			SubscribeProjectEventResponse: &v1beta.SubscribeProjectEventResponse{},
		},
	}, nil
}

func (s *betaEventService) onSubscribeServiceEvent(
	ctx context.Context,
	extension *extensions.Extension,
	request *v1beta.SubscribeServiceEvent,
	broker *grpcbroker.MessageBroker[v1beta.EventMessage],
	trackCancel func(context.CancelFunc),
) (*v1beta.EventMessage, error) {
	if err := validateEventNames(request.GetEventNames()); err != nil {
		return nil, err
	}

	projectConfig, err := s.service.lazyProject.GetValue()
	if err != nil {
		return nil, err
	}

	registrationCtx, cancel := context.WithCancel(ctx)
	trackCancel(cancel)
	for _, eventName := range request.GetEventNames() {
		for _, serviceConfig := range projectConfig.ServiceConfigs() {
			if request.GetLanguage() != "" &&
				string(serviceConfig.Language) != request.GetLanguage() {
				continue
			}
			if request.GetHost() != "" && string(serviceConfig.Host) != request.GetHost() {
				continue
			}

			handler := s.createServiceEventHandler(
				ctx,
				serviceConfig,
				extension,
				eventName,
				broker,
			)
			if err := serviceConfig.AddHandler(registrationCtx, ext.Event(eventName), handler); err != nil {
				cancel()
				return nil, fmt.Errorf("failed to add handler for event %s: %w", eventName, err)
			}
		}
	}

	return &v1beta.EventMessage{
		MessageType: &v1beta.EventMessage_SubscribeServiceEventResponse{
			SubscribeServiceEventResponse: &v1beta.SubscribeServiceEventResponse{},
		},
	}, nil
}

func validateEventNames(eventNames []string) error {
	if len(eventNames) == 0 {
		return status.Error(codes.InvalidArgument, "event names are required")
	}
	for index, eventName := range eventNames {
		if eventName == "" {
			return status.Errorf(
				codes.InvalidArgument,
				"event name at index %d cannot be empty",
				index,
			)
		}
	}
	return nil
}

func (s *betaEventService) createProjectEventHandler(
	streamCtx context.Context,
	extension *extensions.Extension,
	eventName string,
	broker *grpcbroker.MessageBroker[v1beta.EventMessage],
) ext.EventHandlerFn[project.ProjectLifecycleEventArgs] {
	return func(ctx context.Context, args project.ProjectLifecycleEventArgs) error {
		err := func() error {
			previewTitle := fmt.Sprintf("%s (%s)", extension.DisplayName, eventName)
			cleanupPreview, output := s.service.syncExtensionOutput(
				ctx,
				extension,
				previewTitle,
				shouldPersistLifecycleOutput(eventName),
			)
			defer cleanupPreview()

			resolver := noEnvResolver
			env, err := s.service.lazyEnv.GetValue()
			if err == nil && env != nil {
				resolver = env.Getenv
			}

			var stableProject *azdext.ProjectConfig
			if err := mapper.WithResolver(resolver).Convert(args.Project, &stableProject); err != nil {
				return err
			}
			betaProject := &v1beta.ProjectConfig{}
			if err := transcodeStableResponse(stableProject, betaProject); err != nil {
				return fmt.Errorf("convert project config to beta: %w", err)
			}

			invocationCtx, err := addStreamClaims(ctx, streamCtx)
			if err != nil {
				return err
			}
			invokeMessage := &v1beta.EventMessage{
				RequestId: uuid.NewString(),
				MessageType: &v1beta.EventMessage_InvokeProjectHandler{
					InvokeProjectHandler: &v1beta.InvokeProjectHandler{
						EventName: eventName,
						Project:   betaProject,
					},
				},
			}

			return s.service.runWithEnvReload(ctx, func() error {
				response, err := broker.SendAndWaitWithProgress(
					invocationCtx,
					invokeMessage,
					lifecycleOutputProgress(output),
				)
				if err != nil {
					return fmt.Errorf("failed to invoke project event %s: %w", eventName, err)
				}

				statusMessage := response.GetProjectHandlerStatus()
				if statusMessage == nil || statusMessage.GetEventName() != eventName {
					return fmt.Errorf("unexpected response for project event %s", eventName)
				}
				if statusMessage.GetStatus() != "failed" {
					return nil
				}
				if extensionErr := unwrapBetaExtensionError(statusMessage.GetError()); extensionErr != nil {
					return extensionErr
				}
				return fmt.Errorf(
					"extension %s project hook %s failed: %s",
					extension.Id,
					eventName,
					statusMessage.GetMessage(),
				)
			})
		}()

		return extensions.WrapInvocationError(err, extension.Id, extension.Version, eventName)
	}
}

func (s *betaEventService) createServiceEventHandler(
	streamCtx context.Context,
	serviceConfig *project.ServiceConfig,
	extension *extensions.Extension,
	eventName string,
	broker *grpcbroker.MessageBroker[v1beta.EventMessage],
) ext.EventHandlerFn[project.ServiceLifecycleEventArgs] {
	return func(ctx context.Context, args project.ServiceLifecycleEventArgs) error {
		err := func() error {
			previewTitle := fmt.Sprintf(
				"%s (%s.%s)",
				extension.DisplayName,
				args.Service.Name,
				eventName,
			)
			cleanupPreview, output := s.service.syncExtensionOutput(
				ctx,
				extension,
				previewTitle,
				shouldPersistLifecycleOutput(eventName),
			)
			defer cleanupPreview()

			resolver := noEnvResolver
			env, err := s.service.lazyEnv.GetValue()
			if err == nil && env != nil {
				resolver = env.Getenv
			}
			objectMapper := mapper.WithResolver(resolver)

			var stableProject *azdext.ProjectConfig
			if err := objectMapper.Convert(args.Project, &stableProject); err != nil {
				return err
			}
			var stableService *azdext.ServiceConfig
			if err := objectMapper.Convert(args.Service, &stableService); err != nil {
				return err
			}
			var stableContext *azdext.ServiceContext
			if err := objectMapper.Convert(args.ServiceContext, &stableContext); err != nil {
				return err
			}

			betaProject := &v1beta.ProjectConfig{}
			if err := transcodeStableResponse(stableProject, betaProject); err != nil {
				return fmt.Errorf("convert project config to beta: %w", err)
			}
			betaService := &v1beta.ServiceConfig{}
			if err := transcodeStableResponse(stableService, betaService); err != nil {
				return fmt.Errorf("convert service config to beta: %w", err)
			}
			betaContext := &v1beta.ServiceContext{}
			if err := transcodeStableResponse(stableContext, betaContext); err != nil {
				return fmt.Errorf("convert service context to beta: %w", err)
			}

			invocationCtx, err := addStreamClaims(ctx, streamCtx)
			if err != nil {
				return err
			}
			invokeMessage := &v1beta.EventMessage{
				RequestId: uuid.NewString(),
				MessageType: &v1beta.EventMessage_InvokeServiceHandler{
					InvokeServiceHandler: &v1beta.InvokeServiceHandler{
						EventName:      eventName,
						Project:        betaProject,
						Service:        betaService,
						ServiceContext: betaContext,
					},
				},
			}

			return s.service.runWithEnvReload(ctx, func() error {
				response, err := broker.SendAndWaitWithProgress(
					invocationCtx,
					invokeMessage,
					lifecycleOutputProgress(output),
				)
				if err != nil {
					return fmt.Errorf("failed to invoke service event %s: %w", eventName, err)
				}

				statusMessage := response.GetServiceHandlerStatus()
				if statusMessage == nil ||
					statusMessage.GetEventName() != eventName ||
					statusMessage.GetServiceName() != args.Service.Name {
					return fmt.Errorf("unexpected response for service event %s", eventName)
				}
				if statusMessage.GetStatus() != "failed" {
					return nil
				}
				if extensionErr := unwrapBetaExtensionError(statusMessage.GetError()); extensionErr != nil {
					return extensionErr
				}
				return fmt.Errorf(
					"extension %s service hook %s.%s failed: %s",
					extension.Id,
					args.Service.Name,
					eventName,
					statusMessage.GetMessage(),
				)
			})
		}()

		return extensions.WrapInvocationError(err, extension.Id, extension.Version, eventName)
	}
}

func addStreamClaims(ctx, streamCtx context.Context) (context.Context, error) {
	claims, err := extensions.GetClaimsFromContext(streamCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to get extension claims: %w", err)
	}
	return extensions.WithClaimsContext(ctx, claims), nil
}

func isEventSubscription(message *v1beta.EventMessage) bool {
	return message.GetSubscribeProjectEvent() != nil ||
		message.GetSubscribeServiceEvent() != nil
}

type modernBetaEventStream struct {
	grpc.BidiStreamingServer[v1beta.EventMessage, v1beta.EventMessage]
	first *v1beta.EventMessage
}

func (s *modernBetaEventStream) Recv() (*v1beta.EventMessage, error) {
	var message *v1beta.EventMessage
	if s.first != nil {
		message = s.first
		s.first = nil
	} else {
		var err error
		message, err = s.BidiStreamingServer.Recv()
		if err != nil {
			return nil, err
		}
	}
	if err := validateModernBetaEventMessage(message); err != nil {
		return nil, err
	}
	return message, nil
}

type legacyBetaEventStream struct {
	grpc.BidiStreamingServer[v1beta.EventMessage, v1beta.EventMessage]
	first *v1beta.EventMessage
}

func (s *legacyBetaEventStream) Recv() (*v1beta.EventMessage, error) {
	if s.first != nil {
		message := s.first
		s.first = nil
		return message, nil
	}
	message, err := s.BidiStreamingServer.Recv()
	if err != nil {
		return nil, err
	}
	if err := validateLegacyBetaEventMessage(message); err != nil {
		return nil, err
	}
	return message, nil
}

func validateModernBetaEventMessage(message *v1beta.EventMessage) error {
	if message == nil {
		return status.Error(codes.InvalidArgument, "event message is required")
	}
	if message.GetRequestId() == "" {
		return status.Error(codes.InvalidArgument, "beta event request_id is required")
	}
	if message.GetError() != nil {
		return status.Error(codes.InvalidArgument, "extensions cannot send top-level event errors")
	}

	switch content := message.GetMessageType().(type) {
	case *v1beta.EventMessage_SubscribeProjectEvent:
		if content.SubscribeProjectEvent == nil {
			break
		}
	case *v1beta.EventMessage_SubscribeServiceEvent:
		if content.SubscribeServiceEvent == nil {
			break
		}
	case *v1beta.EventMessage_ProjectHandlerStatus:
		if content.ProjectHandlerStatus == nil {
			break
		}
	case *v1beta.EventMessage_ServiceHandlerStatus:
		if content.ServiceHandlerStatus == nil {
			break
		}
	case *v1beta.EventMessage_HandlerOutput:
		if content.HandlerOutput == nil {
			break
		}
	default:
		return status.Error(codes.InvalidArgument, "invalid message for a beta event client")
	}
	if (betaEventMessageEnvelope{}).GetInnerMessage(message) == nil {
		return status.Error(codes.InvalidArgument, "event message payload is required")
	}
	return nil
}

func validateLegacyBetaEventMessage(message *v1beta.EventMessage) error {
	if message == nil {
		return status.Error(codes.InvalidArgument, "event message is required")
	}
	if message.GetRequestId() != "" || message.GetError() != nil ||
		message.GetHandlerOutput() != nil ||
		message.GetSubscribeProjectEventResponse() != nil ||
		message.GetSubscribeServiceEventResponse() != nil {
		return status.Error(
			codes.InvalidArgument,
			"beta event protocol fields require a new stream with a request_id",
		)
	}
	switch message.GetMessageType().(type) {
	case *v1beta.EventMessage_SubscribeProjectEvent,
		*v1beta.EventMessage_SubscribeServiceEvent,
		*v1beta.EventMessage_ProjectHandlerStatus,
		*v1beta.EventMessage_ServiceHandlerStatus:
	default:
		return status.Error(codes.InvalidArgument, "invalid message for a legacy beta event stream")
	}
	if (betaEventMessageEnvelope{}).GetInnerMessage(message) == nil {
		return status.Error(codes.InvalidArgument, "event message payload is required")
	}
	return nil
}
