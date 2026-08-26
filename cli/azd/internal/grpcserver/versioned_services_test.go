// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	v1 "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
)

type recordingRegistrar struct {
	services        map[string]*grpc.ServiceDesc
	implementations map[string]any
}

func (r *recordingRegistrar) RegisterService(description *grpc.ServiceDesc, implementation any) {
	r.services[description.ServiceName] = description
	r.implementations[description.ServiceName] = implementation
}

func TestRegisterBetaServicesUsesGeneratedBetaDescriptorsAndServers(t *testing.T) {
	t.Parallel()

	registrar := &recordingRegistrar{
		services:        map[string]*grpc.ServiceDesc{},
		implementations: map[string]any{},
	}
	require.NoError(t, registerBetaServices(registrar, stableServiceImplementations(), nil))

	const accountService = "azd.extensions.v1beta.AccountService"
	require.Same(t, &v1beta.AccountService_ServiceDesc, registrar.services[accountService])
	require.IsType(t, &betaAccountServiceAdapter{}, registrar.implementations[accountService])
	require.Implements(t, (*v1beta.AccountServiceServer)(nil), registrar.implementations[accountService])
}

func TestRegisterBetaServicesRejectsInvalidOverrides(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		overrides map[BetaService]any
		want      string
	}{
		{
			name: "unknown service",
			overrides: map[BetaService]any{
				BetaService("FutureService"): betaTelemetryOverride{},
			},
			want: "unknown beta service override",
		},
		{
			name: "whole generated server",
			overrides: map[BetaService]any{
				BetaTelemetryService: v1beta.UnimplementedTelemetryServiceServer{},
			},
			want: "must implement focused method override interfaces",
		},
		{
			name: "wrong service method",
			overrides: map[BetaService]any{
				BetaAccountService: betaTelemetryOverride{},
			},
			want: "does not implement a generated focused method override interface",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registrar := &recordingRegistrar{
				services:        map[string]*grpc.ServiceDesc{},
				implementations: map[string]any{},
			}
			err := registerBetaServices(registrar, stableServiceImplementations(), test.overrides)
			require.ErrorContains(t, err, test.want)
		})
	}
}

func TestTranscodeBetaRequestDiscardsUnknownPreviewFields(t *testing.T) {
	t.Parallel()

	request := &v1beta.ReportUsageRequest{
		EventName:  "deploy.completed",
		Attributes: map[string]string{"mode": "code"},
	}
	unknown := protowire.AppendTag(nil, 500, protowire.BytesType)
	unknown = protowire.AppendString(unknown, "preview-only")
	request.ProtoReflect().SetUnknown(unknown)

	stableRequest := new(v1.ReportUsageRequest)
	require.NoError(t, transcodeBetaRequest(request, stableRequest))
	require.Equal(t, request.GetEventName(), stableRequest.GetEventName())
	require.Equal(t, request.GetAttributes(), stableRequest.GetAttributes())
	require.Empty(t, stableRequest.ProtoReflect().GetUnknown())
}

func TestTranslateBetaStatusDetails(t *testing.T) {
	t.Parallel()

	standardDetail := &errdetails.ErrorInfo{
		Reason: "AADSTS530084",
		Domain: "azd.auth",
	}
	stableStatus, err := status.New(codes.Unauthenticated, "request failed").WithDetails(
		&v1.ActionableErrorDetail{
			Suggestion: "sign in again",
			Links: []*v1.ErrorLink{{
				Url:   "https://aka.ms/example",
				Title: "Help",
			}},
		},
		&v1.ServiceErrorDetail{
			ErrorCode:   "AuthorizationFailed",
			StatusCode:  403,
			ServiceName: "management.azure.com",
		},
		&v1.ExtensionError{
			Message: "extension failed",
			Origin:  v1.ErrorOrigin_ERROR_ORIGIN_LOCAL,
			Source: &v1.ExtensionError_LocalError{
				LocalError: &v1.LocalErrorDetail{
					Code:     "invalid_config",
					Category: "validation",
				},
			},
		},
		standardDetail,
	)
	require.NoError(t, err)

	translated, ok := status.FromError(translateBetaStatusDetails(stableStatus.Err()))
	require.True(t, ok)
	require.Equal(t, stableStatus.Code(), translated.Code())
	require.Equal(t, stableStatus.Message(), translated.Message())
	require.IsType(t, &v1beta.ActionableErrorDetail{}, translated.Details()[0])
	require.IsType(t, &v1beta.ServiceErrorDetail{}, translated.Details()[1])
	require.IsType(t, &v1beta.ExtensionError{}, translated.Details()[2])
	require.True(t, proto.Equal(standardDetail, translated.Details()[3].(proto.Message)))

	typeURLs := detailTypeURLs(translated)
	require.Equal(t, "type.googleapis.com/azd.extensions.v1beta.ActionableErrorDetail", typeURLs[0])
	require.Equal(t, "type.googleapis.com/azd.extensions.v1beta.ServiceErrorDetail", typeURLs[1])
	require.Equal(t, "type.googleapis.com/azd.extensions.v1beta.ExtensionError", typeURLs[2])
	require.Equal(t, "type.googleapis.com/google.rpc.ErrorInfo", typeURLs[3])
}

func TestTranslateBetaStatusDetailsRejectsUnknownStableDetail(t *testing.T) {
	t.Parallel()

	stableStatus := status.FromProto(&statuspb.Status{
		Code:    int32(codes.Unknown),
		Message: "request failed",
		Details: []*anypb.Any{{
			TypeUrl: "type.googleapis.com/azd.extensions.v1.FutureErrorDetail",
			Value:   []byte{0x08, 0x01},
		}},
	})

	translated, ok := status.FromError(translateBetaStatusDetails(stableStatus.Err()))
	require.True(t, ok)
	require.Equal(t, codes.Internal, translated.Code())
	require.Contains(t, translated.Message(), "FutureErrorDetail")
	require.Contains(t, translated.Message(), "translate stable gRPC status detail")
}

func detailTypeURLs(st *status.Status) []string {
	details := st.Proto().GetDetails()
	typeURLs := make([]string, len(details))
	for index, detail := range details {
		typeURLs[index] = detail.GetTypeUrl()
	}
	return typeURLs
}

func stableServiceImplementations() map[BetaService]any {
	return map[BetaService]any{
		BetaAccountService:       v1.UnimplementedAccountServiceServer{},
		BetaAiModelService:       v1.UnimplementedAiModelServiceServer{},
		BetaComposeService:       v1.UnimplementedComposeServiceServer{},
		BetaContainerService:     v1.UnimplementedContainerServiceServer{},
		BetaCopilotService:       v1.UnimplementedCopilotServiceServer{},
		BetaDeploymentService:    v1.UnimplementedDeploymentServiceServer{},
		BetaEnvironmentService:   v1.UnimplementedEnvironmentServiceServer{},
		BetaEventService:         v1.UnimplementedEventServiceServer{},
		BetaExtensionService:     v1.UnimplementedExtensionServiceServer{},
		BetaFrameworkService:     v1.UnimplementedFrameworkServiceServer{},
		BetaProjectService:       v1.UnimplementedProjectServiceServer{},
		BetaPromptService:        v1.UnimplementedPromptServiceServer{},
		BetaProvisioningService:  v1.UnimplementedProvisioningServiceServer{},
		BetaServiceTargetService: v1.UnimplementedServiceTargetServiceServer{},
		BetaTelemetryService:     v1.UnimplementedTelemetryServiceServer{},
		BetaUserConfigService:    v1.UnimplementedUserConfigServiceServer{},
		BetaValidationService:    v1.UnimplementedValidationServiceServer{},
		BetaWorkflowService:      v1.UnimplementedWorkflowServiceServer{},
	}
}
