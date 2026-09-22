// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal/guidance"
	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type betaEventService struct {
	service *eventService
}

var _ BetaEventServiceEventStreamOverride = (*betaEventService)(nil)

func (s *betaEventService) EventStream(
	stream grpc.BidiStreamingServer[v1beta.EventMessage, v1beta.EventMessage],
) error {
	ctx := stream.Context()
	claims, err := extensions.GetClaimsFromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get extension claims: %w", err)
	}

	extension, err := s.service.extensionManager.GetInstalled(
		extensions.FilterOptions{Id: claims.Subject},
	)
	if err != nil {
		return status.Errorf(codes.FailedPrecondition, "failed to get extension: %s", err)
	}
	if !extension.HasCapability(extensions.LifecycleEventsCapability) {
		return status.Error(codes.PermissionDenied, "extension does not support lifecycle events")
	}

	broker := grpcbroker.NewMessageBroker(
		stream,
		azdext.NewBetaEventMessageEnvelope(),
		extension.Id,
		log.Default(),
	)
	if err := broker.On(func(
		ctx context.Context,
		msg *v1beta.SubscribeProjectEvent,
	) (*v1beta.EventMessage, error) {
		return nil, s.subscribeProject(ctx, extension, msg, broker)
	}); err != nil {
		return err
	}
	if err := broker.On(func(
		ctx context.Context,
		msg *v1beta.SubscribeServiceEvent,
	) (*v1beta.EventMessage, error) {
		return nil, s.subscribeService(ctx, extension, msg, broker)
	}); err != nil {
		return err
	}
	if err := broker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("broker error: %w", err)
	}
	return nil
}

func (s *betaEventService) subscribeProject(
	ctx context.Context,
	extension *extensions.Extension,
	msg *v1beta.SubscribeProjectEvent,
	broker *grpcbroker.MessageBroker[v1beta.EventMessage],
) error {
	if msg == nil || len(msg.EventNames) == 0 {
		return status.Error(codes.InvalidArgument, "event names are required")
	}
	projectConfig, err := s.service.lazyProject.GetValue()
	if err != nil {
		return err
	}
	for i, eventName := range msg.EventNames {
		if eventName == "" {
			return status.Errorf(codes.InvalidArgument, "event name at index %d cannot be empty", i)
		}
		handler := s.createProjectHandler(ctx, extension, eventName, broker)
		if err := projectConfig.AddHandler(ctx, ext.Event(eventName), handler); err != nil {
			return fmt.Errorf("failed to add handler for event %s: %w", eventName, err)
		}
	}
	return nil
}

func (s *betaEventService) createProjectHandler(
	streamCtx context.Context,
	extension *extensions.Extension,
	eventName string,
	broker *grpcbroker.MessageBroker[v1beta.EventMessage],
) ext.EventHandlerFn[project.ProjectLifecycleEventArgs] {
	return func(ctx context.Context, args project.ProjectLifecycleEventArgs) error {
		var completed bool
		invocationID := s.service.followUps.Begin(extension.Id, eventName)
		defer s.service.followUps.Discard(invocationID)

		err := func() error {
			claims, err := extensions.GetClaimsFromContext(streamCtx)
			if err != nil {
				return fmt.Errorf("failed to get extension claims: %w", err)
			}
			invocationCtx := extensions.WithClaimsContext(ctx, claims)

			defer s.service.syncExtensionOutput(
				ctx,
				extension,
				fmt.Sprintf("%s (%s)", extension.DisplayName, eventName),
			)()

			resolver := noEnvResolver
			env, err := s.service.lazyEnv.GetValue()
			if err == nil && env != nil {
				resolver = env.Getenv
			}
			var stableProject *azdext.ProjectConfig
			if err := mapper.WithResolver(resolver).Convert(args.Project, &stableProject); err != nil {
				return err
			}
			protoProject := new(v1beta.ProjectConfig)
			if err := transcodeStableResponse(stableProject, protoProject); err != nil {
				return fmt.Errorf("convert project config to beta: %w", err)
			}
			invoke := &v1beta.EventMessage{
				MessageType: &v1beta.EventMessage_InvokeProjectHandler{
					InvokeProjectHandler: &v1beta.InvokeProjectHandler{
						EventName:    eventName,
						Project:      protoProject,
						InvocationId: invocationID,
					},
				},
			}
			return s.service.runWithEnvReload(ctx, func() error {
				response, err := broker.SendAndWait(invocationCtx, invoke)
				if err != nil {
					return fmt.Errorf("failed to send invoke message for event %s: %w", eventName, err)
				}
				statusMsg, ok := response.MessageType.(*v1beta.EventMessage_ProjectHandlerStatus)
				if !ok {
					return fmt.Errorf("unexpected response type for project event %s", eventName)
				}
				if statusMsg.ProjectHandlerStatus.Status == "failed" {
					if extErr := unwrapBetaError(statusMsg.ProjectHandlerStatus.Error); extErr != nil {
						return extErr
					}
					return fmt.Errorf(
						"extension %s project hook %s failed: %s",
						extension.Id,
						eventName,
						statusMsg.ProjectHandlerStatus.Message,
					)
				}
				completed = statusMsg.ProjectHandlerStatus.Status == "completed"
				return nil
			})
		}()

		if err == nil && completed {
			text, hasText := s.service.followUps.Commit(invocationID)
			if strings.HasPrefix(eventName, "post") && hasText {
				if collector := guidance.FollowUpCollectorFromContext(ctx); collector != nil {
					collector.Add(guidance.FollowUp{
						ExtensionID:  extension.Id,
						CommandOrder: guidance.FollowUpCommandOrderFromContext(ctx),
						EventName:    eventName,
						Layer:        betaFollowUpLayer(args),
						Text:         text,
					})
				}
			}
		}
		return extensions.WrapInvocationError(err, extension.Id, extension.Version, eventName)
	}
}

func (s *betaEventService) subscribeService(
	ctx context.Context,
	extension *extensions.Extension,
	msg *v1beta.SubscribeServiceEvent,
	broker *grpcbroker.MessageBroker[v1beta.EventMessage],
) error {
	if msg == nil || len(msg.EventNames) == 0 {
		return status.Error(codes.InvalidArgument, "event names are required")
	}
	projectConfig, err := s.service.lazyProject.GetValue()
	if err != nil {
		return err
	}
	for i, eventName := range msg.EventNames {
		if eventName == "" {
			return status.Errorf(codes.InvalidArgument, "event name at index %d cannot be empty", i)
		}
		for _, serviceConfig := range projectConfig.ServiceConfigs() {
			if msg.Language != "" && string(serviceConfig.Language) != msg.Language {
				continue
			}
			if msg.Host != "" && string(serviceConfig.Host) != msg.Host {
				continue
			}
			handler := s.createServiceHandler(ctx, serviceConfig, extension, eventName, broker)
			if err := serviceConfig.AddHandler(ctx, ext.Event(eventName), handler); err != nil {
				return fmt.Errorf("failed to add handler for event %s: %w", eventName, err)
			}
		}
	}
	return nil
}

func (s *betaEventService) createServiceHandler(
	streamCtx context.Context,
	serviceConfig *project.ServiceConfig,
	extension *extensions.Extension,
	eventName string,
	broker *grpcbroker.MessageBroker[v1beta.EventMessage],
) ext.EventHandlerFn[project.ServiceLifecycleEventArgs] {
	return func(ctx context.Context, args project.ServiceLifecycleEventArgs) error {
		err := func() error {
			claims, err := extensions.GetClaimsFromContext(streamCtx)
			if err != nil {
				return fmt.Errorf("failed to get extension claims: %w", err)
			}
			invocationCtx := extensions.WithClaimsContext(ctx, claims)

			defer s.service.syncExtensionOutput(
				ctx,
				extension,
				fmt.Sprintf("%s (%s.%s)", extension.DisplayName, args.Service.Name, eventName),
			)()
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
			protoProject := new(v1beta.ProjectConfig)
			if err := transcodeStableResponse(stableProject, protoProject); err != nil {
				return fmt.Errorf("convert project config to beta: %w", err)
			}
			protoService := new(v1beta.ServiceConfig)
			if err := transcodeStableResponse(stableService, protoService); err != nil {
				return fmt.Errorf("convert service config to beta: %w", err)
			}
			protoContext := new(v1beta.ServiceContext)
			if err := transcodeStableResponse(stableContext, protoContext); err != nil {
				return fmt.Errorf("convert service context to beta: %w", err)
			}
			invoke := &v1beta.EventMessage{
				MessageType: &v1beta.EventMessage_InvokeServiceHandler{
					InvokeServiceHandler: &v1beta.InvokeServiceHandler{
						EventName:      eventName,
						Project:        protoProject,
						Service:        protoService,
						ServiceContext: protoContext,
					},
				},
			}
			return s.service.runWithEnvReload(ctx, func() error {
				response, err := broker.SendAndWait(invocationCtx, invoke)
				if err != nil {
					return fmt.Errorf("failed to send invoke message for service event %s: %w", eventName, err)
				}
				statusMsg, ok := response.MessageType.(*v1beta.EventMessage_ServiceHandlerStatus)
				if !ok {
					return fmt.Errorf("unexpected response type for service event %s", eventName)
				}
				if statusMsg.ServiceHandlerStatus.Status == "failed" {
					if extErr := unwrapBetaError(statusMsg.ServiceHandlerStatus.Error); extErr != nil {
						return extErr
					}
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
		}()
		return extensions.WrapInvocationError(err, extension.Id, extension.Version, eventName)
	}
}

func betaFollowUpLayer(args project.ProjectLifecycleEventArgs) string {
	if args.Args == nil {
		return ""
	}
	if layer, ok := args.Args["layer"].(string); ok && layer != "" {
		return layer
	}
	if path, ok := args.Args["path"].(string); ok {
		return path
	}
	return ""
}

func unwrapBetaError(err *v1beta.ExtensionError) error {
	if err == nil {
		return nil
	}

	wire, marshalErr := proto.Marshal(err)
	if marshalErr == nil {
		stableError := new(azdext.ExtensionError)
		if unmarshalErr := proto.Unmarshal(wire, stableError); unmarshalErr == nil {
			return azdext.UnwrapError(stableError)
		}
	}

	return fmt.Errorf("%s", err.Message)
}
