// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/optimize_api"
	"azureaiagent/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
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
		WithServiceEventHandler("predeploy", func(ctx context.Context, args *azdext.ServiceEventArgs) error {
			return predeployHandler(ctx, azdClient, args)
		}, &azdext.ServiceEventOptions{Host: AiAgentHost}).
		WithServiceEventHandler("postdeploy", func(ctx context.Context, args *azdext.ServiceEventArgs) error {
			return postdeployHandler(ctx, azdClient, args)
		}, &azdext.ServiceEventOptions{Host: AiAgentHost}).
		WithProjectEventHandler("postdown", func(ctx context.Context, args *azdext.ProjectEventArgs) error {
			return postdownHandler(ctx, azdClient, args)
		})
}

func preprovisionHandler(ctx context.Context, azdClient *azdext.AzdClient, args *azdext.ProjectEventArgs) error {
	if err := updateLegacyProjectDeployments(
		ctx,
		azdClient,
		args.Project.Services,
	); err != nil {
		return err
	}
	connections, err := collectConnections(args.Project.Services)
	if err != nil {
		return err
	}

	for _, svc := range args.Project.Services {
		switch svc.Host {
		case AiAgentHost:
			if err := populateContainerSettings(ctx, azdClient, svc); err != nil {
				return fmt.Errorf("failed to populate container settings for service %q: %w", svc.Name, err)
			}
			if err := envUpdate(
				ctx,
				azdClient,
				args.Project,
				svc,
				connections,
			); err != nil {
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
	// Toolboxes are reconciled at deploy time by the azure.ai.toolbox service
	// target (the azure.ai.toolboxes extension), not at provision. The agent
	// service's uses: edges order each toolbox before the agent that consumes
	// it, so the toolbox MCP endpoints are published before the agent deploys.

	hasAgent := false
	for _, svc := range args.Project.Services {
		if svc.Host == AiAgentHost {
			hasAgent = true
			break
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

func updateLegacyProjectDeployments(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	services map[string]*azdext.ServiceConfig,
) error {
	deployments, err := collectLegacyProjectDeployments(services)
	if err != nil {
		return err
	}
	if len(deployments) == 0 {
		return nil
	}

	envName, err := currentEnvName(ctx, azdClient)
	if err != nil {
		return fmt.Errorf(
			"resolving environment for legacy deployments: %w",
			err,
		)
	}
	return deploymentEnvUpdate(
		ctx,
		deployments,
		azdClient,
		envName,
	)
}

// developerRBACOnce ensures CheckDeveloperRBAC runs at most once per extension
// process lifetime. Service-level predeploy handlers fire per-service, but the
// RBAC pre-flight check is project-scoped and idempotent — running it once is
// sufficient and avoids duplicate ARM/Graph calls and noisy output.
var (
	developerRBACOnce sync.Once
	developerRBACErr  error
)

// duplicateAgentNameWarnOnce ensures the duplicate agent-name warning is emitted
// at most once per extension process lifetime. Service-level predeploy handlers
// fire per-service, but the check is project-scoped — a single pass over every
// azure.ai.agent service surfaces all collisions without repeating the warning
// for each colliding service.
var duplicateAgentNameWarnOnce sync.Once

func predeployHandler(ctx context.Context, azdClient *azdext.AzdClient, args *azdext.ServiceEventArgs) error {
	svc := args.Service

	// Warn (once) when multiple agent services resolve to the same Foundry agent
	// name. Foundry identifies an agent by its name, so such services overwrite
	// each other on deploy. Advisory only — deploy continues.
	duplicateAgentNameWarnOnce.Do(func() {
		warnDuplicateAgentNames(args.Project)
	})

	if err := updateLegacyProjectDeployments(
		ctx,
		azdClient,
		args.Project.Services,
	); err != nil {
		return err
	}
	connections, err := collectConnections(args.Project.Services)
	if err != nil {
		return err
	}

	if err := populateContainerSettings(ctx, azdClient, svc); err != nil {
		return fmt.Errorf("failed to populate container settings for service %q: %w", svc.Name, err)
	}
	if err := envUpdate(
		ctx,
		azdClient,
		args.Project,
		svc,
		connections,
	); err != nil {
		return fmt.Errorf("failed to update environment for service %q: %w", svc.Name, err)
	}

	// Capture the current session so it can be resumed on the newly deployed
	// version after deploy (see session_carryover.go). Best-effort; hosted
	// agents only.
	if isHostedAgentService(svc, args.Project) {
		captureSessionForCarryover(ctx, azdClient, svc)
	}

	// Run developer RBAC pre-flight checks only for hosted agent deployments.
	// Guarded by sync.Once since this handler fires per-service but the check
	// is project-scoped.
	if isHostedAgentService(svc, args.Project) {
		developerRBACOnce.Do(func() {
			developerRBACErr = project.CheckDeveloperRBAC(ctx, azdClient)
		})
		if developerRBACErr != nil {
			return developerRBACErr
		}
	}

	return nil
}

// isHostedAgentService checks if a service is a hosted (container) agent by
// resolving its agent definition from the service entry (the unified inline
// shape, or a legacy agent.yaml on disk).
func isHostedAgentService(svc *azdext.ServiceConfig, proj *azdext.ProjectConfig) bool {
	_, isHosted, _, err := project.LoadAgentDefinition(svc, proj.Path)
	return err == nil && isHosted
}

// duplicateAgentNameGroup is a Foundry agent name referenced by more than one
// azure.ai.agent service, with the colliding azure.yaml service keys sorted for
// stable output.
type duplicateAgentNameGroup struct {
	agentName    string
	serviceNames []string
}

// findDuplicateAgentNames groups hosted azure.ai.agent services by their resolved
// Foundry agent name and returns the groups referenced by two or more services.
// Foundry uses the agent name as an agent's unique identifier, so services that
// share a name deploy to the same agent and overwrite each other.
//
// Services whose definition can't be resolved, that aren't hosted agents, or that
// carry an empty name are skipped — their own deploy surfaces any real error. The
// returned groups (and the service keys within each) are sorted for deterministic
// output.
func findDuplicateAgentNames(proj *azdext.ProjectConfig) []duplicateAgentNameGroup {
	if proj == nil {
		return nil
	}

	servicesByAgentName := map[string][]string{}
	for serviceName, svc := range proj.Services {
		if svc.GetHost() != AiAgentHost {
			continue
		}
		ca, isHosted, _, err := project.LoadAgentDefinition(svc, proj.Path)
		if err != nil || !isHosted {
			continue
		}
		agentName := strings.TrimSpace(ca.Name)
		if agentName == "" {
			continue
		}
		servicesByAgentName[agentName] = append(servicesByAgentName[agentName], serviceName)
	}

	var groups []duplicateAgentNameGroup
	for agentName, serviceNames := range servicesByAgentName {
		if len(serviceNames) < 2 {
			continue
		}
		sort.Strings(serviceNames)
		groups = append(groups, duplicateAgentNameGroup{agentName: agentName, serviceNames: serviceNames})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].agentName < groups[j].agentName })
	return groups
}

// warnDuplicateAgentNames prints one warning per agent name shared by multiple
// azure.ai.agent services. The warning names the colliding service keys and the
// shared agent name so the user can give each agent a unique name in azure.yaml.
// It is advisory: deploy continues.
func warnDuplicateAgentNames(proj *azdext.ProjectConfig) {
	for _, group := range findDuplicateAgentNames(proj) {
		fmt.Fprintf(os.Stderr, "%s", output.WithWarningFormat(
			"WARNING: agent name %q is used by multiple services (%s). Foundry identifies an agent "+
				"by its name, so these services deploy to the same agent and overwrite each other. "+
				"Give each agent a unique name in azure.yaml.\n",
			group.agentName, strings.Join(group.serviceNames, ", "),
		))
	}
}

// gatherPostdeployInputs reads the environment inputs shared by the two postdeploy
// steps (activity-bot provisioning and optimization reporting): the current
// environment name, the Foundry project endpoint, the tenant, and a credential
// built from that tenant. It only gathers and returns the first error encountered;
// it does NOT decide skip-vs-fail, so each caller can apply its own policy to the
// returned error (the required Teams bot fails; best-effort reporting skips).
func gatherPostdeployInputs(
	ctx context.Context, azdClient *azdext.AzdClient,
) (envName, endpoint, tenant string, cred *azidentity.AzureDeveloperCLICredential, err error) {
	envResp, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return "", "", "", nil, fmt.Errorf("failed to get current environment: %w", err)
	}
	envName = envResp.Environment.Name

	if endpoint, err = readEnvValue(ctx, azdClient, envName, "FOUNDRY_PROJECT_ENDPOINT"); err != nil {
		return envName, "", "", nil, err
	}
	if tenant, err = readEnvValue(ctx, azdClient, envName, "AZURE_TENANT_ID"); err != nil {
		return envName, endpoint, "", nil, err
	}

	cred, err = azidentity.NewAzureDeveloperCLICredential(
		&azidentity.AzureDeveloperCLICredentialOptions{
			TenantID:                   tenant,
			AdditionallyAllowedTenants: []string{"*"},
		},
	)
	if err != nil {
		return envName, endpoint, tenant, nil, fmt.Errorf("failed to create credential: %w", err)
	}
	return envName, endpoint, tenant, cred, nil
}

func postdeployHandler(ctx context.Context, azdClient *azdext.AzdClient, args *azdext.ServiceEventArgs) error {
	svc := args.Service

	// Skip when the service is not a hosted agent.
	if !isHostedAgentService(svc, args.Project) {
		return nil
	}

	// Whether this service is an activity agent decides how the shared inputs below
	// are treated: an activity agent requires the Teams bot connector, so a missing
	// input must fail the deploy; every other agent only feeds best-effort
	// optimization reporting, which is safe to skip.
	isActivity := false
	if ca, isHosted, _, defErr := project.LoadAgentDefinition(svc, args.Project.Path); defErr == nil && isHosted {
		isActivity = project.ResolveActivityProfile(ca).IsActivity
	}

	// Read the inputs both steps draw from once. Gathering does not decide
	// skip-vs-fail; each step below applies its own policy to inputErr, so the
	// required Teams bot never inherits optimization reporting's "log and skip"
	// preconditions and vice versa.
	envName, endpoint, tenant, cred, inputErr := gatherPostdeployInputs(ctx, azdClient)

	// Step 1 — activity bot: a required connector, provisioned and validated on its
	// own terms. A missing prerequisite fails the deploy rather than being skipped,
	// consistent with the EnsureBot failure handled just below. No-op for other agents.
	if isActivity {
		if inputErr != nil {
			return fmt.Errorf(
				"agent %q deployed successfully, but its required Microsoft Teams bot could not be "+
					"configured: %w\n"+
					"  Ensure the agent is provisioned in this environment, then re-run 'azd deploy'.",
				svc.Name, inputErr,
			)
		}
		if err := ensureActivityBot(
			ctx, azdClient, cred, envName, svc, args.Project, endpoint, tenant,
		); err != nil {
			return fmt.Errorf(
				"agent %q deployed successfully, but configuring its Microsoft Teams bot failed: %w\n"+
					"  The agent version is active — only the Teams channel binding is missing "+
					"(commonly Azure Bot permissions or quota). Resolve the cause and re-run 'azd deploy'.",
				svc.Name, err,
			)
		}
	}

	// Step 2 — optimization reporting: best-effort, skipped on its own terms. A
	// missing input only logs and skips; it never fails an otherwise-successful
	// deploy (the client-side agent-identity RBAC assignment was removed).
	if inputErr != nil {
		log.Printf("postdeploy: skipping optimization reporting for %s: %v", svc.Name, inputErr)
		return nil
	}
	func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("postdeploy: optimization reporting panicked for %s: %v", svc.Name, r)
			}
		}()
		reportSvcOptimizationDeployment(ctx, azdClient, svc, envName, endpoint,
			func(endpoint string) *optimize_api.OptimizeClient {
				return optimize_api.NewOptimizeClient(endpoint, cred)
			},
		)
	}()

	// Resume the pre-deploy session on the newly deployed version so the next
	// invoke continues on the new code with the session's persisted volume
	// intact (see session_carryover.go). Best-effort; never blocks deploy.
	agentClient := agent_api.NewAgentClient(endpoint, cred)
	carryOverSessionAfterDeploy(ctx, azdClient, agentClient, svc, envName)

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

		if cleanupAgentSessionState(ctx, azdClient, envName, svc.Name) {
			fmt.Printf("Cleaned up saved session and conversation for agent %q\n", svc.Name)
		}
	}

	// Delete the Azure Bot created for activity-protocol agents so its globally
	// unique name is freed for future redeploys. Best-effort.
	teardownActivityBots(ctx, azdClient, envName, args.Project)

	return nil
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

func envUpdate(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	azdProject *azdext.ProjectConfig,
	svc *azdext.ServiceConfig,
	connections []project.Connection,
) error {

	foundryAgentConfig, err := project.LoadServiceTargetAgentConfig(svc)
	if err != nil {
		return fmt.Errorf("failed to parse foundry agent config: %w", err)
	}

	currentEnvResponse, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return err
	}

	if err := kindEnvUpdate(ctx, azdClient, azdProject, svc, currentEnvResponse.Environment.Name); err != nil {
		return err
	}

	if foundryAgentConfig != nil && len(foundryAgentConfig.Resources) > 0 {
		if err := resourcesEnvUpdate(ctx, foundryAgentConfig.Resources, azdClient, currentEnvResponse.Environment.Name); err != nil {
			return err
		}
	}

	if len(connections) > 0 {
		if err := connectionsEnvUpdate(
			ctx, connections,
			azdClient, currentEnvResponse.Environment.Name,
		); err != nil {
			return err
		}
	}

	if foundryAgentConfig != nil && len(foundryAgentConfig.ToolConnections) > 0 {
		if err := toolConnectionsEnvUpdate(
			ctx, foundryAgentConfig.ToolConnections,
			azdClient, currentEnvResponse.Environment.Name,
		); err != nil {
			return err
		}
	}

	return nil
}

// kindEnvUpdate inspects the service's on-disk agent.yaml (when present) and
// stamps env vars that signal the agent kind -- today ENABLE_HOSTED_AGENTS=true
// and ENABLE_CAPABILITY_HOST=false for `kind: hosted`; every other kind is a
// no-op past the parse.
//
// Tolerates a missing agent.yaml: the bicepless flow lets users declare prompt
// agents inline in azure.yaml, so a missing file short-circuits cleanly here.
// Service-targets that truly need agent.yaml still surface the error where they
// read its contents.
func kindEnvUpdate(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	azdProject *azdext.ProjectConfig,
	svc *azdext.ServiceConfig,
	envName string,
) error {
	// The agent definition is carried inline on the service entry (unified shape)
	// or, for older projects, in a legacy agent.yaml on disk. A missing or
	// unreadable definition is tolerated here: the bicepless inline path lets
	// users declare prompt agents that carry no hosted definition, and service
	// targets that truly need the definition surface the error where they read it.
	_, isHosted, source, err := project.LoadAgentDefinition(svc, azdProject.Path)
	if err != nil {
		// Tolerate only a missing definition: the bicepless inline path lets users
		// declare prompt agents that carry no hosted definition. Validation and
		// path-traversal errors still propagate so a malformed or out-of-tree
		// definition fails fast here.
		if localErr, ok := errors.AsType[*azdext.LocalError](err); ok &&
			localErr.Code == exterrors.CodeAgentDefinitionNotFound {
			log.Printf("[debug] kindEnvUpdate: no agent definition for %s; skipping (inline-agents path)", svc.Name)
			return nil
		}
		return err
	}
	if source.IsLegacy() {
		project.WarnLegacyAgentShape(source)
	}

	if isHosted {
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
	foundryAgentConfig, err := project.LoadServiceTargetAgentConfig(svc)
	if err != nil {
		return fmt.Errorf("failed to parse foundry agent config: %w", err)
	}

	// Resolve the container resources, applying defaults when unset.
	result := &project.ResourceSettings{}
	if foundryAgentConfig.Container != nil && foundryAgentConfig.Container.Resources != nil {
		result.Memory = foundryAgentConfig.Container.Resources.Memory
		result.Cpu = foundryAgentConfig.Container.Resources.Cpu
	}

	// Set default values if zero or empty
	if result.Memory == "" {
		result.Memory = project.DefaultMemory
	}

	if result.Cpu == "" {
		result.Cpu = project.DefaultCpu
	}

	// Persist the resolved container settings back onto the service's inline
	// properties, preserving the agent definition and other config keys.
	if err := project.SetAgentContainerSettings(svc, &project.ContainerSettings{Resources: result}); err != nil {
		return fmt.Errorf("failed to update agent container settings: %w", err)
	}

	// Need to add the service config back to the project for use further down the pipeline
	req := &azdext.AddServiceRequest{Service: svc}

	if _, err := azdClient.Project().AddService(ctx, req); err != nil {
		return fmt.Errorf("adding agent service to project: %w", err)
	}

	return nil
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

// resolveEnvValue resolves ${VAR} references in a string against the azd environment while
// leaving Foundry server-side ${{...}} expressions untouched. See [project.ExpandEnv].
func resolveEnvValue(value string, azdEnv map[string]string) string {
	resolved, err := project.ExpandEnv(value, func(varName string) string {
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
