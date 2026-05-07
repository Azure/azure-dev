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
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/stretchr/testify/require"
)

func TestComposeService_AddResource_AzdContextError(t *testing.T) {
	t.Parallel()
	lazyCtx := lazy.NewLazy(func() (*azdcontext.AzdContext, error) {
		return nil, errors.New("no azd context")
	})
	lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
		return nil, nil
	})
	lazyMgr := lazy.NewLazy(func() (environment.Manager, error) {
		return nil, nil
	})
	svc := NewComposeService(lazyCtx, lazyEnv, lazyMgr)

	_, err := svc.AddResource(t.Context(), &azdext.AddResourceRequest{
		Resource: &azdext.ComposedResource{Name: "test"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no azd context")
}

func TestComposeService_AddResource_EnvError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ctx := azdcontext.NewAzdContextWithDirectory(dir)
	lazyCtx := lazy.NewLazy(func() (*azdcontext.AzdContext, error) {
		return ctx, nil
	})
	lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
		return nil, errors.New("env error")
	})
	lazyMgr := lazy.NewLazy(func() (environment.Manager, error) {
		return nil, nil
	})
	svc := NewComposeService(lazyCtx, lazyEnv, lazyMgr)

	_, err := svc.AddResource(t.Context(), &azdext.AddResourceRequest{
		Resource: &azdext.ComposedResource{Name: "test"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "env error")
}

func TestComposeService_AddResource_EnvManagerError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ctx := azdcontext.NewAzdContextWithDirectory(dir)
	lazyCtx := lazy.NewLazy(func() (*azdcontext.AzdContext, error) {
		return ctx, nil
	})
	lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
		return environment.NewWithValues("dev", nil), nil
	})
	lazyMgr := lazy.NewLazy(func() (environment.Manager, error) {
		return nil, errors.New("mgr error")
	})
	svc := NewComposeService(lazyCtx, lazyEnv, lazyMgr)

	_, err := svc.AddResource(t.Context(), &azdext.AddResourceRequest{
		Resource: &azdext.ComposedResource{Name: "test"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "mgr error")
}

func TestComposeService_GetResource_AzdContextError(t *testing.T) {
	t.Parallel()
	lazyCtx := lazy.NewLazy(func() (*azdcontext.AzdContext, error) {
		return nil, errors.New("no azd context")
	})
	svc := NewComposeService(lazyCtx, nil, nil)

	_, err := svc.GetResource(t.Context(), &azdext.GetResourceRequest{Name: "test"})
	require.Error(t, err)
}

func TestComposeService_ListResources_AzdContextError(t *testing.T) {
	t.Parallel()
	lazyCtx := lazy.NewLazy(func() (*azdcontext.AzdContext, error) {
		return nil, errors.New("no azd context")
	})
	svc := NewComposeService(lazyCtx, nil, nil)

	_, err := svc.ListResources(t.Context(), &azdext.EmptyRequest{})
	require.Error(t, err)
}

func TestComposeService_GetResourceType_Unimplemented(t *testing.T) {
	t.Parallel()
	svc := NewComposeService(nil, nil, nil)
	_, err := svc.GetResourceType(t.Context(), &azdext.GetResourceTypeRequest{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not yet implemented")
}

func TestComposeService_AddResource_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "azure.yaml"), []byte("name: test-project\n"), 0600)
	require.NoError(t, err)

	ctx := azdcontext.NewAzdContextWithDirectory(dir)
	lazyCtx := lazy.NewLazy(func() (*azdcontext.AzdContext, error) { return ctx, nil })
	lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
		return environment.NewWithValues("dev", nil), nil
	})
	mockMgr := &mockEnvManager{}
	lazyMgr := lazy.NewLazy(func() (environment.Manager, error) { return mockMgr, nil })
	svc := NewComposeService(lazyCtx, lazyEnv, lazyMgr)

	resp, err := svc.AddResource(t.Context(), &azdext.AddResourceRequest{
		Resource: &azdext.ComposedResource{
			Name: "mydb",
			Type: "db.postgres",
		},
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, "mydb", resp.Resource.Name)
}

func TestComposeService_AddResource_WithResourceId(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "azure.yaml"), []byte("name: test-project\n"), 0600)
	require.NoError(t, err)

	ctx := azdcontext.NewAzdContextWithDirectory(dir)
	lazyCtx := lazy.NewLazy(func() (*azdcontext.AzdContext, error) { return ctx, nil })
	lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
		return environment.NewWithValues("dev", nil), nil
	})
	mockMgr := &mockEnvManager{}
	lazyMgr := lazy.NewLazy(func() (environment.Manager, error) { return mockMgr, nil })
	svc := NewComposeService(lazyCtx, lazyEnv, lazyMgr)

	resp, err := svc.AddResource(t.Context(), &azdext.AddResourceRequest{
		Resource: &azdext.ComposedResource{
			Name:       "mydb",
			Type:       "db.postgres",
			ResourceId: "/subscriptions/sub-1/resourceGroups/rg/providers/Microsoft.DBforPostgreSQL/flexibleServers/mydb",
		},
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
}

func TestComposeService_ListResources_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	yaml := "name: test-project\nresources:\n  mydb:\n    type: db.postgres\n"
	err := os.WriteFile(filepath.Join(dir, "azure.yaml"), []byte(yaml), 0600)
	require.NoError(t, err)

	ctx := azdcontext.NewAzdContextWithDirectory(dir)
	lazyCtx := lazy.NewLazy(func() (*azdcontext.AzdContext, error) { return ctx, nil })
	svc := NewComposeService(lazyCtx, nil, nil)

	resp, err := svc.ListResources(t.Context(), &azdext.EmptyRequest{})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, resp.Resources, 1)
}

func TestComposeService_GetResource_NotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "azure.yaml"), []byte("name: test-project\n"), 0600)
	require.NoError(t, err)

	ctx := azdcontext.NewAzdContextWithDirectory(dir)
	lazyCtx := lazy.NewLazy(func() (*azdcontext.AzdContext, error) { return ctx, nil })
	svc := NewComposeService(lazyCtx, nil, nil)

	_, err = svc.GetResource(t.Context(), &azdext.GetResourceRequest{Name: "nonexistent"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestComposeService_GetResource_Found(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	yaml := "name: test-project\nresources:\n  mydb:\n    type: db.postgres\n"
	err := os.WriteFile(filepath.Join(dir, "azure.yaml"), []byte(yaml), 0600)
	require.NoError(t, err)

	ctx := azdcontext.NewAzdContextWithDirectory(dir)
	lazyCtx := lazy.NewLazy(func() (*azdcontext.AzdContext, error) { return ctx, nil })
	svc := NewComposeService(lazyCtx, nil, nil)

	resp, err := svc.GetResource(t.Context(), &azdext.GetResourceRequest{Name: "mydb"})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, "mydb", resp.Resource.Name)
}
