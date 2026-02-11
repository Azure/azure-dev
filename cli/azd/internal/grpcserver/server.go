// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"net"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type ServerInfo struct {
	Address    string
	Port       int
	SigningKey []byte
}

type Server struct {
	grpcServer           *grpc.Server
	projectService       azdext.ProjectServiceServer
	environmentService   azdext.EnvironmentServiceServer
	promptService        azdext.PromptServiceServer
	userConfigService    azdext.UserConfigServiceServer
	deploymentService    azdext.DeploymentServiceServer
	eventService         azdext.EventServiceServer
	composeService       azdext.ComposeServiceServer
	workflowService      azdext.WorkflowServiceServer
	extensionService     azdext.ExtensionServiceServer
	serviceTargetService azdext.ServiceTargetServiceServer
	frameworkService     azdext.FrameworkServiceServer
	containerService     azdext.ContainerServiceServer
	accountService       azdext.AccountServiceServer
	aiModelService       azdext.AiModelServiceServer
}

func NewServer(
	projectService azdext.ProjectServiceServer,
	environmentService azdext.EnvironmentServiceServer,
	promptService azdext.PromptServiceServer,
	userConfigService azdext.UserConfigServiceServer,
	deploymentService azdext.DeploymentServiceServer,
	eventService azdext.EventServiceServer,
	composeService azdext.ComposeServiceServer,
	workflowService azdext.WorkflowServiceServer,
	extensionService azdext.ExtensionServiceServer,
	serviceTargetService azdext.ServiceTargetServiceServer,
	frameworkService azdext.FrameworkServiceServer,
	containerService azdext.ContainerServiceServer,
	accountService azdext.AccountServiceServer,
	aiModelService azdext.AiModelServiceServer,
) *Server {
	return &Server{
		projectService:       projectService,
		environmentService:   environmentService,
		promptService:        promptService,
		userConfigService:    userConfigService,
		deploymentService:    deploymentService,
		eventService:         eventService,
		composeService:       composeService,
		workflowService:      workflowService,
		extensionService:     extensionService,
		serviceTargetService: serviceTargetService,
		frameworkService:     frameworkService,
		containerService:     containerService,
		accountService:       accountService,
		aiModelService:       aiModelService,
	}
}

func (s *Server) Start() (*ServerInfo, error) {
	signingKey, err := generateSigningKey()
	if err != nil {
		return nil, fmt.Errorf("failed to generate access token: %w", err)
	}

	var serverInfo ServerInfo

	s.grpcServer = grpc.NewServer(
		grpc.UnaryInterceptor(s.tokenAuthInterceptor(&serverInfo)),
	)

	// Use ":0" to let the system assign an available random port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to listen: %w", err)
	}

	// Get the assigned random port
	randomPort := listener.Addr().(*net.TCPAddr).Port

	// Register the azd services with the gRPC server
	azdext.RegisterProjectServiceServer(s.grpcServer, s.projectService)
	azdext.RegisterEnvironmentServiceServer(s.grpcServer, s.environmentService)
	azdext.RegisterPromptServiceServer(s.grpcServer, s.promptService)
	azdext.RegisterUserConfigServiceServer(s.grpcServer, s.userConfigService)
	azdext.RegisterDeploymentServiceServer(s.grpcServer, s.deploymentService)
	azdext.RegisterEventServiceServer(s.grpcServer, s.eventService)
	azdext.RegisterComposeServiceServer(s.grpcServer, s.composeService)
	azdext.RegisterWorkflowServiceServer(s.grpcServer, s.workflowService)
	azdext.RegisterExtensionServiceServer(s.grpcServer, s.extensionService)
	azdext.RegisterServiceTargetServiceServer(s.grpcServer, s.serviceTargetService)
	azdext.RegisterFrameworkServiceServer(s.grpcServer, s.frameworkService)
	azdext.RegisterContainerServiceServer(s.grpcServer, s.containerService)
	azdext.RegisterAccountServiceServer(s.grpcServer, s.accountService)
	azdext.RegisterAiModelServiceServer(s.grpcServer, s.aiModelService)

	serverInfo.Address = fmt.Sprintf("localhost:%d", randomPort)
	serverInfo.Port = randomPort
	serverInfo.SigningKey = signingKey

	go func() {
		// Start the gRPC server
		if err := s.grpcServer.Serve(listener); err != nil {
			log.Fatalf("failed to serve: %v", err)
		}
	}()

	log.Printf("AZD gRPC Server listening on port %d", randomPort)

	return &ServerInfo{
		Address:    fmt.Sprintf("localhost:%d", randomPort),
		Port:       randomPort,
		SigningKey: signingKey,
	}, nil
}

func (s *Server) Stop() error {
	if s.grpcServer == nil {
		return fmt.Errorf("server is not running")
	}

	s.grpcServer.Stop()
	log.Println("AZD gRPC Server stopped")

	return nil
}

func (s *Server) tokenAuthInterceptor(serverInfo *ServerInfo) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "metadata missing")
		}

		// Extract the authorization token from metadata
		token := md["authorization"]
		if len(token) == 0 {
			return nil, status.Error(codes.Unauthenticated, "invalid token")
		}

		_, err := ParseExtensionToken(token[0], serverInfo)
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, "invalid token")
		}

		// Proceed to the handler
		return handler(ctx, req)
	}
}

func generateSigningKey() ([]byte, error) {
	bytes := make([]byte, 16) // 128-bit token
	if _, err := rand.Read(bytes); err != nil {
		return nil, err
	}
	return bytes, nil
}
