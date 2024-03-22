package project

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/ai/promptflow"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type AiFlow struct {
	env      *environment.Environment
	aiHelper *AiHelper
}

func NewAiFlow(
	env *environment.Environment,
	aiHelper *AiHelper,
) ServiceTarget {
	return &AiFlow{
		env:      env,
		aiHelper: aiHelper,
	}
}

func (m *AiFlow) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	// Implement the Initialize method here.
	return nil
}

func (m *AiFlow) RequiredExternalTools(ctx context.Context) []tools.ExternalTool {
	// Implement the RequiredExternalTools method here.
	return nil
}

func (m *AiFlow) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	frameworkPackageOutput *ServicePackageResult,
) *async.TaskWithProgress[*ServicePackageResult, ServiceProgress] {
	// Implement the Package method here.
	return async.RunTaskWithProgress(func(task *async.TaskContextWithProgress[*ServicePackageResult, ServiceProgress]) {
		task.SetResult(&ServicePackageResult{})
	})
}

func (m *AiFlow) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	servicePackage *ServicePackageResult,
	targetResource *environment.TargetResource,
) *async.TaskWithProgress[*ServiceDeployResult, ServiceProgress] {
	// Implement the Deploy method here.
	return async.RunTaskWithProgress(func(task *async.TaskContextWithProgress[*ServiceDeployResult, ServiceProgress]) {
		flowConfig, err := promptflow.ParseConfig(serviceConfig.Config)
		if err != nil {
			task.SetError(err)
			return
		}

		endpoints := []string{}

		// Create Connections
		for _, connectionConfig := range flowConfig.Connections {
			connectionName, err := connectionConfig.Name.Envsubst(m.env.Getenv)
			if err != nil {
				task.SetError(err)
				return
			}

			task.SetProgress(NewServiceProgress(fmt.Sprintf("Configuring connection '%s'", connectionName)))
			_, err = m.aiHelper.CreateOrUpdateConnection(ctx, serviceConfig, targetResource, connectionConfig)
			if err != nil {
				task.SetError(err)
				return
			}
		}

		// Deploy environments
		for _, envConfig := range flowConfig.Environments {
			envWorkspace, err := envConfig.Workspace.Envsubst(m.env.Getenv)
			if err != nil {
				task.SetError(err)
				return
			}

			if envWorkspace == "" {
				envConfig.Workspace = flowConfig.Workspace
			}

			envName, err := envConfig.Name.Envsubst(m.env.Getenv)
			if err != nil {
				task.SetError(err)
				return
			}

			task.SetProgress(NewServiceProgress(fmt.Sprintf("Configuring environment '%s'", envName)))
			envVersion, err := m.aiHelper.CreateEnvironmentVersion(ctx, serviceConfig, targetResource, envConfig)
			if err != nil {
				task.SetError(err)
				return
			}

			if envVersion.Properties.Image != nil {
				endpoints = append(endpoints, *envVersion.Properties.Image)
			}
		}

		// Deploy models
		for _, modelConfig := range flowConfig.Models {
			modelWorkspace, err := modelConfig.Workspace.Envsubst(m.env.Getenv)
			if err != nil {
				task.SetError(err)
				return
			}

			if modelWorkspace == "" {
				modelConfig.Workspace = flowConfig.Workspace
			}

			modelName, err := modelConfig.Name.Envsubst(m.env.Getenv)
			if err != nil {
				task.SetError(err)
				return
			}

			task.SetProgress(NewServiceProgress(fmt.Sprintf("Configuring model '%s'", modelName)))
			modelVersion, err := m.aiHelper.CreateModelVersion(ctx, serviceConfig, targetResource, modelConfig)
			if err != nil {
				task.SetError(err)
				return
			}

			endpoints = append(endpoints, *modelVersion.Properties.ModelURI)
		}

		// Deploy endpoints
		for _, endpointConfig := range flowConfig.Endpoints {
			endpointWorkspace, err := endpointConfig.Workspace.Envsubst(m.env.Getenv)
			if err != nil {
				task.SetError(err)
				return
			}

			if endpointWorkspace == "" {
				endpointConfig.Workspace = flowConfig.Workspace
			}

			endpointName, err := endpointConfig.Name.Envsubst(m.env.Getenv)
			if err != nil {
				task.SetError(err)
				return
			}

			task.SetProgress(NewServiceProgress(fmt.Sprintf("Configuring endpoint '%s'", endpointName)))
			endpoint, err := m.aiHelper.CreateOrUpdateEndpoint(ctx, serviceConfig, targetResource, endpointConfig)
			if err != nil {
				task.SetError(err)
				return
			}

			endpoints = append(endpoints, fmt.Sprintf("Swagger: %s", *endpoint.Properties.SwaggerURI))
			endpoints = append(endpoints, fmt.Sprintf("Scoring: %s", *endpoint.Properties.ScoringURI))

			deploymentWorkspace, err := endpointConfig.Deployment.Workspace.Envsubst(m.env.Getenv)
			if err != nil {
				task.SetError(err)
				return
			}

			if deploymentWorkspace == "" {
				endpointConfig.Deployment.Workspace = flowConfig.Workspace
			}

			task.SetProgress(NewServiceProgress(fmt.Sprintf("Deploying endpoint '%s'", endpointName)))
			_, err = m.aiHelper.DeployToEndpoint(ctx, serviceConfig, targetResource, endpointConfig)
			if err != nil {
				task.SetError(err)
				return
			}
		}

		// Deploy flow
		flowName, err := flowConfig.Name.Envsubst(m.env.Getenv)
		if err != nil {
			task.SetError(err)
			return
		}

		task.SetProgress(NewServiceProgress(fmt.Sprintf("Deploying flow '%s'", flowName)))
		updatedFlow, err := m.aiHelper.CreateOrUpdateFlow(ctx, serviceConfig, targetResource, flowConfig)
		if err != nil {
			task.SetError(err)
			return
		}

		task.SetResult(&ServiceDeployResult{
			Package:   servicePackage,
			Details:   updatedFlow,
			Endpoints: endpoints,
		})
	})
}

func (m *AiFlow) Endpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) ([]string, error) {
	// Implement the Endpoints method here.
	return []string{}, nil
}
