// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"math/big"
	"testing"

	"github.com/azure/azure-dev/pkg/azdext"
	"github.com/azure/azure-dev/pkg/environment"
	"github.com/azure/azure-dev/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/pkg/lazy"
	"github.com/azure/azure-dev/pkg/project"
	"github.com/azure/azure-dev/test/mocks"
	"github.com/azure/azure-dev/test/mocks/mockenv"
	"github.com/stretchr/testify/require"
)

func Test_ComposeService_AddResource(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	temp := t.TempDir()
	azdCtx := azdcontext.NewAzdContextWithDirectory(temp)
	projectConfig := project.ProjectConfig{
		Name: "test",
	}
	err := project.Save(*mockContext.Context, &projectConfig, azdCtx.ProjectPath())
	require.NoError(t, err)
	lazyAzdContext := lazy.From(azdCtx)
	env := environment.New("test")
	envManager := &mockenv.MockEnvManager{}
	lazyEnvManager := lazy.NewLazy(func() (environment.Manager, error) {
		return envManager, nil
	})
	lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
		return env, nil
	})
	composeService := NewComposeService(lazyAzdContext, lazyEnv, lazyEnvManager)

	t.Run("success", func(t *testing.T) {
		addReq := &azdext.AddResourceRequest{
			Resource: &azdext.ComposedResource{
				Name:   "resource1",
				Type:   "Storage",
				Config: []byte("{}"),
				Uses:   []string{},
			},
		}
		addResp, err := composeService.AddResource(*mockContext.Context, addReq)
		require.NoError(t, err)
		require.NotNil(t, addResp)
		require.Equal(t, addReq.Resource.Name, addResp.Resource.Name)

		updatedConfig, err := project.Load(*mockContext.Context, azdCtx.ProjectPath())
		require.NoError(t, err)
		res, exists := updatedConfig.Resources["resource1"]
		require.True(t, exists)
		require.Equal(t, "resource1", res.Name)
		require.Equal(t, project.ResourceType("Storage"), res.Type)
	})

	t.Run("invalid config", func(t *testing.T) {
		// reuse the same common setup.
		addReq := &azdext.AddResourceRequest{
			Resource: &azdext.ComposedResource{
				Name:   "invalid",
				Type:   "storage",
				Config: []byte("invalid json"),
				Uses:   []string{},
			},
		}
		_, err := composeService.AddResource(*mockContext.Context, addReq)
		require.Error(t, err)
	})
}

func Test_ComposeService_GetResource(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	temp := t.TempDir()
	azdCtx := azdcontext.NewAzdContextWithDirectory(temp)
	projectConfig := project.ProjectConfig{
		Name: "test",
		Resources: map[string]*project.ResourceConfig{
			"resource1": {
				Name:  "resource1",
				Type:  project.ResourceTypeStorage,
				Props: project.StorageProps{},
				Uses:  []string{},
			},
		},
	}
	err := project.Save(*mockContext.Context, &projectConfig, azdCtx.ProjectPath())
	require.NoError(t, err)
	lazyAzdContext := lazy.From(azdCtx)
	env := environment.New("test")
	envManager := &mockenv.MockEnvManager{}
	lazyEnvManager := lazy.NewLazy(func() (environment.Manager, error) {
		return envManager, nil
	})
	lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
		return env, nil
	})
	composeService := NewComposeService(lazyAzdContext, lazyEnv, lazyEnvManager)

	t.Run("success", func(t *testing.T) {
		getReq := &azdext.GetResourceRequest{
			Name: "resource1",
		}
		getResp, err := composeService.GetResource(*mockContext.Context, getReq)
		require.NoError(t, err)
		require.NotNil(t, getResp)
		require.Equal(t, "resource1", getResp.Resource.Name)
		require.Equal(t, "storage", getResp.Resource.Type)

		configBytes, err := json.Marshal(project.StorageProps{})
		require.NoError(t, err)
		require.JSONEq(t, string(configBytes), string(getResp.Resource.Config))
	})

	t.Run("resource not found", func(t *testing.T) {
		getReq := &azdext.GetResourceRequest{
			Name: "nonexistent",
		}
		_, err = composeService.GetResource(*mockContext.Context, getReq)
		require.Error(t, err)
	})
}

func Test_ComposeService_ListResources(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	t.Run("success", func(t *testing.T) {
		temp := t.TempDir()
		azdCtx := azdcontext.NewAzdContextWithDirectory(temp)
		projectConfig := project.ProjectConfig{
			Name: "test",
			Resources: map[string]*project.ResourceConfig{
				"resource1": {
					Name:  "resource1",
					Type:  project.ResourceTypeStorage,
					Props: project.StorageProps{},
					Uses:  []string{},
				},
				"resource2": {
					Name:  "resource2",
					Type:  project.ResourceTypeDbCosmos,
					Props: project.CosmosDBProps{},
					Uses:  []string{"resource1"},
				},
			},
		}
		err := project.Save(*mockContext.Context, &projectConfig, azdCtx.ProjectPath())
		require.NoError(t, err)
		lazyAzdContext := lazy.From(azdCtx)
		env := environment.New("test")
		envManager := &mockenv.MockEnvManager{}
		lazyEnvManager := lazy.NewLazy(func() (environment.Manager, error) {
			return envManager, nil
		})
		lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
			return env, nil
		})
		composeService := NewComposeService(lazyAzdContext, lazyEnv, lazyEnvManager)

		listResp, err := composeService.ListResources(*mockContext.Context, &azdext.EmptyRequest{})
		require.NoError(t, err)
		require.NotNil(t, listResp)
		require.Len(t, listResp.Resources, 2)
		names := map[string]bool{}
		for _, res := range listResp.Resources {
			names[res.Name] = true
		}
		require.True(t, names["resource1"])
		require.True(t, names["resource2"])
	})

	t.Run("no project", func(t *testing.T) {
		// For this subtest, simulate no project using a lazy context
		lazyAzdContext := lazy.NewLazy(func() (*azdcontext.AzdContext, error) {
			return nil, azdcontext.ErrNoProject
		})
		env := environment.New("test")
		envManager := &mockenv.MockEnvManager{}
		lazyEnvManager := lazy.NewLazy(func() (environment.Manager, error) {
			return envManager, nil
		})
		lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
			return env, nil
		})
		composeService := NewComposeService(lazyAzdContext, lazyEnv, lazyEnvManager)
		_, err := composeService.ListResources(*mockContext.Context, &azdext.EmptyRequest{})
		require.Error(t, err)
	})
}

func Test_Test_ComposeService_ListResourceTypes(t *testing.T) {
	// Setup a mock context.
	mockContext := mocks.NewMockContext(context.Background())
	lazyAzdContext := lazy.NewLazy(func() (*azdcontext.AzdContext, error) {
		return nil, azdcontext.ErrNoProject
	})

	// Create the service and call ListResourceTypes
	env := environment.New("test")
	envManager := &mockenv.MockEnvManager{}
	lazyEnvManager := lazy.NewLazy(func() (environment.Manager, error) {
		return envManager, nil
	})
	lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
		return env, nil
	})
	service := NewComposeService(lazyAzdContext, lazyEnv, lazyEnvManager)
	response, err := service.ListResourceTypes(*mockContext.Context, &azdext.EmptyRequest{})
	require.NoError(t, err)
	require.NotNil(t, response)
	require.NotEmpty(t, response.ResourceTypes)

	// Verify a resource type.
	maxIndex := big.NewInt(int64(len(response.ResourceTypes)))
	randomIndexBig, err := rand.Int(rand.Reader, maxIndex)
	require.NoError(t, err)
	randomIndex := randomIndexBig.Int64()
	randomResource := response.ResourceTypes[randomIndex]
	require.NotNil(t, randomResource)
	require.NotEmpty(t, randomResource.Name)
	require.NotEmpty(t, randomResource.DisplayName)
	require.NotEmpty(t, randomResource.Type)
}
