// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"errors"
	"fmt"
	"io"
	"log"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/external"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ProvisioningService implements azdext.ProvisioningServiceServer.
type ProvisioningService struct {
	azdext.UnimplementedProvisioningServiceServer
	container        *ioc.NestedContainer
	extensionManager *extensions.Manager
	providerMap      map[string]azdext.ProvisioningService_StreamServer
}

// NewProvisioningService creates a new ProvisioningService instance.
func NewProvisioningService(
	container *ioc.NestedContainer,
	extensionManager *extensions.Manager,
) azdext.ProvisioningServiceServer {
	return &ProvisioningService{
		container:        container,
		extensionManager: extensionManager,
		providerMap:      make(map[string]azdext.ProvisioningService_StreamServer),
	}
}

// Stream handles the bi-directional streaming for provisioning operations.
func (s *ProvisioningService) Stream(
	stream azdext.ProvisioningService_StreamServer,
) error {
	ctx := stream.Context()
	extensionClaims, err := GetExtensionClaims(ctx)
	if err != nil {
		return fmt.Errorf("failed to get extension claims: %w", err)
	}

	options := extensions.LookupOptions{
		Id: extensionClaims.Subject,
	}

	extension, err := s.extensionManager.GetInstalled(options)
	if err != nil {
		return status.Errorf(codes.FailedPrecondition, "failed to get extension: %s", err.Error())
	}

	if !extension.HasCapability(extensions.ProvisionProviderCapability) {
		return status.Errorf(codes.PermissionDenied, "extension does not support provisioning-provider capability")
	}

	msg, err := stream.Recv()
	if errors.Is(err, io.EOF) {
		log.Println("Stream closed by client")
		return nil
	}
	if err != nil {
		return err
	}

	regRequest := msg.GetRegisterProviderRequest()
	if regRequest == nil {
		return status.Errorf(codes.FailedPrecondition, "expected RegisterProviderRequest, got %T", msg.GetMessageType())
	}

	providerName := regRequest.Name
	if _, has := s.providerMap[providerName]; has {
		return status.Errorf(codes.AlreadyExists, "provider %s already registered", providerName)
	}

	err = s.container.RegisterNamedTransient(providerName, func(
		envManager environment.Manager,
		env *environment.Environment,
		console input.Console,
		prompter prompt.Prompter,
	) provisioning.Provider {
		return external.NewExternalProvider(providerName, extension, stream, envManager, env, console, prompter)
	})

	if err != nil {
		return status.Errorf(codes.Internal, "failed to register provider: %s", err.Error())
	}

	resp := &azdext.ProvisioningMessage{
		RequestId: msg.RequestId,
		MessageType: &azdext.ProvisioningMessage_RegisterProviderResponse{
			RegisterProviderResponse: &azdext.RegisterProviderResponse{},
		},
	}

	if err := stream.Send(resp); err != nil {
		return status.Errorf(codes.Internal, "failed to send response: %s", err.Error())
	}

	s.providerMap[providerName] = stream
	log.Printf("Registered provisioning provider: %s", providerName)

	// Wait for the stream context to be done (client disconnects or server shutdown)
	<-stream.Context().Done()
	log.Printf("Stream closed for provider: %s", providerName)
	return nil
}
