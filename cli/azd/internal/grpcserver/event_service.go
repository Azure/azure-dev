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
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"google.golang.org/grpc"
)

// eventService implements azdext.EventServiceServer.
type eventService struct {
	azdext.UnimplementedEventServiceServer
	extensionManager *extensions.Manager
	projectConfig    *project.ProjectConfig
	env              *environment.Environment

	projectEvents map[string]chan *azdext.ProjectHandlerStatus
	serviceEvents map[string]chan *azdext.ServiceHandlerStatus
	projectMutex  sync.Mutex
	serviceMutex  sync.Mutex
}

func NewEventService(
	extensionManager *extensions.Manager,
	projectConfig *project.ProjectConfig,
	env *environment.Environment,
) azdext.EventServiceServer {
	return &eventService{
		extensionManager: extensionManager,
		projectConfig:    projectConfig,
		env:              env,
		projectEvents:    make(map[string]chan *azdext.ProjectHandlerStatus),
		serviceEvents:    make(map[string]chan *azdext.ServiceHandlerStatus),
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
				s.handleProjectHandlerStatus(extension, statusMsg)
			case *azdext.EventMessage_ServiceHandlerStatus:
				statusMsg := msg.GetServiceHandlerStatus()
				s.handleServiceHandlerStatus(extension, statusMsg)
			}
		}
	}
}

// handleSubscribeProjectEvent processes subscribe events.
func (s *eventService) handleSubscribeProjectEvent(
	extension *extensions.Extension,
	subscribeMsg *azdext.SubscribeProjectEvent,
	stream grpc.BidiStreamingServer[azdext.EventMessage, azdext.EventMessage],
) error {
	for _, eventName := range subscribeMsg.EventNames {
		// Create a channel for this event.
		s.projectMutex.Lock()
		s.projectEvents[eventName] = make(chan *azdext.ProjectHandlerStatus, 1)
		s.projectMutex.Unlock()

		evt := ext.Event(eventName)
		err := s.projectConfig.AddHandler(
			evt,
			func(ctx context.Context, args project.ProjectLifecycleEventArgs) error {
				// Send invoke message to the extension.
				err := stream.Send(&azdext.EventMessage{
					MessageType: &azdext.EventMessage_InvokeProjectHandler{
						InvokeProjectHandler: &azdext.InvokeProjectHandler{
							EventName: eventName,
							Project:   s.createProjectConfig(args.Project),
						},
					},
				})
				if err != nil {
					return fmt.Errorf("failed to send invoke project event message: %w", err)
				}

				// Wait for a status message using select to honor context cancellation.
				s.projectMutex.Lock()
				ch, ok := s.projectEvents[eventName]
				s.projectMutex.Unlock()
				if !ok {
					return fmt.Errorf("no status channel for event: %s", eventName)
				}

				var projectHandlerResponse *azdext.ProjectHandlerStatus
				select {
				case projectHandlerResponse = <-ch:
					// Received status from channel.
				case <-ctx.Done():
					return ctx.Err()
				}

				// Clean up the channel.
				s.projectMutex.Lock()
				delete(s.projectEvents, eventName)
				s.projectMutex.Unlock()

				if projectHandlerResponse.Status == "failed" {
					return fmt.Errorf(
						"extension %s project hook %s failed: %s",
						extension.Id,
						eventName,
						projectHandlerResponse.Message,
					)
				}
				return nil
			})
		if err != nil {
			return fmt.Errorf("failed to add handler for event %s: %w", eventName, err)
		}
	}

	return nil
}

// handleSubscribeServiceEvent processes subscribe service events.
func (s *eventService) handleSubscribeServiceEvent(
	extension *extensions.Extension,
	subscribeMsg *azdext.SubscribeServiceEvent,
	stream grpc.BidiStreamingServer[azdext.EventMessage, azdext.EventMessage],
) error {
	for _, eventName := range subscribeMsg.EventNames {
		// Create a channel for this event.
		s.serviceMutex.Lock()
		s.serviceEvents[eventName] = make(chan *azdext.ServiceHandlerStatus, 1)
		s.serviceMutex.Unlock()

		evt := ext.Event(eventName)

		for _, serviceConfig := range s.projectConfig.Services {
			// Extension can register filters by service language and/or host
			if subscribeMsg.Language != "" && string(serviceConfig.Language) != subscribeMsg.Language {
				continue
			}

			if subscribeMsg.Host != "" && string(serviceConfig.Host) != subscribeMsg.Host {
				continue
			}

			err := serviceConfig.AddHandler(evt, func(ctx context.Context, args project.ServiceLifecycleEventArgs) error {
				// Send invoke message for service event handler.
				err := stream.Send(&azdext.EventMessage{
					MessageType: &azdext.EventMessage_InvokeServiceHandler{
						InvokeServiceHandler: &azdext.InvokeServiceHandler{
							EventName: eventName,
							Project:   s.createProjectConfig(args.Project),
							Service:   s.createServiceConfig(args.Service),
						},
					},
				})
				if err != nil {
					return err
				}

				// Wait for a status message using select to honor context cancellation.
				s.serviceMutex.Lock()
				ch, ok := s.serviceEvents[eventName]
				s.serviceMutex.Unlock()
				if !ok {
					return fmt.Errorf("no status channel for event: %s", eventName)
				}

				var serviceHandlerResponse *azdext.ServiceHandlerStatus
				select {
				case serviceHandlerResponse = <-ch:
					// Received status.
				case <-ctx.Done():
					return ctx.Err()
				}

				// Clean up the channel.
				s.serviceMutex.Lock()
				delete(s.serviceEvents, eventName)
				s.serviceMutex.Unlock()

				if serviceHandlerResponse.Status == "failed" {
					return fmt.Errorf(
						"extension %s service hook %s failed: %s",
						extension.Id,
						eventName,
						serviceHandlerResponse.Message,
					)
				}

				return nil
			})

			if err != nil {
				return fmt.Errorf("failed to add handler for event %s: %w", eventName, err)
			}
		}
	}

	return nil
}

// handleStatusMessage processes status messages and dispatches them to the appropriate channel.
func (s *eventService) handleProjectHandlerStatus(
	_ *extensions.Extension,
	statusMessage *azdext.ProjectHandlerStatus,
) {
	s.projectMutex.Lock()
	ch, ok := s.projectEvents[statusMessage.EventName]
	s.projectMutex.Unlock()

	if ok {
		ch <- statusMessage
	}
}

// handleStatusMessage processes status messages and dispatches them to the appropriate channel.
func (s *eventService) handleServiceHandlerStatus(
	_ *extensions.Extension,
	statusMessage *azdext.ServiceHandlerStatus,
) {
	s.serviceMutex.Lock()
	ch, ok := s.serviceEvents[statusMessage.EventName]
	s.serviceMutex.Unlock()

	if ok {
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
