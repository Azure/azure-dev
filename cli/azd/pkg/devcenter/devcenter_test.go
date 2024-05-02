package devcenter

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/devcentersdk"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_ParseConfig(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		partialConfig := map[string]any{
			"name":                  "DEVCENTER_NAME",
			"project":               "PROJECT",
			"environmentDefinition": "ENVIRONMENT_DEFINITION",
		}

		config, err := ParseConfig(partialConfig)
		require.NoError(t, err)
		require.Equal(t, "DEVCENTER_NAME", config.Name)
		require.Equal(t, "PROJECT", config.Project)
		require.Equal(t, "ENVIRONMENT_DEFINITION", config.EnvironmentDefinition)
	})

	t.Run("Failure", func(t *testing.T) {
		partialConfig := "not a map"
		config, err := ParseConfig(partialConfig)
		require.Error(t, err)
		require.Nil(t, config)
	})
}

func Test_MergeConfigs(t *testing.T) {
	t.Run("MergeMissingValues", func(t *testing.T) {
		baseConfig := &Config{
			Name:                  "DEVCENTER_NAME",
			Project:               "PROJECT",
			EnvironmentDefinition: "ENVIRONMENT_DEFINITION",
		}

		overrideConfig := &Config{
			EnvironmentType: "Dev",
		}

		mergedConfig := MergeConfigs(baseConfig, overrideConfig)

		require.Equal(t, "DEVCENTER_NAME", mergedConfig.Name)
		require.Equal(t, "PROJECT", mergedConfig.Project)
		require.Equal(t, "ENVIRONMENT_DEFINITION", mergedConfig.EnvironmentDefinition)
		require.Equal(t, "Dev", mergedConfig.EnvironmentType)
	})

	t.Run("OverrideEmpty", func(t *testing.T) {
		baseConfig := &Config{}

		overrideConfig := &Config{
			Name:                  "OVERRIDE",
			Project:               "OVERRIDE",
			EnvironmentDefinition: "OVERRIDE",
			Catalog:               "OVERRIDE",
			EnvironmentType:       "OVERRIDE",
		}

		mergedConfig := MergeConfigs(baseConfig, overrideConfig)

		require.Equal(t, "OVERRIDE", mergedConfig.Name)
		require.Equal(t, "OVERRIDE", mergedConfig.Project)
		require.Equal(t, "OVERRIDE", mergedConfig.EnvironmentDefinition)
		require.Equal(t, "OVERRIDE", mergedConfig.Catalog)
		require.Equal(t, "OVERRIDE", mergedConfig.EnvironmentType)
	})

	// The base config is a full configuration so there isn't anything to override
	t.Run("NoOverride", func(t *testing.T) {
		baseConfig := &Config{
			Name:                  "DEVCENTER_NAME",
			Project:               "PROJECT",
			EnvironmentDefinition: "ENVIRONMENT_DEFINITION",
			Catalog:               "CATALOG",
			EnvironmentType:       "ENVIRONMENT_TYPE",
		}

		overrideConfig := &Config{
			Name:                  "OVERRIDE",
			Project:               "OVERRIDE",
			EnvironmentDefinition: "OVERRIDE",
			Catalog:               "OVERRIDE",
			EnvironmentType:       "OVERRIDE",
		}

		mergedConfig := MergeConfigs(baseConfig, overrideConfig)

		require.Equal(t, "DEVCENTER_NAME", mergedConfig.Name)
		require.Equal(t, "PROJECT", mergedConfig.Project)
		require.Equal(t, "ENVIRONMENT_DEFINITION", mergedConfig.EnvironmentDefinition)
		require.Equal(t, "CATALOG", mergedConfig.Catalog)
		require.Equal(t, "ENVIRONMENT_TYPE", mergedConfig.EnvironmentType)
	})
}

type mockDevCenterManager struct {
	mock.Mock
}

func (m *mockDevCenterManager) WritableProjects(ctx context.Context) ([]*devcentersdk.Project, error) {
	args := m.Called(ctx)
	return args.Get(0).([]*devcentersdk.Project), args.Error(1)
}

func (m *mockDevCenterManager) WritableProjectsWithFilter(
	ctx context.Context,
	devCenterFilter DevCenterFilterPredicate,
	projectFilter ProjectFilterPredicate,
) ([]*devcentersdk.Project, error) {
	args := m.Called(ctx, devCenterFilter, projectFilter)
	return args.Get(0).([]*devcentersdk.Project), args.Error(1)
}

func (m *mockDevCenterManager) Deployment(
	ctx context.Context,
	config *Config,
	env *devcentersdk.Environment,
	filter DeploymentFilterPredicate,
) (infra.Deployment, error) {
	args := m.Called(ctx, config, env, filter)
	return args.Get(0).(infra.Deployment), args.Error(1)
}

func (m *mockDevCenterManager) LatestArmDeployment(
	ctx context.Context,
	config *Config,
	env *devcentersdk.Environment,
	filter DeploymentFilterPredicate,
) (*armresources.DeploymentExtended, error) {
	args := m.Called(ctx, config, env, filter)
	return args.Get(0).(*armresources.DeploymentExtended), args.Error(1)
}

func (m *mockDevCenterManager) Outputs(
	ctx context.Context,
	config *Config,
	env *devcentersdk.Environment,
) (map[string]provisioning.OutputParameter, error) {
	args := m.Called(ctx, config, env)

	outputs, ok := args.Get(0).(map[string]provisioning.OutputParameter)
	if ok {
		return outputs, args.Error(1)
	}

	return nil, args.Error(1)
}

var mockDevCenterList []*devcentersdk.DevCenter = []*devcentersdk.DevCenter{
	{
		//nolint:lll
		Id:             "/subscriptions/SUBSCRIPTION_01/resourceGroups/RESOURCE_GROUP_01/providers/Microsoft.DevCenter/devcenters/DEV_CENTER_01",
		SubscriptionId: "SUBSCRIPTION_01",
		ResourceGroup:  "RESOURCE_GROUP_01",
		Name:           "DEV_CENTER_01",
		ServiceUri:     "https://DEV_CENTER_01.eastus2.devcenter.azure.com",
	},
	{
		//nolint:lll
		Id:             "/subscriptions/SUBSCRIPTION_02/resourceGroups/RESOURCE_GROUP_02/providers/Microsoft.DevCenter/devcenters/DEV_CENTER_02",
		SubscriptionId: "SUBSCRIPTION_02",
		ResourceGroup:  "RESOURCE_GROUP_02",
		Name:           "DEV_CENTER_02",
		ServiceUri:     "https://DEV_CENTER_02.eastus2.devcenter.azure.com",
	},
}

var mockProjects []*devcentersdk.Project = []*devcentersdk.Project{
	{
		Id:        "/projects/Project1",
		Name:      "Project1",
		DevCenter: mockDevCenterList[0],
	},
	{
		Id:        "/projects/Project2",
		Name:      "Project2",
		DevCenter: mockDevCenterList[0],
	},
	{
		Id:        "/projects/Project3",
		Name:      "Project3",
		DevCenter: mockDevCenterList[1],
	},
	{
		Id:        "/projects/Project4",
		Name:      "Project4",
		DevCenter: mockDevCenterList[1],
	},
}

var mockEnvironmentTypes []*devcentersdk.EnvironmentType = []*devcentersdk.EnvironmentType{
	{
		Name:               "EnvType_01",
		DeploymentTargetId: "/subscriptions/SUBSCRIPTION_01/",
		Status:             "Enabled",
	},
	{
		Name:               "EnvType_02",
		DeploymentTargetId: "/subscriptions/SUBSCRIPTION_01/",
		Status:             "Enabled",
	},
	{
		Name:               "EnvType_03",
		DeploymentTargetId: "/subscriptions/SUBSCRIPTION_02/",
		Status:             "Enabled",
	},
	{
		Name:               "EnvType_04",
		DeploymentTargetId: "/subscriptions/SUBSCRIPTION_02/",
		Status:             "Enabled",
	},
}

var mockEnvDefinitions []*devcentersdk.EnvironmentDefinition = []*devcentersdk.EnvironmentDefinition{
	{
		Id:           "/projects/Project1/catalogs/SampleCatalog/environmentDefinitions/WebApp",
		Name:         "EnvDefinition_01",
		CatalogName:  "SampleCatalog",
		Description:  "Description of WebApp",
		TemplatePath: "azuredeploy.json",
		Parameters: []devcentersdk.Parameter{
			{
				Id:      "repoUrl",
				Name:    "repoUrl",
				Type:    devcentersdk.ParameterTypeString,
				Default: "https://github.com/Azure-Samples/todo-nodejs-mongo",
			},
		},
	},
	{
		Id:           "/projects/Project1/catalogs/SampleCatalog/environmentDefinitions/EnvDefinition_02",
		Name:         "EnvDefinition_02",
		CatalogName:  "SampleCatalog",
		Description:  "Description of EnvDefinition_02",
		TemplatePath: "azuredeploy.json",
		Parameters: []devcentersdk.Parameter{
			{
				Id:      "repoUrl",
				Name:    "repoUrl",
				Type:    devcentersdk.ParameterTypeString,
				Default: "https://github.com/Azure-Samples/todo-nodejs-mongo-aca",
			},
		},
	},
	{
		Id:           "/projects/Project1/catalogs/SampleCatalog/environmentDefinitions/EnvDefinition_03",
		Name:         "EnvDefinition_03",
		CatalogName:  "SampleCatalog",
		Description:  "Description of EnvDefinition_03",
		TemplatePath: "azuredeploy.json",
		Parameters: []devcentersdk.Parameter{
			{
				Id:      "repoUrl",
				Name:    "repoUrl",
				Type:    devcentersdk.ParameterTypeString,
				Default: "https://github.com/Azure-Samples/todo-nodejs-mongo-swa-func",
			},
		},
	},
	{
		Id:           "/projects/Project1/catalogs/SampleCatalog/environmentDefinitions/EnvDefinition_04",
		Name:         "EnvDefinition_04",
		CatalogName:  "SampleCatalog",
		Description:  "Description of EnvDefinition_04",
		TemplatePath: "azuredeploy.json",
		Parameters: []devcentersdk.Parameter{
			{
				Id:      "repoUrl",
				Name:    "repoUrl",
				Type:    devcentersdk.ParameterTypeString,
				Default: "https://github.com/Azure-Samples/todo-nodejs-mongo-swa-func",
			},
			{
				Id:   "param01",
				Name: "Param 1",
				Type: devcentersdk.ParameterTypeString,
			},
			{
				Id:   "param02",
				Name: "Param 2",
				Type: devcentersdk.ParameterTypeString,
			},
		},
	},
}
