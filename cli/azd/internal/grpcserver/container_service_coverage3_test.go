// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestNewContainerService(t *testing.T) {
	t.Parallel()
	svc := NewContainerService(nil, nil, nil, nil, nil)
	require.NotNil(t, svc)
}

func TestContainerService_Build_EmptyServiceName(t *testing.T) {
	t.Parallel()
	svc := NewContainerService(nil, nil, nil, nil, nil)
	_, err := svc.Build(t.Context(), &azdext.ContainerBuildRequest{
		ServiceName: "",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
}

func TestContainerService_Build_LazyProjectError(t *testing.T) {
	t.Parallel()
	lazyProject := lazy.NewLazy(func() (*project.ProjectConfig, error) {
		return nil, errors.New("project load failed")
	})
	svc := NewContainerService(nil, nil, nil, lazyProject, nil)

	_, err := svc.Build(t.Context(), &azdext.ContainerBuildRequest{
		ServiceName: "web",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "project load failed")
}

func TestContainerService_Build_ServiceNotFound(t *testing.T) {
	t.Parallel()
	lazyProject := lazy.NewLazy(func() (*project.ProjectConfig, error) {
		return &project.ProjectConfig{
			Services: map[string]*project.ServiceConfig{},
		}, nil
	})
	svc := NewContainerService(nil, nil, nil, lazyProject, nil)

	_, err := svc.Build(t.Context(), &azdext.ContainerBuildRequest{
		ServiceName: "nonexistent",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.NotFound, st.Code())
}

func TestContainerService_Build_ContainerHelperError(t *testing.T) {
	t.Parallel()
	lazyProject := lazy.NewLazy(func() (*project.ProjectConfig, error) {
		return &project.ProjectConfig{
			Services: map[string]*project.ServiceConfig{
				"web": {Name: "web"},
			},
		}, nil
	})
	lazyHelper := lazy.NewLazy(func() (*project.ContainerHelper, error) {
		return nil, errors.New("container helper error")
	})
	svc := NewContainerService(nil, lazyHelper, nil, lazyProject, nil)

	_, err := svc.Build(t.Context(), &azdext.ContainerBuildRequest{
		ServiceName: "web",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "container helper error")
}

func TestContainerService_Package_EmptyServiceName(t *testing.T) {
	t.Parallel()
	svc := NewContainerService(nil, nil, nil, nil, nil)
	_, err := svc.Package(t.Context(), &azdext.ContainerPackageRequest{
		ServiceName: "",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
}

func TestContainerService_Package_LazyProjectError(t *testing.T) {
	t.Parallel()
	lazyProject := lazy.NewLazy(func() (*project.ProjectConfig, error) {
		return nil, errors.New("project fail")
	})
	svc := NewContainerService(nil, nil, nil, lazyProject, nil)

	_, err := svc.Package(t.Context(), &azdext.ContainerPackageRequest{
		ServiceName: "api",
	})
	require.Error(t, err)
}

func TestContainerService_Package_ServiceNotFound(t *testing.T) {
	t.Parallel()
	lazyProject := lazy.NewLazy(func() (*project.ProjectConfig, error) {
		return &project.ProjectConfig{
			Services: map[string]*project.ServiceConfig{},
		}, nil
	})
	svc := NewContainerService(nil, nil, nil, lazyProject, nil)

	_, err := svc.Package(t.Context(), &azdext.ContainerPackageRequest{
		ServiceName: "missing",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.NotFound, st.Code())
}

func TestContainerService_Package_ContainerHelperError(t *testing.T) {
	t.Parallel()
	lazyProject := lazy.NewLazy(func() (*project.ProjectConfig, error) {
		return &project.ProjectConfig{
			Services: map[string]*project.ServiceConfig{"api": {Name: "api"}},
		}, nil
	})
	lazyHelper := lazy.NewLazy(func() (*project.ContainerHelper, error) {
		return nil, errors.New("helper not available")
	})
	svc := NewContainerService(nil, lazyHelper, nil, lazyProject, nil)

	_, err := svc.Package(t.Context(), &azdext.ContainerPackageRequest{
		ServiceName: "api",
	})
	require.Error(t, err)
}

func TestContainerService_Publish_EmptyServiceName(t *testing.T) {
	t.Parallel()
	svc := NewContainerService(nil, nil, nil, nil, nil)
	_, err := svc.Publish(t.Context(), &azdext.ContainerPublishRequest{
		ServiceName: "",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
}

func TestContainerService_Publish_LazyProjectError(t *testing.T) {
	t.Parallel()
	lazyProject := lazy.NewLazy(func() (*project.ProjectConfig, error) {
		return nil, errors.New("project fail")
	})
	svc := NewContainerService(nil, nil, nil, lazyProject, nil)

	_, err := svc.Publish(t.Context(), &azdext.ContainerPublishRequest{
		ServiceName: "web",
	})
	require.Error(t, err)
}

func TestContainerService_Publish_ServiceNotFound(t *testing.T) {
	t.Parallel()
	lazyProject := lazy.NewLazy(func() (*project.ProjectConfig, error) {
		return &project.ProjectConfig{
			Services: map[string]*project.ServiceConfig{},
		}, nil
	})
	svc := NewContainerService(nil, nil, nil, lazyProject, nil)

	_, err := svc.Publish(t.Context(), &azdext.ContainerPublishRequest{
		ServiceName: "missing",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.NotFound, st.Code())
}

func TestContainerService_Build_EnvironmentError(t *testing.T) {
	t.Parallel()
	lazyProject := lazy.NewLazy(func() (*project.ProjectConfig, error) {
		return &project.ProjectConfig{
			Services: map[string]*project.ServiceConfig{"web": {Name: "web"}},
		}, nil
	})
	lazyHelper := lazy.NewLazy(func() (*project.ContainerHelper, error) {
		return &project.ContainerHelper{}, nil
	})
	lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
		return nil, errors.New("env error")
	})
	svc := NewContainerService(nil, lazyHelper, nil, lazyProject, lazyEnv)

	_, err := svc.Build(t.Context(), &azdext.ContainerBuildRequest{
		ServiceName: "web",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "env error")
}

func TestContainerService_Package_EnvironmentError(t *testing.T) {
	t.Parallel()
	lazyProject := lazy.NewLazy(func() (*project.ProjectConfig, error) {
		return &project.ProjectConfig{
			Services: map[string]*project.ServiceConfig{"api": {Name: "api"}},
		}, nil
	})
	lazyHelper := lazy.NewLazy(func() (*project.ContainerHelper, error) {
		return &project.ContainerHelper{}, nil
	})
	lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
		return nil, errors.New("env error")
	})
	svc := NewContainerService(nil, lazyHelper, nil, lazyProject, lazyEnv)

	_, err := svc.Package(t.Context(), &azdext.ContainerPackageRequest{
		ServiceName: "api",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "env error")
}

func TestContainerService_Publish_ContainerHelperError(t *testing.T) {
	t.Parallel()
	lazyProject := lazy.NewLazy(func() (*project.ProjectConfig, error) {
		return &project.ProjectConfig{
			Services: map[string]*project.ServiceConfig{"web": {Name: "web"}},
		}, nil
	})
	lazyHelper := lazy.NewLazy(func() (*project.ContainerHelper, error) {
		return nil, errors.New("helper error")
	})
	svc := NewContainerService(nil, lazyHelper, nil, lazyProject, nil)

	_, err := svc.Publish(t.Context(), &azdext.ContainerPublishRequest{
		ServiceName: "web",
	})
	require.Error(t, err)
}

func TestContainerService_Publish_ServiceManagerError(t *testing.T) {
	t.Parallel()
	lazyProject := lazy.NewLazy(func() (*project.ProjectConfig, error) {
		return &project.ProjectConfig{
			Services: map[string]*project.ServiceConfig{"web": {Name: "web"}},
		}, nil
	})
	lazyHelper := lazy.NewLazy(func() (*project.ContainerHelper, error) {
		return &project.ContainerHelper{}, nil
	})
	lazySvcMgr := lazy.NewLazy(func() (project.ServiceManager, error) {
		return nil, errors.New("service manager error")
	})
	svc := NewContainerService(nil, lazyHelper, lazySvcMgr, lazyProject, nil)

	_, err := svc.Publish(t.Context(), &azdext.ContainerPublishRequest{
		ServiceName: "web",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "service manager error")
}
