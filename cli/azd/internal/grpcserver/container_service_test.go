// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func TestNewContainerService(t *testing.T) {
	t.Parallel()
	svc := NewContainerService(nil, nil, nil, nil, nil)
	require.NotNil(t, svc)
}

func TestContainerServiceConfigOverridesPathWithoutMutation(t *testing.T) {
	t.Parallel()

	source := &project.ServiceConfig{
		Name:         "web",
		RelativePath: "original",
	}
	projectConfig := &project.ProjectConfig{
		Path:     t.TempDir(),
		Services: map[string]*project.ServiceConfig{"web": source},
	}

	effective, err := containerServiceConfig(
		projectConfig,
		"web",
		"resolved/path",
	)

	require.NoError(t, err)
	require.Equal(t, "resolved/path", effective.RelativePath)
	require.Equal(t, "original", source.RelativePath)
}

func TestContainerServiceConfigRejectsPathTraversal(t *testing.T) {
	t.Parallel()

	projectConfig := &project.ProjectConfig{
		Path: t.TempDir(),
		Services: map[string]*project.ServiceConfig{
			"web": {Name: "web"},
		},
	}

	_, err := containerServiceConfig(projectConfig, "web", "../outside")

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
}

func TestContainerServiceConfigRejectsSymlinkEscape(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	link := filepath.Join(projectRoot, "linked")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	projectConfig := &project.ProjectConfig{
		Path: projectRoot,
		Services: map[string]*project.ServiceConfig{
			"web": {Name: "web"},
		},
	}

	_, err := containerServiceConfig(projectConfig, "web", "linked")

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
}

func TestContainerServiceConfigDecodesForwardCompatiblePath(
	t *testing.T,
) {
	t.Parallel()

	data := protowire.AppendTag(nil, 1, protowire.BytesType)
	data = protowire.AppendString(data, "web")
	data = protowire.AppendTag(data, 3, protowire.BytesType)
	data = protowire.AppendString(data, "resolved/path")
	request := &azdext.ContainerBuildRequest{}
	require.NoError(t, proto.Unmarshal(data, request))

	projectConfig := &project.ProjectConfig{
		Path: t.TempDir(),
		Services: map[string]*project.ServiceConfig{
			"web": {Name: "web"},
		},
	}
	effective, err := containerServiceConfig(
		projectConfig,
		request.GetServiceName(),
		request.GetServicePath(),
	)

	require.NoError(t, err)
	require.Equal(t, "resolved/path", effective.RelativePath)
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
