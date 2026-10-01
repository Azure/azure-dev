// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// AgentEndpointOperation identifies an endpoint operation whose agent-kind
// support must be validated.
type AgentEndpointOperation string

const (
	// AgentEndpointOperationShow reads endpoint information for one agent.
	AgentEndpointOperationShow AgentEndpointOperation = "show"
	// AgentEndpointOperationUpdate patches hosted-agent endpoint and card configuration.
	AgentEndpointOperationUpdate AgentEndpointOperation = "update"
	// AgentEndpointOperationReport reports callable endpoints through the azd service target.
	AgentEndpointOperationReport AgentEndpointOperation = "report"
)

// ValidateAgentEndpointOperation validates the effective agent definition and
// verifies that its kind supports the requested endpoint operation.
func ValidateAgentEndpointOperation(
	svc *azdext.ServiceConfig,
	projectRoot string,
	operation AgentEndpointOperation,
) (AgentDefinitionValidation, error) {
	validation, err := ValidateAgentServiceDefinition(svc, projectRoot)
	if err != nil {
		return AgentDefinitionValidation{}, err
	}

	supported := false
	switch operation {
	case AgentEndpointOperationShow, AgentEndpointOperationReport:
		supported = validation.Kind == agent_yaml.AgentKindHosted ||
			validation.Kind == agent_yaml.AgentKindPrompt ||
			agent_yaml.IsVoiceAgentKind(validation.Kind)
	case AgentEndpointOperationUpdate:
		supported = validation.Kind == agent_yaml.AgentKindHosted
	default:
		return AgentDefinitionValidation{}, exterrors.Internal(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("unknown agent endpoint operation %q", operation),
		)
	}
	if supported {
		return validation, nil
	}

	return AgentDefinitionValidation{}, unsupportedAgentEndpointOperationError(
		svc,
		validation.Kind,
		operation,
	)
}

func unsupportedAgentEndpointOperationError(
	svc *azdext.ServiceConfig,
	kind agent_yaml.AgentKind,
	operation AgentEndpointOperation,
) error {
	suggestion := "use `azd ai agent show` to inspect this agent; endpoint reporting supports hosted, prompt, and voice agents"
	if operation == AgentEndpointOperationUpdate {
		suggestion = "edit the agent definition in azure.yaml and run `azd deploy`; " +
			"endpoint and card updates apply only to hosted agents"
	}

	return exterrors.Validation(
		exterrors.CodeUnsupportedAgentKind,
		fmt.Sprintf(
			"agent endpoint %s does not support kind %q for service %q",
			operation,
			kind,
			svc.GetName(),
		),
		suggestion,
	)
}
