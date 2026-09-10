// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package agentkind resolves an agent service's declared kind (hosted,
// workflow, prompt-voice, …) from the same sources, in the same precedence
// order, that the deploy path uses. It exists as a small leaf package so the
// deploy path (project), the endpoint/next-step readers (project, nextstep),
// and any future caller all answer "what kind is this service?" identically —
// without either the project or nextstep package importing the other (project
// imports nextstep, so a shared helper in either would create an import cycle).
package agentkind

import (
	"os"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/paths"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
	"go.yaml.in/yaml/v3"
	"google.golang.org/protobuf/types/known/structpb"
)

// Kind resolves the declared kind for an agent service. Precedence mirrors the
// deploy path (resolveVoiceAgentForDeploy):
//
//  1. overridePath — an explicit definition file (e.g. AGENT_DEFINITION_PATH).
//     When set it wins outright, matching the deploy-time override precedence.
//  2. the inline (preferred) or legacy config kind carried on the service entry,
//     resolving a `$ref` file reference when the kind is not inline.
//  3. the on-disk agent.yaml / agent.yml in the service directory (the legacy
//     shape, where the service entry carries no kind at all).
//
// It returns "" when no source declares a kind. An error is returned only when a
// referenced file or manifest is present but malformed; callers that treat kind
// detection as best-effort (endpoint/next-step readers) may ignore it, while the
// deploy path propagates it.
func Kind(svc *azdext.ServiceConfig, projectRoot, overridePath string) (string, error) {
	if overridePath != "" {
		return fileKind(overridePath)
	}
	if kind, err := entryKind(svc, projectRoot); err != nil || kind != "" {
		return kind, err
	}
	return serviceDirKind(svc, projectRoot)
}

// IsPromptVoice reports whether the service resolves to kind: prompt-voice.
func IsPromptVoice(svc *azdext.ServiceConfig, projectRoot, overridePath string) (bool, error) {
	kind, err := Kind(svc, projectRoot, overridePath)
	if err != nil {
		return false, err
	}
	return agent_yaml.IsVoiceAgentKind(agent_yaml.AgentKind(kind)), nil
}

// IsHosted reports whether the service resolves to kind: hosted.
func IsHosted(svc *azdext.ServiceConfig, projectRoot, overridePath string) (bool, error) {
	kind, err := Kind(svc, projectRoot, overridePath)
	if err != nil {
		return false, err
	}
	return kind == string(agent_yaml.AgentKindHosted), nil
}

// entryKind returns the kind declared inline on the service entry (or in the
// legacy config block), resolving a `$ref` file reference when the kind is not
// carried directly. Returns "" when the entry declares no kind.
func entryKind(svc *azdext.ServiceConfig, projectRoot string) (string, error) {
	if svc == nil {
		return "", nil
	}
	for _, props := range []*structpb.Struct{svc.GetAdditionalProperties(), svc.GetConfig()} {
		if props == nil || len(props.GetFields()) == 0 {
			continue
		}
		values := props.AsMap()
		if kind := kindFromMap(values); kind != "" {
			return kind, nil
		}
		if _, hasRef := values["$ref"]; !hasRef {
			continue
		}
		resolved, err := foundry.ResolveFileRefs(values, projectRoot)
		if err != nil {
			return "", err
		}
		if kind := kindFromMap(resolved); kind != "" {
			return kind, nil
		}
	}
	return "", nil
}

// serviceDirKind returns the kind declared by the service directory's on-disk
// agent.yaml (then agent.yml), or "" when neither is present.
func serviceDirKind(svc *azdext.ServiceConfig, projectRoot string) (string, error) {
	if svc == nil || projectRoot == "" {
		return "", nil
	}
	relativePath := svc.GetRelativePath()
	for _, name := range []string{"agent.yaml", "agent.yml"} {
		manifestPath, err := paths.JoinAllowRoot(projectRoot, relativePath, name)
		if err != nil {
			continue
		}
		if _, err := os.Stat(manifestPath); err != nil {
			continue
		}
		return fileKind(manifestPath)
	}
	return "", nil
}

// fileKind reads the top-level `kind` scalar from a YAML manifest file.
func fileKind(path string) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is validated under the project root by callers
	if err != nil {
		return "", err
	}
	var def struct {
		Kind string `yaml:"kind"`
	}
	if err := yaml.Unmarshal(data, &def); err != nil {
		return "", err
	}
	return strings.TrimSpace(def.Kind), nil
}

// kindFromMap reads a trimmed top-level `kind` string from a resolved props map.
func kindFromMap(values map[string]any) string {
	kind, _ := values["kind"].(string)
	return strings.TrimSpace(kind)
}
