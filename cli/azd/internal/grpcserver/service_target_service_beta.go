// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type betaServiceTargetServiceOverride struct {
	service *ServiceTargetService
}

var _ BetaServiceTargetServiceStreamOverride = (*betaServiceTargetServiceOverride)(nil)

func (o *betaServiceTargetServiceOverride) Stream(
	stream grpc.BidiStreamingServer[v1beta.ServiceTargetMessage, v1beta.ServiceTargetMessage],
) error {
	ctx := stream.Context()
	extensionClaims, err := extensions.GetClaimsFromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get extension claims: %w", err)
	}
	extension, err := o.service.extensionManager.GetInstalled(extensions.FilterOptions{Id: extensionClaims.Subject})
	if err != nil {
		return status.Errorf(codes.FailedPrecondition, "failed to get extension: %s", err.Error())
	}
	if !extension.HasCapability(extensions.ServiceTargetProviderCapability) {
		return status.Errorf(codes.PermissionDenied, "extension does not support service-target-provider capability")
	}

	broker := grpcbroker.NewMessageBroker(stream, azdext.NewBetaServiceTargetEnvelope(), extension.Id, log.Default())
	var registeredHostType string
	if err := broker.On(func(
		ctx context.Context,
		request *v1beta.RegisterServiceTargetRequest,
	) (*v1beta.ServiceTargetMessage, error) {
		return o.onRegisterRequest(ctx, request, extension, broker, &registeredHostType)
	}); err != nil {
		return fmt.Errorf("failed to register handler: %w", err)
	}

	if err := broker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("Broker error for provider %s: %v", registeredHostType, err)
		return fmt.Errorf("broker error: %w", err)
	}
	o.service.providerMapMu.Lock()
	delete(o.service.providerMap, registeredHostType)
	o.service.providerMapMu.Unlock()
	return nil
}

func (o *betaServiceTargetServiceOverride) onRegisterRequest(
	ctx context.Context,
	request *v1beta.RegisterServiceTargetRequest,
	extension *extensions.Extension,
	broker *grpcbroker.MessageBroker[v1beta.ServiceTargetMessage],
	registeredHostType *string,
) (*v1beta.ServiceTargetMessage, error) {
	hostType := request.GetHost()
	o.service.providerMapMu.Lock()
	defer o.service.providerMapMu.Unlock()
	if _, exists := o.service.providerMap[hostType]; exists {
		return nil, status.Errorf(codes.AlreadyExists, "provider %s already registered", hostType)
	}
	if !request.GetSupportsPreview() {
		return nil, status.Error(
			codes.InvalidArgument,
			"beta service target registration must advertise deployment preview support",
		)
	}

	err := o.service.container.RegisterNamedSingleton(hostType, func(
		console input.Console,
		prompter prompt.Prompter,
	) project.ServiceTarget {
		return project.NewBetaExternalServiceTarget(
			hostType,
			project.ServiceTargetKind(hostType),
			extension,
			broker,
			console,
			prompter,
			o.service.lazyEnv,
		)
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to register service target: %s", err.Error())
	}

	o.service.providerMap[hostType] = struct{}{}
	*registeredHostType = hostType
	log.Printf("Registered beta service target: %s", hostType)
	return &v1beta.ServiceTargetMessage{
		MessageType: &v1beta.ServiceTargetMessage_RegisterServiceTargetResponse{
			RegisterServiceTargetResponse: &v1beta.RegisterServiceTargetResponse{},
		},
	}, nil
}
