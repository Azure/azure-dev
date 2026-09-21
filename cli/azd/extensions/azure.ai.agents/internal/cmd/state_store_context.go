// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const stateStoreConfigField = "stateStores"

type stateStoreTarget struct {
	AgentContext
	agentKey string
}

// stateStoreTargetFromEndpoint also canonicalizes percent-encoded project names, so
// project-based and explicit-endpoint commands share selection. Protocol and version
// are intentionally excluded, unlike session/conversation context keys.
func stateStoreTargetFromEndpoint(endpoint string) (*stateStoreTarget, error) {
	parsed, err := parseAgentEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(parsed.ProjectEndpoint)
	if err != nil {
		return nil, err
	}
	u.RawPath = ""
	projectEndpoint := u.String()
	return &stateStoreTarget{
		AgentContext: AgentContext{ProjectEndpoint: projectEndpoint, Name: parsed.AgentName},
		agentKey:     normalizeEndpoint(projectEndpoint) + "/agents/" + parsed.AgentName,
	}, nil
}

func resolveStateStoreTarget(
	ctx context.Context, host *azdext.AzdClient, flags *stateStoreFlags,
) (*stateStoreTarget, error) {
	if flags.agentEndpoint != "" {
		return stateStoreTargetFromEndpoint(flags.agentEndpoint)
	}
	info, err := resolveAgentServiceFromProject(ctx, host, flags.agentName, flags.noPrompt,
		withAgentEnvironment(flags.environment))
	if err != nil {
		if ambiguous, ok := errors.AsType[*ambiguousAgentServicesError](err); ok {
			return nil, exterrors.Validation(exterrors.CodeInvalidAgentName,
				"multiple agent services found: "+strings.Join(ambiguous.names, ", "),
				"supply --agent-name <service-name> to select one")
		}
		return nil, err
	}
	if info.AgentName == "" {
		return nil, exterrors.Dependency(exterrors.CodeMissingAgentEnvVars,
			"the selected service has no deployed agent name",
			"deploy the agent or supply --agent-endpoint")
	}
	endpoint := info.ProjectEndpoint
	if endpoint == "" && info.AgentEndpoint != "" {
		if project, _, found := strings.Cut(info.AgentEndpoint, "/agents/"); found {
			endpoint = project
		}
	}
	if endpoint == "" && flags.environment != "" {
		value, err := host.Environment().GetValue(ctx, &azdext.GetEnvRequest{
			EnvName: flags.environment, Key: "FOUNDRY_PROJECT_ENDPOINT",
		})
		if err != nil {
			return nil, exterrors.FromHost(err, exterrors.CodeEnvironmentValuesFailed,
				"reading the selected environment's project endpoint")
		}
		if value == nil || strings.TrimSpace(value.Value) == "" {
			return nil, exterrors.Dependency(exterrors.CodeMissingProjectEndpoint,
				fmt.Sprintf("no Foundry project endpoint in environment %q", flags.environment),
				"set FOUNDRY_PROJECT_ENDPOINT in that environment or supply --agent-endpoint")
		}
		endpoint = value.Value
	}
	if endpoint == "" {
		endpoint, err = resolveAgentEndpoint(ctx, "", "")
		if err != nil {
			return nil, err
		}
	}
	// Only reuse the endpoint parser, not invocation/protocol resolution. A multi-protocol
	// agent has one State Store collection and needs no protocol selection or API call.
	return stateStoreTargetFromEndpoint(strings.TrimRight(endpoint, "/") + "/agents/" +
		url.PathEscape(info.AgentName) + "/endpoint/protocols/invocations")
}

func (a *stateStoreAction) resolveStore(ctx context.Context, explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	name, err := getAgentSpecificContextValue(ctx, a.host, stateStoreConfigField, a.target.agentKey)
	if err != nil {
		if exterrors.IsCancellation(err) {
			return "", exterrors.Cancelled("reading State Store selection cancelled")
		}
		return "", exterrors.Configuration(exterrors.CodeStateStoreSelection,
			"could not read the active State Store selection",
			"repair the stateStores user configuration or supply --store explicitly")
	}
	if name == "" {
		return "", exterrors.Validation(exterrors.CodeStateStoreSelection,
			"no active State Store for this agent",
			"run `azd ai agent state-stores select <store-name>` or supply an explicit store")
	}
	return name, nil
}

func (a *stateStoreAction) selectStore(ctx context.Context, name string) (any, error) {
	if name == "" {
		if a.flags.noPrompt {
			return nil, exterrors.Validation(exterrors.CodeStateStoreSelection,
				"a store name is required with --no-prompt", "provide a name to state-stores select")
		}
		var err error
		name, err = a.pickStore(ctx)
		if err != nil {
			return nil, err
		}
	}
	store, err := a.api.GetStateStore(ctx, a.target.Name, name)
	if err != nil {
		return nil, err
	}
	if err := setAgentSpecificContextValue(ctx, a.host, stateStoreConfigField, a.target.agentKey, name); err != nil {
		if exterrors.IsCancellation(err) {
			return nil, exterrors.Cancelled("saving State Store selection cancelled")
		}
		return nil, exterrors.Configuration(exterrors.CodeStateStoreSelection,
			"could not save the active State Store selection", "check access to the azd user configuration and retry")
	}
	return store, nil
}

func (a *stateStoreAction) pickStore(ctx context.Context) (string, error) {
	options := a.flags.page
	seen := map[string]bool{}
	for {
		page, err := a.api.ListStateStores(ctx, a.target.Name, options)
		if err != nil {
			return "", err
		}
		choices := make([]*azdext.SelectChoice, 0, len(page.Data)+1)
		for _, store := range page.Data {
			choices = append(choices, &azdext.SelectChoice{Label: store.Name, Value: store.Name})
		}
		if page.HasMore {
			if page.LastID == nil || *page.LastID == "" || seen[*page.LastID] {
				return "", fmt.Errorf("invalid State Store pagination cursor")
			}
			choices = append(choices, &azdext.SelectChoice{Label: "Next page", Value: "next-page"})
		}
		if len(choices) == 0 {
			return "", exterrors.Dependency(exterrors.CodeStateStoreSelection,
				"no State Stores available on this page", "create a store using agent code, or select a known store by name")
		}
		selected, err := a.prompt.Select(ctx, &azdext.SelectRequest{Options: &azdext.SelectOptions{
			Message: "Select a State Store", Choices: choices,
		}})
		if err != nil {
			return "", exterrors.FromPrompt(err, "selecting a State Store")
		}
		if selected == nil || selected.Value == nil {
			return "", exterrors.Cancelled("State Store selection cancelled")
		}
		index := int(*selected.Value)
		if index < 0 || index >= len(choices) {
			return "", fmt.Errorf("invalid State Store selection index")
		}
		if index < len(page.Data) {
			if page.Data[index].Name == "" {
				return "", fmt.Errorf("service returned an empty State Store name")
			}
			return page.Data[index].Name, nil
		}
		seen[*page.LastID] = true
		options.After = *page.LastID
	}
}
