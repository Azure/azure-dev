// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

// Test_ProjectService_NoProject ensures that when no project exists,
// the service returns an error.
func Test_ProjectService_NoProject(t *testing.T) {
	// Setup a mock context.
	mockContext := mocks.NewMockContext(context.Background())

	// Lazy loaders return a "no project" error.
	lazyAzdContext := lazy.NewLazy(func() (*azdcontext.AzdContext, error) {
		return nil, azdcontext.ErrNoProject
	})
	lazyEnvManager := lazy.NewLazy(func() (environment.Manager, error) {
		return nil, azdcontext.ErrNoProject
	})

	// Create the service.
	service := NewProjectService(lazyAzdContext, lazyEnvManager)
	_, err := service.Get(*mockContext.Context, &azdext.EmptyRequest{})
	require.Error(t, err)
}

// Test_ProjectService_Flow validates the complete project service flow:
// creating a project, setting environment variables and retrieving project details.
func Test_ProjectService_Flow(t *testing.T) {
	// Setup a mock context and temporary project directory.
	mockContext := mocks.NewMockContext(context.Background())
	temp := t.TempDir()

	// Initialize AzdContext with the temporary directory.
	azdContext := azdcontext.NewAzdContextWithDirectory(temp)

	// Define and save project configuration.
	projectConfig := project.ProjectConfig{
		Name: "test",
	}
	err := project.Save(*mockContext.Context, &projectConfig, azdContext.ProjectPath())
	require.NoError(t, err)

	// Configure and initialize environment manager.
	fileConfigManager := config.NewFileConfigManager(config.NewManager())
	localDataStore := environment.NewLocalFileDataStore(azdContext, fileConfigManager)
	envManager, err := environment.NewManager(mockContext.Container, azdContext, mockContext.Console, localDataStore, nil)
	require.NoError(t, err)
	require.NotNil(t, envManager)

	// Create lazy-loaded instances.
	lazyAzdContext := lazy.From(azdContext)
	lazyEnvManager := lazy.From(envManager)

	// Create an environment and set an environment variable.
	testEnv1, err := envManager.Create(*mockContext.Context, environment.Spec{
		Name: "test1",
	})
	require.NoError(t, err)
	require.NotNil(t, testEnv1)
	testEnv1.DotenvSet("foo", "bar")
	err = envManager.Save(*mockContext.Context, testEnv1)
	require.NoError(t, err)

	// Create the service.
	service := NewProjectService(lazyAzdContext, lazyEnvManager)

	// Test: Retrieve project details.
	getResponse, err := service.Get(*mockContext.Context, &azdext.EmptyRequest{})
	require.NoError(t, err)
	require.NotNil(t, getResponse)
	require.Equal(t, projectConfig.Name, getResponse.Project.Name)
}
