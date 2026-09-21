// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package agentkind resolves an agent service's declared kind (hosted,
// workflow, prompt-voice, …) from its azure.yaml service definition. It exists
// as a small leaf package so the
// deploy path (project), the endpoint/next-step readers (project, nextstep),
// and any future caller all answer "what kind is this service?" identically —
// without either the project or nextstep package importing the other (project
// imports nextstep, so a shared helper in either would create an import cycle).
package agentkind

import (
	"fmt"
	"os"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
)

// Kind resolves the declared kind from service-level properties, expanding an
// explicit root $ref with the existing Foundry resolver.
// It returns "" when no source declares a kind. An error is returned only when a
// referenced file is present but malformed; callers that treat kind
// detection as best-effort (endpoint/next-step readers) may ignore it, while the
// deploy path propagates it.
func Kind(svc *azdext.ServiceConfig, projectRoot string) (string, error) {
	if value := os.Getenv("AGENT_DEFINITION_PATH"); value != "" {
		return "", exterrors.Validation(
			exterrors.CodeUnsupportedAgentDefinitionPath,
			"AGENT_DEFINITION_PATH is no longer supported for agent runtime configuration",
			"move the agent definition to the azure.ai.agent service in azure.yaml, "+
				"or add a service-level $ref to a direct agent definition",
		)
	}
	if svc != nil && svc.GetHost() == "azure.ai.agent" &&
		svc.GetConfig() != nil && len(svc.GetConfig().GetFields()) > 0 {
		return "", exterrors.Validation(
			exterrors.CodeDeprecatedAgentServiceConfig,
			fmt.Sprintf("service %q uses the unsupported nested config block", svc.GetName()),
			"move the agent definition to service-level properties in azure.yaml, "+
				"or add a service-level $ref to a direct agent definition",
		)
	}
	return entryKind(svc, projectRoot)
}

// IsPromptVoice reports whether the service resolves to kind: prompt-voice.
func IsPromptVoice(svc *azdext.ServiceConfig, projectRoot string) (bool, error) {
	kind, err := Kind(svc, projectRoot)
	if err != nil {
		return false, err
	}
	return agent_yaml.IsVoiceAgentKind(agent_yaml.AgentKind(kind)), nil
}

// IsHosted reports whether the service resolves to kind: hosted.
func IsHosted(svc *azdext.ServiceConfig, projectRoot string) (bool, error) {
	kind, err := Kind(svc, projectRoot)
	if err != nil {
		return false, err
	}
	return kind == string(agent_yaml.AgentKindHosted), nil
}

// entryKind returns the kind declared on the service entry, resolving a `$ref`
// file reference when the kind is not carried directly.
func entryKind(svc *azdext.ServiceConfig, projectRoot string) (string, error) {
	if svc == nil {
		return "", nil
	}
	props := svc.GetAdditionalProperties()
	if props == nil || len(props.GetFields()) == 0 {
		return "", nil
	}
	values := props.AsMap()
	if kind := kindFromMap(values); kind != "" {
		return kind, nil
	}
	if _, hasRef := values["$ref"]; !hasRef {
		return "", nil
	}
	resolved, err := foundry.ResolveFileRefs(values, projectRoot)
	if err != nil {
		return "", err
	}
	return kindFromMap(resolved), nil
}

// kindFromMap reads a trimmed top-level `kind` string from a resolved props map.
func kindFromMap(values map[string]any) string {
	kind, _ := values["kind"].(string)
	return strings.TrimSpace(kind)
}
