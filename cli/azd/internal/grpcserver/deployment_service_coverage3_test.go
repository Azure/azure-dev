// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/bicep"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/stretchr/testify/require"
)

func TestNewDeploymentService(t *testing.T) {
	t.Parallel()
	svc := NewDeploymentService(nil, nil, nil, nil, nil)
	require.NotNil(t, svc)
}

func TestDeploymentService_GetDeployment_AzdContextError(t *testing.T) {
	t.Parallel()
	lazyCtx := lazy.NewLazy(func() (*azdcontext.AzdContext, error) {
		return nil, errors.New("no azd context")
	})
	svc := NewDeploymentService(lazyCtx, nil, nil, nil, nil)

	_, err := svc.GetDeployment(t.Context(), &azdext.EmptyRequest{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no azd context")
}

func TestDeploymentService_GetDeploymentContext_AzdContextError(t *testing.T) {
	t.Parallel()
	lazyCtx := lazy.NewLazy(func() (*azdcontext.AzdContext, error) {
		return nil, errors.New("no azd context")
	})
	svc := NewDeploymentService(lazyCtx, nil, nil, nil, nil)

	_, err := svc.GetDeploymentContext(t.Context(), &azdext.EmptyRequest{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no azd context")
}

func TestDeploymentService_GetDeployment_ProjectConfigError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	lazyCtx := lazy.NewLazy(func() (*azdcontext.AzdContext, error) {
		return azdcontext.NewAzdContextWithDirectory(dir), nil
	})
	lazyProject := lazy.NewLazy(func() (*project.ProjectConfig, error) {
		return nil, errors.New("project config error")
	})
	svc := NewDeploymentService(lazyCtx, nil, lazyProject, nil, nil)

	_, err := svc.GetDeployment(t.Context(), &azdext.EmptyRequest{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "project config error")
}

func TestDeploymentService_GetDeployment_BicepProviderError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	lazyCtx := lazy.NewLazy(func() (*azdcontext.AzdContext, error) {
		return azdcontext.NewAzdContextWithDirectory(dir), nil
	})
	lazyProject := lazy.NewLazy(func() (*project.ProjectConfig, error) {
		return &project.ProjectConfig{}, nil
	})
	lazyBicep := lazy.NewLazy(func() (*bicep.BicepProvider, error) {
		return nil, errors.New("bicep provider error")
	})
	svc := NewDeploymentService(lazyCtx, nil, lazyProject, lazyBicep, nil)

	_, err := svc.GetDeployment(t.Context(), &azdext.EmptyRequest{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "bicep provider error")
}

func TestDeploymentService_GetDeploymentContext_EnvManagerError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ctx := azdcontext.NewAzdContextWithDirectory(dir)
	// Set a default env so we get past the empty check
	require.NoError(t, ctx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "test-env"}))

	lazyCtx := lazy.NewLazy(func() (*azdcontext.AzdContext, error) {
		return ctx, nil
	})
	lazyEnvManager := lazy.NewLazy(func() (environment.Manager, error) {
		return nil, errors.New("env manager error")
	})
	svc := NewDeploymentService(lazyCtx, lazyEnvManager, nil, nil, nil)

	_, err := svc.GetDeploymentContext(t.Context(), &azdext.EmptyRequest{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "env manager error")
}

func TestDeploymentService_GetDeploymentContext_NoDefaultEnv(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ctx := azdcontext.NewAzdContextWithDirectory(dir)
	// Don't set default environment - should return ErrDefaultEnvironmentNotFound

	lazyCtx := lazy.NewLazy(func() (*azdcontext.AzdContext, error) {
		return ctx, nil
	})
	svc := NewDeploymentService(lazyCtx, nil, nil, nil, nil)

	_, err := svc.GetDeploymentContext(t.Context(), &azdext.EmptyRequest{})
	require.Error(t, err)
}

func TestDeploymentService_GetDeploymentContext_EnvGetError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ctx := azdcontext.NewAzdContextWithDirectory(dir)
	require.NoError(t, ctx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "test-env"}))

	lazyCtx := lazy.NewLazy(func() (*azdcontext.AzdContext, error) {
		return ctx, nil
	})

	mockMgr := &mockEnvManager{
		getFunc: func(_ context.Context, name string) (*environment.Environment, error) {
			return nil, errors.New("env not found")
		},
	}
	lazyEnvManager := lazy.NewLazy(func() (environment.Manager, error) {
		return mockMgr, nil
	})
	svc := NewDeploymentService(lazyCtx, lazyEnvManager, nil, nil, nil)

	_, err := svc.GetDeploymentContext(t.Context(), &azdext.EmptyRequest{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "env not found")
}

// TestDeploymentService_GetDeploymentContext_EnvResolved_DeploymentFails tests that env vars are read
// but then GetDeployment fails (no project config). Covers lines 123-137.
func TestDeploymentService_GetDeploymentContext_EnvResolved_DeploymentFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ctx := azdcontext.NewAzdContextWithDirectory(dir)
	require.NoError(t, ctx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "test-env"}))

	lazyCtx := lazy.NewLazy(func() (*azdcontext.AzdContext, error) {
		return ctx, nil
	})

	mockMgr := &mockEnvManager{
		getFunc: func(_ context.Context, name string) (*environment.Environment, error) {
			return environment.NewWithValues("test-env", map[string]string{
				"AZURE_TENANT_ID":       "tenant-1",
				"AZURE_SUBSCRIPTION_ID": "sub-1",
				"AZURE_RESOURCE_GROUP":  "rg-1",
				"AZURE_LOCATION":        "eastus",
			}), nil
		},
	}
	lazyEnvManager := lazy.NewLazy(func() (environment.Manager, error) { return mockMgr, nil })

	// No project config → GetDeployment will fail
	lazyProject := lazy.NewLazy(func() (*project.ProjectConfig, error) {
		return nil, errors.New("no project config")
	})
	svc := NewDeploymentService(lazyCtx, lazyEnvManager, lazyProject, nil, nil)

	_, err := svc.GetDeploymentContext(t.Context(), &azdext.EmptyRequest{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no project config")
}
