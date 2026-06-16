// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

// Agent kind discriminators for the unified microsoft.foundry shape.
const (
	foundryKindHosted = "hosted"
	foundryKindPrompt = "prompt"
)

// foundryAgentInput carries the normalized fields needed to build a single
// inline agent for a `host: microsoft.foundry` service entry. It decouples the
// builder from the two init flows (manifest/template and from-code) that feed it.
type foundryAgentInput struct {
	serviceName     string
	agentName       string
	kind            string
	description     string
	projectPath     string
	env             map[string]string
	protocols       []project.AgentProtocol
	containerCPU    string
	containerMemory string

	// Hosted-agent deploy mode. Exactly one path is taken: code-deploy
	// (runtime), a prebuilt image, or a container (docker) build.
	isCodeDeploy   bool
	runtime        string // raw runtime identifier, e.g. "python_3_13"
	entryPoint     string // e.g. "main.py"
	image          string // prebuilt image reference
	startupCommand string // container/docker entry command

	// Prompt-agent instructions (inline text or a path to a prompt file).
	instructions string
}

// buildFoundryServiceConfig builds the azure.yaml service entry for a unified
// `host: microsoft.foundry` service with a single inline agent. The Foundry keys
// (deployments, agents) are carried on AdditionalProperties rather than under
// `config:`, and no service-level `project`/`language` is set — each agent owns
// its own `project` and deploy mode (design spec #8590 §2.1, §2.3, §2.7).
func buildFoundryServiceConfig(
	in foundryAgentInput,
	deployments []project.Deployment,
) (*azdext.ServiceConfig, error) {
	agent := project.FoundryAgent{
		Name:        in.agentName,
		Kind:        in.kind,
		Description: in.description,
		Env:         in.env,
		Protocols:   in.protocols,
	}

	if in.kind == foundryKindPrompt {
		agent.Instructions = in.instructions
	} else {
		agent.Kind = foundryKindHosted
		agent.Project = in.projectPath

		switch {
		case in.isCodeDeploy:
			stack, version := splitRuntime(in.runtime)
			agent.Runtime = &project.AgentRuntime{Stack: stack, Version: version}
			agent.StartupCommand = strings.TrimSpace(
				agent_yaml.RuntimeCmdPrefix(in.runtime) + " " + in.entryPoint,
			)
		case in.image != "":
			agent.Image = in.image
		default:
			// Container build. The microsoft.foundry service target does not
			// deploy docker builds yet (only runtime/image); the entry is still
			// written so the unified shape is faithful and a later release can
			// deploy it without re-running init.
			agent.Docker = &project.AgentDocker{Path: "Dockerfile", RemoteBuild: true}
			agent.StartupCommand = in.startupCommand
		}

		if in.containerCPU != "" || in.containerMemory != "" {
			agent.Container = &project.AgentContainer{
				Resources: &project.ResourceSettings{
					Cpu:    in.containerCPU,
					Memory: in.containerMemory,
				},
			}
		}
	}

	props := project.FoundryServiceProperties{
		Deployments: deployments,
		Agents:      []project.FoundryAgent{agent},
	}
	additional, err := project.MarshalStruct(&props)
	if err != nil {
		return nil, fmt.Errorf("marshaling microsoft.foundry service config: %w", err)
	}

	return &azdext.ServiceConfig{
		Name:                 strings.ReplaceAll(in.serviceName, " ", ""),
		Host:                 project.FoundryHost,
		AdditionalProperties: additional,
	}, nil
}

// splitRuntime splits a runtime identifier like "python_3_13" into its stack
// ("python") and dotted version ("3.13"), the inverse of the service target's
// runtimeString. A bare stack ("python") yields an empty version.
func splitRuntime(runtime string) (stack string, version string) {
	if runtime == "" {
		return "", ""
	}
	idx := strings.Index(runtime, "_")
	if idx < 0 {
		return runtime, ""
	}
	return runtime[:idx], strings.ReplaceAll(runtime[idx+1:], "_", ".")
}

// envVarsToMap converts the manifest's environment-variable list into the map
// shape a FoundryAgent's `env` uses. Returns nil when there is nothing to map.
func envVarsToMap(vars *[]agent_yaml.EnvironmentVariable) map[string]string {
	if vars == nil || len(*vars) == 0 {
		return nil
	}
	out := make(map[string]string, len(*vars))
	for _, v := range *vars {
		out[v.Name] = v.Value
	}
	return out
}

// protocolsToFoundry maps the manifest protocol records onto the FoundryAgent
// protocol shape. Returns nil when there is nothing to map.
func protocolsToFoundry(records []agent_yaml.ProtocolVersionRecord) []project.AgentProtocol {
	if len(records) == 0 {
		return nil
	}
	out := make([]project.AgentProtocol, 0, len(records))
	for _, r := range records {
		out = append(out, project.AgentProtocol{Protocol: r.Protocol, Version: r.Version})
	}
	return out
}

// ensureAgentIgnore writes a default .agentignore into targetDir when one does
// not already exist, so code-deploy packaging excludes the right files. The
// unified microsoft.foundry shape no longer emits agent.yaml, but .agentignore
// is still relevant to the agent source directory.
func ensureAgentIgnore(targetDir string) error {
	agentIgnorePath := filepath.Join(targetDir, ".agentignore")
	if _, err := os.Stat(agentIgnorePath); os.IsNotExist(err) {
		if err := os.WriteFile(
			agentIgnorePath, []byte(project.DefaultAgentIgnoreContent()), osutil.PermissionFile,
		); err != nil {
			return fmt.Errorf("writing .agentignore: %w", err)
		}
	}
	return nil
}
