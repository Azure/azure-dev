// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"fmt"
	osexec "os/exec"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/containerregistry"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	azdexec "github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
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

func TestMapContainerPublishError_RemoteBuildRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status armcontainerregistry.RunStatus
		code   string
	}{
		{
			name:   "failed",
			status: armcontainerregistry.RunStatusFailed,
			code:   "container_publish_acr_run_failed",
		},
		{
			name:   "error",
			status: armcontainerregistry.RunStatusError,
			code:   "container_publish_acr_run_error",
		},
		{
			name:   "timeout",
			status: armcontainerregistry.RunStatusTimeout,
			code:   "container_publish_acr_run_timeout",
		},
		{
			name:   "canceled",
			status: armcontainerregistry.RunStatusCanceled,
			code:   "container_publish_acr_run_canceled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := fmt.Errorf("publish wrapper: %w", &containerregistry.RemoteBuildRunError{
				Status: tt.status,
			})
			mapped := mapContainerPublishError(err)

			var localErr *azdext.LocalError
			require.ErrorAs(t, mapped, &localErr)
			require.Equal(t, tt.code, localErr.Code)
			require.Equal(t, azdext.LocalErrorCategoryInternal, localErr.Category)
			require.Equal(t, err.Error(), localErr.Message)
		})
	}
}

func TestMapContainerPublishError_PreservesUnclassifiedErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
	}{
		{
			name: "unknown remote build status",
			err: fmt.Errorf("publish wrapper: %w", &containerregistry.RemoteBuildRunError{
				Status: armcontainerregistry.RunStatusSucceeded,
			}),
		},
		{
			name: "plain error",
			err:  errors.New("publish failed"),
		},
		{
			name: "canceled",
			err:  fmt.Errorf("publish canceled: %w", context.Canceled),
		},
		{
			name: "deadline exceeded",
			err:  fmt.Errorf("publish deadline: %w", context.DeadlineExceeded),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Same(t, tt.err, mapContainerPublishError(tt.err))
		})
	}
}

func TestMapContainerPublishError_PreservesResponseError(t *testing.T) {
	t.Parallel()

	responseErr := &azcore.ResponseError{StatusCode: 500}
	remoteBuildErr := &containerregistry.RemoteBuildRunError{
		Status: armcontainerregistry.RunStatusFailed,
	}
	err := errors.Join(responseErr, remoteBuildErr)

	require.Same(t, err, mapContainerPublishError(err))
}

func TestMapContainerToolError(t *testing.T) {
	t.Parallel()

	exitErr := &azdexec.ExitError{
		Cmd:      `C:\Program Files\Docker\docker.exe`,
		ExitCode: 17,
	}
	missingProcessErr := &osexec.Error{Name: "Docker", Err: osexec.ErrNotFound}
	missingToolsErr := &tools.MissingToolErrors{ToolNames: []string{"Docker"}}
	multipleToolsErr := &tools.MissingToolErrors{ToolNames: []string{"Docker", "Podman"}}

	tests := []struct {
		name     string
		err      error
		wantKind azdext.ToolErrorKind
		wantName string
		wantCode *int
	}{
		{
			name:     "exit error",
			err:      fmt.Errorf("build failed: %w", exitErr),
			wantKind: azdext.ToolErrorKindFailed,
			wantName: "docker",
			wantCode: new(17),
		},
		{
			name:     "missing process",
			err:      fmt.Errorf("build failed: %w", missingProcessErr),
			wantKind: azdext.ToolErrorKindMissing,
			wantName: "docker",
		},
		{
			name:     "missing tool",
			err:      missingToolsErr,
			wantKind: azdext.ToolErrorKindMissing,
			wantName: "docker",
		},
		{
			name:     "multiple missing tools",
			err:      multipleToolsErr,
			wantKind: azdext.ToolErrorKindMissing,
			wantName: "multiple",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mapped := mapContainerToolError(tt.err)

			var toolErr *azdext.ToolError
			require.ErrorAs(t, mapped, &toolErr)
			require.Equal(t, tt.wantKind, toolErr.Kind)
			require.Equal(t, tt.wantName, toolErr.ToolName)
			require.Equal(t, tt.wantCode, toolErr.ExitCode)
			require.Equal(t, tt.err.Error(), toolErr.Message)
			require.ErrorIs(t, mapped, tt.err)
		})
	}
}

func TestMapContainerToolError_PreservesNonToolErrors(t *testing.T) {
	t.Parallel()

	responseErr := &azcore.ResponseError{StatusCode: 500}
	serviceErr := &azdext.ServiceError{Message: "service failed"}
	localErr := &azdext.LocalError{Message: "local failed"}
	toolErr := &azdext.ToolError{Message: "tool failed", ToolName: "docker"}
	unknownErr := errors.New("unknown failure")

	tests := []struct {
		name string
		err  error
	}{
		{name: "response", err: responseErr},
		{name: "canceled", err: context.Canceled},
		{name: "deadline", err: context.DeadlineExceeded},
		{name: "service", err: serviceErr},
		{name: "local", err: localErr},
		{name: "tool", err: toolErr},
		{name: "unknown", err: unknownErr},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.err, mapContainerToolError(tt.err))
		})
	}
}
