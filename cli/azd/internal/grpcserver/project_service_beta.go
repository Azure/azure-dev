// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/braydonk/yaml"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// betaProjectServiceOverride implements preview service authoring.
// AddService shares mutation logic with stable v1.
// Conditional raw authoring is beta-only.
type betaProjectServiceOverride struct {
	service *projectService
	custom  any
}

var _ BetaProjectServiceAddServiceOverride = (*betaProjectServiceOverride)(nil)

// ApplyServiceConfig locks the precondition, write and cache reload.
// YAML preserves numeric precision and unexpanded templates.
func (o *betaProjectServiceOverride) ApplyServiceConfig(
	ctx context.Context, req *v1beta.ApplyServiceConfigRequest,
) (*v1beta.EmptyResponse, error) {
	if err := azdext.ValidateServiceName(req.GetServiceName()); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid service name")
	}
	expected, err := decodeServiceAuthoringObject(req.GetExpectedServicesYaml())
	if err != nil {
		return nil, err
	}
	candidate, err := decodeServiceAuthoringObject(req.GetServiceYaml())
	if err != nil {
		return nil, err
	}
	s := o.service
	s.configMutationMu.Lock()
	defer s.configMutationMu.Unlock()
	azdContext, err := s.lazyAzdContext.GetValue()
	if err != nil {
		return nil, err
	}
	cfg, err := project.LoadConfig(ctx, azdContext.ProjectPath())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "could not read project configuration")
	}
	if _, layered := cfg.Raw()["layers"]; layered {
		return nil, status.Error(codes.InvalidArgument, "service authoring requires a top-level services manifest")
	}
	services := map[string]any{}
	if raw, found := cfg.Raw()["services"]; found {
		var ok bool
		services, ok = raw.(map[string]any)
		if !ok {
			return nil, status.Error(codes.InvalidArgument, "project services must be a mapping")
		}
	}
	currentYAML, err := canonicalServiceAuthoringYAML(services)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "could not encode project services")
	}
	expectedYAML, err := canonicalServiceAuthoringYAML(expected)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "could not encode services precondition")
	}
	// Canonical YAML compares equivalent numeric representations.
	// Unlike protobuf Struct, it retains large integer precision.
	if !bytes.Equal(currentYAML, expectedYAML) {
		return nil, status.Error(codes.Aborted,
			"project services changed while the service was being planned; rerun the command")
	}
	services[req.GetServiceName()] = candidate
	candidateYAML, err := canonicalServiceAuthoringYAML(services)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "could not encode candidate services")
	}
	if bytes.Equal(currentYAML, candidateYAML) {
		return &v1beta.EmptyResponse{}, nil
	}
	cfg.Raw()["services"] = services
	if err := project.SaveRawConfig(ctx, cfg, azdContext.ProjectPath()); err != nil {
		return nil, status.Error(codes.InvalidArgument, "could not save project configuration")
	}
	if err := s.reloadAndCacheProjectConfig(ctx, azdContext.ProjectPath()); err != nil {
		return nil, status.Error(codes.Internal, "project saved but configuration cache could not be refreshed")
	}
	return &v1beta.EmptyResponse{}, nil
}

func canonicalServiceAuthoringYAML(object map[string]any) ([]byte, error) {
	wire, err := yaml.Marshal(object)
	if err != nil {
		return nil, err
	}
	var normalized map[string]any
	if err := yaml.Unmarshal(wire, &normalized); err != nil {
		return nil, err
	}
	return yaml.Marshal(normalized)
}

func decodeServiceAuthoringObject(value string) (map[string]any, error) {
	decoder := yaml.NewDecoder(strings.NewReader(value))
	var object map[string]any
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, status.Error(codes.InvalidArgument, "service authoring YAML must contain exactly one mapping")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, status.Error(codes.InvalidArgument, "service authoring YAML must contain exactly one mapping")
	}
	return object, nil
}

// GetAddServiceCapabilities only advertises the built-in acknowledgment implementation.
// A custom AddService override must supply its own capability implementation to opt in.
func (o *betaProjectServiceOverride) GetAddServiceCapabilities(
	context.Context, *v1beta.EmptyRequest,
) (*v1beta.GetAddServiceCapabilitiesResponse, error) {
	_, custom := findBetaOverride[BetaProjectServiceAddServiceOverride](o.custom)
	return &v1beta.GetAddServiceCapabilitiesResponse{AcknowledgmentSupported: !custom}, nil
}

// AddService adapts the v1beta AddServiceRequest.operation_id field onto the shared mutation
// logic. On an acknowledged failure it attaches an AddServiceAcknowledgment detail to the
// returned gRPC status instead of a trailer, so the capability is discoverable from the v1beta
// service definition and generated clients rather than relying on an undocumented header name.
// Operation identifiers longer than 64 bytes are rejected before project mutation.
func (o *betaProjectServiceOverride) AddService(
	ctx context.Context, req *v1beta.AddServiceRequest,
) (*v1beta.EmptyResponse, error) {
	if len(req.GetOperationId()) > maxAddServiceOperationIDBytes {
		return nil, status.Errorf(
			codes.InvalidArgument, "operation_id must not exceed %d bytes", maxAddServiceOperationIDBytes,
		)
	}

	stableReq := new(azdext.AddServiceRequest)
	if err := transcodeBetaRequest(req, stableReq); err != nil {
		return nil, err
	}

	acknowledged, err := o.service.addService(ctx, stableReq, req.GetOperationId())
	if err != nil {
		if acknowledged {
			withDetails, detailErr := status.Convert(mapHostError(err)).WithDetails(
				&v1beta.AddServiceAcknowledgment{OperationId: req.GetOperationId()},
			)
			if detailErr == nil {
				return nil, withDetails.Err()
			}
			return nil, status.Errorf(codes.Internal,
				"AddService failed: %v; attaching completion acknowledgment: %v", err, detailErr)
		}
		return nil, err
	}

	return &v1beta.EmptyResponse{}, nil
}
