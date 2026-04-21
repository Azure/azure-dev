// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"net"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
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
	copilotService       azdext.CopilotServiceServer
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
	copilotService azdext.CopilotServiceServer,
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
		copilotService:       copilotService,
	}
}

func (s *Server) Start() (*ServerInfo, error) {
	signingKey, err := generateSigningKey()
	if err != nil {
		return nil, fmt.Errorf("failed to generate access token: %w", err)
	}

	var serverInfo ServerInfo

	s.grpcServer = grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			s.errorWrappingInterceptor(),
			s.tokenAuthInterceptor(&serverInfo),
		),
		grpc.ChainStreamInterceptor(
			s.errorWrappingStreamInterceptor(),
			s.tokenAuthStreamInterceptor(&serverInfo),
		),
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
	azdext.RegisterCopilotServiceServer(s.grpcServer, s.copilotService)

	serverInfo.Address = fmt.Sprintf("127.0.0.1:%d", randomPort)
	serverInfo.Port = randomPort
	serverInfo.SigningKey = signingKey

	go func() {
		// Start the gRPC server
		if err := s.grpcServer.Serve(listener); err != nil {
			log.Fatalf("failed to serve: %v", err)
		}
	}()

	log.Printf("azd gRPC Server listening on port %d", randomPort)

	return &serverInfo, nil
}

func (s *Server) Stop() error {
	if s.grpcServer == nil {
		return fmt.Errorf("server is not running")
	}

	s.grpcServer.Stop()
	log.Println("azd gRPC Server stopped")

	return nil
}

// errorWrappingInterceptor wraps ErrorWithSuggestion errors to include their suggestion text
// in the error message. This ensures that helpful suggestions (like "run azd auth login")
// are preserved when errors are transmitted over gRPC, where only the error message string is sent.
func (s *Server) errorWrappingInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		resp, err := handler(ctx, req)
		if err != nil {
			err = wrapErrorWithSuggestion(err)
		}
		return resp, err
	}
}

// errorWrappingStreamInterceptor is the streaming counterpart of errorWrappingInterceptor.
// It wraps ErrorWithSuggestion errors from stream handlers so that actionable suggestions
// are preserved in gRPC stream error responses.
func (s *Server) errorWrappingStreamInterceptor() grpc.StreamServerInterceptor {
	return func(
		srv any,
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		err := handler(srv, ss)
		if err != nil {
			err = wrapErrorWithSuggestion(err)
		}
		return err
	}
}

// validateAuthToken extracts and validates the authorization token from gRPC metadata,
// returning a new context with validated claims attached. This shared helper ensures
// both unary and stream RPCs enforce the same token validation.
func (s *Server) validateAuthToken(ctx context.Context, serverInfo *ServerInfo) (context.Context, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ctx, status.Error(codes.Unauthenticated, "metadata missing")
	}

	// Extract the authorization token from metadata
	token := md["authorization"]
	if len(token) == 0 {
		return ctx, status.Error(codes.Unauthenticated, "invalid token")
	}

	claims, err := ParseExtensionToken(token[0], serverInfo)
	if err != nil {
		return ctx, status.Error(codes.Unauthenticated, "invalid token")
	}

	// Store validated claims in context for downstream handlers
	return extensions.WithClaimsContext(ctx, claims), nil
}

func (s *Server) tokenAuthInterceptor(serverInfo *ServerInfo) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		ctx, err := s.validateAuthToken(ctx, serverInfo)
		if err != nil {
			return nil, err
		}

		// Proceed to the handler with enriched context
		return handler(ctx, req)
	}
}

func (s *Server) tokenAuthStreamInterceptor(serverInfo *ServerInfo) grpc.StreamServerInterceptor {
	return func(
		srv any,
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		ctx, err := s.validateAuthToken(ss.Context(), serverInfo)
		if err != nil {
			return err
		}

		// Wrap the stream to inject validated claims into its context
		wrappedStream := &authenticatedStream{
			ServerStream: ss,
			ctx:          ctx,
		}

		return handler(srv, wrappedStream)
	}
}

// authenticatedStream wraps a grpc.ServerStream to provide a context with validated claims.
type authenticatedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *authenticatedStream) Context() context.Context {
	return s.ctx
}

// wrapErrorWithSuggestion checks if the error contains an ErrorWithSuggestion and if so,
// returns a new error that includes the suggestion text in the error message.
// This ensures that helpful suggestions (like "run azd auth login") are preserved
// when errors are transmitted over gRPC, where only the error message string is sent.
//
// Auth-related errors are returned with gRPC auth-adjacent status codes so extensions can
// distinguish failures that can be fixed by re-authentication from policy blocks that require
// administrator action.
func wrapErrorWithSuggestion(err error) error {
	if err == nil {
		return nil
	}

	_, authInteractionErr := errors.AsType[auth.AuthInteractionError](err)
	isAuthErr := errors.Is(err, auth.ErrNoCurrentUser) || authInteractionErr
	authCode := grpcAuthCode(err)

	if suggestionErr, ok := errors.AsType[*internal.ErrorWithSuggestion](err); ok {
		msg := fmt.Sprintf("%s\n%s", err.Error(), suggestionErr.Suggestion)
		if isAuthErr {
			return status.Error(authCode, msg)
		}
		return fmt.Errorf("%w\n%s", err, suggestionErr.Suggestion)
	}

	if isAuthErr {
		return status.Error(authCode, err.Error())
	}

	return err
}

func grpcAuthCode(err error) codes.Code {
	if _, ok := errors.AsType[*auth.TokenProtectionBlockedError](err); ok {
		return codes.PermissionDenied
	}

	return codes.Unauthenticated
}

func generateSigningKey() ([]byte, error) {
	bytes := make([]byte, 32) // 256-bit HMAC signing key
	if _, err := rand.Read(bytes); err != nil {
		return nil, err
	}
	return bytes, nil
}
