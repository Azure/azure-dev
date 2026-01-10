// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// noEnvResolver is a resolver that always returns an empty string.
// This is used when an environment is not available to resolve environment variables referenced in project config.
var noEnvResolver = func(name string) string {
	return ""
}

// eventService implements azdext.EventServiceServer.
type eventService struct {
	azdext.UnimplementedEventServiceServer
	extensionManager *extensions.Manager
	console          input.Console

	lazyEnvManager *lazy.Lazy[environment.Manager]
	lazyProject    *lazy.Lazy[*project.ProjectConfig]
	lazyEnv        *lazy.Lazy[*environment.Environment]
}

func NewEventService(
	extensionManager *extensions.Manager,
	lazyEnvManager *lazy.Lazy[environment.Manager],
	lazyProject *lazy.Lazy[*project.ProjectConfig],
	lazyEnv *lazy.Lazy[*environment.Environment],
	console input.Console,
) azdext.EventServiceServer {
	return &eventService{
		extensionManager: extensionManager,
		lazyEnvManager:   lazyEnvManager,
		lazyProject:      lazyProject,
		lazyEnv:          lazyEnv,
		console:          console,
	}
}

// EventStream handles bidirectional streaming.
func (s *eventService) EventStream(stream grpc.BidiStreamingServer[azdext.EventMessage, azdext.EventMessage]) error {
	ctx := stream.Context()
	extensionClaims, err := extensions.GetClaimsFromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get extension claims: %w", err)
	}

	options := extensions.FilterOptions{
		Id: extensionClaims.Subject,
	}

	extension, err := s.extensionManager.GetInstalled(options)
	if err != nil {
		return status.Errorf(codes.FailedPrecondition, "failed to get extension: %s", err.Error())
	}

	if !extension.HasCapability(extensions.LifecycleEventsCapability) {
		return status.Errorf(codes.PermissionDenied, "extension does not support lifecycle events")
	}

	// Create message broker with EventMessageEnvelope
	envelope := azdext.NewEventMessageEnvelope()
	broker := grpcbroker.NewMessageBroker(stream, envelope, extension.Id)

	// Register handlers for incoming subscription requests (no response needed)
	broker.On(func(ctx context.Context, msg *azdext.SubscribeProjectEvent) (*azdext.EventMessage, error) {
		return nil, s.onSubscribeProjectEvent(ctx, extension, msg, broker)
	})

	broker.On(func(ctx context.Context, msg *azdext.SubscribeServiceEvent) (*azdext.EventMessage, error) {
		return nil, s.onSubscribeServiceEvent(ctx, extension, msg, broker)
	})

	// Run the broker's dispatcher (blocking)
	if err := broker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("broker error: %w", err)
	}

	return nil
}

// ----- Project Event Handlers -----

func (s *eventService) onSubscribeProjectEvent(
	ctx context.Context,
	extension *extensions.Extension,
	subscribeMsg *azdext.SubscribeProjectEvent,
	broker *grpcbroker.MessageBroker[azdext.EventMessage],
) error {
	projectConfig, err := s.lazyProject.GetValue()
	if err != nil {
		return err
	}

	for i := 0; i < len(subscribeMsg.EventNames); i++ {
		eventName := subscribeMsg.EventNames[i]

		evt := ext.Event(eventName)
		// Pass the stream context (ctx) which has extension claims
		handler := s.createProjectEventHandler(ctx, extension, eventName, broker)
		if err := projectConfig.AddHandler(ctx, evt, handler); err != nil {
			return fmt.Errorf("failed to add handler for event %s: %w", eventName, err)
		}
	}

	return nil
}

func (s *eventService) createProjectEventHandler(
	streamCtx context.Context,
	extension *extensions.Extension,
	eventName string,
	broker *grpcbroker.MessageBroker[azdext.EventMessage],
) ext.EventHandlerFn[project.ProjectLifecycleEventArgs] {
	return func(ctx context.Context, args project.ProjectLifecycleEventArgs) error {
		previewTitle := fmt.Sprintf("%s (%s)", extension.DisplayName, eventName)
		defer s.syncExtensionOutput(ctx, extension, previewTitle)()

		resolver := noEnvResolver
		env, err := s.lazyEnv.GetValue()
		if err == nil && env != nil {
			resolver = env.Getenv
		}

		var protoProjectConfig *azdext.ProjectConfig
		if err := mapper.WithResolver(resolver).Convert(args.Project, &protoProjectConfig); err != nil {
			return err
		}

		// Send invoke message and wait for status using broker's Send method
		invokeMsg := &azdext.EventMessage{
			MessageType: &azdext.EventMessage_InvokeProjectHandler{
				InvokeProjectHandler: &azdext.InvokeProjectHandler{
					EventName: eventName,
					Project:   protoProjectConfig,
				},
			},
		}

		return s.runWithEnvReload(ctx, func() error {
			// Use streamCtx which has extension claims for correlation
			response, err := broker.SendAndWait(streamCtx, invokeMsg)
			if err != nil {
				return fmt.Errorf("failed to send invoke message for event %s: %w", eventName, err)
			}

			// Extract status from response
			statusMsg, ok := response.MessageType.(*azdext.EventMessage_ProjectHandlerStatus)
			if !ok {
				return fmt.Errorf("unexpected response type for project event %s", eventName)
			}

			if statusMsg.ProjectHandlerStatus.Status == "failed" {
				return fmt.Errorf(
					"extension %s project hook %s failed: %s",
					extension.Id,
					eventName,
					statusMsg.ProjectHandlerStatus.Message,
				)
			}

			return nil
		})
	}
}

// ----- Service Event Handlers -----

func (s *eventService) onSubscribeServiceEvent(
	ctx context.Context,
	extension *extensions.Extension,
	subscribeMsg *azdext.SubscribeServiceEvent,
	broker *grpcbroker.MessageBroker[azdext.EventMessage],
) error {
	projectConfig, err := s.lazyProject.GetValue()
	if err != nil {
		return err
	}

	for i := 0; i < len(subscribeMsg.EventNames); i++ {
		eventName := subscribeMsg.EventNames[i]
		evt := ext.Event(eventName)
		for _, serviceConfig := range projectConfig.Services {
			if subscribeMsg.Language != "" && string(serviceConfig.Language) != subscribeMsg.Language {
				continue
			}
			if subscribeMsg.Host != "" && string(serviceConfig.Host) != subscribeMsg.Host {
				continue
			}

			// Pass the stream context (ctx) which has extension claims
			handler := s.createServiceEventHandler(ctx, serviceConfig, extension, eventName, broker)
			if err := serviceConfig.AddHandler(ctx, evt, handler); err != nil {
				return fmt.Errorf("failed to add handler for event %s: %w", eventName, err)
			}
		}
	}

	return nil
}

func (s *eventService) createServiceEventHandler(
	streamCtx context.Context,
	serviceConfig *project.ServiceConfig,
	extension *extensions.Extension,
	eventName string,
	broker *grpcbroker.MessageBroker[azdext.EventMessage],
) ext.EventHandlerFn[project.ServiceLifecycleEventArgs] {
	return func(ctx context.Context, args project.ServiceLifecycleEventArgs) error {
		previewTitle := fmt.Sprintf("%s (%s.%s)", extension.DisplayName, args.Service.Name, eventName)
		defer s.syncExtensionOutput(ctx, extension, previewTitle)()

		resolver := noEnvResolver
		env, err := s.lazyEnv.GetValue()
		if err == nil && env != nil {
			resolver = env.Getenv
		}

		objectMapper := mapper.WithResolver(resolver)

		var protoProjectConfig *azdext.ProjectConfig
		if err := objectMapper.Convert(args.Project, &protoProjectConfig); err != nil {
			return err
		}

		var protoServiceConfig *azdext.ServiceConfig
		if err := objectMapper.Convert(args.Service, &protoServiceConfig); err != nil {
			return err
		}

		var protoServiceContext *azdext.ServiceContext
		if err := objectMapper.Convert(args.ServiceContext, &protoServiceContext); err != nil {
			return err
		}

		// Send invoke message and wait for status using broker's Send method
		invokeMsg := &azdext.EventMessage{
			MessageType: &azdext.EventMessage_InvokeServiceHandler{
				InvokeServiceHandler: &azdext.InvokeServiceHandler{
					EventName:      eventName,
					Project:        protoProjectConfig,
					Service:        protoServiceConfig,
					ServiceContext: protoServiceContext,
				},
			},
		}

		return s.runWithEnvReload(ctx, func() error {
			// Use streamCtx which has extension claims for correlation
			response, err := broker.SendAndWait(streamCtx, invokeMsg)
			if err != nil {
				return fmt.Errorf("failed to send invoke message for service event %s: %w", eventName, err)
			}

			// Extract status from response
			statusMsg, ok := response.MessageType.(*azdext.EventMessage_ServiceHandlerStatus)
			if !ok {
				return fmt.Errorf("unexpected response type for service event %s", eventName)
			}

			if statusMsg.ServiceHandlerStatus.Status == "failed" {
				return fmt.Errorf(
					"extension %s service hook %s.%s failed: %s",
					extension.Id,
					args.Service.Name,
					eventName,
					statusMsg.ServiceHandlerStatus.Message,
				)
			}

			return nil
		})
	}
}

// syncExtensionOutput displays the extension output in the preview experience.
// defer the returned function to stop the previewer when the function exits.
func (s *eventService) syncExtensionOutput(
	ctx context.Context,
	extension *extensions.Extension,
	previewTitle string,
) func() {
	// Display the extension output in the preview experience
	previewOptions := &input.ShowPreviewerOptions{
		Prefix:       "  ",
		Title:        previewTitle,
		MaxLineCount: 8,
	}
	// This gets the multi-writer used by the extension and adds the preview writer to it.
	// Any output from stdout on the extension will be shown in the preview window.
	extOut := extension.StdOut()
	previewWriter := s.console.ShowPreviewer(ctx, previewOptions)
	extOut.AddWriter(previewWriter)

	// Stop the previewer when the function exits.
	return func() {
		s.console.StopPreviewer(ctx, false)
		extOut.RemoveWriter(previewWriter)
	}
}

// runWithEnvReload reloads the environment before and after executing the provided action.
func (s *eventService) runWithEnvReload(ctx context.Context, action func() error) error {
	envManager, err := s.lazyEnvManager.GetValue()
	if err != nil {
		return err
	}

	env, err := s.lazyEnv.GetValue()
	if err != nil {
		return err
	}

	// Reload before invoking event handler to ensure environment is updated
	if err := envManager.Reload(ctx, env); err != nil {
		return err
	}

	actionErr := action()
	if actionErr != nil {
		return actionErr
	}

	// Reload after invoking event handler to ensure environment is updated
	return envManager.Reload(ctx, env)
}
