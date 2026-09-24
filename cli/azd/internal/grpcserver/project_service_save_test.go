// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type projectSaveTransport struct {
	mu       sync.Mutex
	trailers metadata.MD
}

func (*projectSaveTransport) Method() string               { return "/azd.extensions.v1.ProjectService/AddService" }
func (*projectSaveTransport) SetHeader(metadata.MD) error  { return nil }
func (*projectSaveTransport) SendHeader(metadata.MD) error { return nil }
func (s *projectSaveTransport) SetTrailer(md metadata.MD) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trailers = metadata.Join(s.trailers, md)
	return nil
}

func (s *projectSaveTransport) savedFailure() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.trailers.Get("azd-project-add-service-save-failed")...)
}

func TestProjectAddServiceAcknowledgesOnlyCompletedFailedSave(t *testing.T) {
	for _, existed := range []bool{false, true} {
		name := "new"
		if existed {
			name = "existing"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			azdContext := azdcontext.NewAzdContextWithDirectory(dir)
			cfg := &project.ProjectConfig{Name: "test"}
			if existed {
				cfg.Services = map[string]*project.ServiceConfig{
					"api": {Name: "api", Language: project.ServiceLanguagePython, Host: project.ContainerAppTarget},
				}
			}
			require.NoError(t, project.Save(t.Context(), cfg, azdContext.ProjectPath()))
			before, err := os.ReadFile(azdContext.ProjectPath())
			require.NoError(t, err)
			server := NewProjectService(lazy.From(azdContext), nil, nil, lazy.From(cfg), nil, nil)
			service, ok := server.(*projectService)
			require.True(t, ok)

			started, finish := make(chan struct{}), make(chan struct{})
			var once sync.Once
			release := func() { once.Do(func() { close(finish) }) }
			t.Cleanup(release)
			service.saveProject = func(_ context.Context, _ *project.ProjectConfig, path string) error {
				close(started)
				<-finish
				return &os.PathError{Op: "save", Path: path, Err: os.ErrPermission}
			}
			stream := &projectSaveTransport{}
			ctx := grpc.NewContextWithServerTransportStream(t.Context(), stream)
			ctx = metadata.NewIncomingContext(ctx, metadata.Pairs("azd-project-add-service-operation", "this-attempt"))
			request := &azdext.AddServiceRequest{Service: &azdext.ServiceConfig{
				Name: "api", Host: "containerapp", Language: "js",
			}}
			done := make(chan error, 1)
			go func() {
				_, err := service.AddService(ctx, request)
				done <- err
			}()
			select {
			case <-started:
			case <-time.After(10 * time.Second):
				t.Fatal("save was not called")
			}
			assert.Empty(t, stream.savedFailure(), "an in-flight save must never be acknowledged")
			release()
			select {
			case err := <-done:
				require.ErrorIs(t, err, os.ErrPermission)
			case <-time.After(10 * time.Second):
				t.Fatal("save did not finish")
			}
			assert.Equal(t, []string{"this-attempt"}, stream.savedFailure())
			after, err := os.ReadFile(azdContext.ProjectPath())
			require.NoError(t, err)
			assert.Equal(t, before, after)
			cached, err := service.lazyProjectConfig.GetValue()
			require.NoError(t, err)
			if existed {
				require.Contains(t, cached.Services, "api")
				assert.Equal(t, project.ServiceLanguagePython, cached.Services["api"].Language)
			} else {
				assert.NotContains(t, cached.Services, "api", "failed additions must not survive in the host cache")
			}

			service.saveProject = project.Save
			successStream := &projectSaveTransport{}
			ctx = grpc.NewContextWithServerTransportStream(t.Context(), successStream)
			ctx = metadata.NewIncomingContext(ctx, metadata.Pairs("azd-project-add-service-operation", "retry"))
			_, err = service.AddService(ctx, request)
			require.NoError(t, err)
			assert.Empty(t, successStream.savedFailure(), "success must not reuse a previous failure acknowledgment")
			saved, err := project.Load(t.Context(), azdContext.ProjectPath())
			require.NoError(t, err)
			assert.Equal(t, project.ServiceLanguageJavaScript, saved.Services["api"].Language)
		})
	}
}

func TestProjectAddServiceOmitsAcknowledgementWithoutOneOperationToken(t *testing.T) {
	for _, tokens := range [][]string{nil, {""}, {"one", "two"}} {
		t.Run("", func(t *testing.T) {
			dir := t.TempDir()
			azdContext := azdcontext.NewAzdContextWithDirectory(dir)
			cfg := &project.ProjectConfig{Name: "test"}
			require.NoError(t, project.Save(t.Context(), cfg, azdContext.ProjectPath()))
			server := NewProjectService(lazy.From(azdContext), nil, nil, lazy.From(cfg), nil, nil)
			service, ok := server.(*projectService)
			require.True(t, ok)
			service.saveProject = func(context.Context, *project.ProjectConfig, string) error { return os.ErrPermission }
			stream := &projectSaveTransport{}
			ctx := grpc.NewContextWithServerTransportStream(t.Context(), stream)
			ctx = metadata.NewIncomingContext(ctx, metadata.MD{"azd-project-add-service-operation": tokens})
			_, err := service.AddService(ctx, &azdext.AddServiceRequest{Service: &azdext.ServiceConfig{
				Name: "api", Host: "containerapp", Language: "python",
			}})
			require.ErrorIs(t, err, os.ErrPermission)
			assert.Empty(t, stream.savedFailure())
		})
	}
}

func TestProjectAddServiceFailedSaveRestorationDoesNotDropConcurrentAddition(t *testing.T) {
	dir := t.TempDir()
	azdContext := azdcontext.NewAzdContextWithDirectory(dir)
	cfg := &project.ProjectConfig{Name: "test"}
	require.NoError(t, project.Save(t.Context(), cfg, azdContext.ProjectPath()))
	server := NewProjectService(lazy.From(azdContext), nil, nil, lazy.From(cfg), nil, nil)
	service, ok := server.(*projectService)
	require.True(t, ok)
	started, finish := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(finish) }) }
	t.Cleanup(release)
	service.saveProject = func(ctx context.Context, cfg *project.ProjectConfig, path string) error {
		if _, failed := cfg.Services["failed"]; failed {
			close(started)
			<-finish
			return os.ErrPermission
		}
		return project.Save(ctx, cfg, path)
	}
	stream := &projectSaveTransport{}
	ctx := grpc.NewContextWithServerTransportStream(t.Context(), stream)
	ctx = metadata.NewIncomingContext(ctx, metadata.Pairs("azd-project-add-service-operation", "failed-attempt"))
	first := make(chan error, 1)
	go func() {
		_, err := service.AddService(ctx, &azdext.AddServiceRequest{
			Service: &azdext.ServiceConfig{Name: "failed", Host: "containerapp", Language: "python"},
		})
		first <- err
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the first save did not start")
	}
	if service.configMutationMu.TryLock() {
		service.configMutationMu.Unlock()
		t.Fatal("save and compensation must hold the project mutation lock")
	}
	second := make(chan error, 1)
	go func() {
		_, err := service.AddService(t.Context(), &azdext.AddServiceRequest{
			Service: &azdext.ServiceConfig{Name: "kept", Host: "containerapp", Language: "python"},
		})
		second <- err
	}()
	release()
	require.ErrorIs(t, <-first, os.ErrPermission)
	require.NoError(t, <-second)
	assert.Equal(t, []string{"failed-attempt"}, stream.savedFailure())
	cached, err := service.lazyProjectConfig.GetValue()
	require.NoError(t, err)
	assert.Contains(t, cached.Services, "kept")
	assert.NotContains(t, cached.Services, "failed")
	saved, err := project.Load(t.Context(), azdContext.ProjectPath())
	require.NoError(t, err)
	assert.Contains(t, saved.Services, "kept")
	assert.NotContains(t, saved.Services, "failed")
}
