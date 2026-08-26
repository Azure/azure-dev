// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// PatchServiceConfig creates or updates one service under the project configuration
// mutation lock. It serializes concurrent project RPC mutations in this azd process;
// it is not a cross-process filesystem transaction.
func (s *projectService) PatchServiceConfig(
	ctx context.Context,
	req *azdext.PatchServiceConfigRequest,
) (*azdext.EmptyResponse, error) {
	setPaths, unsetPaths, err := validateServiceConfigPatch(req)
	if err != nil {
		return nil, err
	}

	s.configMutationMu.Lock()
	defer s.configMutationMu.Unlock()

	azdContext, err := s.lazyAzdContext.GetValue()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "loading azd context: %v", err)
	}
	projectPath := azdContext.ProjectPath()
	if _, err := project.Load(ctx, projectPath); err != nil {
		return nil, projectPatchError(codes.FailedPrecondition, "loading current project configuration", err)
	}
	cachedProject, err := s.lazyProjectConfig.GetValue()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "loading cached project configuration: %v", err)
	}
	cfg, err := project.LoadConfig(ctx, projectPath)
	if err != nil {
		return nil, projectPatchError(codes.Internal, "reading current project configuration", err)
	}
	if s.patchConfigLoaded != nil {
		s.patchConfigLoaded()
	}

	services, found := cfg.Raw()["services"].(map[string]any)
	if !found {
		if rawServices, exists := cfg.Raw()["services"]; exists && rawServices != nil {
			return nil, status.Error(codes.FailedPrecondition, "services configuration is not a mapping")
		}
		services = map[string]any{}
		cfg.Raw()["services"] = services
	}

	serviceConfig, exists, err := patchTargetService(services, req)
	if err != nil {
		return nil, err
	}
	if err := validateExpectedServiceUses(serviceConfig, exists, req.GetExpectedUses()); err != nil {
		return nil, err
	}
	if !exists {
		serviceConfig = map[string]any{"host": req.GetRequiredHost()}
		services[req.GetServiceName()] = serviceConfig
	}

	serviceValues := config.NewConfig(serviceConfig)
	for _, path := range unsetPaths {
		if err := serviceValues.Unset(path); err != nil {
			return nil, status.Errorf(codes.FailedPrecondition, "cannot unset service path %q: %v", path, err)
		}
	}
	for _, path := range setPaths {
		if err := serviceValues.Set(path, req.GetSetValues()[path].AsInterface()); err != nil {
			return nil, status.Errorf(codes.FailedPrecondition, "cannot set service path %q: %v", path, err)
		}
	}

	preparedProject, projectBytes, err := prepareProjectConfigPatch(ctx, cfg, projectPath)
	if err != nil {
		return nil, err
	}
	cachedProject.CopyRuntimeStateTo(preparedProject)
	if err := azdext.WriteFileAtomic(projectPath, projectBytes, 0); err != nil {
		return nil, status.Errorf(codes.Internal, "writing project configuration: %v", err)
	}
	s.lazyProjectConfig.SetValue(preparedProject)

	return &azdext.EmptyResponse{}, nil
}

func validateExpectedServiceUses(
	serviceConfig map[string]any,
	serviceExists bool,
	expected *azdext.StringListValue,
) error {
	if expected == nil {
		return nil
	}

	var current []string
	if serviceExists {
		rawUses, found := serviceConfig["uses"]
		if found {
			items, ok := rawUses.([]any)
			if !ok {
				return status.Error(codes.FailedPrecondition, "service uses configuration is not a list")
			}
			current = make([]string, len(items))
			for index, item := range items {
				serviceName, ok := item.(string)
				if !ok {
					return status.Error(codes.FailedPrecondition, "service uses configuration contains a non-string value")
				}
				current[index] = serviceName
			}
		}
	}

	if !slices.Equal(current, expected.GetValues()) {
		return status.Error(codes.Aborted, "service uses configuration changed; reload the project and retry")
	}
	return nil
}

func prepareProjectConfigPatch(
	ctx context.Context,
	cfg config.Config,
	projectPath string,
) (*project.ProjectConfig, []byte, error) {
	tempFile, err := os.CreateTemp(filepath.Dir(projectPath), ".azure-yaml-patch-*")
	if err != nil {
		return nil, nil, status.Errorf(codes.Internal, "creating project patch file: %v", err)
	}
	tempPath := tempFile.Name()
	if err := tempFile.Close(); err != nil {
		_ = os.Remove(tempPath)
		return nil, nil, status.Errorf(codes.Internal, "closing project patch file: %v", err)
	}
	defer os.Remove(tempPath)

	if err := project.SaveConfig(ctx, cfg, tempPath); err != nil {
		return nil, nil, projectPatchError(codes.InvalidArgument, "validating patched project configuration", err)
	}
	preparedProject, err := project.Load(ctx, tempPath)
	if err != nil {
		return nil, nil, projectPatchError(codes.FailedPrecondition, "loading patched project configuration", err)
	}
	projectBytes, err := os.ReadFile(tempPath)
	if err != nil {
		return nil, nil, status.Errorf(codes.Internal, "reading project patch file: %v", err)
	}
	return preparedProject, projectBytes, nil
}

func projectPatchError(defaultCode codes.Code, action string, err error) error {
	if _, ok := errors.AsType[*fs.PathError](err); ok {
		defaultCode = codes.Internal
	}
	return status.Errorf(defaultCode, "%s: %v", action, err)
}

func patchTargetService(
	services map[string]any,
	req *azdext.PatchServiceConfigRequest,
) (map[string]any, bool, error) {
	rawService, exists := services[req.GetServiceName()]
	if !exists {
		if !req.GetCreateIfMissing() {
			return nil, false, status.Errorf(codes.NotFound, "service %q not found", req.GetServiceName())
		}
		return nil, false, nil
	}

	serviceConfig, ok := rawService.(map[string]any)
	if !ok {
		return nil, false, status.Errorf(codes.FailedPrecondition, "service %q is not a mapping", req.GetServiceName())
	}
	host, ok := serviceConfig["host"].(string)
	if !ok || host != req.GetRequiredHost() {
		return nil, false, status.Errorf(
			codes.FailedPrecondition,
			"service %q has host %q; expected %q",
			req.GetServiceName(),
			host,
			req.GetRequiredHost(),
		)
	}
	return serviceConfig, true, nil
}

func validateServiceConfigPatch(req *azdext.PatchServiceConfigRequest) ([]string, []string, error) {
	if req == nil {
		return nil, nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if strings.TrimSpace(req.GetServiceName()) == "" {
		return nil, nil, status.Error(codes.InvalidArgument, "service_name is required")
	}
	if strings.TrimSpace(req.GetRequiredHost()) == "" {
		return nil, nil, status.Error(codes.InvalidArgument, "required_host is required")
	}

	setPaths := make([]string, 0, len(req.GetSetValues()))
	for path, value := range req.GetSetValues() {
		if err := validateServicePatchPath(path); err != nil {
			return nil, nil, err
		}
		if value == nil {
			return nil, nil, status.Errorf(codes.InvalidArgument, "set value for path %q is required", path)
		}
		if isTypedServiceField(strings.Split(path, ".")[0]) && containsNull(value.AsInterface()) {
			return nil, nil, status.Errorf(
				codes.InvalidArgument,
				"typed service field %q cannot contain null; unset the field instead",
				path,
			)
		}
		setPaths = append(setPaths, path)
	}
	slices.Sort(setPaths)
	for i, path := range setPaths {
		for _, other := range setPaths[i+1:] {
			if strings.HasPrefix(other, path+".") {
				return nil, nil, status.Errorf(
					codes.InvalidArgument,
					"set paths %q and %q overlap",
					path,
					other,
				)
			}
		}
	}

	unsetPaths := slices.Clone(req.GetUnsetPaths())
	for _, path := range unsetPaths {
		if err := validateServicePatchPath(path); err != nil {
			return nil, nil, err
		}
	}
	slices.Sort(unsetPaths)
	return setPaths, unsetPaths, nil
}

func containsNull(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case map[string]any:
		for _, child := range typed {
			if containsNull(child) {
				return true
			}
		}
	case []any:
		return slices.ContainsFunc(typed, containsNull)
	}
	return false
}

func isTypedServiceField(field string) bool {
	switch field {
	case "apiVersion", "condition", "config", "dist", "docker", "env",
		"hooks", "image", "infra", "k8s", "module", "project",
		"remoteBuild", "resourceGroup", "resourceName", "uses":
		return true
	default:
		return false
	}
}

func validateServicePatchPath(path string) error {
	parts := strings.Split(path, ".")
	if path == "" || slices.Contains(parts, "") {
		return status.Errorf(codes.InvalidArgument, "service path %q is invalid", path)
	}
	if parts[0] == "host" || parts[0] == "language" {
		return status.Errorf(codes.InvalidArgument, "%s cannot be patched", parts[0])
	}
	return nil
}
