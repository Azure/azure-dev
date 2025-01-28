// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdgrpc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
	Address     string
	Port        int
	AccessToken string
}

type Server struct {
	grpcServer         *grpc.Server
	projectService     azdext.ProjectServiceServer
	environmentService azdext.EnvironmentServiceServer
	promptService      azdext.PromptServiceServer
	userConfigService  azdext.UserConfigServiceServer
	deploymentService  azdext.DeploymentServiceServer
}

func NewServer(
	projectService azdext.ProjectServiceServer,
	environmentService azdext.EnvironmentServiceServer,
	promptService azdext.PromptServiceServer,
	userConfigService azdext.UserConfigServiceServer,
	deploymentService azdext.DeploymentServiceServer,
) *Server {
	return &Server{
		projectService:     projectService,
		environmentService: environmentService,
		promptService:      promptService,
		userConfigService:  userConfigService,
		deploymentService:  deploymentService,
	}
}

func (s *Server) Start() (*ServerInfo, error) {
	accessToken, err := generateToken()
	if err != nil {
		return nil, fmt.Errorf("failed to generate access token: %w", err)
	}

	s.grpcServer = grpc.NewServer(
		grpc.UnaryInterceptor(tokenAuthInterceptor(accessToken)),
	)

	// Use ":0" to let the system assign an available random port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to listen: %w", err)
	}

	// Get the assigned random port
	randomPort := listener.Addr().(*net.TCPAddr).Port

	// Register the Greeter service with the gRPC server
	azdext.RegisterProjectServiceServer(s.grpcServer, s.projectService)
	azdext.RegisterEnvironmentServiceServer(s.grpcServer, s.environmentService)
	azdext.RegisterPromptServiceServer(s.grpcServer, s.promptService)
	azdext.RegisterUserConfigServiceServer(s.grpcServer, s.userConfigService)
	azdext.RegisterDeploymentServiceServer(s.grpcServer, s.deploymentService)

	go func() {
		// Start the gRPC server
		if err := s.grpcServer.Serve(listener); err != nil {
			log.Fatalf("failed to serve: %v", err)
		}
	}()

	log.Printf("AZD Server listening on port %d", randomPort)

	return &ServerInfo{
		Address:     fmt.Sprintf("localhost:%d", randomPort),
		Port:        randomPort,
		AccessToken: accessToken,
	}, nil
}

func (s *Server) Stop() error {
	if s.grpcServer == nil {
		return fmt.Errorf("server is not running")
	}

	s.grpcServer.Stop()
	log.Println("AZD Server stopped")

	return nil
}

func tokenAuthInterceptor(expectedToken string) grpc.UnaryServerInterceptor {
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
		if len(token) == 0 || token[0] != expectedToken {
			return nil, status.Error(codes.Unauthenticated, "invalid token")
		}

		// Proceed to the handler
		return handler(ctx, req)
	}
}

func generateToken() (string, error) {
	bytes := make([]byte, 16) // 128-bit token
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
