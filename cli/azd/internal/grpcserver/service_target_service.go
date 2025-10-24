// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"errors"
	"fmt"
	"io"
	"log"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ServiceTargetService implements azdext.ServiceTargetServiceServer.
type ServiceTargetService struct {
	azdext.UnimplementedServiceTargetServiceServer
	container        *ioc.NestedContainer
	extensionManager *extensions.Manager
	lazyEnv          *lazy.Lazy[*environment.Environment]
	providerMap      map[string]azdext.ServiceTargetService_StreamServer
	providerMapMu    sync.Mutex
}

// NewServiceTargetService creates a new ServiceTargetService instance.
func NewServiceTargetService(
	container *ioc.NestedContainer,
	extensionManager *extensions.Manager,
	lazyEnv *lazy.Lazy[*environment.Environment],
) azdext.ServiceTargetServiceServer {
	return &ServiceTargetService{
		container:        container,
		extensionManager: extensionManager,
		lazyEnv:          lazyEnv,
		providerMap:      make(map[string]azdext.ServiceTargetService_StreamServer),
	}
}

// Stream handles the bi-directional streaming for service target operations.
func (s *ServiceTargetService) Stream(
	stream azdext.ServiceTargetService_StreamServer,
) error {
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

	if !extension.HasCapability(extensions.ServiceTargetProviderCapability) {
		return status.Errorf(codes.PermissionDenied, "extension does not support service-target-provider capability")
	}

	msg, err := stream.Recv()
	if errors.Is(err, io.EOF) {
		log.Println("Stream closed by client")
		return nil
	}
	if err != nil {
		return err
	}

	regRequest := msg.GetRegisterServiceTargetRequest()
	if regRequest == nil {
		return status.Errorf(codes.FailedPrecondition, "expected RegisterProviderRequest, got %T", msg.GetMessageType())
	}

	hostType := regRequest.GetHost()
	s.providerMapMu.Lock()
	if _, has := s.providerMap[hostType]; has {
		s.providerMapMu.Unlock()
		return status.Errorf(codes.AlreadyExists, "provider %s already registered", hostType)
	}

	// Register external service target with DI container
	err = s.container.RegisterNamedSingleton(hostType, func(
		console input.Console,
		prompter prompt.Prompter,
	) project.ServiceTarget {
		env, _ := s.lazyEnv.GetValue()
		return project.NewExternalServiceTarget(
			hostType,
			project.ServiceTargetKind(hostType),
			extension,
			stream,
			console,
			prompter,
			env,
		)
	})

	if err != nil {
		s.providerMapMu.Unlock()
		return status.Errorf(codes.Internal, "failed to register service target: %s", err.Error())
	}

	resp := &azdext.ServiceTargetMessage{
		RequestId: msg.RequestId,
		MessageType: &azdext.ServiceTargetMessage_RegisterServiceTargetResponse{
			RegisterServiceTargetResponse: &azdext.RegisterServiceTargetResponse{},
		},
	}

	if err := stream.Send(resp); err != nil {
		s.providerMapMu.Unlock()
		return status.Errorf(codes.Internal, "failed to send response: %s", err.Error())
	}

	s.providerMap[hostType] = stream
	s.providerMapMu.Unlock()
	log.Printf("Registered service target: %s", hostType)

	// Wait for the stream context to be done (client disconnects or server shutdown)
	<-stream.Context().Done()
	log.Printf("Stream closed for provider: %s", hostType)

	s.providerMapMu.Lock()
	delete(s.providerMap, hostType)
	s.providerMapMu.Unlock()
	return nil
}
