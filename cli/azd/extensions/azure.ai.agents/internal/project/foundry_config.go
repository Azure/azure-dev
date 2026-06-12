// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"
)

// FoundryHost is the azure.yaml service host kind for a unified Foundry project.
// A single service with this host owns the Foundry project and all of its
// data-plane state (deployments, connections, toolboxes, skills, routines, and
// agents) declared as top-level service properties (design spec #8590 §2.1).
const FoundryHost = "microsoft.foundry"

// Agent kind discriminators for a FoundryAgent.
const (
	foundryAgentKindHosted = "hosted"
	foundryAgentKindPrompt = "prompt"
)

// FoundryProjectConfig is the typed view of a `host: microsoft.foundry` service
// entry. The keys arrive on ServiceConfig.AdditionalProperties (the inline map
// captured by yaml:",inline" in azd core) rather than under `config:`.
//
// Only `endpoint` and `agents` are typed here; the remaining project-scoped
// arrays are retained as raw maps because this foundation does not yet reconcile
// them (deferred to the data-plane reconcile work). They are kept on the struct
// so binding does not silently drop them.
type FoundryProjectConfig struct {
	Endpoint    string           `json:"endpoint,omitempty"`
	Deployments []map[string]any `json:"deployments,omitempty"`
	Connections []map[string]any `json:"connections,omitempty"`
	Toolboxes   []map[string]any `json:"toolboxes,omitempty"`
	Skills      []map[string]any `json:"skills,omitempty"`
	Routines    []map[string]any `json:"routines,omitempty"`
	Agents      []FoundryAgent   `json:"agents,omitempty"`
}

// FoundryAgent is the union of a hosted agent and a prompt agent, matching
// Agent.json. A hosted agent carries exactly one deploy mode (`docker`,
// `runtime`, or a prebuilt `image`); a prompt agent carries `instructions`.
type FoundryAgent struct {
	// Ref holds a `$ref` file include. Resolving includes is deferred to the
	// $ref resolver work (#8627); this foundation rejects unresolved refs.
	Ref string `json:"$ref,omitempty"`

	Name        string            `json:"name,omitempty"`
	Kind        string            `json:"kind,omitempty"`
	Description string            `json:"description,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Toolboxes   []string          `json:"toolboxes,omitempty"`
	Tools       []map[string]any  `json:"tools,omitempty"`
	Skill       string            `json:"skill,omitempty"`
	Metadata    map[string]any    `json:"metadata,omitempty"`

	// Hosted-agent fields.
	Protocols      []AgentProtocol `json:"protocols,omitempty"`
	Project        string          `json:"project,omitempty"`
	Image          string          `json:"image,omitempty"`
	Docker         *AgentDocker    `json:"docker,omitempty"`
	Runtime        *AgentRuntime   `json:"runtime,omitempty"`
	StartupCommand string          `json:"startupCommand,omitempty"`
	Container      *AgentContainer `json:"container,omitempty"`

	// Prompt-agent fields.
	Instructions string `json:"instructions,omitempty"`
}

// AgentProtocol is a single protocol/version pair a hosted agent implements.
type AgentProtocol struct {
	Protocol string `json:"protocol"`
	Version  string `json:"version"`
}

// AgentDocker holds container build options for a hosted agent (container mode).
type AgentDocker struct {
	Path        string `json:"path,omitempty"`
	RemoteBuild bool   `json:"remoteBuild,omitempty"`
}

// AgentRuntime holds the code-deploy runtime stack for a hosted agent.
type AgentRuntime struct {
	Stack       string `json:"stack,omitempty"`
	Version     string `json:"version,omitempty"`
	RemoteBuild bool   `json:"remoteBuild,omitempty"`
}

// AgentContainer holds container runtime settings (CPU/memory) for a hosted agent.
type AgentContainer struct {
	Resources *ResourceSettings `json:"resources,omitempty"`
}

// deployMode identifies how a hosted agent is built and deployed.
type deployMode int

const (
	deployModeNone deployMode = iota
	deployModeImage
	deployModeRuntime
	deployModeDocker
)

// deployMode reports the single deploy mode declared on a hosted agent. A hosted
// agent must declare exactly one of `image`, `runtime`, or `docker`; validation
// (see validateHostedAgent) rejects zero or more than one.
func (a FoundryAgent) deployMode() deployMode {
	switch {
	case a.Docker != nil:
		return deployModeDocker
	case a.Runtime != nil:
		return deployModeRuntime
	case a.Image != "":
		return deployModeImage
	default:
		return deployModeNone
	}
}

// modeCount returns how many deploy modes are declared, used to enforce mutual
// exclusivity.
func (a FoundryAgent) modeCount() int {
	count := 0
	if a.Docker != nil {
		count++
	}
	if a.Runtime != nil {
		count++
	}
	if a.Image != "" {
		count++
	}
	return count
}

// Validate checks the Foundry project config for the subset this foundation
// supports: a single hosted agent with exactly one deploy mode. Multi-agent
// fan-out, prompt agents, and data-plane reconcile are intentionally out of
// scope and rejected with actionable errors.
func (c *FoundryProjectConfig) Validate() (FoundryAgent, error) {
	if len(c.Agents) == 0 {
		return FoundryAgent{}, exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			"no agents defined on the microsoft.foundry service",
			"add an agent under the service 'agents:' array in azure.yaml",
		)
	}

	if len(c.Agents) > 1 {
		return FoundryAgent{}, exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("the microsoft.foundry service declares %d agents; "+
				"multiple agents per service are not yet supported", len(c.Agents)),
			"declare a single agent in 'agents:' for now; multi-agent fan-out is coming in a later release",
		)
	}

	agent := c.Agents[0]
	if agent.Ref != "" {
		return FoundryAgent{}, exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			"agents declared via '$ref' are not yet supported",
			"inline the agent definition under 'agents:' in azure.yaml",
		)
	}

	if err := validateAgent(agent); err != nil {
		return FoundryAgent{}, err
	}

	return agent, nil
}

// validateAgent validates a single agent's kind and deploy mode.
func validateAgent(agent FoundryAgent) error {
	if agent.Name == "" {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			"agent is missing a 'name'",
			"set a 'name' on the agent in azure.yaml",
		)
	}

	switch agent.Kind {
	case foundryAgentKindHosted:
		return validateHostedAgent(agent)
	case foundryAgentKindPrompt:
		return exterrors.Validation(
			exterrors.CodeUnsupportedAgentKind,
			"prompt agents are not yet supported by the microsoft.foundry service target",
			"use a hosted agent (kind: hosted) for now",
		)
	case "":
		return exterrors.Validation(
			exterrors.CodeMissingAgentKind,
			fmt.Sprintf("agent %q is missing a 'kind'", agent.Name),
			"set 'kind: hosted' on the agent in azure.yaml",
		)
	default:
		return exterrors.Validation(
			exterrors.CodeUnsupportedAgentKind,
			fmt.Sprintf("agent %q has unsupported kind %q", agent.Name, agent.Kind),
			"use a supported kind: 'hosted'",
		)
	}
}

// validateHostedAgent enforces exactly one deploy mode and the project
// requirement for build-based modes.
func validateHostedAgent(agent FoundryAgent) error {
	switch n := agent.modeCount(); {
	case n == 0:
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("hosted agent %q has no deploy mode", agent.Name),
			"set exactly one of 'image', 'runtime', or 'docker' on the agent",
		)
	case n > 1:
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("hosted agent %q declares more than one deploy mode", agent.Name),
			"set exactly one of 'image', 'runtime', or 'docker' on the agent",
		)
	}

	switch agent.deployMode() {
	case deployModeRuntime:
		if agent.Project == "" {
			return exterrors.Validation(
				exterrors.CodeInvalidServiceConfig,
				fmt.Sprintf("hosted agent %q sets 'runtime' but is missing 'project'", agent.Name),
				"set 'project' to the agent source directory (relative to azure.yaml)",
			)
		}
		switch agent.Runtime.Stack {
		case "python", "dotnet":
			// supported by the code-deploy packaging + runtime command path
		case "":
			return exterrors.Validation(
				exterrors.CodeInvalidServiceConfig,
				fmt.Sprintf("hosted agent %q sets 'runtime' but is missing 'stack'", agent.Name),
				"set 'runtime.stack' to 'python' or 'dotnet'",
			)
		default:
			return exterrors.Validation(
				exterrors.CodeUnsupportedAgentKind,
				fmt.Sprintf("hosted agent %q uses runtime stack %q, which is not supported yet",
					agent.Name, agent.Runtime.Stack),
				"use a 'python' or 'dotnet' runtime stack, or a prebuilt 'image', for now",
			)
		}
		if agent.StartupCommand == "" {
			return exterrors.Validation(
				exterrors.CodeInvalidServiceConfig,
				fmt.Sprintf("hosted agent %q sets 'runtime' but is missing 'startupCommand'", agent.Name),
				"set 'startupCommand' (e.g., 'python main.py') so the entry point can be resolved",
			)
		}
	case deployModeDocker:
		return exterrors.Validation(
			exterrors.CodeUnsupportedAgentKind,
			fmt.Sprintf("hosted agent %q uses 'docker' build, which the microsoft.foundry "+
				"service target does not support yet", agent.Name),
			"use a prebuilt 'image' or a code-deploy 'runtime' for now; "+
				"container builds land with per-agent build support",
		)
	}

	return nil
}

// toContainerAgent converts a validated hosted FoundryAgent into the
// agent_yaml.ContainerAgent shape the existing deploy machinery consumes, so the
// CreateAgentVersion request can be built with agent_yaml.CreateAgentAPIRequestFromDefinition.
func (a FoundryAgent) toContainerAgent() (agent_yaml.ContainerAgent, error) {
	ca := agent_yaml.ContainerAgent{
		AgentDefinition: agent_yaml.AgentDefinition{
			Kind: agent_yaml.AgentKindHosted,
			Name: a.Name,
		},
		Image: a.Image,
	}

	if a.Description != "" {
		desc := a.Description
		ca.Description = &desc
	}
	if len(a.Metadata) > 0 {
		meta := a.Metadata
		ca.Metadata = &meta
	}

	for _, p := range a.Protocols {
		ca.Protocols = append(ca.Protocols, agent_yaml.ProtocolVersionRecord{
			Protocol: p.Protocol,
			Version:  p.Version,
		})
	}

	if a.deployMode() == deployModeRuntime {
		entryPoint, err := a.codeEntryPoint()
		if err != nil {
			return agent_yaml.ContainerAgent{}, err
		}
		ca.CodeConfiguration = &agent_yaml.CodeConfiguration{
			Runtime:    runtimeString(a.Runtime),
			EntryPoint: entryPoint,
		}
	}

	return ca, nil
}

// runtimeString maps the typed runtime block to the runtime identifier the
// Foundry API expects, e.g. {stack: python, version: "3.13"} -> "python_3_13".
func runtimeString(rt *AgentRuntime) string {
	if rt == nil {
		return ""
	}
	if rt.Version == "" {
		return rt.Stack
	}
	return fmt.Sprintf("%s_%s", rt.Stack, strings.ReplaceAll(rt.Version, ".", "_"))
}

// codeEntryPoint derives the code-deploy entry point from startupCommand by
// stripping a leading runtime command prefix (e.g. "python main.py" -> "main.py").
//
// The Foundry agent schema models code-deploy entry via startupCommand rather
// than an explicit entryPoint field; this derivation is the documented seam if
// the schema later adds an explicit field.
func (a FoundryAgent) codeEntryPoint() (string, error) {
	fields := strings.Fields(a.StartupCommand)
	if len(fields) == 0 {
		return "", exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("hosted agent %q has an empty 'startupCommand'", a.Name),
			"set 'startupCommand' (e.g., 'python main.py')",
		)
	}

	prefix := agent_yaml.RuntimeCmdPrefix(runtimeString(a.Runtime))
	if len(fields) > 1 && fields[0] == prefix {
		return strings.Join(fields[1:], " "), nil
	}

	// No recognizable prefix: treat the whole command as the entry point.
	return strings.Join(fields, " "), nil
}

// resolvedEnv expands the agent's env values, resolving azd ${VAR} references via
// the supplied environment while preserving Foundry ${{...}} expressions verbatim
// (design spec §2.5, shared ExpandEnv helper).
func (a FoundryAgent) resolvedEnv(azdEnv map[string]string) map[string]string {
	if len(a.Env) == 0 {
		return nil
	}
	resolved := make(map[string]string, len(a.Env))
	for k, v := range a.Env {
		expanded, err := ExpandEnv(v, func(name string) string { return azdEnv[name] })
		if err != nil {
			expanded = v
		}
		resolved[k] = expanded
	}
	return resolved
}
