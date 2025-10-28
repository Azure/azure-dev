// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"

	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
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

	lazyProject *lazy.Lazy[*project.ProjectConfig]
	lazyEnv     *lazy.Lazy[*environment.Environment]

	projectEvents sync.Map // key: string, value: chan *azdext.ProjectHandlerStatus
	serviceEvents sync.Map // key: string, value: chan *azdext.ServiceHandlerStatus
}

func NewEventService(
	extensionManager *extensions.Manager,
	lazyProject *lazy.Lazy[*project.ProjectConfig],
	lazyEnv *lazy.Lazy[*environment.Environment],
	console input.Console,
) azdext.EventServiceServer {
	return &eventService{
		extensionManager: extensionManager,
		lazyProject:      lazyProject,
		lazyEnv:          lazyEnv,
		console:          console,
	}
}

// EventStream handles bidirectional streaming.
func (s *eventService) EventStream(stream grpc.BidiStreamingServer[azdext.EventMessage, azdext.EventMessage]) error {
	ctx := stream.Context()
	extensionClaims, err := GetExtensionClaims(ctx)
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

	for {
		select {
		case <-ctx.Done():
			log.Println("Context cancelled by caller, exiting EventStream")
			return nil
		default:
			msg, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				log.Println("Stream closed by server")
				return nil
			}
			if err != nil {
				return err
			}

			switch msg.MessageType.(type) {
			case *azdext.EventMessage_SubscribeProjectEvent:
				subscribeMsg := msg.GetSubscribeProjectEvent()
				if err := s.handleSubscribeProjectEvent(ctx, extension, subscribeMsg, stream); err != nil {
					log.Println(err.Error())
				}
			case *azdext.EventMessage_SubscribeServiceEvent:
				subscribeMsg := msg.GetSubscribeServiceEvent()
				if err := s.handleSubscribeServiceEvent(ctx, extension, subscribeMsg, stream); err != nil {
					log.Println(err.Error())
				}
			case *azdext.EventMessage_ProjectHandlerStatus:
				statusMsg := msg.GetProjectHandlerStatus()
				s.handleProjectHandlerStatus(extension, statusMsg)
			case *azdext.EventMessage_ServiceHandlerStatus:
				statusMsg := msg.GetServiceHandlerStatus()
				s.handleServiceHandlerStatus(extension, statusMsg)
			case *azdext.EventMessage_ExtensionReadyEvent:
				s.handleReadyEvent(extension)
			}
		}
	}
}

func (s *eventService) handleReadyEvent(extension *extensions.Extension) {
	extension.Initialize()
}

// ----- Project Event Handlers -----

func (s *eventService) handleSubscribeProjectEvent(
	ctx context.Context,
	extension *extensions.Extension,
	subscribeMsg *azdext.SubscribeProjectEvent,
	stream grpc.BidiStreamingServer[azdext.EventMessage, azdext.EventMessage],
) error {
	projectConfig, err := s.lazyProject.GetValue()
	if err != nil {
		return err
	}

	for i := 0; i < len(subscribeMsg.EventNames); i++ {
		eventName := subscribeMsg.EventNames[i]
		fullEventName := fmt.Sprintf("%s.%s", extension.Id, eventName)

		// Create a channel for this event.
		s.projectEvents.Store(fullEventName, make(chan *azdext.ProjectHandlerStatus, 1))

		evt := ext.Event(eventName)
		handler := s.createProjectEventHandler(stream, extension, eventName)
		if err := projectConfig.AddHandler(ctx, evt, handler); err != nil {
			return fmt.Errorf("failed to add handler for event %s: %w", eventName, err)
		}
	}
	return nil
}

func (s *eventService) createProjectEventHandler(
	stream grpc.BidiStreamingServer[azdext.EventMessage, azdext.EventMessage],
	extension *extensions.Extension,
	eventName string,
) ext.EventHandlerFn[project.ProjectLifecycleEventArgs] {
	return func(ctx context.Context, args project.ProjectLifecycleEventArgs) error {
		previewTitle := fmt.Sprintf("%s (%s)", extension.DisplayName, eventName)
		defer s.syncExtensionOutput(ctx, extension, previewTitle)()

		// Send the invoke message.
		if err := s.sendProjectInvokeMessage(stream, eventName, args); err != nil {
			return err
		}

		// Wait for status response.
		return s.waitForProjectStatus(ctx, eventName, extension)
	}
}

func (s *eventService) sendProjectInvokeMessage(
	stream grpc.BidiStreamingServer[azdext.EventMessage, azdext.EventMessage],
	eventName string,
	args project.ProjectLifecycleEventArgs,
) error {
	resolver := noEnvResolver
	env, err := s.lazyEnv.GetValue()
	if err == nil && env != nil {
		resolver = env.Getenv
	}

	var protoProjectConfig *azdext.ProjectConfig
	if err := mapper.WithResolver(resolver).Convert(args.Project, &protoProjectConfig); err != nil {
		return err
	}

	return stream.Send(&azdext.EventMessage{
		MessageType: &azdext.EventMessage_InvokeProjectHandler{
			InvokeProjectHandler: &azdext.InvokeProjectHandler{
				EventName: eventName,
				Project:   protoProjectConfig,
			},
		},
	})
}

func (s *eventService) waitForProjectStatus(ctx context.Context, eventName string, extension *extensions.Extension) error {
	extensionEventName := fmt.Sprintf("%s.%s", extension.Id, eventName)
	val, ok := s.projectEvents.Load(extensionEventName)
	if !ok {
		return fmt.Errorf("no status channel for event: %s", eventName)
	}
	ch := val.(chan *azdext.ProjectHandlerStatus)

	var status *azdext.ProjectHandlerStatus
	select {
	case <-ctx.Done():
		return ctx.Err()
	case status = <-ch:
		// Clean up after receiving status.
		s.projectEvents.Delete(extensionEventName)
	}

	if status.Status == "failed" {
		return fmt.Errorf("extension %s project hook %s failed: %s", extension.Id, eventName, status.Message)
	}

	return nil
}

// ----- Service Event Handlers -----

func (s *eventService) handleSubscribeServiceEvent(
	ctx context.Context,
	extension *extensions.Extension,
	subscribeMsg *azdext.SubscribeServiceEvent,
	stream grpc.BidiStreamingServer[azdext.EventMessage, azdext.EventMessage],
) error {
	projectConfig, err := s.lazyProject.GetValue()
	if err != nil {
		return err
	}

	for i := 0; i < len(subscribeMsg.EventNames); i++ {
		eventName := subscribeMsg.EventNames[i]
		evt := ext.Event(eventName)
		for serviceName, serviceConfig := range projectConfig.Services {
			if subscribeMsg.Language != "" && string(serviceConfig.Language) != subscribeMsg.Language {
				continue
			}
			if subscribeMsg.Host != "" && string(serviceConfig.Host) != subscribeMsg.Host {
				continue
			}

			// Create a channel for this event.
			// fullEventName is used to uniquely identify the event for a specific service.
			fullEventName := fmt.Sprintf("%s.%s.%s", extension.Id, serviceName, eventName)
			s.serviceEvents.Store(fullEventName, make(chan *azdext.ServiceHandlerStatus, 1))

			handler := s.createServiceEventHandler(stream, serviceConfig, extension, eventName)
			if err := serviceConfig.AddHandler(ctx, evt, handler); err != nil {
				return fmt.Errorf("failed to add handler for event %s: %w", eventName, err)
			}
		}
	}
	return nil
}

func (s *eventService) createServiceEventHandler(
	stream grpc.BidiStreamingServer[azdext.EventMessage, azdext.EventMessage],
	serviceConfig *project.ServiceConfig,
	extension *extensions.Extension,
	eventName string,
) ext.EventHandlerFn[project.ServiceLifecycleEventArgs] {
	fullEventName := fmt.Sprintf("%s.%s.%s", extension.Id, serviceConfig.Name, eventName)

	return func(ctx context.Context, args project.ServiceLifecycleEventArgs) error {
		previewTitle := fmt.Sprintf("%s (%s.%s)", extension.DisplayName, args.Service.Name, eventName)
		defer s.syncExtensionOutput(ctx, extension, previewTitle)()

		// Send the invoke message.
		if err := s.sendServiceInvokeMessage(stream, eventName, args); err != nil {
			return err
		}

		// Wait for status response.
		return s.waitForServiceStatus(ctx, fullEventName, extension)
	}
}

func (s *eventService) sendServiceInvokeMessage(
	stream grpc.BidiStreamingServer[azdext.EventMessage, azdext.EventMessage],
	eventName string,
	args project.ServiceLifecycleEventArgs,
) error {
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

	return stream.Send(&azdext.EventMessage{
		MessageType: &azdext.EventMessage_InvokeServiceHandler{
			InvokeServiceHandler: &azdext.InvokeServiceHandler{
				EventName:      eventName,
				Project:        protoProjectConfig,
				Service:        protoServiceConfig,
				ServiceContext: protoServiceContext,
			},
		},
	})
}

func (s *eventService) waitForServiceStatus(
	ctx context.Context,
	fullEventName string,
	extension *extensions.Extension,
) error {
	val, ok := s.serviceEvents.Load(fullEventName)
	if !ok {
		return fmt.Errorf("no status channel for event: %s", fullEventName)
	}
	ch := val.(chan *azdext.ServiceHandlerStatus)

	var status *azdext.ServiceHandlerStatus
	select {
	case <-ctx.Done():
		return ctx.Err()
	case status = <-ch:
		// Clean up after receiving status.
		s.serviceEvents.Delete(fullEventName)
	}
	if status.Status == "failed" {
		return fmt.Errorf("extension %s service hook %s failed: %s", extension.Id, fullEventName, status.Message)
	}
	return nil
}

// ----- Dispatch Handlers -----

func (s *eventService) handleProjectHandlerStatus(
	extension *extensions.Extension,
	statusMessage *azdext.ProjectHandlerStatus,
) {
	fullEventName := fmt.Sprintf("%s.%s", extension.Id, statusMessage.EventName)
	if val, ok := s.projectEvents.Load(fullEventName); ok {
		ch := val.(chan *azdext.ProjectHandlerStatus)
		ch <- statusMessage
	}
}

func (s *eventService) handleServiceHandlerStatus(
	extension *extensions.Extension,
	statusMessage *azdext.ServiceHandlerStatus,
) {
	fullEventName := fmt.Sprintf("%s.%s.%s", extension.Id, statusMessage.ServiceName, statusMessage.EventName)
	if val, ok := s.serviceEvents.Load(fullEventName); ok {
		ch := val.(chan *azdext.ServiceHandlerStatus)
		ch <- statusMessage
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
