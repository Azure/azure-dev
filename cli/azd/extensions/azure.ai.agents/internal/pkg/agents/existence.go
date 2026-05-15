// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agents

import (
	"context"
	"errors"
	"net/http"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// AgentChecker can look up a named agent in a Foundry project.
type AgentChecker interface {
	GetAgent(ctx context.Context, agentName, apiVersion string) (*agent_api.AgentObject, error)
}

// AgentExists reports whether an agent with the given name exists in the Foundry project.
// It returns false (without error) when the API returns 404, and an error for any other failure.
func AgentExists(ctx context.Context, client AgentChecker, agentName, apiVersion string) (bool, error) {
	_, err := client.GetAgent(ctx, agentName, apiVersion)
	if err == nil {
		return true, nil
	}

	if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok &&
		respErr.StatusCode == http.StatusNotFound {
		return false, nil
	}

	return false, err
}

// ExistingAgentWarning returns the formatted warning message shown to the user when
// an agent with the given name already exists in the Foundry project.
func ExistingAgentWarning(agentName string) string {
	return output.WithWarningFormat(
		"An agent named '%s' already exists in this Foundry project. "+
			"Deploying with this name will create a new version of the existing agent, not a separate agent.\n",
		agentName,
	)
}
