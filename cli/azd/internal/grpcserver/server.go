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
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal/grpcserver/legacybridge"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	_ "google.golang.org/grpc/encoding/gzip" // registers gzip compressor for gRPC streams
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
	composeService       v1beta.ComposeServiceServer
	workflowService      azdext.WorkflowServiceServer
	extensionService     azdext.ExtensionServiceServer
	serviceTargetService azdext.ServiceTargetServiceServer
	frameworkService     azdext.FrameworkServiceServer
	containerService     azdext.ContainerServiceServer
	accountService       azdext.AccountServiceServer
	aiModelService       azdext.AiModelServiceServer
	copilotService       v1beta.CopilotServiceServer
	provisioningService  azdext.ProvisioningServiceServer
	validationService    azdext.ValidationServiceServer
	telemetryService     azdext.TelemetryServiceServer
	betaServiceOverrides map[BetaService]any
}

func NewServer(
	projectService azdext.ProjectServiceServer,
	environmentService azdext.EnvironmentServiceServer,
	promptService azdext.PromptServiceServer,
	userConfigService azdext.UserConfigServiceServer,
	deploymentService azdext.DeploymentServiceServer,
	eventService azdext.EventServiceServer,
	composeService v1beta.ComposeServiceServer,
	workflowService azdext.WorkflowServiceServer,
	extensionService azdext.ExtensionServiceServer,
	serviceTargetService azdext.ServiceTargetServiceServer,
	frameworkService azdext.FrameworkServiceServer,
	containerService azdext.ContainerServiceServer,
	accountService azdext.AccountServiceServer,
	aiModelService azdext.AiModelServiceServer,
	copilotService v1beta.CopilotServiceServer,
	provisioningService azdext.ProvisioningServiceServer,
	validationService azdext.ValidationServiceServer,
	telemetryService azdext.TelemetryServiceServer,
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
		provisioningService:  provisioningService,
		validationService:    validationService,
		telemetryService:     telemetryService,
		betaServiceOverrides: map[BetaService]any{},
	}
}

// WithOptions applies optional beta service configuration before the server starts.
func (s *Server) WithOptions(options ...ServerOption) *Server {
	for _, option := range options {
		if option != nil {
			option(s)
		}
	}
	return s
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
			s.traceContextInterceptor(),
			legacybridge.UnaryUsageInterceptor(),
		),
		grpc.ChainStreamInterceptor(
			s.errorWrappingStreamInterceptor(),
			s.tokenAuthStreamInterceptor(&serverInfo),
			s.traceContextStreamInterceptor(),
			legacybridge.StreamUsageInterceptor(),
		),
	)

	if err := s.registerServices(); err != nil {
		return nil, fmt.Errorf("failed to register gRPC services: %w", err)
	}

	// Use ":0" to let the system assign an available random port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to listen: %w", err)
	}

	// Get the assigned random port
	randomPort := listener.Addr().(*net.TCPAddr).Port

	serverInfo.Address = fmt.Sprintf("127.0.0.1:%d", randomPort)
	serverInfo.Port = randomPort
	serverInfo.SigningKey = signingKey

	go func() {
		// Start the gRPC server
		if err := s.grpcServer.Serve(listener); err != nil &&
			!errors.Is(err, grpc.ErrServerStopped) {
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

// errorWrappingInterceptor maps host errors into gRPC status errors with structured details
// (auth ErrorInfo, ActionableErrorDetail) so extensions can preserve actionable guidance
// without parsing status message text. See [mapHostError] for the contract.
func (s *Server) errorWrappingInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		resp, err := handler(ctx, req)
		if err != nil {
			err = mapHostError(err)
			if strings.HasPrefix(info.FullMethod, "/azd.extensions.v1beta.") {
				err = translateBetaStatusDetails(err)
			} else if legacybridge.IsLegacyMethod(info.FullMethod) {
				err = legacybridge.TranslateStatusDetails(err)
			}
		}
		return resp, err
	}
}

// errorWrappingStreamInterceptor is the streaming counterpart of errorWrappingInterceptor.
func (s *Server) errorWrappingStreamInterceptor() grpc.StreamServerInterceptor {
	return func(
		srv any,
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		err := handler(srv, ss)
		if err != nil {
			err = mapHostError(err)
			if strings.HasPrefix(info.FullMethod, "/azd.extensions.v1beta.") {
				err = translateBetaStatusDetails(err)
			} else if legacybridge.IsLegacyMethod(info.FullMethod) {
				err = legacybridge.TranslateStatusDetails(err)
			}
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
		wrappedStream := &contextStream{
			ServerStream: ss,
			ctx:          ctx,
		}

		return handler(srv, wrappedStream)
	}
}

// contextStream wraps a grpc.ServerStream to override the context seen by the
// handler, for example to carry validated claims or trace context.
type contextStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *contextStream) Context() context.Context {
	return s.ctx
}

func generateSigningKey() ([]byte, error) {
	bytes := make([]byte, 32) // 256-bit HMAC signing key
	if _, err := rand.Read(bytes); err != nil {
		return nil, err
	}
	return bytes, nil
}
