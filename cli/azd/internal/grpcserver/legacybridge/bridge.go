// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package legacybridge provides temporary wire compatibility for extension
// binaries compiled against the pre-versioning azdext protobuf package.
//
// TEMPORARY: Remove this entire package, its two server interceptor hooks, and
// ExtensionLegacyGrpcCallCount when
// extension.grpc.legacy_call_count remains zero for a full stable release
// cycle and all supported extensions have migrated to versioned contracts.
package legacybridge

import (
	"context"
	"fmt"
	"slices"
	"strings"

	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1 "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
)

const (
	// MethodPrefix identifies RPCs from extensions compiled against the legacy package.
	MethodPrefix = "/azdext."

	stableServicePrefix = "azd.extensions.v1."
	betaServicePrefix   = "azd.extensions.v1beta."
	stableTypeURLPrefix = "type.googleapis.com/azd.extensions.v1."
	betaTypeURLPrefix   = "type.googleapis.com/azd.extensions.v1beta."
	legacyTypeURLPrefix = "type.googleapis.com/azdext."
)

type frozenService struct {
	description *grpc.ServiceDesc
	methods     []string
}

// frozenServices is the complete legacy surface. New stable methods must not be
// added here: extensions should use azd.extensions.v1 for all new contracts.
var frozenServices = []frozenService{
	{&v1.AccountService_ServiceDesc, []string{"ListSubscriptions", "LookupTenant"}},
	{&v1.AiModelService_ServiceDesc, []string{
		"ListModels",
		"ResolveModelDeployments",
		"ListUsages",
		"ListLocationsWithQuota",
		"ListModelLocationsWithQuota",
	}},
	{&v1beta.ComposeService_ServiceDesc, []string{
		"ListResources",
		"GetResource",
		"ListResourceTypes",
		"GetResourceType",
		"AddResource",
	}},
	{&v1.ContainerService_ServiceDesc, []string{"Build", "Package", "Publish"}},
	{&v1beta.CopilotService_ServiceDesc, []string{
		"Initialize",
		"ListSessions",
		"SendMessage",
		"GetUsageMetrics",
		"GetFileChanges",
		"StopSession",
		"GetMessages",
	}},
	{&v1.DeploymentService_ServiceDesc, []string{"GetDeployment", "GetDeploymentContext"}},
	{&v1.EnvironmentService_ServiceDesc, []string{
		"GetCurrent",
		"List",
		"Get",
		"Select",
		"GetValues",
		"GetValue",
		"SetValue",
		"GetConfig",
		"GetConfigString",
		"GetConfigSection",
		"SetConfig",
		"UnsetConfig",
	}},
	{&v1.EventService_ServiceDesc, []string{"EventStream"}},
	{&v1.ExtensionService_ServiceDesc, []string{"Ready", "ReportError"}},
	{&v1.FrameworkService_ServiceDesc, []string{"Stream"}},
	{&v1.ProjectService_ServiceDesc, []string{
		"Get",
		"GetServiceTargetResource",
		"AddService",
		"GetResolvedServices",
		"ParseGitHubUrl",
		"GetConfigSection",
		"GetConfigValue",
		"SetConfigSection",
		"SetConfigValue",
		"UnsetConfig",
		"GetServiceConfigSection",
		"GetServiceConfigValue",
		"SetServiceConfigSection",
		"SetServiceConfigValue",
		"UnsetServiceConfig",
	}},
	{&v1.PromptService_ServiceDesc, []string{
		"PromptSubscription",
		"PromptLocation",
		"PromptResourceGroup",
		"Confirm",
		"Prompt",
		"Select",
		"MultiSelect",
		"PromptSubscriptionResource",
		"PromptResourceGroupResource",
		"PromptAiModel",
		"PromptAiDeployment",
		"PromptAiLocationWithQuota",
		"PromptAiModelLocationWithQuota",
	}},
	{&v1.ProvisioningService_ServiceDesc, []string{"Stream"}},
	{&v1.ServiceTargetService_ServiceDesc, []string{"Stream"}},
	{&v1.TelemetryService_ServiceDesc, []string{"ReportUsage"}},
	{&v1.UserConfigService_ServiceDesc, []string{"Get", "GetString", "GetSection", "Set", "Unset"}},
	{&v1.ValidationService_ServiceDesc, []string{"Stream"}},
	{&v1.WorkflowService_ServiceDesc, []string{"Run"}},
}

// Register adds the frozen azdext service names using the stable service handlers.
func Register(registrar grpc.ServiceRegistrar, implementations map[string]any) error {
	descriptions := make([]grpc.ServiceDesc, 0, len(frozenServices))
	for _, service := range frozenServices {
		implementation, ok := implementations[service.description.ServiceName]
		if !ok {
			return fmt.Errorf("legacy bridge implementation missing for %s", service.description.ServiceName)
		}

		description, err := freezeDescription(service)
		if err != nil {
			return err
		}
		descriptions = append(descriptions, description)
		registrar.RegisterService(&descriptions[len(descriptions)-1], implementation)
	}

	return nil
}

func freezeDescription(service frozenService) (grpc.ServiceDesc, error) {
	description := *service.description
	versionedPrefix := ""
	for _, prefix := range []string{stableServicePrefix, betaServicePrefix} {
		if strings.HasPrefix(description.ServiceName, prefix) {
			versionedPrefix = prefix
			break
		}
	}
	if versionedPrefix == "" {
		return grpc.ServiceDesc{}, fmt.Errorf("legacy bridge service has unexpected name %q", description.ServiceName)
	}

	description.ServiceName = "azdext." + strings.TrimPrefix(description.ServiceName, versionedPrefix)
	description.Methods = slices.DeleteFunc(slices.Clone(description.Methods), func(method grpc.MethodDesc) bool {
		return !slices.Contains(service.methods, method.MethodName)
	})
	for index := range description.Methods {
		method := &description.Methods[index]
		method.Handler = legacyUnaryHandler(
			method.Handler,
			fmt.Sprintf("/%s/%s", description.ServiceName, method.MethodName),
		)
	}
	description.Streams = slices.DeleteFunc(slices.Clone(description.Streams), func(stream grpc.StreamDesc) bool {
		return !slices.Contains(service.methods, stream.StreamName)
	})
	description.Metadata = nil

	available := make([]string, 0, len(description.Methods)+len(description.Streams))
	for _, method := range description.Methods {
		available = append(available, method.MethodName)
	}
	for _, stream := range description.Streams {
		available = append(available, stream.StreamName)
	}
	for _, method := range service.methods {
		if !slices.Contains(available, method) {
			return grpc.ServiceDesc{}, fmt.Errorf(
				"legacy bridge method %s.%s no longer exists in the stable contract",
				service.description.ServiceName,
				method,
			)
		}
	}

	return description, nil
}

func legacyUnaryHandler(handler grpc.MethodHandler, fullMethod string) grpc.MethodHandler {
	return func(
		srv any,
		ctx context.Context,
		dec func(any) error,
		interceptor grpc.UnaryServerInterceptor,
	) (any, error) {
		if interceptor == nil {
			return handler(srv, ctx, dec, nil)
		}

		return handler(srv, ctx, dec, func(
			ctx context.Context,
			req any,
			info *grpc.UnaryServerInfo,
			next grpc.UnaryHandler,
		) (any, error) {
			legacyInfo := *info
			legacyInfo.FullMethod = fullMethod
			return interceptor(ctx, req, &legacyInfo, next)
		})
	}
}

// IsLegacyMethod reports whether a full gRPC method uses the frozen legacy package.
func IsLegacyMethod(fullMethod string) bool {
	return strings.HasPrefix(fullMethod, MethodPrefix)
}

// UnaryUsageInterceptor counts calls through the temporary legacy bridge.
func UnaryUsageInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		if IsLegacyMethod(info.FullMethod) {
			tracing.IncrementUsageAttribute(fields.ExtensionLegacyGrpcCallCount.Int(1))
		}
		return handler(ctx, req)
	}
}

// StreamUsageInterceptor counts streams opened through the temporary legacy bridge.
func StreamUsageInterceptor() grpc.StreamServerInterceptor {
	return func(
		srv any,
		stream grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		if IsLegacyMethod(info.FullMethod) {
			tracing.IncrementUsageAttribute(fields.ExtensionLegacyGrpcCallCount.Int(1))
		}
		return handler(srv, stream)
	}
}

// TranslateStatusDetails rewrites stable contract-local Any type URLs for legacy clients.
// The stable and legacy detail messages have the same frozen wire schema.
func TranslateStatusDetails(err error) error {
	if err == nil {
		return nil
	}

	st, ok := azdext.GRPCStatusFromError(err)
	if !ok {
		return err
	}

	statusProto := proto.Clone(st.Proto()).(*statuspb.Status)
	translated := false
	for _, detail := range statusProto.Details {
		if detail == nil {
			continue
		}
		for _, prefix := range []string{stableTypeURLPrefix, betaTypeURLPrefix} {
			if suffix, ok := strings.CutPrefix(detail.TypeUrl, prefix); ok {
				detail.TypeUrl = legacyTypeURLPrefix + suffix
				translated = true
				break
			}
		}
	}
	if !translated {
		return err
	}

	return status.FromProto(statusProto).Err()
}
