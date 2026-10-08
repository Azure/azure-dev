// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/pipeline"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
)

func Test_NewPipelineConfigAction_Constructor(t *testing.T) {
	t.Parallel()
	flags := &pipelineConfigFlags{}
	console := mockinput.NewMockConsole()
	a := newPipelineConfigAction(nil, console, flags, nil, nil, nil, nil, nil, nil, nil)
	pa := a.(*pipelineConfigAction)
	require.Same(t, flags, pa.flags)
}

func Test_NewPipelineConfigCmd(t *testing.T) {
	t.Parallel()
	cmd := newPipelineConfigCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "config", cmd.Use)
}

func Test_NewPipelineConfigFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newPipelineConfigFlags(cmd, global)
	require.NotNil(t, flags)
}

type pipelineConfigTestActivator struct {
	providerNames              []string
	environmentName            string
	provisioningProviderNames  []string
	serviceTargetProviderNames []string
	requiredExtensionIds       []string
	cleanupCalled              bool
}

func (a *pipelineConfigTestActivator) EnsureProvisioningProviders(
	_ context.Context,
	providerNames []string,
	environmentName string,
) (func(), error) {
	a.providerNames = providerNames
	a.environmentName = environmentName
	return func() {
		a.cleanupCalled = true
	}, nil
}

func (a *pipelineConfigTestActivator) ExtensionsForProject(
	provisioningProviderNames []string,
	serviceTargetProviderNames []string,
	requiredExtensionIds []string,
) ([]middleware.ProjectExtension, error) {
	a.provisioningProviderNames = provisioningProviderNames
	a.serviceTargetProviderNames = serviceTargetProviderNames
	a.requiredExtensionIds = requiredExtensionIds
	return []middleware.ProjectExtension{
		{Id: "custom.provisioning", Version: "1.2.3"},
		{Id: "custom.service", Version: "4.5.6"},
	}, nil
}

type pipelineConfigTestManager struct {
	requiredExtensions []pipeline.RequiredExtension
}

func (m *pipelineConfigTestManager) CiProviderName() string {
	return "test"
}

func (m *pipelineConfigTestManager) SetParameters([]provisioning.Parameter) {}

func (m *pipelineConfigTestManager) SetRequiredExtensions(extensions []pipeline.RequiredExtension) error {
	m.requiredExtensions = extensions
	return errors.New("stop after required extensions")
}

func (m *pipelineConfigTestManager) Configure(
	context.Context,
	string,
	*project.Infra,
) (*pipeline.PipelineConfigResult, error) {
	panic("Configure should not be called")
}

func Test_PipelineConfigAction_ActivatesProvidersAndForwardsExtensions(t *testing.T) {
	t.Parallel()

	activator := &pipelineConfigTestActivator{}
	manager := &pipelineConfigTestManager{}
	version := "1.0.0"
	projectConfig := &project.ProjectConfig{
		Name: "test-project",
		Layers: project.LayerConfigs{
			{
				Name: "custom-layer",
				Infra: []provisioning.Options{
					{Provider: provisioning.ProviderKind("custom.provider")},
				},
			},
		},
		Services: map[string]*project.ServiceConfig{
			"custom-service": {Host: project.ServiceTargetKind("custom.service")},
		},
		RequiredVersions: &project.RequiredVersions{
			Extensions: map[string]*string{"explicit.extension": &version},
		},
	}
	action := &pipelineConfigAction{
		flags:              &pipelineConfigFlags{},
		manager:            manager,
		extensionActivator: activator,
		env:                environment.NewWithValues("test-env", nil),
		console:            mockinput.NewMockConsole(),
		projectConfig:      projectConfig,
		importManager:      project.NewImportManager(nil),
	}

	_, err := action.Run(t.Context())

	require.EqualError(t, err, "configuring required pipeline extensions: stop after required extensions")
	assert.Equal(t, []string{"custom.provider"}, activator.providerNames)
	assert.Equal(t, "test-env", activator.environmentName)
	assert.Equal(t, []string{"custom.provider"}, activator.provisioningProviderNames)
	assert.Equal(t, []string{"custom.service"}, activator.serviceTargetProviderNames)
	assert.Equal(t, []string{"explicit.extension"}, activator.requiredExtensionIds)
	assert.Equal(t, []pipeline.RequiredExtension{
		{Id: "custom.provisioning", Version: "1.2.3"},
		{Id: "custom.service", Version: "4.5.6"},
	}, manager.requiredExtensions)
	assert.True(t, activator.cleanupCalled)
}
