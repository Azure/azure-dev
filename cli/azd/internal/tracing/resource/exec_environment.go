// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package resource

import (
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/runcontext"
	"github.com/azure/azure-dev/cli/azd/internal/runcontext/agentdetect"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
)

func getExecutionEnvironment() string {
	// calling programs receive the highest priority, since they end up wrapping the CLI and are the most
	// inner layers.
	env := execEnvFromCaller()

	// Check for AI coding agents if no caller was detected via user agent
	if env == "" {
		env = execEnvFromAgent()
	}

	if env == "" {
		// machine-level execution environments
		env = execEnvForHosts()
	}

	if env == "" {
		// machine-level CI execution environments
		env = execEnvForCi()
	}

	// no special execution environment found, default to plain desktop
	if env == "" {
		env = fields.EnvDesktop
	}

	// global modifiers that are applicable to all environments
	modifiers := execEnvModifiers()

	return strings.Join(append([]string{env}, modifiers...), ";")
}

func execEnvFromCaller() string {
	userAgent := os.Getenv(internal.AzdUserAgentEnvVar)

	if strings.Contains(userAgent, internal.VsCodeAzureCopilotAgentPrefix) {
		return fields.EnvVSCodeAzureCopilot
	}

	if strings.Contains(userAgent, internal.VsCodeAgentPrefix) {
		return fields.EnvVisualStudioCode
	}

	if strings.Contains(userAgent, internal.VsAgentPrefix) {
		return fields.EnvVisualStudio
	}

	return ""
}

// execEnvFromAgent detects AI coding agents via the agentdetect package.
func execEnvFromAgent() string {
	agent := agentdetect.GetCallingAgent()
	if !agent.Detected {
		return ""
	}

	// Map agent types to telemetry environment values
	switch agent.Type {
	case agentdetect.AgentTypeClaudeCode:
		return fields.EnvClaudeCode
	case agentdetect.AgentTypeGitHubCopilotCLI:
		return fields.EnvGitHubCopilotCLI
	case agentdetect.AgentTypeVSCodeCopilot:
		return fields.EnvVSCodeAzureCopilot
	case agentdetect.AgentTypeGemini:
		return fields.EnvGemini
	case agentdetect.AgentTypeOpenCode:
		return fields.EnvOpenCode
	default:
		return ""
	}
}

func execEnvForHosts() string {
	if _, ok := os.LookupEnv(runcontext.AzdInCloudShellEnvVar); ok {
		return fields.EnvCloudShell
	}

	// GitHub Codespaces
	// https://docs.github.com/en/codespaces/developing-in-codespaces/default-environment-variables-for-your-codespacei
	if _, ok := os.LookupEnv("CODESPACES"); ok {
		return fields.EnvCodespaces
	}

	return ""
}

func execEnvModifiers() []string {
	modifiers := []string{}
	userAgent := os.Getenv(internal.AzdUserAgentEnvVar)

	if strings.Contains(userAgent, "azure_app_space_portal") {
		modifiers = append(modifiers, fields.EnvModifierAzureSpace)
	}

	return modifiers
}
