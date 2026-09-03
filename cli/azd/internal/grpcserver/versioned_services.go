// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const stableContractMessagePrefix = "azd.extensions.v1."

// BetaService identifies a beta service that can receive focused method overrides.
type BetaService string

// ServerOption customizes gRPC server construction.
type ServerOption func(*Server)

// WithBetaServiceOverride supplies an implementation of one or more generated beta method override interfaces.
// Shared beta methods dispatch to the override before adapting to stable business logic. Beta-only methods dispatch
// to the override or return codes.Unimplemented.
func WithBetaServiceOverride(service BetaService, override any) ServerOption {
	return func(server *Server) {
		if server.betaServiceOverrides == nil {
			server.betaServiceOverrides = map[BetaService]any{}
		}
		server.betaServiceOverrides[service] = override
	}
}

func validateBetaServiceOverride(
	service string,
	override any,
	wholeServer reflect.Type,
	focusedOverrides ...reflect.Type,
) error {
	if override == nil {
		return nil
	}

	overrideType := reflect.TypeOf(override)
	if overrideType.Implements(wholeServer) {
		return fmt.Errorf(
			"beta override for %s must implement focused method override interfaces, not v1beta.%sServer",
			service,
			service,
		)
	}
	if slices.ContainsFunc(focusedOverrides, overrideType.Implements) {
		return nil
	}

	return fmt.Errorf(
		"beta override for %s does not implement a generated focused method override interface",
		service,
	)
}

func (s *Server) registerServices() error {
	azdext.RegisterProjectServiceServer(s.grpcServer, s.projectService)
	azdext.RegisterEnvironmentServiceServer(s.grpcServer, s.environmentService)
	azdext.RegisterPromptServiceServer(s.grpcServer, s.promptService)
	azdext.RegisterUserConfigServiceServer(s.grpcServer, s.userConfigService)
	azdext.RegisterDeploymentServiceServer(s.grpcServer, s.deploymentService)
	azdext.RegisterEventServiceServer(s.grpcServer, s.eventService)
	azdext.RegisterWorkflowServiceServer(s.grpcServer, s.workflowService)
	azdext.RegisterExtensionServiceServer(s.grpcServer, s.extensionService)
	azdext.RegisterServiceTargetServiceServer(s.grpcServer, s.serviceTargetService)
	azdext.RegisterFrameworkServiceServer(s.grpcServer, s.frameworkService)
	azdext.RegisterContainerServiceServer(s.grpcServer, s.containerService)
	azdext.RegisterAccountServiceServer(s.grpcServer, s.accountService)
	azdext.RegisterAiModelServiceServer(s.grpcServer, s.aiModelService)
	azdext.RegisterProvisioningServiceServer(s.grpcServer, s.provisioningService)
	azdext.RegisterValidationServiceServer(s.grpcServer, s.validationService)

	if err := s.registerLegacyServices(); err != nil {
		return fmt.Errorf("register legacy extension services: %w", err)
	}

	return registerBetaServices(
		s.grpcServer,
		map[BetaService]any{
			BetaProjectService:       s.projectService,
			BetaEnvironmentService:   s.environmentService,
			BetaPromptService:        s.promptService,
			BetaUserConfigService:    s.userConfigService,
			BetaDeploymentService:    s.deploymentService,
			BetaEventService:         s.eventService,
			BetaComposeService:       s.composeService,
			BetaWorkflowService:      s.workflowService,
			BetaExtensionService:     s.extensionService,
			BetaServiceTargetService: s.serviceTargetService,
			BetaFrameworkService:     s.frameworkService,
			BetaContainerService:     s.containerService,
			BetaAccountService:       s.accountService,
			BetaAiModelService:       s.aiModelService,
			BetaCopilotService:       s.copilotService,
			BetaProvisioningService:  s.provisioningService,
			BetaValidationService:    s.validationService,
			BetaTelemetryService:     s.telemetryService,
		},
		s.betaServiceOverrides,
	)
}

func transcodeBetaRequest(source, destination proto.Message) error {
	return transcodeVersionedMessage(source, destination, true)
}

func transcodeStableResponse(source, destination proto.Message) error {
	return transcodeVersionedMessage(source, destination, false)
}

func transcodeVersionedMessage(source, destination proto.Message, discardUnknown bool) error {
	if isNilProtoMessage(source) {
		return errors.New("source protobuf message is nil")
	}
	if isNilProtoMessage(destination) {
		return errors.New("destination protobuf message is nil")
	}

	wire, err := proto.Marshal(source)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", source.ProtoReflect().Descriptor().FullName(), err)
	}
	if err := (proto.UnmarshalOptions{DiscardUnknown: discardUnknown}).Unmarshal(wire, destination); err != nil {
		return fmt.Errorf("unmarshal %s: %w", destination.ProtoReflect().Descriptor().FullName(), err)
	}

	return nil
}

func adaptBetaUnary[
	BetaRequest proto.Message,
	StableRequest proto.Message,
	StableResponse proto.Message,
	BetaResponse proto.Message,
](
	ctx context.Context,
	betaRequest BetaRequest,
	stableRequest StableRequest,
	invokeStable func(context.Context, StableRequest) (StableResponse, error),
	betaResponse BetaResponse,
	operation string,
) (BetaResponse, error) {
	var zero BetaResponse
	if err := transcodeBetaRequest(betaRequest, stableRequest); err != nil {
		return zero, fmt.Errorf("convert %s request from beta to stable: %w", operation, err)
	}

	stableResponse, err := invokeStable(ctx, stableRequest)
	if err != nil {
		return zero, err
	}
	if isNilProtoMessage(stableResponse) {
		return zero, fmt.Errorf("stable %s returned a nil response", operation)
	}
	if err := transcodeStableResponse(stableResponse, betaResponse); err != nil {
		return zero, fmt.Errorf("convert %s response from stable to beta: %w", operation, err)
	}

	return betaResponse, nil
}

func isNilProtoMessage(message proto.Message) bool {
	if message == nil {
		return true
	}
	value := reflect.ValueOf(message)
	return value.Kind() == reflect.Pointer && value.IsNil()
}

type versionedBidiServerStream[
	StableRequest any,
	StableResponse any,
	BetaRequest any,
	BetaResponse any,
] struct {
	grpc.ServerStream
	beta            grpc.BidiStreamingServer[BetaRequest, BetaResponse]
	operation       string
	requestToStable func(*BetaRequest) (*StableRequest, error)
	responseToBeta  func(*StableResponse) (*BetaResponse, error)
}

func (s *versionedBidiServerStream[StableRequest, StableResponse, BetaRequest, BetaResponse]) Recv() (
	*StableRequest,
	error,
) {
	request, err := s.beta.Recv()
	if err != nil {
		return nil, err
	}
	stableRequest, err := s.requestToStable(request)
	if err != nil {
		return nil, fmt.Errorf("convert %s received message from beta to stable: %w", s.operation, err)
	}
	return stableRequest, nil
}

func (s *versionedBidiServerStream[StableRequest, StableResponse, BetaRequest, BetaResponse]) Send(
	response *StableResponse,
) error {
	betaResponse, err := s.responseToBeta(response)
	if err != nil {
		return fmt.Errorf("convert %s sent message from stable to beta: %w", s.operation, err)
	}
	return s.beta.Send(betaResponse)
}

// translateBetaStatusDetails changes contract-local stable Any details into their beta message equivalents.
// Non-contract details, including all google.rpc details, retain their original bytes and type URLs.
func translateBetaStatusDetails(err error) error {
	if err == nil {
		return nil
	}

	st, ok := azdext.GRPCStatusFromError(err)
	if !ok {
		return err
	}

	statusProto := proto.Clone(st.Proto()).(*statuspb.Status)
	translated := false
	for index, detail := range statusProto.Details {
		if detail == nil {
			return status.Errorf(codes.Internal, "translate gRPC status detail %d to beta: detail is nil", index)
		}
		messageName := string(detail.MessageName())
		if !strings.HasPrefix(messageName, stableContractMessagePrefix) {
			continue
		}

		betaName := "azd.extensions.v1beta." + strings.TrimPrefix(messageName, stableContractMessagePrefix)
		messageType, findErr := protoregistry.GlobalTypes.FindMessageByName(protoreflect.FullName(betaName))
		if findErr != nil {
			return status.Errorf(
				codes.Internal,
				"translate stable gRPC status detail %q to beta: %v",
				messageName,
				findErr,
			)
		}

		betaMessage := messageType.New().Interface()
		if unmarshalErr := proto.Unmarshal(detail.Value, betaMessage); unmarshalErr != nil {
			return status.Errorf(
				codes.Internal,
				"translate stable gRPC status detail %q to beta: %v",
				messageName,
				unmarshalErr,
			)
		}
		betaDetail, anyErr := anypb.New(betaMessage)
		if anyErr != nil {
			return status.Errorf(
				codes.Internal,
				"pack translated gRPC status detail %q: %v",
				betaName,
				anyErr,
			)
		}
		statusProto.Details[index] = betaDetail
		translated = true
	}

	if !translated {
		return err
	}
	return status.FromProto(statusProto).Err()
}
