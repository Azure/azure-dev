// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/protobuf/types/known/structpb"
)

// PromptAgentInline is a prompt-agent definition carried as flat service-level
// properties on the azure.ai.agent service entry — the same unified shape
// hosted and voice agents use, so every agent kind is authored in one file.
//
// It exists because [agent_yaml.PromptAgent]'s JSON tags are the Foundry wire
// format rather than the azure.yaml format. `memory` is tagged `json:"-"` there
// because the prompt-agent API defines no such field (azd provisions the store
// and injects a memory_search_preview tool instead), so marshaling a
// PromptAgent straight into service properties would silently drop an authored
// memory block. Re-declaring Memory at depth zero shadows the embedded field
// for azure.yaml while leaving the wire type untouched.
type PromptAgentInline struct {
	agent_yaml.PromptAgent

	// Memory shadows the embedded PromptAgent.Memory so the authored block
	// round-trips through azure.yaml.
	Memory *agent_yaml.PromptMemory `json:"memory,omitempty"`
}

// promptAgentToInline projects a PromptAgent into the shape written to
// azure.yaml, moving Memory onto the field that carries a JSON tag.
func promptAgentToInline(pa agent_yaml.PromptAgent) PromptAgentInline {
	memory := pa.Memory
	// Cleared so the inline value has one source of truth for the block.
	pa.Memory = nil
	return PromptAgentInline{PromptAgent: pa, Memory: memory}
}

// toPromptAgent rebuilds an agent_yaml.PromptAgent from the inline definition.
func (d PromptAgentInline) toPromptAgent() agent_yaml.PromptAgent {
	out := d.PromptAgent
	out.Memory = d.Memory
	return out
}

// PromptAgentDefinitionToServiceProperties marshals a PromptAgent (kind:
// prompt) into the inline service-level properties written to azure.yaml.
//
// Prompt agents carry no container, image, or code configuration — the harness
// owns the runtime — so, unlike the container writer, there is no `container`
// block to split out and nothing lands on the core service fields.
func PromptAgentDefinitionToServiceProperties(
	pa agent_yaml.PromptAgent,
) (*structpb.Struct, error) {
	inline := promptAgentToInline(pa)

	defStruct, err := MarshalStruct(&inline)
	if err != nil {
		return nil, fmt.Errorf("marshaling prompt agent definition: %w", err)
	}

	return defStruct, nil
}

// PromptAgentFromResolvedService resolves a prompt agent definition from a
// service entry's inline (preferred) or legacy config properties. It returns the
// parsed PromptAgent and whether a prompt definition was found. Definitions of
// another kind — and services carrying none — return found=false with no error
// so callers fall through to the file-based path unchanged.
//
// File includes are expanded by resolveServiceProps before the kind is read, so
// a service whose definition lives behind `$ref:` resolves here too.
func PromptAgentFromResolvedService(
	svc *azdext.ServiceConfig,
	projectRoot string,
) (agent_yaml.PromptAgent, bool, error) {
	candidates := []*structpb.Struct{
		svc.GetAdditionalProperties(),
		svc.GetConfig(),
	}
	for _, props := range candidates {
		if props == nil || len(props.GetFields()) == 0 {
			continue
		}
		resolved, err := resolveServiceProps(props, svc.GetName(), projectRoot)
		if err != nil {
			return agent_yaml.PromptAgent{}, false, err
		}
		if !structHasKind(resolved) {
			continue
		}
		if !strings.EqualFold(structKind(resolved), string(agent_yaml.AgentKindPrompt)) {
			// A definition is present but it is not a prompt agent.
			return agent_yaml.PromptAgent{}, false, nil
		}

		// The authored blocks are checked before the decode so a typo is
		// reported as a typo. UnmarshalYAML never runs on this route: core azd
		// parsed azure.yaml and handed the properties over as protobuf.
		if err := agent_yaml.ValidateInlinePromptAgent(resolved.AsMap()); err != nil {
			return agent_yaml.PromptAgent{}, false, exterrors.Validation(
				exterrors.CodeInvalidAgentManifest,
				fmt.Sprintf("agent %q is not a valid prompt agent: %s", svc.GetName(), err),
				"correct the agent definition on the service entry in azure.yaml",
			)
		}

		var inline PromptAgentInline
		if err := UnmarshalStruct(resolved, &inline); err != nil {
			return agent_yaml.PromptAgent{}, false, exterrors.Validation(
				exterrors.CodeInvalidAgentManifest,
				fmt.Sprintf("prompt agent service config is not valid: %s", err),
				"re-run `azd ai agent init` to regenerate the agent service entry",
			)
		}
		return inline.toPromptAgent(), true, nil
	}

	return agent_yaml.PromptAgent{}, false, nil
}
