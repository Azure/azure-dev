package project

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/machinelearning/armmachinelearning/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/ai"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type machineLearningEndpointTarget struct {
	env        *environment.Environment
	envManager environment.Manager
	aiHelper   *AiHelper
}

func NewMachineLearningEndpointTarget(
	env *environment.Environment,
	envManager environment.Manager,
	aiHelper *AiHelper,
) ServiceTarget {
	return &machineLearningEndpointTarget{
		env:        env,
		envManager: envManager,
		aiHelper:   aiHelper,
	}
}

type EndpointDeployment struct {
	Environment *armmachinelearning.EnvironmentVersion
	Model       *armmachinelearning.ModelVersion
	Flow        *ai.Flow
	Deployment  *armmachinelearning.OnlineDeployment
}

func (m *machineLearningEndpointTarget) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	// Implement the Initialize method here.
	return nil
}

func (m *machineLearningEndpointTarget) RequiredExternalTools(ctx context.Context) []tools.ExternalTool {
	// Implement the RequiredExternalTools method here.
	return nil
}

func (m *machineLearningEndpointTarget) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	frameworkPackageOutput *ServicePackageResult,
) *async.TaskWithProgress[*ServicePackageResult, ServiceProgress] {
	// Implement the Package method here.
	return async.RunTaskWithProgress(func(task *async.TaskContextWithProgress[*ServicePackageResult, ServiceProgress]) {
		task.SetResult(&ServicePackageResult{})
	})
}

func (m *machineLearningEndpointTarget) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	servicePackage *ServicePackageResult,
	targetResource *environment.TargetResource,
) *async.TaskWithProgress[*ServiceDeployResult, ServiceProgress] {
	// Implement the Deploy method here.
	return async.RunTaskWithProgress(func(task *async.TaskContextWithProgress[*ServiceDeployResult, ServiceProgress]) {
		endpointConfig, err := ai.ParseConfig[ai.EndpointDeploymentConfig](serviceConfig.Config)
		if err != nil {
			task.SetError(err)
			return
		}

		workspaceScope, err := m.getWorkspaceScope(serviceConfig, targetResource)
		if err != nil {
			task.SetError(err)
			return
		}

		deployResult := &EndpointDeployment{}

		// Ensure the workspace is valid
		if err := m.aiHelper.EnsureWorkspace(ctx, workspaceScope); err != nil {
			task.SetError(
				fmt.Errorf("workspace '%s' was not found within subscription '%s' and resource group '%s': %w",
					workspaceScope.Workspace(),
					workspaceScope.SubscriptionId(),
					workspaceScope.ResourceGroup(),
					err,
				),
			)
			return
		}

		// Deploy flow
		if endpointConfig.Flow != nil {
			flowName, err := endpointConfig.Flow.Name.Envsubst(m.env.Getenv)
			if err != nil {
				task.SetError(err)
				return
			}

			task.SetProgress(NewServiceProgress(fmt.Sprintf("Deploying flow '%s'", flowName)))
			flow, err := m.aiHelper.CreateOrUpdateFlow(ctx, workspaceScope, serviceConfig, endpointConfig.Flow)
			if err != nil {
				task.SetError(err)
				return
			}

			deployResult.Flow = flow
		}

		// Deploy environment
		if endpointConfig.Environment != nil {
			envName, err := endpointConfig.Environment.Name.Envsubst(m.env.Getenv)
			if err != nil {
				task.SetError(err)
				return
			}

			task.SetProgress(NewServiceProgress(fmt.Sprintf("Configuring environment '%s'", envName)))
			envVersion, err := m.aiHelper.CreateEnvironmentVersion(
				ctx,
				workspaceScope,
				serviceConfig,
				endpointConfig.Environment,
			)
			if err != nil {
				task.SetError(err)
				return
			}

			deployResult.Environment = envVersion
		}

		// Deploy models
		if endpointConfig.Model != nil {
			modelName, err := endpointConfig.Model.Name.Envsubst(m.env.Getenv)
			if err != nil {
				task.SetError(err)
				return
			}

			task.SetProgress(NewServiceProgress(fmt.Sprintf("Configuring model '%s'", modelName)))
			modelVersion, err := m.aiHelper.CreateModelVersion(ctx, workspaceScope, serviceConfig, endpointConfig.Model)
			if err != nil {
				task.SetError(err)
				return
			}

			deployResult.Model = modelVersion
		}

		// Deploy endpoints
		if endpointConfig.Deployment != nil {
			endpointName := filepath.Base(targetResource.ResourceName())

			task.SetProgress(NewServiceProgress(fmt.Sprintf("Deploying endpoint '%s'", endpointName)))
			onlineDeployment, err := m.aiHelper.DeployToEndpoint(
				ctx,
				workspaceScope,
				serviceConfig,
				endpointName,
				endpointConfig,
			)
			if err != nil {
				task.SetError(err)
				return
			}

			deployResult.Deployment = onlineDeployment
		}

		endpoints, err := m.Endpoints(ctx, serviceConfig, targetResource)
		if err != nil {
			task.SetError(err)
			return
		}

		if err := m.envManager.Save(ctx, m.env); err != nil {
			task.SetError(fmt.Errorf("failed saving environment: %w", err))
			return
		}

		task.SetResult(&ServiceDeployResult{
			Details:   deployResult,
			Package:   servicePackage,
			Endpoints: endpoints,
		})
	})
}

func (m *machineLearningEndpointTarget) Endpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) ([]string, error) {
	workspaceScope, err := m.getWorkspaceScope(serviceConfig, targetResource)
	if err != nil {
		return nil, err
	}

	endpointName := filepath.Base(targetResource.ResourceName())
	onlineEndpoint, err := m.aiHelper.GetEndpoint(ctx, workspaceScope, endpointName)
	if err != nil {
		return nil, err
	}

	return []string{
		fmt.Sprintf("Scoring: %s", *onlineEndpoint.Properties.ScoringURI),
		fmt.Sprintf("Swagger: %s", *onlineEndpoint.Properties.SwaggerURI),
	}, nil
}

func (m *machineLearningEndpointTarget) getWorkspaceScope(
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) (*ai.Scope, error) {
	endpointConfig, err := ai.ParseConfig[ai.EndpointDeploymentConfig](serviceConfig.Config)
	if err != nil {
		return nil, err
	}

	workspaceName, err := endpointConfig.Workspace.Envsubst(m.env.Getenv)
	if err != nil {
		return nil, err
	}

	// Workspace name can come from the following:
	// 1. The workspace field in the endpoint service config
	// 2. The AZUREML_WORKSPACE_NAME environment variable
	if workspaceName == "" {
		workspaceName = m.env.Getenv("AZUREML_WORKSPACE_NAME")
	}

	if workspaceName == "" {
		return nil, fmt.Errorf("workspace name is required")
	}

	return ai.NewScope(
		m.env.GetSubscriptionId(),
		targetResource.ResourceGroupName(),
		workspaceName,
	), nil
}
