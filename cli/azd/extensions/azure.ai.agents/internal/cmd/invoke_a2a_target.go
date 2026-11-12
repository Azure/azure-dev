// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
)

func (a *InvokeAction) validateA2ATarget(ctx context.Context, rc *remoteContext) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if rc.agent == nil {
		return nil
	}

	enabled := resolveEndpointProtocols(rc.agent.AgentEndpoint)
	var missing []string
	if !slices.Contains(enabled, string(agent_api.AgentEndpointProtocolA2A)) {
		missing = append(missing, string(agent_api.AgentEndpointProtocolA2A))
	}

	// Kind is fixed across versions; native hosted A2A does not require Responses translation.
	definition, _ := rc.agent.Versions.Latest.Definition.(map[string]any)
	kind, _ := definition["kind"].(string)
	if (kind == string(agent_api.AgentKindPrompt) || kind == string(agent_api.AgentKindWorkflow)) &&
		!slices.Contains(enabled, string(agent_api.AgentEndpointProtocolResponses)) {
		missing = append(missing, string(agent_api.AgentEndpointProtocolResponses))
	}
	if len(missing) == 0 {
		return nil
	}

	return exterrors.Compatibility(
		exterrors.CodeAgentEndpointProtocolNotEnabled,
		fmt.Sprintf(
			"the current deployed agent endpoint does not enable required A2A protocols: %s",
			strings.Join(missing, ", "),
		),
		"enable the required public endpoint protocols or invoke through an enabled protocol; "+
			"changing local YAML alone does not update the deployed endpoint",
	)
}
