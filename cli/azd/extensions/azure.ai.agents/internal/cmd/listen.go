// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/azure"
	"azureaiagent/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
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

			setupDebugLogging(cmd.Flags())

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
				WithProjectEventHandler("postprovision", func(ctx context.Context, args *azdext.ProjectEventArgs) error {
					return postprovisionHandler(ctx, azdClient, args)
				}).
				WithProjectEventHandler("predeploy", func(ctx context.Context, args *azdext.ProjectEventArgs) error {
					return predeployHandler(ctx, azdClient, projectParser, args)
				}).
				WithProjectEventHandler("postdeploy", func(ctx context.Context, args *azdext.ProjectEventArgs) error {
					return postdeployHandler(ctx, projectParser, args)
				})

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

func postprovisionHandler(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	args *azdext.ProjectEventArgs,
) error {
	for _, svc := range args.Project.Services {
		if svc.Host != AiAgentHost {
			continue
		}

		if err := provisionToolboxes(ctx, azdClient, svc); err != nil {
			return fmt.Errorf(
				"failed to provision toolboxes for service %q: %w",
				svc.Name, err,
			)
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

func postdeployHandler(ctx context.Context, projectParser *project.FoundryParser, args *azdext.ProjectEventArgs) error {
	if err := projectParser.CoboPostDeploy(ctx, args); err != nil {
		return err
	}

	// Ensure agent identity RBAC is configured when vnext is enabled.
	// Runs post-deploy because the platform provisions the identity during agent deployment.
	if err := projectParser.EnsureAgentIdentityRBAC(ctx); err != nil {
		return fmt.Errorf("agent identity RBAC setup failed: %w", err)
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

	if len(foundryAgentConfig.Connections) > 0 {
		if err := connectionsEnvUpdate(
			ctx, foundryAgentConfig.Connections,
			azdClient, currentEnvResponse.Environment.Name,
		); err != nil {
			return err
		}
	}

	return nil
}

func kindEnvUpdate(ctx context.Context, azdClient *azdext.AzdClient, project *azdext.ProjectConfig, svc *azdext.ServiceConfig, envName string) error {
	servicePath := svc.RelativePath
	fullPath := filepath.Join(project.Path, servicePath)
	agentYamlPath := filepath.Join(fullPath, "agent.yaml")

	//nolint:gosec // agentYamlPath is resolved from project/service paths in current workspace
	data, err := os.ReadFile(agentYamlPath)
	if err != nil {
		return fmt.Errorf("failed to read YAML file: %w", err)
	}

	err = agent_yaml.ValidateAgentDefinition(data)
	if err != nil {
		return fmt.Errorf("agent.yaml is not valid: %w", err)
	}

	var genericTemplate map[string]any
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

func connectionsEnvUpdate(
	ctx context.Context,
	connections []project.Connection,
	azdClient *azdext.AzdClient,
	envName string,
) error {
	connectionsJson, err := json.Marshal(connections)
	if err != nil {
		return fmt.Errorf("failed to marshal connection details to JSON: %w", err)
	}

	// Escape backslashes and double quotes for environment variable
	jsonString := string(connectionsJson)
	escapedJsonString := strings.ReplaceAll(jsonString, "\\", "\\\\")
	escapedJsonString = strings.ReplaceAll(escapedJsonString, "\"", "\\\"")

	return setEnvVar(ctx, azdClient, envName, "AI_PROJECT_CONNECTIONS", escapedJsonString)
}

func containerAgentHandling(ctx context.Context, azdClient *azdext.AzdClient, project *azdext.ProjectConfig, svc *azdext.ServiceConfig) error {
	servicePath := svc.RelativePath
	fullPath := filepath.Join(project.Path, servicePath)
	agentYamlPath := filepath.Join(fullPath, "agent.yaml")

	//nolint:gosec // agentYamlPath is resolved from project/service paths in current workspace
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

	// Initialize result with existing values
	result := &project.ContainerSettings{}

	// Check and populate base object
	containerSettings := foundryAgentConfig.Container
	if containerSettings == nil {
		containerSettings = &project.ContainerSettings{}
	}

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

	// Set default values if zero or empty
	if result.Resources.Memory == "" {
		result.Resources.Memory = project.DefaultMemory
	}

	if result.Resources.Cpu == "" {
		result.Resources.Cpu = project.DefaultCpu
	}

	if result.Scale.MinReplicas == 0 {
		result.Scale.MinReplicas = project.DefaultMinReplicas
	}

	if result.Scale.MaxReplicas == 0 {
		result.Scale.MaxReplicas = project.DefaultMaxReplicas
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

// provisionToolboxes creates or updates Foundry Toolsets for each toolbox
// in the service config. Called during post-provision after the project
// endpoint has been created by Bicep.
func provisionToolboxes(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	svc *azdext.ServiceConfig,
) error {
	var config *project.ServiceTargetAgentConfig
	if err := project.UnmarshalStruct(svc.Config, &config); err != nil {
		return fmt.Errorf("failed to parse service config: %w", err)
	}

	if config == nil || len(config.Toolboxes) == 0 {
		return nil
	}

	currentEnv, err := azdClient.Environment().GetCurrent(
		ctx, &azdext.EmptyRequest{},
	)
	if err != nil {
		return fmt.Errorf("failed to get current environment: %w", err)
	}

	envValue, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: currentEnv.Environment.Name,
		Key:     "AZURE_AI_PROJECT_ENDPOINT",
	})
	if err != nil || envValue.Value == "" {
		return exterrors.Dependency(
			exterrors.CodeMissingAiProjectEndpoint,
			"AZURE_AI_PROJECT_ENDPOINT is required for toolbox provisioning",
			"run 'azd provision' to create the AI project first",
		)
	}
	projectEndpoint := envValue.Value

	envValue, err = azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: currentEnv.Environment.Name,
		Key:     "AZURE_TENANT_ID",
	})
	if err != nil || envValue.Value == "" {
		return exterrors.Dependency(
			exterrors.CodeMissingAzureTenantId,
			"AZURE_TENANT_ID is required for toolbox provisioning",
			"run 'azd auth login' to authenticate",
		)
	}
	tenantId := envValue.Value

	cred, err := azidentity.NewAzureDeveloperCLICredential(
		&azidentity.AzureDeveloperCLICredentialOptions{
			TenantID:                   tenantId,
			AdditionallyAllowedTenants: []string{"*"},
		},
	)
	if err != nil {
		return exterrors.Auth(
			exterrors.CodeCredentialCreationFailed,
			fmt.Sprintf("failed to create credential: %s", err),
			"run 'azd auth login' to authenticate",
		)
	}

	toolsetsClient := azure.NewFoundryToolsetsClient(
		projectEndpoint, cred,
	)

	for _, toolbox := range config.Toolboxes {
		fmt.Fprintf(
			os.Stderr, "Provisioning toolbox: %s\n", toolbox.Name,
		)

		if err := upsertToolset(
			ctx, toolsetsClient, toolbox,
		); err != nil {
			return err
		}

		if err := registerToolboxEnvVars(
			ctx, azdClient,
			currentEnv.Environment.Name,
			projectEndpoint, toolbox.Name,
		); err != nil {
			return err
		}

		fmt.Fprintf(
			os.Stderr, "Toolbox '%s' provisioned\n", toolbox.Name,
		)
	}

	return nil
}

// upsertToolset creates a toolset, or skips if it already exists.
func upsertToolset(
	ctx context.Context,
	client *azure.FoundryToolsetsClient,
	toolbox project.Toolbox,
) error {
	createReq := &azure.CreateToolsetRequest{
		Name:        toolbox.Name,
		Description: toolbox.Description,
		Tools:       toolbox.Tools,
	}

	_, err := client.CreateToolset(ctx, createReq)
	if err == nil {
		return nil
	}

	// 409 Conflict means the toolset already exists
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) && respErr.StatusCode == http.StatusConflict {
		fmt.Fprintf(
			os.Stderr,
			"  Toolset '%s' already exists, skipping\n",
			toolbox.Name,
		)
		return nil
	}

	return exterrors.Internal(
		exterrors.CodeCreateToolsetFailed,
		fmt.Sprintf(
			"failed to create toolset '%s': %s",
			toolbox.Name, err,
		),
	)
}

// registerToolboxEnvVars sets TOOLBOX_{NAME}_MCP_ENDPOINT.
func registerToolboxEnvVars(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	envName string,
	projectEndpoint string,
	toolboxName string,
) error {
	key := strings.ToUpper(
		strings.ReplaceAll(toolboxName, "-", "_"),
	)
	envKey := fmt.Sprintf(
		"TOOLBOX_%s_MCP_ENDPOINT", key,
	)

	endpoint := strings.TrimRight(projectEndpoint, "/")
	mcpEndpoint := fmt.Sprintf(
		"%s/toolsets/%s/mcp", endpoint, toolboxName,
	)

	return setEnvVar(
		ctx, azdClient, envName, envKey, mcpEndpoint,
	)
}
