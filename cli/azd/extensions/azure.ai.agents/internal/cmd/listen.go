// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/braydonk/yaml"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/structpb"
)

func newListenCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "listen",
		Short:  "Starts the extension and listens for events.",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create a new context that includes the AZD access token.
			ctx := azdext.WithAccessToken(cmd.Context())

			// Create a new AZD client.
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			projectParser := &project.FoundryParser{AzdClient: azdClient}
			// IMPORTANT: service target name here must match the name used in the extension manifest.
			host := azdext.NewExtensionHost(azdClient).
				WithServiceTarget(AiAgentHost, func() azdext.ServiceTargetProvider {
					return project.NewAgentServiceTargetProvider(azdClient)
				}).
				WithProjectEventHandler("preprovision", func(ctx context.Context, args *azdext.ProjectEventArgs) error {
					return preprovisionHandler(ctx, azdClient, projectParser, args)
				}).
				WithProjectEventHandler("predeploy", func(ctx context.Context, args *azdext.ProjectEventArgs) error {
					return predeployHandler(ctx, azdClient, projectParser, args)
				}).
				WithProjectEventHandler("postdeploy", projectParser.CoboPostDeploy)

			// Start listening for events
			// This is a blocking call and will not return until the server connection is closed.
			if err := host.Run(ctx); err != nil {
				return fmt.Errorf("failed to run extension: %w", err)
			}

			return nil
		},
	}
}

func preprovisionHandler(ctx context.Context, azdClient *azdext.AzdClient, projectParser *project.FoundryParser, args *azdext.ProjectEventArgs) error {
	if err := projectParser.SetIdentity(ctx, args); err != nil {
		return fmt.Errorf("failed to set identity: %w", err)
	}

	for _, svc := range args.Project.Services {
		switch svc.Host {
		case AiAgentHost:
			if err := populateContainerSettings(ctx, azdClient, svc); err != nil {
				return fmt.Errorf("failed to populate container settings for service %q: %w", svc.Name, err)
			}
			if err := envUpdate(ctx, azdClient, args.Project, svc); err != nil {
				return fmt.Errorf("failed to update environment for service %q: %w", svc.Name, err)
			}
		case ContainerAppHost:
			if err := containerAgentHandling(ctx, azdClient, args.Project, svc); err != nil {
				return fmt.Errorf("failed to handle container agent for service %q: %w", svc.Name, err)
			}
		}
	}

	return nil
}

func predeployHandler(ctx context.Context, azdClient *azdext.AzdClient, projectParser *project.FoundryParser, args *azdext.ProjectEventArgs) error {
	if err := projectParser.SetIdentity(ctx, args); err != nil {
		return fmt.Errorf("failed to set identity: %w", err)
	}

	for _, svc := range args.Project.Services {
		switch svc.Host {
		case AiAgentHost:
			if err := populateContainerSettings(ctx, azdClient, svc); err != nil {
				return fmt.Errorf("failed to populate container settings for service %q: %w", svc.Name, err)
			}
			if err := envUpdate(ctx, azdClient, args.Project, svc); err != nil {
				return fmt.Errorf("failed to update environment for service %q: %w", svc.Name, err)
			}
		}
	}

	return nil
}

func envUpdate(ctx context.Context, azdClient *azdext.AzdClient, azdProject *azdext.ProjectConfig, svc *azdext.ServiceConfig) error {

	var foundryAgentConfig *project.ServiceTargetAgentConfig

	if err := project.UnmarshalStruct(svc.Config, &foundryAgentConfig); err != nil {
		return fmt.Errorf("failed to parse foundry agent config: %w", err)
	}

	currentEnvResponse, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return err
	}

	if err := kindEnvUpdate(ctx, azdClient, azdProject, svc, currentEnvResponse.Environment.Name); err != nil {
		return err
	}

	if len(foundryAgentConfig.Deployments) > 0 {
		if err := deploymentEnvUpdate(ctx, foundryAgentConfig.Deployments, azdClient, currentEnvResponse.Environment.Name); err != nil {
			return err
		}
	}

	if len(foundryAgentConfig.Resources) > 0 {
		if err := resourcesEnvUpdate(ctx, foundryAgentConfig.Resources, azdClient, currentEnvResponse.Environment.Name); err != nil {
			return err
		}
	}

	return nil
}

func kindEnvUpdate(ctx context.Context, azdClient *azdext.AzdClient, project *azdext.ProjectConfig, svc *azdext.ServiceConfig, envName string) error {
	servicePath := svc.RelativePath
	fullPath := filepath.Join(project.Path, servicePath)
	agentYamlPath := filepath.Join(fullPath, "agent.yaml")

	data, err := os.ReadFile(agentYamlPath)
	if err != nil {
		return fmt.Errorf("failed to read YAML file: %w", err)
	}

	err = agent_yaml.ValidateAgentDefinition(data)
	if err != nil {
		return fmt.Errorf("agent.yaml is not valid: %w", err)
	}

	var genericTemplate map[string]interface{}
	if err := yaml.Unmarshal(data, &genericTemplate); err != nil {
		return fmt.Errorf("YAML content is not valid: %w", err)
	}

	kind, ok := genericTemplate["kind"].(string)
	if !ok {
		return fmt.Errorf("kind field is not a valid string")
	}

	switch kind {
	case string(agent_yaml.AgentKindHosted):
		if err := setEnvVar(ctx, azdClient, envName, "ENABLE_HOSTED_AGENTS", "true"); err != nil {
			return err
		}
	}

	return nil
}

func deploymentEnvUpdate(ctx context.Context, deployments []project.Deployment, azdClient *azdext.AzdClient, envName string) error {
	deploymentsJson, err := json.Marshal(deployments)
	if err != nil {
		return fmt.Errorf("failed to marshal deployment details to JSON: %w", err)
	}

	// Escape backslashes and double quotes for environment variable
	jsonString := string(deploymentsJson)
	escapedJsonString := strings.ReplaceAll(jsonString, "\\", "\\\\")
	escapedJsonString = strings.ReplaceAll(escapedJsonString, "\"", "\\\"")

	return setEnvVar(ctx, azdClient, envName, "AI_PROJECT_DEPLOYMENTS", escapedJsonString)
}

func resourcesEnvUpdate(ctx context.Context, resources []project.Resource, azdClient *azdext.AzdClient, envName string) error {
	resourcesJson, err := json.Marshal(resources)
	if err != nil {
		return fmt.Errorf("failed to marshal resource details to JSON: %w", err)
	}

	// Escape backslashes and double quotes for environment variable
	jsonString := string(resourcesJson)
	escapedJsonString := strings.ReplaceAll(jsonString, "\\", "\\\\")
	escapedJsonString = strings.ReplaceAll(escapedJsonString, "\"", "\\\"")

	return setEnvVar(ctx, azdClient, envName, "AI_PROJECT_DEPENDENT_RESOURCES", escapedJsonString)
}

func containerAgentHandling(ctx context.Context, azdClient *azdext.AzdClient, project *azdext.ProjectConfig, svc *azdext.ServiceConfig) error {
	servicePath := svc.RelativePath
	fullPath := filepath.Join(project.Path, servicePath)
	agentYamlPath := filepath.Join(fullPath, "agent.yaml")

	data, err := os.ReadFile(agentYamlPath)
	if err != nil {
		return nil
	}

	var agentDef agent_yaml.AgentDefinition
	if err := yaml.Unmarshal(data, &agentDef); err != nil {
		return fmt.Errorf("YAML content is not valid: %w", err)
	}

	// If there is an agent.yaml in the project, and it can be properly parsed into an agent definition, add the env var to enable container agents
	currentEnvResponse, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return err
	}

	if err := setEnvVar(ctx, azdClient, currentEnvResponse.Environment.Name, "ENABLE_CONTAINER_AGENTS", "true"); err != nil {
		return err
	}

	return nil
}

func setEnvVar(ctx context.Context, azdClient *azdext.AzdClient, envName string, key string, value string) error {
	_, err := azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: envName,
		Key:     key,
		Value:   value,
	})
	if err != nil {
		return fmt.Errorf("failed to set environment variable %s=%s: %w", key, value, err)
	}

	fmt.Printf("Set environment variable: %s=%s\n", key, value)
	return nil
}

func populateContainerSettings(ctx context.Context, azdClient *azdext.AzdClient, svc *azdext.ServiceConfig) error {
	var foundryAgentConfig *project.ServiceTargetAgentConfig
	if err := project.UnmarshalStruct(svc.Config, &foundryAgentConfig); err != nil {
		return fmt.Errorf("failed to parse foundry agent config: %w", err)
	}

	containerSettings := foundryAgentConfig.Container
	if containerSettings == nil {
		containerSettings = &project.ContainerSettings{}
	}

	// Default values
	defaultMemory := project.DefaultMemory
	defaultCpu := project.DefaultCpu
	defaultMinReplicas := fmt.Sprintf("%d", project.DefaultMinReplicas)
	defaultMaxReplicas := fmt.Sprintf("%d", project.DefaultMaxReplicas)

	// Initialize result with existing values
	result := &project.ContainerSettings{}

	// Check and populate Resources
	if containerSettings.Resources == nil {
		result.Resources = &project.ResourceSettings{}
	} else {
		result.Resources = &project.ResourceSettings{
			Memory: containerSettings.Resources.Memory,
			Cpu:    containerSettings.Resources.Cpu,
		}
	}

	// Check and populate Scale
	if containerSettings.Scale == nil {
		result.Scale = &project.ScaleSettings{}
	} else {
		result.Scale = &project.ScaleSettings{
			MinReplicas: containerSettings.Scale.MinReplicas,
			MaxReplicas: containerSettings.Scale.MaxReplicas,
		}
	}

	// Prompt for memory allocation only if not set or empty
	if result.Resources.Memory == "" {
		memoryResp, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
			Options: &azdext.PromptOptions{
				Message:      "Enter desired container memory allocation (e.g., '1Gi', '512Mi'):",
				DefaultValue: defaultMemory,
			},
		})
		if err != nil {
			return fmt.Errorf("prompting for memory allocation: %w", err)
		}
		result.Resources.Memory = memoryResp.Value
	}

	// Prompt for CPU allocation only if not set or empty
	if result.Resources.Cpu == "" {
		cpuResp, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
			Options: &azdext.PromptOptions{
				Message:      "Enter desired container CPU allocation (e.g., '1', '500m'):",
				DefaultValue: defaultCpu,
			},
		})
		if err != nil {
			return fmt.Errorf("prompting for CPU allocation: %w", err)
		}
		result.Resources.Cpu = cpuResp.Value
	}

	// Prompt for minimum replicas only if not set (0 means not set for int)
	if result.Scale.MinReplicas == 0 {
		minReplicasResp, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
			Options: &azdext.PromptOptions{
				Message:      "Enter desired container minimum number of replicas:",
				DefaultValue: defaultMinReplicas,
			},
		})
		if err != nil {
			return fmt.Errorf("prompting for minimum replicas: %w", err)
		}

		minReplicas, err := strconv.Atoi(minReplicasResp.Value)
		if err != nil {
			return fmt.Errorf("invalid minimum replicas value: %w", err)
		}
		result.Scale.MinReplicas = minReplicas
	}

	// Prompt for maximum replicas only if not set (0 means not set for int)
	if result.Scale.MaxReplicas == 0 {
		maxReplicasResp, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
			Options: &azdext.PromptOptions{
				Message:      "Enter desired container maximum number of replicas:",
				DefaultValue: defaultMaxReplicas,
			},
		})
		if err != nil {
			return fmt.Errorf("prompting for maximum replicas: %w", err)
		}

		maxReplicas, err := strconv.Atoi(maxReplicasResp.Value)
		if err != nil {
			return fmt.Errorf("invalid maximum replicas value: %w", err)
		}
		result.Scale.MaxReplicas = maxReplicas
	}

	// Validate that max replicas >= min replicas
	if result.Scale.MaxReplicas < result.Scale.MinReplicas {
		return fmt.Errorf("maximum replicas (%d) must be greater than or equal to minimum replicas (%d)", result.Scale.MaxReplicas, result.Scale.MinReplicas)
	}

	// Update the container settings in the existing config
	foundryAgentConfig.Container = result

	// Marshal the complete updated agent config back to the service config
	var agentConfigStruct *structpb.Struct
	var err error
	if agentConfigStruct, err = project.MarshalStruct(foundryAgentConfig); err != nil {
		return fmt.Errorf("failed to marshal agent config: %w", err)
	}

	svc.Config = agentConfigStruct

	// Need to add the service config back to the project for use further down the pipeline
	req := &azdext.AddServiceRequest{Service: svc}

	if _, err := azdClient.Project().AddService(ctx, req); err != nil {
		return fmt.Errorf("adding agent service to project: %w", err)
	}

	return nil
}
