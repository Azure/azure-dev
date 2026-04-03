// Copyright (c) Microsoft Corporation. Licensed under the MIT License.
// Tests for service_target_springapp.go, service_target_ai_endpoint.go,
// service_target_dotnet_containerapp.go constructors and simple methods,
// and service_target_containerapp.go RequiredExternalTools.
package project

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Spring App target (all methods return errSpringAppDeprecated) ---

func Test_NewSpringAppTarget_Coverage3(t *testing.T) {
	env := environment.NewWithValues("test", nil)
	target := NewSpringAppTarget(env, nil)
	require.NotNil(t, target)
}

func Test_springAppTarget_RequiredExternalTools_Coverage3(t *testing.T) {
	target := NewSpringAppTarget(environment.NewWithValues("test", nil), nil)
	tools := target.RequiredExternalTools(t.Context(), &ServiceConfig{})
	assert.Empty(t, tools)
}

func Test_springAppTarget_Initialize_Coverage3(t *testing.T) {
	target := NewSpringAppTarget(environment.NewWithValues("test", nil), nil)
	err := target.Initialize(t.Context(), &ServiceConfig{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Azure Spring Apps is no longer supported")
}

func Test_springAppTarget_Package_Coverage3(t *testing.T) {
	target := NewSpringAppTarget(environment.NewWithValues("test", nil), nil)
	result, err := target.Package(t.Context(), &ServiceConfig{}, NewServiceContext(), nil)
	assert.Nil(t, result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Azure Spring Apps is no longer supported")
}

func Test_springAppTarget_Publish_Coverage3(t *testing.T) {
	target := NewSpringAppTarget(environment.NewWithValues("test", nil), nil)
	result, err := target.Publish(t.Context(), &ServiceConfig{}, NewServiceContext(), nil, nil, nil)
	assert.Nil(t, result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Azure Spring Apps is no longer supported")
}

func Test_springAppTarget_Deploy_Coverage3(t *testing.T) {
	target := NewSpringAppTarget(environment.NewWithValues("test", nil), nil)
	result, err := target.Deploy(t.Context(), &ServiceConfig{}, NewServiceContext(), nil, nil)
	assert.Nil(t, result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Azure Spring Apps is no longer supported")
}

func Test_springAppTarget_Endpoints_Coverage3(t *testing.T) {
	target := NewSpringAppTarget(environment.NewWithValues("test", nil), nil)
	endpoints, err := target.Endpoints(t.Context(), &ServiceConfig{}, nil)
	assert.Nil(t, endpoints)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Azure Spring Apps is no longer supported")
}

// --- AI Endpoint target ---

func Test_aiEndpointTarget_Initialize_Coverage3(t *testing.T) {
	target := &aiEndpointTarget{}
	err := target.Initialize(t.Context(), &ServiceConfig{})
	require.NoError(t, err)
}

func Test_aiEndpointTarget_Package_Coverage3(t *testing.T) {
	target := &aiEndpointTarget{}
	result, err := target.Package(t.Context(), &ServiceConfig{}, NewServiceContext(), nil)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func Test_aiEndpointTarget_Publish_Coverage3(t *testing.T) {
	target := &aiEndpointTarget{}
	result, err := target.Publish(t.Context(), &ServiceConfig{}, NewServiceContext(), nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
}

// --- DotNet Container App target (simple methods) ---

func Test_NewDotNetContainerAppTarget_Coverage3(t *testing.T) {
	cli := dotnet.NewCli(exec.NewCommandRunner(nil))
	target := NewDotNetContainerAppTarget(nil, nil, nil, nil, cli, nil, nil, nil, nil, nil, nil, nil)
	require.NotNil(t, target)
}

func Test_dotnetContainerAppTarget_RequiredExternalTools_Coverage3(t *testing.T) {
	cli := dotnet.NewCli(exec.NewCommandRunner(nil))
	target := NewDotNetContainerAppTarget(nil, nil, nil, nil, cli, nil, nil, nil, nil, nil, nil, nil)
	tools := target.RequiredExternalTools(t.Context(), &ServiceConfig{})
	require.Len(t, tools, 1)
	assert.Equal(t, cli, tools[0])
}

func Test_dotnetContainerAppTarget_Initialize_Coverage3(t *testing.T) {
	target := NewDotNetContainerAppTarget(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	err := target.Initialize(t.Context(), &ServiceConfig{})
	require.NoError(t, err)
}
