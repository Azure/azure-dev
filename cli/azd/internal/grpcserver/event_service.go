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

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"google.golang.org/grpc"
)

// eventService implements azdext.EventServiceServer.
type eventService struct {
	azdext.UnimplementedEventServiceServer
	extensionManager *extensions.Manager
	projectConfig    *project.ProjectConfig
	env              *environment.Environment
	console          input.Console

	projectEvents sync.Map // key: string, value: chan *azdext.ProjectHandlerStatus
	serviceEvents sync.Map // key: string, value: chan *azdext.ServiceHandlerStatus
}

func NewEventService(
	extensionManager *extensions.Manager,
	projectConfig *project.ProjectConfig,
	env *environment.Environment,
	console input.Console,
) azdext.EventServiceServer {
	return &eventService{
		extensionManager: extensionManager,
		projectConfig:    projectConfig,
		env:              env,
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

	options := extensions.GetInstalledOptions{
		Id: extensionClaims.Subject,
	}

	extension, err := s.extensionManager.GetInstalled(options)
	if err != nil {
		return fmt.Errorf("failed to get extension: %w", err)
	}

	if !extension.HasCapability(extensions.LifecycleEventsCapability) {
		return fmt.Errorf("extension does not support lifecycle events")
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
				if err := s.handleSubscribeProjectEvent(extension, subscribeMsg, stream); err != nil {
					log.Println(err.Error())
				}
			case *azdext.EventMessage_SubscribeServiceEvent:
				subscribeMsg := msg.GetSubscribeServiceEvent()
				if err := s.handleSubscribeServiceEvent(extension, subscribeMsg, stream); err != nil {
					log.Println(err.Error())
				}
			case *azdext.EventMessage_ProjectHandlerStatus:
				statusMsg := msg.GetProjectHandlerStatus()
				s.handleProjectHandlerStatus(statusMsg)
			case *azdext.EventMessage_ServiceHandlerStatus:
				statusMsg := msg.GetServiceHandlerStatus()
				s.handleServiceHandlerStatus(statusMsg)
			}
		}
	}
}

// ----- Project Event Handlers -----

func (s *eventService) handleSubscribeProjectEvent(
	extension *extensions.Extension,
	subscribeMsg *azdext.SubscribeProjectEvent,
	stream grpc.BidiStreamingServer[azdext.EventMessage, azdext.EventMessage],
) error {
	for i := 0; i < len(subscribeMsg.EventNames); i++ {
		eventName := subscribeMsg.EventNames[i]

		// Create a channel for this event.
		s.projectEvents.Store(eventName, make(chan *azdext.ProjectHandlerStatus, 1))

		evt := ext.Event(eventName)
		handler := s.createProjectEventHandler(stream, extension, eventName)
		if err := s.projectConfig.AddHandler(evt, handler); err != nil {
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
		if err := s.sendProjectInvokeMessage(stream, eventName, args.Project); err != nil {
			return err
		}

		// Wait for status response.
		return s.waitForProjectStatus(ctx, eventName, extension)
	}
}

func (s *eventService) sendProjectInvokeMessage(
	stream grpc.BidiStreamingServer[azdext.EventMessage, azdext.EventMessage],
	eventName string,
	proj *project.ProjectConfig,
) error {
	return stream.Send(&azdext.EventMessage{
		MessageType: &azdext.EventMessage_InvokeProjectHandler{
			InvokeProjectHandler: &azdext.InvokeProjectHandler{
				EventName: eventName,
				Project:   s.createProjectConfig(proj),
			},
		},
	})
}

func (s *eventService) waitForProjectStatus(ctx context.Context, eventName string, extension *extensions.Extension) error {
	val, ok := s.projectEvents.Load(eventName)
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
		s.projectEvents.Delete(eventName)
	}

	if status.Status == "failed" {
		return fmt.Errorf("extension %s project hook %s failed: %s", extension.Id, eventName, status.Message)
	}

	return nil
}

// ----- Service Event Handlers -----

func (s *eventService) handleSubscribeServiceEvent(
	extension *extensions.Extension,
	subscribeMsg *azdext.SubscribeServiceEvent,
	stream grpc.BidiStreamingServer[azdext.EventMessage, azdext.EventMessage],
) error {
	for i := 0; i < len(subscribeMsg.EventNames); i++ {
		eventName := subscribeMsg.EventNames[i]
		evt := ext.Event(eventName)
		for serviceName, serviceConfig := range s.projectConfig.Services {
			if subscribeMsg.Language != "" && string(serviceConfig.Language) != subscribeMsg.Language {
				continue
			}
			if subscribeMsg.Host != "" && string(serviceConfig.Host) != subscribeMsg.Host {
				continue
			}

			// Create a channel for this event.
			// fullEventName is used to uniquely identify the event for a specific service.
			fullEventName := fmt.Sprintf("%s.%s", serviceName, eventName)
			s.serviceEvents.Store(fullEventName, make(chan *azdext.ServiceHandlerStatus, 1))

			handler := s.createServiceEventHandler(stream, serviceConfig, extension, eventName)
			if err := serviceConfig.AddHandler(evt, handler); err != nil {
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
	fullEventName := fmt.Sprintf("%s.%s", serviceConfig.Name, eventName)

	return func(ctx context.Context, args project.ServiceLifecycleEventArgs) error {
		previewTitle := fmt.Sprintf("%s (%s.%s)", extension.DisplayName, args.Service.Name, eventName)
		defer s.syncExtensionOutput(ctx, extension, previewTitle)()

		// Send the invoke message.
		if err := s.sendServiceInvokeMessage(stream, eventName, args.Project, args.Service); err != nil {
			return err
		}

		// Wait for status response.
		return s.waitForServiceStatus(ctx, fullEventName, extension)
	}
}

func (s *eventService) sendServiceInvokeMessage(
	stream grpc.BidiStreamingServer[azdext.EventMessage, azdext.EventMessage],
	eventName string,
	proj *project.ProjectConfig,
	svc *project.ServiceConfig,
) error {
	return stream.Send(&azdext.EventMessage{
		MessageType: &azdext.EventMessage_InvokeServiceHandler{
			InvokeServiceHandler: &azdext.InvokeServiceHandler{
				EventName: eventName,
				Project:   s.createProjectConfig(proj),
				Service:   s.createServiceConfig(svc),
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

func (s *eventService) handleProjectHandlerStatus(statusMessage *azdext.ProjectHandlerStatus) {
	if val, ok := s.projectEvents.Load(statusMessage.EventName); ok {
		ch := val.(chan *azdext.ProjectHandlerStatus)
		ch <- statusMessage
	}
}

func (s *eventService) handleServiceHandlerStatus(statusMessage *azdext.ServiceHandlerStatus) {
	fullEventName := fmt.Sprintf("%s.%s", statusMessage.ServiceName, statusMessage.EventName)

	if val, ok := s.serviceEvents.Load(fullEventName); ok {
		ch := val.(chan *azdext.ServiceHandlerStatus)
		ch <- statusMessage
	}
}

// createProjectConfig converts a project.ProjectConfig into the azdext.ProjectConfig wire format.
func (s *eventService) createProjectConfig(proj *project.ProjectConfig) *azdext.ProjectConfig {
	resourceGroupName, err := proj.ResourceGroupName.Envsubst(s.env.Getenv)
	if err != nil {
		log.Printf("failed to envsubst resource group name: %v", err)
	}

	services := make(map[string]*azdext.ServiceConfig, len(proj.Services))
	for i, svc := range proj.Services {
		services[i] = s.createServiceConfig(svc)
	}

	projectConfig := &azdext.ProjectConfig{
		Name:              proj.Name,
		ResourceGroupName: resourceGroupName,
		Path:              proj.Path,
		Metadata: func() *azdext.ProjectMetadata {
			if proj.Metadata != nil {
				return &azdext.ProjectMetadata{Template: proj.Metadata.Template}
			}
			return nil
		}(),
		Infra: &azdext.InfraOptions{
			Provider: string(proj.Infra.Provider),
			Path:     proj.Infra.Path,
			Module:   proj.Infra.Module,
		},
		Services: services,
	}

	return projectConfig
}

// createServiceConfig converts a project.ServiceConfig into the azdext.ServiceConfig wire format.
func (s *eventService) createServiceConfig(svc *project.ServiceConfig) *azdext.ServiceConfig {
	resourceGroupName, err := svc.ResourceGroupName.Envsubst(s.env.Getenv)
	if err != nil {
		log.Printf("failed to envsubst resource group name: %v", err)
	}

	resourceName, err := svc.ResourceName.Envsubst(s.env.Getenv)
	if err != nil {
		log.Printf("failed to envsubst resource name: %v", err)
	}

	image, err := svc.Image.Envsubst(s.env.Getenv)
	if err != nil {
		log.Printf("failed to envsubst image: %v", err)
	}

	return &azdext.ServiceConfig{
		Name:              svc.Name,
		ResourceGroupName: resourceGroupName,
		ResourceName:      resourceName,
		ApiVersion:        svc.ApiVersion,
		RelativePath:      svc.RelativePath,
		Host:              string(svc.Host),
		Language:          string(svc.Language),
		OutputPath:        svc.OutputPath,
		Image:             image,
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
