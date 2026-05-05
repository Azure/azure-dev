// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"context"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// AiAgentHost is the azure.yaml host value for an agent service.
const AiAgentHost = "azure.ai.agent"

// AiToolboxHost is the azure.yaml host value for a toolbox service.
const AiToolboxHost = "azure.ai.toolbox"

// projectEndpointEnvKey is the azd environment key holding the
// Foundry project endpoint URL.
const projectEndpointEnvKey = "AZURE_AI_PROJECT_ENDPOINT"

// AssembleState reads the current azd project + environment and
// produces a *State for resolvers to consume.
//
// Missing or partial data is not an error — AssembleState returns the
// best-effort state it can build. A non-nil error is only returned
// when the underlying RPC fails in a way that gives no usable
// information (for example, the azd project service is unreachable).
//
// Callers that need network probes (OpenAPI availability, Foundry
// agent status, etc.) should perform those separately and pass the
// results into the appropriate resolver.
func AssembleState(ctx context.Context, azdClient *azdext.AzdClient) (*State, error) {
	if azdClient == nil {
		return nil, fmt.Errorf("nil azd client")
	}

	state := &State{}

	projectResp, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err == nil && projectResp != nil && projectResp.Project != nil {
		state.HasAzureYaml = true
		populateServices(state, projectResp.Project)
	}

	if envName, ok := currentEnvName(ctx, azdClient); ok {
		state.EnvName = envName
		if v, ok := lookupEnvValue(ctx, azdClient, envName, projectEndpointEnvKey); ok && v != "" {
			state.HasProjectEndpoint = true
			state.ProjectEndpoint = v
		}
		populateDeployedAgents(ctx, azdClient, state, envName)
	}

	return state, nil
}

// populateServices walks azure.yaml services and fills in
// AgentServices and ToolboxNames.
func populateServices(state *State, project *azdext.ProjectConfig) {
	for _, svc := range project.Services {
		if svc == nil {
			continue
		}
		switch svc.Host {
		case AiAgentHost:
			state.AgentServices = append(state.AgentServices, ServiceState{
				ServiceName: svc.Name,
			})
		case AiToolboxHost:
			state.ToolboxNames = append(state.ToolboxNames, svc.Name)
		}
	}
	state.HasToolboxes = len(state.ToolboxNames) > 0
}

// populateDeployedAgents reads AGENT_<KEY>_NAME / VERSION / ENDPOINT
// for every agent service and updates the corresponding ServiceState.
func populateDeployedAgents(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	state *State,
	envName string,
) {
	for i := range state.AgentServices {
		svc := &state.AgentServices[i]
		key := ToServiceKey(svc.ServiceName)
		if v, ok := lookupEnvValue(ctx, azdClient, envName, "AGENT_"+key+"_NAME"); ok && v != "" {
			svc.DeployedName = v
			svc.IsDeployed = true
		}
		if v, ok := lookupEnvValue(ctx, azdClient, envName, "AGENT_"+key+"_VERSION"); ok && v != "" {
			svc.DeployedVersion = v
		}
		if v, ok := lookupEnvValue(ctx, azdClient, envName, "AGENT_"+key+"_ENDPOINT"); ok && v != "" {
			svc.Endpoint = v
		}
	}
}

// currentEnvName retrieves the current azd environment name. Returns
// "", false when no environment is selected.
func currentEnvName(ctx context.Context, azdClient *azdext.AzdClient) (string, bool) {
	resp, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil || resp == nil || resp.Environment == nil {
		return "", false
	}
	return resp.Environment.Name, true
}

// lookupEnvValue reads a single key from the named azd environment.
// Returns the value and true when found, or "", false otherwise.
func lookupEnvValue(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	envName, key string,
) (string, bool) {
	if envName == "" || key == "" {
		return "", false
	}
	resp, err := azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envName,
		Key:     key,
	})
	if err != nil || resp == nil {
		return "", false
	}
	return resp.Value, resp.Value != ""
}

// ToServiceKey converts a service name into the env var key segment
// (uppercase, with hyphens and spaces replaced by underscores). It
// mirrors the helper in cmd/helpers.go and is exported so other
// packages can derive the AGENT_<KEY>_* keys without re-implementing
// the rule.
func ToServiceKey(serviceName string) string {
	key := strings.ReplaceAll(serviceName, " ", "_")
	key = strings.ReplaceAll(key, "-", "_")
	return strings.ToUpper(key)
}
