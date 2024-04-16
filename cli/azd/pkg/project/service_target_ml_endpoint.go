package project

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/machinelearning/armmachinelearning/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/ai"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
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
	return serviceConfig.Project.AddHandler("postprovision", func(ctx context.Context, args ProjectLifecycleEventArgs) error {
		// TODO: Move target resource to here.
		projectName := m.env.Getenv("AZUREML_AI_PROJECT_NAME")
		aiStudioLink := ai.AzureAiStudioLink(
			m.env.GetTenantId(),
			m.env.GetSubscriptionId(),
			m.env.Getenv("AZUREML_RESOURCE_GROUP"),
			projectName,
		)

		err := m.env.Config.Set("provision.links.aiStudio", &output.Link{
			Name:        "Azure AI Studio",
			Description: fmt.Sprintf("View the %s project in Azure AI studio:", projectName),
			Url:         aiStudioLink,
		})
		if err != nil {
			return fmt.Errorf("failed setting aiStudio link: %w", err)
		}

		if err := m.envManager.Save(ctx, m.env); err != nil {
			return fmt.Errorf("failed saving environment: %w", err)
		}

		return nil
	})
}

func (m *machineLearningEndpointTarget) RequiredExternalTools(ctx context.Context) []tools.ExternalTool {
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

		// Initialize the AI project that will be used for the python bridge
		task.SetProgress(NewServiceProgress("Initializing AI project"))
		if err := m.aiHelper.Init(ctx); err != nil {
			task.SetError(fmt.Errorf("failed initializing AI project: %w", err))
			return
		}

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
			task.SetProgress(NewServiceProgress("Deploying prompt flow"))
			flow, err := m.aiHelper.CreateOrUpdateFlow(ctx, workspaceScope, serviceConfig, endpointConfig.Flow)
			if err != nil {
				task.SetError(err)
				return
			}

			deployResult.Flow = flow
		}

		// Deploy environment
		if endpointConfig.Environment != nil {
			task.SetProgress(NewServiceProgress("Configuring environment"))
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
			task.SetProgress(NewServiceProgress("Configuring model"))
			modelVersion, err := m.aiHelper.CreateModelVersion(ctx, workspaceScope, serviceConfig, endpointConfig.Model)
			if err != nil {
				task.SetError(err)
				return
			}

			deployResult.Model = modelVersion
		}

		// Deploy endpoints
		if endpointConfig.Deployment != nil {
			task.SetProgress(NewServiceProgress("Deploying to endpoint"))
			endpointName := filepath.Base(targetResource.ResourceName())
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
