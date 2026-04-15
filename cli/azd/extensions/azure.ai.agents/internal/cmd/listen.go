// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/azure"
	"azureaiagent/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/braydonk/yaml"
	"github.com/drone/envsubst"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/structpb"
)

// nonAlphanumEnvKeyRe matches any character that is not an uppercase letter or
// digit, used to sanitize environment variable key segments.
var nonAlphanumEnvKeyRe = regexp.MustCompile(`[^A-Z0-9]+`)

func newListenCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "listen",
		Short:  "Starts the extension and listens for events.",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create a new context that includes the AZD access token.
			ctx := azdext.WithAccessToken(cmd.Context())

			logCleanup := setupDebugLogging(cmd.Flags())
			defer logCleanup()

			// Create a new AZD client.
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			// IMPORTANT: service target name here must match the name used in the extension manifest.
			host := azdext.NewExtensionHost(azdClient).
				WithServiceTarget(AiAgentHost, func() azdext.ServiceTargetProvider {
					return project.NewAgentServiceTargetProvider(azdClient)
				}).
				WithProjectEventHandler("preprovision", func(ctx context.Context, args *azdext.ProjectEventArgs) error {
					return preprovisionHandler(ctx, azdClient, args)
				}).
				WithProjectEventHandler("postprovision", func(ctx context.Context, args *azdext.ProjectEventArgs) error {
					return postprovisionHandler(ctx, azdClient, args)
				}).
				WithProjectEventHandler("predeploy", func(ctx context.Context, args *azdext.ProjectEventArgs) error {
					return predeployHandler(ctx, azdClient, args)
				}).
				WithProjectEventHandler("postdeploy", func(ctx context.Context, args *azdext.ProjectEventArgs) error {
					return postdeployHandler(ctx, azdClient, args)
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

func preprovisionHandler(ctx context.Context, azdClient *azdext.AzdClient, args *azdext.ProjectEventArgs) error {
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

func predeployHandler(ctx context.Context, azdClient *azdext.AzdClient, args *azdext.ProjectEventArgs) error {
	hasHostedAgent := false
	for _, svc := range args.Project.Services {
		switch svc.Host {
		case AiAgentHost:
			if err := populateContainerSettings(ctx, azdClient, svc); err != nil {
				return fmt.Errorf("failed to populate container settings for service %q: %w", svc.Name, err)
			}
			if err := envUpdate(ctx, azdClient, args.Project, svc); err != nil {
				return fmt.Errorf("failed to update environment for service %q: %w", svc.Name, err)
			}
			if isHostedAgentService(svc, args.Project) {
				hasHostedAgent = true
			}
		}
	}

	// Run developer RBAC pre-flight checks for hosted agent deployments.
	if hasHostedAgent {
		if err := project.CheckDeveloperRBAC(ctx, azdClient); err != nil {
			return err
		}
	}

	return nil
}

// isHostedAgentService checks if a service is a hosted (container) agent by reading
// the agent.yaml kind from the service directory.
func isHostedAgentService(svc *azdext.ServiceConfig, proj *azdext.ProjectConfig) bool {
	agentYamlPath := filepath.Join(proj.Path, svc.RelativePath, "agent.yaml")
	data, err := os.ReadFile(agentYamlPath) //nolint:gosec // path from azd project config
	if err != nil {
		return false
	}
	var generic map[string]any
	if err := yaml.Unmarshal(data, &generic); err != nil {
		return false
	}
	kind, ok := generic["kind"].(string)
	return ok && kind == string(agent_yaml.AgentKindHosted)
}

func postdeployHandler(ctx context.Context, azdClient *azdext.AzdClient, args *azdext.ProjectEventArgs) error {
	// Agent identity RBAC is only relevant for vnext-enabled projects.
	if !isVNextEnabled(ctx, azdClient) {
		return nil
	}

	// Collect agent identities from hosted agent services that were deployed.
	// After deploy, each hosted agent's name/version is stored as AGENT_{SERVICE_KEY}_NAME/VERSION.
	// We fetch the full agent version object from the API to get the instance identity principal ID,
	// which allows us to skip the slow Graph API discovery during RBAC assignment.
	envResp, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return fmt.Errorf("failed to get current environment for agent identity RBAC: %w", err)
	}

	envName := envResp.Environment.Name

	// Read the project endpoint for API calls.
	endpointResp, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envName,
		Key:     "AZURE_AI_PROJECT_ENDPOINT",
	})
	if err != nil {
		return fmt.Errorf("failed to read AZURE_AI_PROJECT_ENDPOINT: %w", err)
	}
	if endpointResp.Value == "" {
		return fmt.Errorf("AZURE_AI_PROJECT_ENDPOINT is not set in the environment")
	}

	// Create a credential for API calls.
	tenantResp, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envName,
		Key:     "AZURE_TENANT_ID",
	})
	if err != nil {
		return fmt.Errorf("failed to read AZURE_TENANT_ID: %w", err)
	}
	if tenantResp.Value == "" {
		return fmt.Errorf("AZURE_TENANT_ID is not set in the environment")
	}

	cred, err := azidentity.NewAzureDeveloperCLICredential(
		&azidentity.AzureDeveloperCLICredentialOptions{
			TenantID:                   tenantResp.Value,
			AdditionallyAllowedTenants: []string{"*"},
		},
	)
	if err != nil {
		return fmt.Errorf("failed to create credential for agent identity RBAC: %w", err)
	}

	agentClient := agent_api.NewAgentClient(endpointResp.Value, cred)

	// Build name→principalID map by fetching the agent version for each hosted service.
	agentIdentities := make(map[string]string)
	for _, svc := range args.Project.Services {
		if svc.Host != AiAgentHost || !isHostedAgentService(svc, args.Project) {
			continue
		}
		serviceKey := toServiceKey(svc.Name)

		nameResp, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
			EnvName: envName,
			Key:     fmt.Sprintf("AGENT_%s_NAME", serviceKey),
		})
		if err != nil {
			return fmt.Errorf(
				"failed to read AGENT_%s_NAME from environment: %w",
				serviceKey, err,
			)
		}
		if nameResp.Value == "" {
			continue
		}

		versionResp, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
			EnvName: envName,
			Key:     fmt.Sprintf("AGENT_%s_VERSION", serviceKey),
		})
		if err != nil {
			return fmt.Errorf(
				"failed to read AGENT_%s_VERSION from environment: %w",
				serviceKey, err,
			)
		}
		if versionResp.Value == "" {
			continue
		}

		// Fetch the agent version to get the instance identity principal ID.
		versionObj, err := agentClient.GetAgentVersion(
			ctx, nameResp.Value, versionResp.Value, DefaultVNextAgentAPIVersion,
		)
		if err != nil {
			return fmt.Errorf(
				"failed to fetch agent version for %s/%s: %w",
				nameResp.Value, versionResp.Value, err,
			)
		}

		principalID := ""
		if versionObj.InstanceIdentity != nil {
			principalID = versionObj.InstanceIdentity.PrincipalID
		}

		agentIdentities[nameResp.Value] = principalID
	}

	if len(agentIdentities) == 0 {
		return nil
	}

	if err := project.EnsureAgentIdentityRBAC(ctx, azdClient, agentIdentities); err != nil {
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

	if len(foundryAgentConfig.ToolConnections) > 0 {
		if err := toolConnectionsEnvUpdate(
			ctx, foundryAgentConfig.ToolConnections,
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

		vnextValue := os.Getenv("enableHostedAgentVNext")
		if vnextValue == "" {
			azdEnv, err := loadAzdEnvironment(ctx, azdClient)
			if err == nil {
				vnextValue = azdEnv["enableHostedAgentVNext"]
			}
		}
		if enabled, err := strconv.ParseBool(vnextValue); err == nil && enabled {
			if err := setEnvVar(ctx, azdClient, envName, "ENABLE_CAPABILITY_HOST", "false"); err != nil {
				return err
			}
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
	// Strip credentials from the connections env var — Bicep's ConnectionConfig
	// type doesn't include credentials (they're a separate @secure param).
	// Including them causes "unable to deserialize request body" errors.
	stripped := make([]project.Connection, len(connections))
	copy(stripped, connections)
	for i := range stripped {
		stripped[i].Credentials = nil
	}

	if err := marshalAndSetEnvVar(ctx, azdClient, envName, "AI_PROJECT_CONNECTIONS", stripped); err != nil {
		return err
	}

	return connectionCredentialsEnvUpdate(ctx, connections, azdClient, envName)
}

// connectionCredentialsEnvUpdate builds a dictionary of connection name → credentials
// and serializes it to AI_PROJECT_CONNECTION_CREDENTIALS. Credential values may contain
// ${VAR} env var references (from externalization during init); these are resolved to
// their actual values before serialization so Bicep receives real secrets.
func connectionCredentialsEnvUpdate(
	ctx context.Context,
	connections []project.Connection,
	azdClient *azdext.AzdClient,
	envName string,
) error {
	credMap := buildConnectionCredentials(connections)
	if len(credMap) == 0 {
		return nil
	}

	// Resolve ${VAR} references in credential values to actual secrets.
	azdEnv, err := getAllEnvVars(ctx, azdClient, envName)
	if err != nil {
		return fmt.Errorf("loading env vars for credential resolution: %w", err)
	}
	for connName, creds := range credMap {
		credMap[connName] = resolveMapValues(creds, azdEnv)
	}

	return marshalAndSetEnvVar(ctx, azdClient, envName, "AI_PROJECT_CONNECTION_CREDENTIALS", credMap)
}

// buildConnectionCredentials returns a map of connection name → credentials object
// for all connections that have non-empty credentials.
func buildConnectionCredentials(connections []project.Connection) map[string]map[string]any {
	result := map[string]map[string]any{}
	for _, conn := range connections {
		if len(conn.Credentials) > 0 {
			result[conn.Name] = conn.Credentials
		}
	}

	return result
}

// toolConnectionsEnvUpdate serializes tool connections to AI_PROJECT_TOOL_CONNECTIONS env var.
func toolConnectionsEnvUpdate(
	ctx context.Context,
	connections []project.ToolConnection,
	azdClient *azdext.AzdClient,
	envName string,
) error {
	return marshalAndSetEnvVar(ctx, azdClient, envName, "AI_PROJECT_TOOL_CONNECTIONS", connections)
}

// marshalAndSetEnvVar serializes a value to JSON, escapes it for safe storage
// in an azd environment variable, and persists it.
func marshalAndSetEnvVar(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	envName string,
	key string,
	value any,
) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal %s to JSON: %w", key, err)
	}

	jsonString := string(data)
	escaped := strings.ReplaceAll(jsonString, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "\"", "\\\"")

	return setEnvVar(ctx, azdClient, envName, key, escaped)
}

func setEnvVar(ctx context.Context, azdClient *azdext.AzdClient, envName string, key string, value string) error {
	_, err := azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: envName,
		Key:     key,
		Value:   value,
	})
	if err != nil {
		return fmt.Errorf("failed to set environment variable %s: %w", key, err)
	}

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

	// Preserve existing Scale settings from azure.yaml, but don't create new defaults for VNext
	if containerSettings.Scale != nil {
		result.Scale = &project.ScaleSettings{
			MinReplicas: containerSettings.Scale.MinReplicas,
			MaxReplicas: containerSettings.Scale.MaxReplicas,
		}
	} else if !isVNextEnabled(ctx, azdClient) {
		result.Scale = &project.ScaleSettings{}
	}

	// Set default values if zero or empty
	if result.Resources.Memory == "" {
		result.Resources.Memory = project.DefaultMemory
	}

	if result.Resources.Cpu == "" {
		result.Resources.Cpu = project.DefaultCpu
	}

	if result.Scale != nil {
		if result.Scale.MinReplicas == 0 {
			result.Scale.MinReplicas = project.DefaultMinReplicas
		}

		if result.Scale.MaxReplicas == 0 {
			result.Scale.MaxReplicas = project.DefaultMaxReplicas
		}
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

	toolboxClient := azure.NewFoundryToolboxClient(
		projectEndpoint, cred,
	)

	// Build azd env lookup for resolving ${VAR} references in tool entries
	azdEnv, err := getAllEnvVars(ctx, azdClient, currentEnv.Environment.Name)
	if err != nil {
		return fmt.Errorf("failed to load environment variables: %w", err)
	}

	// Build connection ID lookup from bicep outputs (name → ARM resource ID)
	connIDMap, err := parseConnectionIDs(azdEnv["AI_PROJECT_CONNECTION_IDS_JSON"])
	if err != nil {
		return fmt.Errorf("loading connection IDs: %w", err)
	}

	// Build connection lookup for enriching tool entries with server_url/server_label
	connByName := map[string]project.ToolConnection{}
	for _, c := range config.ToolConnections {
		connByName[c.Name] = c
	}

	for _, toolbox := range config.Toolboxes {
		fmt.Fprintf(
			os.Stderr, "Provisioning toolbox: %s\n", toolbox.Name,
		)

		// Resolve ${VAR} references in tool map values before sending to API
		resolveToolboxEnvVars(&toolbox, azdEnv)

		// Fill in server_url/server_label from connection data
		enrichToolboxFromConnections(&toolbox, connByName)

		// Replace project_connection_id friendly names with ARM resource IDs
		resolveToolboxConnectionIDs(&toolbox, connIDMap)

		version, err := createToolboxVersion(
			ctx, toolboxClient, toolbox,
		)
		if err != nil {
			return err
		}

		if err := registerToolboxEnvVars(
			ctx, azdClient,
			currentEnv.Environment.Name,
			projectEndpoint, toolbox.Name, version,
		); err != nil {
			return err
		}

		fmt.Fprintf(
			os.Stderr, "Toolbox '%s' provisioned\n", toolbox.Name,
		)
	}

	return nil
}

// createToolboxVersion creates a new version of a toolbox.
// If the toolbox does not exist, it will be created automatically.
// Returns the version identifier of the newly created version.
func createToolboxVersion(
	ctx context.Context,
	client *azure.FoundryToolboxClient,
	toolbox project.Toolbox,
) (string, error) {
	req := &azure.CreateToolboxVersionRequest{
		Description: toolbox.Description,
		Tools:       toolbox.Tools,
	}

	result, err := client.CreateToolboxVersion(ctx, toolbox.Name, req)
	if err != nil {
		return "", exterrors.Internal(
			exterrors.CodeCreateToolboxVersionFailed,
			fmt.Sprintf("failed to create toolbox version '%s': %s", toolbox.Name, err),
		)
	}

	return result.Version, nil
}

// registerToolboxEnvVars sets TOOLBOX_{NAME}_MCP_ENDPOINT with the versioned URL.
func registerToolboxEnvVars(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	envName string,
	projectEndpoint string,
	toolboxName string,
	toolboxVersion string,
) error {
	envKey := toolboxMCPEndpointEnvKey(toolboxName)

	endpoint := strings.TrimRight(projectEndpoint, "/")
	mcpEndpoint := fmt.Sprintf(
		"%s/toolboxes/%s/versions/%s/mcp?api-version=v1",
		endpoint, url.PathEscape(toolboxName), url.PathEscape(toolboxVersion),
	)

	return setEnvVar(
		ctx, azdClient, envName, envKey, mcpEndpoint,
	)
}

// toolboxMCPEndpointEnvKey builds the TOOLBOX_{NAME}_MCP_ENDPOINT env var key.
// Non-alphanumeric characters are replaced with underscores for a valid env key.
func toolboxMCPEndpointEnvKey(toolboxName string) string {
	sanitized := nonAlphanumEnvKeyRe.ReplaceAllString(strings.ToUpper(toolboxName), "_")
	return fmt.Sprintf("TOOLBOX_%s_MCP_ENDPOINT", sanitized)
}

// resolveToolboxEnvVars resolves ${VAR} references in toolbox name, description,
// and all tool map values using the provided azd environment variables.
func resolveToolboxEnvVars(toolbox *project.Toolbox, azdEnv map[string]string) {
	toolbox.Name = resolveEnvValue(toolbox.Name, azdEnv)
	toolbox.Description = resolveEnvValue(toolbox.Description, azdEnv)
	for i, tool := range toolbox.Tools {
		toolbox.Tools[i] = resolveMapValues(tool, azdEnv)
	}
}

// enrichToolboxFromConnections fills in server_url and server_label on toolbox
// tools that reference a connection via project_connection_id. This keeps the
// azure.yaml toolbox entries minimal while sending complete data to the API.
func enrichToolboxFromConnections(
	toolbox *project.Toolbox,
	connByName map[string]project.ToolConnection,
) {
	for i, tool := range toolbox.Tools {
		connID, _ := tool["project_connection_id"].(string)
		if connID == "" {
			continue
		}
		conn, ok := connByName[connID]
		if !ok {
			fmt.Fprintf(os.Stderr, "warning: tool references connection %q but no matching connection was found\n", connID)
			continue
		}
		if _, has := tool["server_url"]; !has && conn.Target != "" {
			toolbox.Tools[i]["server_url"] = conn.Target
		}
		if _, has := tool["server_label"]; !has {
			toolbox.Tools[i]["server_label"] = conn.Name
		}
	}
}

// parseConnectionIDs parses the AI_PROJECT_CONNECTION_IDS_JSON env var
// (a JSON array of {name, id} objects) into a map of name → ARM resource ID.
func parseConnectionIDs(jsonStr string) (map[string]string, error) {
	result := map[string]string{}
	if jsonStr == "" {
		return result, nil
	}

	var entries []struct {
		Name string `json:"name"`
		ID   string `json:"id"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &entries); err != nil {
		return nil, fmt.Errorf("failed to parse AI_PROJECT_CONNECTION_IDS_JSON: %w", err)
	}

	for _, e := range entries {
		if e.Name != "" && e.ID != "" {
			result[e.Name] = e.ID
		}
	}
	return result, nil
}

// resolveToolboxConnectionIDs replaces project_connection_id friendly names
// with their actual ARM resource IDs from bicep provisioning outputs.
func resolveToolboxConnectionIDs(
	toolbox *project.Toolbox,
	connIDs map[string]string,
) {
	if len(connIDs) == 0 {
		return
	}
	for i, tool := range toolbox.Tools {
		connName, _ := tool["project_connection_id"].(string)
		if connName == "" {
			continue
		}
		connName = resolveTemplateRef(connName)
		if armID, ok := connIDs[connName]; ok {
			toolbox.Tools[i]["project_connection_id"] = armID
		}
	}
}

// resolveTemplateRef strips {{ }} template wrapping and trims whitespace.
// "{{ my_conn }}" → "my_conn", "my_conn" → "my_conn" (unchanged).
func resolveTemplateRef(s string) string {
	if strings.HasPrefix(s, "{{") && strings.HasSuffix(s, "}}") {
		return strings.TrimSpace(s[2 : len(s)-2])
	}
	return s
}

// resolveEnvValue resolves ${VAR} references in a string using envsubst.
func resolveEnvValue(value string, azdEnv map[string]string) string {
	resolved, err := envsubst.Eval(value, func(varName string) string {
		return azdEnv[varName]
	})
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"Warning: failed to resolve env references in %q: %s\n", value, err)
		return value
	}
	return resolved
}

// resolveMapValues recursively resolves ${VAR} references in all string values of a map.
func resolveMapValues(m map[string]any, azdEnv map[string]string) map[string]any {
	resolved := make(map[string]any, len(m))
	for k, v := range m {
		resolved[k] = resolveAnyValue(v, azdEnv)
	}
	return resolved
}

// resolveAnyValue resolves ${VAR} references in a value of any type.
func resolveAnyValue(v any, azdEnv map[string]string) any {
	switch val := v.(type) {
	case string:
		return resolveEnvValue(val, azdEnv)
	case map[string]any:
		return resolveMapValues(val, azdEnv)
	case []any:
		resolved := make([]any, len(val))
		for i, item := range val {
			resolved[i] = resolveAnyValue(item, azdEnv)
		}
		return resolved
	default:
		return v
	}
}

// getAllEnvVars loads all environment variables from the azd environment.
func getAllEnvVars(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	envName string,
) (map[string]string, error) {
	resp, err := azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
		Name: envName,
	})
	if err != nil {
		return nil, err
	}

	envMap := make(map[string]string, len(resp.KeyValues))
	for _, kv := range resp.KeyValues {
		envMap[kv.Key] = kv.Value
	}
	return envMap, nil
}
