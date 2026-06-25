// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/agents/optimize_api"
	"azureaiagent/internal/pkg/azure"
	"azureaiagent/internal/pkg/envkey"
	"azureaiagent/internal/pkg/paths"
	"azureaiagent/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/braydonk/yaml"
	"github.com/drone/envsubst"
	"google.golang.org/protobuf/types/known/structpb"
)

// configureExtensionHost wires the service target and event handlers on the
// supplied [azdext.ExtensionHost]. It is passed to [azdext.NewListenCommand]
// from the root command, which handles the surrounding setup (access token,
// AzdClient creation, and host.Run lifecycle).
func configureExtensionHost(host *azdext.ExtensionHost) {
	azdClient := host.Client()

	// IMPORTANT: service target name here must match the name used in the extension manifest.
	host.
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
		}).
		WithProjectEventHandler("postdown", func(ctx context.Context, args *azdext.ProjectEventArgs) error {
			return postdownHandler(ctx, azdClient, args)
		})
}

func preprovisionHandler(ctx context.Context, azdClient *azdext.AzdClient, args *azdext.ProjectEventArgs) error {
	for _, svc := range args.Project.Services {
		switch svc.Host {
		case AiAgentHost:
			// Prompt (kind=managed) agents have no container to provision
			// settings for — the harness owns the runtime. But they DO carry a
			// model deployment in their service config, so still run envUpdate
			// (which translates `deployments` into AI_PROJECT_DEPLOYMENTS for
			// Bicep). Only the container-settings step is hosted-specific.
			_, isPrompt := promptSettingsFromService(svc)
			if !isPrompt {
				if err := populateContainerSettings(ctx, azdClient, svc); err != nil {
					return fmt.Errorf("failed to populate container settings for service %q: %w", svc.Name, err)
				}
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
	hasAgent := false
	for _, svc := range args.Project.Services {
		if svc.Host != AiAgentHost {
			continue
		}
		hasAgent = true

		// Prompt (kind=managed) agents have no toolboxes to provision on a
		// Foundry project — the harness owns those. Skip toolbox provisioning
		// but still treat the project as having an agent (for the
		// pending-provision signal clear below).
		if _, isPrompt := promptSettingsFromService(svc); isPrompt {
			continue
		}

		if err := provisionToolboxes(ctx, azdClient, svc); err != nil {
			return fmt.Errorf(
				"failed to provision toolboxes for service %q: %w",
				svc.Name, err,
			)
		}
	}

	// Clear the AI_AGENT_PENDING_PROVISION signal now that provision has
	// finished successfully. Init writes resource-class tags into this
	// variable when it configures non-existent infra (a new model
	// deployment, a new Foundry project, a blank ACR/AppInsights input)
	// so the post-init trailer and `azd ai agent doctor` can recommend
	// `azd provision`. Once provision returns success the signal is
	// stale: subsequent runs of doctor/init/run/show/deploy should rely
	// on the canonical post-provision env vars (FOUNDRY_PROJECT_ENDPOINT
	// and friends) and the agent.yaml-vs-env diff. The clear is gated on
	// the presence of at least one azure.ai.agent service so toolbox-only
	// or non-agent provisions don't write to a variable they don't own.
	// Best-effort: a transport failure here is logged but not returned —
	// the user's provision DID succeed and surfacing a clear-time error
	// would be confusing. The next init/doctor run will simply re-emit
	// the suggestion until the variable is cleared by a future
	// successful provision (or by the user via `azd env set ... ""`).
	if hasAgent {
		envName, err := currentEnvName(ctx, azdClient)
		switch {
		case err != nil:
			log.Printf(
				"warning: failed to look up current environment to clear %s: %v",
				pendingProvisionEnvVar, err,
			)
		case envName == "":
			log.Printf(
				"warning: no current environment selected; skipping clear of %s",
				pendingProvisionEnvVar,
			)
		default:
			if clearErr := clearPendingProvisionReasons(ctx, azdClient, envName); clearErr != nil {
				log.Printf(
					"warning: failed to clear %s after provision: %v",
					pendingProvisionEnvVar, clearErr,
				)
			}
		}
	}

	return nil
}

// currentEnvName returns the name of the currently selected azd
// environment, or empty string + error when no environment is
// selected. Wraps Environment().GetCurrent so callers (notably
// postprovisionHandler) can read the current env name without
// duplicating the request shape.
func currentEnvName(ctx context.Context, azdClient *azdext.AzdClient) (string, error) {
	resp, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return "", err
	}
	if resp == nil || resp.Environment == nil {
		return "", nil
	}
	return resp.Environment.Name, nil
}

func predeployHandler(ctx context.Context, azdClient *azdext.AzdClient, args *azdext.ProjectEventArgs) error {
	hasHostedAgentService := false
	for _, svc := range args.Project.Services {
		if svc.Host != AiAgentHost {
			continue
		}

		// Prompt (kind=managed) agents have no container settings and no
		// developer-RBAC pre-flight — the harness owns the runtime.
		if _, isPrompt := promptSettingsFromService(svc); isPrompt {
			continue
		}

		if err := populateContainerSettings(ctx, azdClient, svc); err != nil {
			return fmt.Errorf("failed to populate container settings for service %q: %w", svc.Name, err)
		}
		if err := envUpdate(ctx, azdClient, args.Project, svc); err != nil {
			return fmt.Errorf("failed to update environment for service %q: %w", svc.Name, err)
		}

		if !hasHostedAgentService && isHostedAgentService(svc, args.Project) {
			hasHostedAgentService = true
		}
	}

	// Run developer RBAC pre-flight checks only for hosted agent deployments.
	if hasHostedAgentService {
		if err := project.CheckDeveloperRBAC(ctx, azdClient); err != nil {
			return err
		}
	}

	return nil
}

// isHostedAgentService checks if a service is a hosted (container) agent by reading
// the agent.yaml kind from the service directory.
func isHostedAgentService(svc *azdext.ServiceConfig, proj *azdext.ProjectConfig) bool {
	agentYamlPath, err := paths.JoinAllowRoot(proj.Path, svc.RelativePath, "agent.yaml")
	if err != nil {
		return false
	}
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
	// Skip when the project has no hosted agent services. `postdeploy` fires on every
	// `azd deploy`, so without this guard the FOUNDRY_PROJECT_ENDPOINT/AZURE_TENANT_ID
	// reads below would fail for projects that don't use this extension. See #7373.
	var hostedAgents []*azdext.ServiceConfig
	for _, svc := range args.Project.Services {
		if svc.Host == AiAgentHost && isHostedAgentService(svc, args.Project) {
			hostedAgents = append(hostedAgents, svc)
		}
	}
	if len(hostedAgents) == 0 {
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
		Key:     "FOUNDRY_PROJECT_ENDPOINT",
	})
	if err != nil {
		return fmt.Errorf("failed to read FOUNDRY_PROJECT_ENDPOINT: %w", err)
	}
	if endpointResp.Value == "" {
		return fmt.Errorf("FOUNDRY_PROJECT_ENDPOINT is not set in the environment")
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
	for _, svc := range hostedAgents {
		serviceKey := toServiceKey(svc.Name)

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
			ctx, svc.Name, versionResp.Value, DefaultAgentAPIVersion,
		)
		if err != nil {
			return fmt.Errorf(
				"failed to fetch agent version for %s/%s: %w",
				svc.Name, versionResp.Value, err,
			)
		}

		principalID := ""
		if versionObj.InstanceIdentity != nil {
			principalID = versionObj.InstanceIdentity.PrincipalID
		}

		agentIdentities[svc.Name] = principalID
	}

	if len(agentIdentities) == 0 {
		return nil
	}

	if err := project.EnsureAgentIdentityRBAC(ctx, azdClient, agentIdentities); err != nil {
		return fmt.Errorf("agent identity RBAC setup failed: %w", err)
	}

	// Report optimization candidate deployments to the optimization service.
	// If a service has AGENT_{KEY}_OPTIMIZATION_CANDIDATE_ID in the azd environment,
	// the agent was deployed from an optimization candidate. We notify the
	// optimization service so it can track which candidates have been deployed.
	reportOptimizationDeployments(ctx, azdClient, hostedAgents, envName, endpointResp.Value,
		func(endpoint string) *optimize_api.OptimizeClient {
			return optimize_api.NewOptimizeClient(endpoint, cred)
		},
	)

	return nil
}

// postdownHandler cleans up config store entries (sessions, conversations) for agent services
// that were torn down. This is best-effort — failures are logged but do not block azd down.
func postdownHandler(ctx context.Context, azdClient *azdext.AzdClient, args *azdext.ProjectEventArgs) error {
	envResp, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		log.Printf("postdown: failed to get current environment: %v", err)
		return nil
	}

	envName := envResp.Environment.Name

	for _, svc := range args.Project.Services {
		if svc.Host != AiAgentHost {
			continue
		}

		// Prompt (kind=managed) agents are removed from the harness on down so
		// `azd down` fully tears down the agent alongside the infrastructure.
		// Best-effort: a harness failure is logged but does not block down.
		if settings, isPrompt := promptSettingsFromService(svc); isPrompt {
			deletePromptAgentOnDown(ctx, svc, settings)
		}

		if cleanupAgentSessionState(ctx, azdClient, envName, svc.Name) {
			fmt.Printf("Cleaned up saved session and conversation for agent %q\n", svc.Name)
		}
	}

	return nil
}

// deletePromptAgentOnDown best-effort deletes a prompt agent from the harness
// during `azd down`. Failures are logged, never returned — teardown of the
// project should not be blocked by a harness hiccup.
func deletePromptAgentOnDown(
	ctx context.Context,
	svc *azdext.ServiceConfig,
	settings *project.PromptAgentSettings,
) {
	settings.ApplyEnvOverrides()
	if err := settings.Validate(); err != nil {
		log.Printf("postdown: skipping harness delete for %q: %v", svc.Name, err)
		return
	}
	client, err := project.NewPromptAgentClient(settings)
	if err != nil {
		log.Printf("postdown: failed to build harness client for %q: %v", svc.Name, err)
		return
	}
	if _, err := client.DeleteAgent(ctx, svc.Name, settings.EffectiveAPIVersion(), true); err != nil {
		log.Printf("postdown: failed to delete prompt agent %q from harness: %v", svc.Name, err)
		return
	}
	fmt.Printf("Deleted prompt agent %q from the harness\n", svc.Name)
}

// cleanupAgentSessionState removes saved session and conversation IDs for a
// single agent service. Returns true if cleanup succeeded, false otherwise.
// Shared by postdownHandler and delete command.
func cleanupAgentSessionState(ctx context.Context, azdClient *azdext.AzdClient, envName, serviceName string) bool {
	serviceKey := toServiceKey(serviceName)

	endpointResp, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envName,
		Key:     fmt.Sprintf("AGENT_%s_ENDPOINT", serviceKey),
	})
	if err != nil || endpointResp.Value == "" {
		return false
	}

	agentKey := buildRemoteAgentKeyFromEndpoint(endpointResp.Value)

	var failed bool
	if err := deleteContextValue(ctx, azdClient, "sessions", agentKey); err != nil {
		log.Printf("cleanupAgentSessionState: failed to clean sessions for %s: %v", agentKey, err)
		failed = true
	}
	if err := deleteContextValue(ctx, azdClient, "conversations", agentKey); err != nil {
		log.Printf("cleanupAgentSessionState: failed to clean conversations for %s: %v", agentKey, err)
		failed = true
	}

	return !failed
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
	agentYamlPath, err := paths.JoinAllowRoot(project.Path, svc.RelativePath, "agent.yaml")
	if err != nil {
		return fmt.Errorf("invalid service path: %w", err)
	}

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

		if err := setEnvVar(ctx, azdClient, envName, "ENABLE_CAPABILITY_HOST", "false"); err != nil {
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

	// Set default values if zero or empty
	if result.Resources.Memory == "" {
		result.Resources.Memory = project.DefaultMemory
	}

	if result.Resources.Cpu == "" {
		result.Resources.Cpu = project.DefaultCpu
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
		Key:     "FOUNDRY_PROJECT_ENDPOINT",
	})
	if err != nil || envValue.Value == "" {
		return exterrors.Dependency(
			exterrors.CodeMissingAiProjectEndpoint,
			"FOUNDRY_PROJECT_ENDPOINT is required for toolbox provisioning",
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
	connByName := toolboxConnectionsByName(config)

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
	envKey := envkey.ToolboxMCPEndpoint(toolboxName)

	endpoint := strings.TrimRight(projectEndpoint, "/")
	mcpEndpoint := fmt.Sprintf(
		"%s/toolboxes/%s/versions/%s/mcp?api-version=v1",
		endpoint, url.PathEscape(toolboxName), url.PathEscape(toolboxVersion),
	)

	return setEnvVar(
		ctx, azdClient, envName, envKey, mcpEndpoint,
	)
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
	connByName map[string]toolboxConnection,
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

type toolboxConnection struct {
	Name   string
	Target string
}

func toolboxConnectionsByName(config *project.ServiceTargetAgentConfig) map[string]toolboxConnection {
	connByName := map[string]toolboxConnection{}
	if config == nil {
		return connByName
	}

	for _, c := range config.Connections {
		connByName[c.Name] = toolboxConnection{Name: c.Name, Target: c.Target}
	}
	for _, c := range config.ToolConnections {
		connByName[c.Name] = toolboxConnection{Name: c.Name, Target: c.Target}
	}

	return connByName
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
