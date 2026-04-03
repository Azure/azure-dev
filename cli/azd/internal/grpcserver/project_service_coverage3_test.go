// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestNewProjectService(t *testing.T) {
	t.Parallel()
	svc := NewProjectService(nil, nil, nil, nil, nil, nil, nil)
	require.NotNil(t, svc)
}

func TestProjectService_GetServiceTargetResource_EmptyServiceName(t *testing.T) {
	t.Parallel()
	svc := NewProjectService(nil, nil, nil, nil, nil, nil, nil)
	_, err := svc.GetServiceTargetResource(t.Context(), &azdext.GetServiceTargetResourceRequest{
		ServiceName: "",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
}

func TestProjectService_GetServiceTargetResource_ProjectConfigError(t *testing.T) {
	t.Parallel()
	lazyProject := lazy.NewLazy(func() (*project.ProjectConfig, error) {
		return nil, errors.New("config error")
	})
	svc := NewProjectService(nil, nil, nil, nil, lazyProject, nil, nil)

	_, err := svc.GetServiceTargetResource(t.Context(), &azdext.GetServiceTargetResourceRequest{
		ServiceName: "web",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.Internal, st.Code())
}

func TestProjectService_GetServiceTargetResource_ServiceNotFound(t *testing.T) {
	t.Parallel()
	lazyProject := lazy.NewLazy(func() (*project.ProjectConfig, error) {
		return &project.ProjectConfig{
			Services: map[string]*project.ServiceConfig{},
		}, nil
	})
	svc := NewProjectService(nil, nil, nil, nil, lazyProject, nil, nil)

	_, err := svc.GetServiceTargetResource(t.Context(), &azdext.GetServiceTargetResourceRequest{
		ServiceName: "nonexistent",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.NotFound, st.Code())
}

func TestProjectService_GetServiceTargetResource_EnvError(t *testing.T) {
	t.Parallel()
	lazyProject := lazy.NewLazy(func() (*project.ProjectConfig, error) {
		return &project.ProjectConfig{
			Services: map[string]*project.ServiceConfig{
				"web": {Name: "web"},
			},
		}, nil
	})
	lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
		return nil, errors.New("env not found")
	})
	svc := NewProjectService(nil, nil, nil, lazyEnv, lazyProject, nil, nil)

	_, err := svc.GetServiceTargetResource(t.Context(), &azdext.GetServiceTargetResourceRequest{
		ServiceName: "web",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.Internal, st.Code())
}

func TestProjectService_GetServiceTargetResource_SubscriptionEmpty(t *testing.T) {
	t.Parallel()
	lazyProject := lazy.NewLazy(func() (*project.ProjectConfig, error) {
		return &project.ProjectConfig{
			Services: map[string]*project.ServiceConfig{
				"web": {Name: "web"},
			},
		}, nil
	})
	// environment.New returns env with NO AZURE_SUBSCRIPTION_ID set
	lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
		return environment.New("test"), nil
	})
	svc := NewProjectService(nil, nil, nil, lazyEnv, lazyProject, nil, nil)

	_, err := svc.GetServiceTargetResource(t.Context(), &azdext.GetServiceTargetResourceRequest{
		ServiceName: "web",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.FailedPrecondition, st.Code())
	require.Contains(t, st.Message(), "AZURE_SUBSCRIPTION_ID")
}

func TestProjectService_GetServiceTargetResource_ResourceManagerError(t *testing.T) {
	t.Parallel()
	lazyProject := lazy.NewLazy(func() (*project.ProjectConfig, error) {
		return &project.ProjectConfig{
			Services: map[string]*project.ServiceConfig{
				"web": {Name: "web"},
			},
		}, nil
	})
	lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
		return environment.NewWithValues("test", map[string]string{
			"AZURE_SUBSCRIPTION_ID": "sub-123",
		}), nil
	})
	lazyRM := lazy.NewLazy(func() (project.ResourceManager, error) {
		return nil, errors.New("resource manager unavailable")
	})
	svc := NewProjectService(nil, nil, lazyRM, lazyEnv, lazyProject, nil, nil)

	_, err := svc.GetServiceTargetResource(t.Context(), &azdext.GetServiceTargetResourceRequest{
		ServiceName: "web",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.Internal, st.Code())
	require.Contains(t, st.Message(), "resource manager")
}

// mockResourceManager implements project.ResourceManager for testing.
type mockResourceManager struct {
	getTargetResourceFunc func(
		ctx context.Context, subscriptionId string, serviceConfig *project.ServiceConfig,
	) (*environment.TargetResource, error)
}

func (m *mockResourceManager) GetResourceGroupName(
	_ context.Context, _ string, _ osutil.ExpandableString,
) (string, error) {
	return "", nil
}

func (m *mockResourceManager) GetServiceResources(
	_ context.Context, _ string, _ string, _ *project.ServiceConfig,
) ([]*azapi.ResourceExtended, error) {
	return nil, nil
}

func (m *mockResourceManager) GetServiceResource(
	_ context.Context, _ string, _ string, _ *project.ServiceConfig, _ string,
) (*azapi.ResourceExtended, error) {
	return nil, nil
}

func (m *mockResourceManager) GetTargetResource(
	ctx context.Context, subscriptionId string, serviceConfig *project.ServiceConfig,
) (*environment.TargetResource, error) {
	if m.getTargetResourceFunc != nil {
		return m.getTargetResourceFunc(ctx, subscriptionId, serviceConfig)
	}
	return nil, errors.New("not implemented")
}

func TestProjectService_GetServiceTargetResource_GetTargetResourceError(t *testing.T) {
	t.Parallel()
	lazyProject := lazy.NewLazy(func() (*project.ProjectConfig, error) {
		return &project.ProjectConfig{
			Services: map[string]*project.ServiceConfig{
				"web": {Name: "web"},
			},
		}, nil
	})
	lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
		return environment.NewWithValues("test", map[string]string{
			"AZURE_SUBSCRIPTION_ID": "sub-123",
		}), nil
	})
	rm := &mockResourceManager{
		getTargetResourceFunc: func(
			_ context.Context, _ string, _ *project.ServiceConfig,
		) (*environment.TargetResource, error) {
			return nil, errors.New("target resource error")
		},
	}
	lazyRM := lazy.NewLazy(func() (project.ResourceManager, error) {
		return rm, nil
	})
	svc := NewProjectService(nil, nil, lazyRM, lazyEnv, lazyProject, nil, nil)

	_, err := svc.GetServiceTargetResource(t.Context(), &azdext.GetServiceTargetResourceRequest{
		ServiceName: "web",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.Internal, st.Code())
	require.Contains(t, st.Message(), "target resource error")
}

func TestProjectService_GetServiceTargetResource_Success(t *testing.T) {
	t.Parallel()
	lazyProject := lazy.NewLazy(func() (*project.ProjectConfig, error) {
		return &project.ProjectConfig{
			Services: map[string]*project.ServiceConfig{
				"web": {Name: "web"},
			},
		}, nil
	})
	lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
		return environment.NewWithValues("test", map[string]string{
			"AZURE_SUBSCRIPTION_ID": "sub-123",
		}), nil
	})
	rm := &mockResourceManager{
		getTargetResourceFunc: func(
			_ context.Context, subId string, _ *project.ServiceConfig,
		) (*environment.TargetResource, error) {
			return environment.NewTargetResource(subId, "rg-test", "web-app", "Microsoft.Web/sites"), nil
		},
	}
	lazyRM := lazy.NewLazy(func() (project.ResourceManager, error) {
		return rm, nil
	})
	svc := NewProjectService(nil, nil, lazyRM, lazyEnv, lazyProject, nil, nil)

	resp, err := svc.GetServiceTargetResource(t.Context(), &azdext.GetServiceTargetResourceRequest{
		ServiceName: "web",
	})
	require.NoError(t, err)
	require.NotNil(t, resp.TargetResource)
	require.Equal(t, "sub-123", resp.TargetResource.SubscriptionId)
	require.Equal(t, "rg-test", resp.TargetResource.ResourceGroupName)
	require.Equal(t, "web-app", resp.TargetResource.ResourceName)
	require.Equal(t, "Microsoft.Web/sites", resp.TargetResource.ResourceType)
}

func TestProjectService_GetResolvedServices_AzdContextError(t *testing.T) {
	t.Parallel()
	lazyCtx := lazy.NewLazy(func() (*azdcontext.AzdContext, error) {
		return nil, errors.New("no azd context")
	})
	svc := NewProjectService(lazyCtx, nil, nil, nil, nil, nil, nil)

	_, err := svc.GetResolvedServices(t.Context(), &azdext.EmptyRequest{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no azd context")
}

func TestProjectService_ParseGitHubUrl_Empty(t *testing.T) {
	t.Parallel()
	svc := NewProjectService(nil, nil, nil, nil, nil, nil, nil)
	_, err := svc.ParseGitHubUrl(t.Context(), &azdext.ParseGitHubUrlRequest{
		Url: "",
	})
	// Empty URL should fail parsing
	require.Error(t, err)
}

// newProjectServiceWithYaml creates a projectService backed by a temp dir with a minimal azure.yaml.
func newProjectServiceWithYaml(t *testing.T, yamlContent string) azdext.ProjectServiceServer {
	t.Helper()
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "azure.yaml"), []byte(yamlContent), 0600)
	require.NoError(t, err)

	ctx := azdcontext.NewAzdContextWithDirectory(dir)
	lazyCtx := lazy.NewLazy(func() (*azdcontext.AzdContext, error) { return ctx, nil })

	pc, err := project.Load(t.Context(), filepath.Join(dir, "azure.yaml"))
	require.NoError(t, err)
	lazyPC := lazy.NewLazy(func() (*project.ProjectConfig, error) { return pc, nil })

	lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
		return environment.NewWithValues("dev", nil), nil
	})

	return NewProjectService(lazyCtx, nil, nil, lazyEnv, lazyPC, nil, nil)
}

func TestProjectService_GetConfigValue_EmptyPath(t *testing.T) {
	t.Parallel()
	svc := newProjectServiceWithYaml(t, "name: test-project\n")
	_, err := svc.GetConfigValue(t.Context(), &azdext.GetProjectConfigValueRequest{Path: ""})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
}

func TestProjectService_GetConfigValue_Found(t *testing.T) {
	t.Parallel()
	svc := newProjectServiceWithYaml(t, "name: test-project\n")
	resp, err := svc.GetConfigValue(t.Context(), &azdext.GetProjectConfigValueRequest{Path: "name"})
	require.NoError(t, err)
	require.True(t, resp.Found)
	require.Equal(t, "test-project", resp.Value.GetStringValue())
}

func TestProjectService_GetConfigValue_NotFound(t *testing.T) {
	t.Parallel()
	svc := newProjectServiceWithYaml(t, "name: test-project\n")
	resp, err := svc.GetConfigValue(t.Context(), &azdext.GetProjectConfigValueRequest{Path: "nonexistent"})
	require.NoError(t, err)
	require.False(t, resp.Found)
}

func TestProjectService_GetConfigSection_AzdContextError(t *testing.T) {
	t.Parallel()
	lazyCtx := lazy.NewLazy(func() (*azdcontext.AzdContext, error) {
		return nil, errors.New("no azd context")
	})
	svc := NewProjectService(lazyCtx, nil, nil, nil, nil, nil, nil)
	_, err := svc.GetConfigSection(t.Context(), &azdext.GetProjectConfigSectionRequest{Path: "infra"})
	require.Error(t, err)
}

func TestProjectService_GetConfigSection_NotFound(t *testing.T) {
	t.Parallel()
	svc := newProjectServiceWithYaml(t, "name: test-project\n")
	resp, err := svc.GetConfigSection(t.Context(), &azdext.GetProjectConfigSectionRequest{Path: "missing"})
	require.NoError(t, err)
	require.False(t, resp.Found)
}

func TestProjectService_SetConfigSection_EmptyPath(t *testing.T) {
	t.Parallel()
	svc := newProjectServiceWithYaml(t, "name: test-project\n")
	section, _ := structpb.NewStruct(map[string]any{"key": "value"})
	_, err := svc.SetConfigSection(t.Context(), &azdext.SetProjectConfigSectionRequest{
		Path:    "",
		Section: section,
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
}

func TestProjectService_SetConfigSection_AzdContextError(t *testing.T) {
	t.Parallel()
	lazyCtx := lazy.NewLazy(func() (*azdcontext.AzdContext, error) {
		return nil, errors.New("no ctx")
	})
	svc := NewProjectService(lazyCtx, nil, nil, nil, nil, nil, nil)
	section, _ := structpb.NewStruct(map[string]any{"key": "val"})
	_, err := svc.SetConfigSection(t.Context(), &azdext.SetProjectConfigSectionRequest{
		Path:    "custom",
		Section: section,
	})
	require.Error(t, err)
}

func TestProjectService_SetConfigValue_EmptyPath(t *testing.T) {
	t.Parallel()
	svc := newProjectServiceWithYaml(t, "name: test-project\n")
	_, err := svc.SetConfigValue(t.Context(), &azdext.SetProjectConfigValueRequest{Path: ""})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
}

func TestProjectService_SetConfigValue_AzdContextError(t *testing.T) {
	t.Parallel()
	lazyCtx := lazy.NewLazy(func() (*azdcontext.AzdContext, error) {
		return nil, errors.New("no ctx")
	})
	svc := NewProjectService(lazyCtx, nil, nil, nil, nil, nil, nil)
	val, _ := structpb.NewValue("test")
	_, err := svc.SetConfigValue(t.Context(), &azdext.SetProjectConfigValueRequest{
		Path:  "custom.key",
		Value: val,
	})
	require.Error(t, err)
}

func TestProjectService_UnsetConfig_EmptyPath(t *testing.T) {
	t.Parallel()
	svc := newProjectServiceWithYaml(t, "name: test-project\n")
	_, err := svc.UnsetConfig(t.Context(), &azdext.UnsetProjectConfigRequest{Path: ""})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
}

func TestProjectService_UnsetConfig_AzdContextError(t *testing.T) {
	t.Parallel()
	lazyCtx := lazy.NewLazy(func() (*azdcontext.AzdContext, error) {
		return nil, errors.New("no ctx")
	})
	svc := NewProjectService(lazyCtx, nil, nil, nil, nil, nil, nil)
	_, err := svc.UnsetConfig(t.Context(), &azdext.UnsetProjectConfigRequest{Path: "custom"})
	require.Error(t, err)
}

func TestProjectService_AddService_EmptyName(t *testing.T) {
	t.Parallel()
	svc := NewProjectService(nil, nil, nil, nil, nil, nil, nil)
	_, err := svc.AddService(t.Context(), &azdext.AddServiceRequest{
		Service: &azdext.ServiceConfig{Name: ""},
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
}

func TestProjectService_AddService_NilService(t *testing.T) {
	t.Parallel()
	svc := NewProjectService(nil, nil, nil, nil, nil, nil, nil)
	_, err := svc.AddService(t.Context(), &azdext.AddServiceRequest{Service: nil})
	require.Error(t, err)
}

func TestProjectService_AddService_AzdContextError(t *testing.T) {
	t.Parallel()
	lazyCtx := lazy.NewLazy(func() (*azdcontext.AzdContext, error) {
		return nil, errors.New("no ctx")
	})
	svc := NewProjectService(lazyCtx, nil, nil, nil, nil, nil, nil)
	_, err := svc.AddService(t.Context(), &azdext.AddServiceRequest{
		Service: &azdext.ServiceConfig{Name: "web"},
	})
	require.Error(t, err)
}

func TestProjectService_AddService_ProjectConfigError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ctx := azdcontext.NewAzdContextWithDirectory(dir)
	lazyCtx := lazy.NewLazy(func() (*azdcontext.AzdContext, error) { return ctx, nil })
	lazyPC := lazy.NewLazy(func() (*project.ProjectConfig, error) {
		return nil, errors.New("config error")
	})
	svc := NewProjectService(lazyCtx, nil, nil, nil, lazyPC, nil, nil)
	_, err := svc.AddService(t.Context(), &azdext.AddServiceRequest{
		Service: &azdext.ServiceConfig{Name: "web"},
	})
	require.Error(t, err)
}

func TestProjectService_ValidateServiceExists_ConfigError(t *testing.T) {
	t.Parallel()
	lazyPC := lazy.NewLazy(func() (*project.ProjectConfig, error) {
		return nil, errors.New("config error")
	})
	svc := &projectService{lazyProjectConfig: lazyPC}
	err := svc.validateServiceExists(t.Context(), "web")
	require.Error(t, err)
}

func TestProjectService_ValidateServiceExists_NotFound(t *testing.T) {
	t.Parallel()
	lazyPC := lazy.NewLazy(func() (*project.ProjectConfig, error) {
		return &project.ProjectConfig{
			Services: map[string]*project.ServiceConfig{},
		}, nil
	})
	svc := &projectService{lazyProjectConfig: lazyPC}
	err := svc.validateServiceExists(t.Context(), "nonexistent")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestProjectService_ValidateServiceExists_NilServices(t *testing.T) {
	t.Parallel()
	lazyPC := lazy.NewLazy(func() (*project.ProjectConfig, error) {
		return &project.ProjectConfig{Services: nil}, nil
	})
	svc := &projectService{lazyProjectConfig: lazyPC}
	err := svc.validateServiceExists(t.Context(), "web")
	require.Error(t, err)
}

func TestProjectService_ValidateServiceExists_Found(t *testing.T) {
	t.Parallel()
	lazyPC := lazy.NewLazy(func() (*project.ProjectConfig, error) {
		return &project.ProjectConfig{
			Services: map[string]*project.ServiceConfig{
				"web": {Name: "web"},
			},
		}, nil
	})
	svc := &projectService{lazyProjectConfig: lazyPC}
	err := svc.validateServiceExists(t.Context(), "web")
	require.NoError(t, err)
}

func TestProjectService_Get_AzdContextError(t *testing.T) {
	t.Parallel()
	lazyCtx := lazy.NewLazy(func() (*azdcontext.AzdContext, error) {
		return nil, errors.New("no ctx")
	})
	svc := NewProjectService(lazyCtx, nil, nil, nil, nil, nil, nil)
	_, err := svc.Get(t.Context(), &azdext.EmptyRequest{})
	require.Error(t, err)
}

func TestProjectService_Get_ProjectConfigError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ctx := azdcontext.NewAzdContextWithDirectory(dir)
	lazyCtx := lazy.NewLazy(func() (*azdcontext.AzdContext, error) { return ctx, nil })
	lazyPC := lazy.NewLazy(func() (*project.ProjectConfig, error) {
		return nil, errors.New("config error")
	})
	svc := NewProjectService(lazyCtx, nil, nil, nil, lazyPC, nil, nil)
	_, err := svc.Get(t.Context(), &azdext.EmptyRequest{})
	require.Error(t, err)
}

func TestProjectService_GetResolvedServices_ProjectConfigError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ctx := azdcontext.NewAzdContextWithDirectory(dir)
	lazyCtx := lazy.NewLazy(func() (*azdcontext.AzdContext, error) { return ctx, nil })
	lazyPC := lazy.NewLazy(func() (*project.ProjectConfig, error) {
		return nil, errors.New("config error")
	})
	svc := NewProjectService(lazyCtx, nil, nil, nil, lazyPC, nil, nil)
	_, err := svc.GetResolvedServices(t.Context(), &azdext.EmptyRequest{})
	require.Error(t, err)
}

// Test service config methods with validation errors

func TestProjectService_GetServiceConfigSection_EmptyServiceName(t *testing.T) {
	t.Parallel()
	svc := NewProjectService(nil, nil, nil, nil, nil, nil, nil)
	_, err := svc.GetServiceConfigSection(t.Context(), &azdext.GetServiceConfigSectionRequest{
		ServiceName: "",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
}

func TestProjectService_GetServiceConfigValue_EmptyServiceName(t *testing.T) {
	t.Parallel()
	svc := NewProjectService(nil, nil, nil, nil, nil, nil, nil)
	_, err := svc.GetServiceConfigValue(t.Context(), &azdext.GetServiceConfigValueRequest{
		ServiceName: "",
	})
	require.Error(t, err)
}

func TestProjectService_SetServiceConfigSection_EmptyServiceName(t *testing.T) {
	t.Parallel()
	svc := NewProjectService(nil, nil, nil, nil, nil, nil, nil)
	_, err := svc.SetServiceConfigSection(t.Context(), &azdext.SetServiceConfigSectionRequest{
		ServiceName: "",
	})
	require.Error(t, err)
}

func TestProjectService_SetServiceConfigValue_EmptyServiceName(t *testing.T) {
	t.Parallel()
	svc := NewProjectService(nil, nil, nil, nil, nil, nil, nil)
	_, err := svc.SetServiceConfigValue(t.Context(), &azdext.SetServiceConfigValueRequest{
		ServiceName: "",
	})
	require.Error(t, err)
}

func TestProjectService_UnsetServiceConfig_EmptyServiceName(t *testing.T) {
	t.Parallel()
	svc := NewProjectService(nil, nil, nil, nil, nil, nil, nil)
	_, err := svc.UnsetServiceConfig(t.Context(), &azdext.UnsetServiceConfigRequest{
		ServiceName: "",
	})
	require.Error(t, err)
}

// --- Happy path tests for Set/Unset config ---

func TestProjectService_SetConfigSection_HappyPath(t *testing.T) {
	t.Parallel()
	svc := newProjectServiceWithYaml(t, "name: test-project\n")
	section, err := structpb.NewStruct(map[string]any{"key1": "value1"})
	require.NoError(t, err)

	_, err = svc.SetConfigSection(t.Context(), &azdext.SetProjectConfigSectionRequest{
		Path:    "metadata",
		Section: section,
	})
	require.NoError(t, err)
}

func TestProjectService_SetConfigValue_HappyPath(t *testing.T) {
	t.Parallel()
	svc := newProjectServiceWithYaml(t, "name: test-project\n")
	val := structpb.NewStringValue("hello")

	_, err := svc.SetConfigValue(t.Context(), &azdext.SetProjectConfigValueRequest{
		Path:  "metadata.greeting",
		Value: val,
	})
	require.NoError(t, err)
}

func TestProjectService_UnsetConfig_HappyPath(t *testing.T) {
	t.Parallel()
	svc := newProjectServiceWithYaml(t, "name: test-project\nmetadata:\n  key1: value1\n")

	_, err := svc.UnsetConfig(t.Context(), &azdext.UnsetProjectConfigRequest{
		Path: "metadata",
	})
	require.NoError(t, err)
}

func TestProjectService_Get_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	yamlContent := "name: test-project\nservices:\n  api:\n" +
		"    host: appservice\n    language: python\n    project: ./src/api\n"
	err := os.WriteFile(filepath.Join(dir, "azure.yaml"), []byte(yamlContent), 0600)
	require.NoError(t, err)

	ctx := azdcontext.NewAzdContextWithDirectory(dir)
	lazyCtx := lazy.NewLazy(func() (*azdcontext.AzdContext, error) { return ctx, nil })
	pc, err := project.Load(t.Context(), filepath.Join(dir, "azure.yaml"))
	require.NoError(t, err)
	lazyPC := lazy.NewLazy(func() (*project.ProjectConfig, error) { return pc, nil })
	lazyEnvMgr := lazy.NewLazy(func() (environment.Manager, error) {
		return &mockEnvManager{}, nil
	})

	svc := NewProjectService(lazyCtx, lazyEnvMgr, nil, nil, lazyPC, nil, nil)
	resp, err := svc.Get(t.Context(), &azdext.EmptyRequest{})
	require.NoError(t, err)
	require.NotNil(t, resp.Project)
	require.Equal(t, "test-project", resp.Project.Name)
}

func TestProjectService_Get_WithDefaultEnv(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	yamlContent := "name: test-project\n"
	err := os.WriteFile(filepath.Join(dir, "azure.yaml"), []byte(yamlContent), 0600)
	require.NoError(t, err)

	ctx := azdcontext.NewAzdContextWithDirectory(dir)
	require.NoError(t, ctx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "dev"}))
	lazyCtx := lazy.NewLazy(func() (*azdcontext.AzdContext, error) { return ctx, nil })
	pc, err := project.Load(t.Context(), filepath.Join(dir, "azure.yaml"))
	require.NoError(t, err)
	lazyPC := lazy.NewLazy(func() (*project.ProjectConfig, error) { return pc, nil })
	mockMgr := &mockEnvManager{
		getFunc: func(_ context.Context, name string) (*environment.Environment, error) {
			return environment.NewWithValues("dev", map[string]string{"MY_VAR": "hello"}), nil
		},
	}
	lazyEnvMgr := lazy.NewLazy(func() (environment.Manager, error) { return mockMgr, nil })

	svc := NewProjectService(lazyCtx, lazyEnvMgr, nil, nil, lazyPC, nil, nil)
	resp, err := svc.Get(t.Context(), &azdext.EmptyRequest{})
	require.NoError(t, err)
	require.NotNil(t, resp.Project)
}

// --- Happy path tests for service-level config ---

const yamlWithService = `name: test-project
services:
  api:
    host: appservice
    language: python
    project: ./src/api
`

func TestProjectService_SetServiceConfigSection_HappyPath(t *testing.T) {
	t.Parallel()
	svc := newProjectServiceWithYaml(t, yamlWithService)
	section, err := structpb.NewStruct(map[string]any{"port": float64(8080)})
	require.NoError(t, err)

	_, err = svc.SetServiceConfigSection(t.Context(), &azdext.SetServiceConfigSectionRequest{
		ServiceName: "api",
		Path:        "custom",
		Section:     section,
	})
	require.NoError(t, err)
}

func TestProjectService_SetServiceConfigValue_HappyPath(t *testing.T) {
	t.Parallel()
	svc := newProjectServiceWithYaml(t, yamlWithService)
	val := structpb.NewStringValue("containerapp")

	_, err := svc.SetServiceConfigValue(t.Context(), &azdext.SetServiceConfigValueRequest{
		ServiceName: "api",
		Path:        "host",
		Value:       val,
	})
	require.NoError(t, err)
}

func TestProjectService_UnsetServiceConfig_HappyPath(t *testing.T) {
	t.Parallel()
	svc := newProjectServiceWithYaml(t, yamlWithService)

	_, err := svc.UnsetServiceConfig(t.Context(), &azdext.UnsetServiceConfigRequest{
		ServiceName: "api",
		Path:        "language",
	})
	require.NoError(t, err)
}

func TestProjectService_AddService_HappyPath(t *testing.T) {
	t.Parallel()
	svc := newProjectServiceWithYaml(t, "name: test-project\n")

	_, err := svc.AddService(t.Context(), &azdext.AddServiceRequest{
		Service: &azdext.ServiceConfig{
			Name:         "web",
			Host:         "appservice",
			Language:     "javascript",
			RelativePath: "./src/web",
		},
	})
	require.NoError(t, err)
}

func TestProjectService_GetConfigSection_Found(t *testing.T) {
	t.Parallel()
	yaml := "name: test-project\nmetadata:\n  key1: value1\n  key2: value2\n"
	svc := newProjectServiceWithYaml(t, yaml)

	resp, err := svc.GetConfigSection(t.Context(), &azdext.GetProjectConfigSectionRequest{
		Path: "metadata",
	})
	require.NoError(t, err)
	require.True(t, resp.Found)
	require.NotNil(t, resp.Section)
}

func TestProjectService_GetServiceConfigSection_HappyPath(t *testing.T) {
	t.Parallel()
	svc := newProjectServiceWithYaml(t, yamlWithService)

	resp, err := svc.GetServiceConfigSection(t.Context(), &azdext.GetServiceConfigSectionRequest{
		ServiceName: "api",
		Path:        "",
	})
	require.NoError(t, err)
	require.True(t, resp.Found)
	require.NotNil(t, resp.Section)
}

func TestProjectService_GetServiceConfigValue_HappyPath(t *testing.T) {
	t.Parallel()
	svc := newProjectServiceWithYaml(t, yamlWithService)

	resp, err := svc.GetServiceConfigValue(t.Context(), &azdext.GetServiceConfigValueRequest{
		ServiceName: "api",
		Path:        "host",
	})
	require.NoError(t, err)
	require.True(t, resp.Found)
	require.NotNil(t, resp.Value)
}

func TestProjectService_ParseGitHubUrl_Valid(t *testing.T) {
	t.Parallel()
	// ParseGitHubUrl requires ghCli for HTTPS urls, so just test that it's called correctly
	// with an API URL that doesn't need authentication
	svc := NewProjectService(nil, nil, nil, nil, nil, nil, nil)
	_, err := svc.ParseGitHubUrl(t.Context(), &azdext.ParseGitHubUrlRequest{
		Url: "https://api.github.com/repos/Azure/azure-dev/contents/README.md?ref=main",
	})
	// API URL format succeeds without ghCli
	require.NoError(t, err)
}

func TestProjectService_ParseGitHubUrl_Invalid(t *testing.T) {
	t.Parallel()
	svc := NewProjectService(nil, nil, nil, nil, nil, nil, nil)
	_, err := svc.ParseGitHubUrl(t.Context(), &azdext.ParseGitHubUrlRequest{
		Url: "not-a-url",
	})
	require.Error(t, err)
}
