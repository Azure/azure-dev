// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package copilot

// Config key constants for the copilot.* namespace in azd user configuration.
// All keys are built from shared prefix constants so renaming any level requires a single change.
const (
	// DisplayTitle is the user-facing brand name for the agent experience.
	// Change this single constant to rebrand across all UI text.
	DisplayTitle = "GitHub Copilot"

	// ConfigRoot is the root namespace for all Copilot agent configuration keys.
	ConfigRoot = "copilot"

	// -- Model --

	// ConfigKeyModelRoot is the root for model configuration.
	ConfigKeyModelRoot = ConfigRoot + ".model"
	// ConfigKeyModelType is the model provider type (e.g., "copilot").
	ConfigKeyModelType = ConfigKeyModelRoot + ".type"
	// ConfigKeyModel is the model name for Copilot agent sessions.
	ConfigKeyModel = ConfigKeyModelRoot

	// -- Tools --

	// ConfigKeyToolsRoot is the root for tool control configuration.
	ConfigKeyToolsRoot = ConfigRoot + ".tools"
	// ConfigKeyToolsAvailable is an allowlist of tools available to the agent.
	ConfigKeyToolsAvailable = ConfigKeyToolsRoot + ".available"
	// ConfigKeyToolsExcluded is a denylist of tools blocked from the agent.
	ConfigKeyToolsExcluded = ConfigKeyToolsRoot + ".excluded"

	// -- Skills --

	// ConfigKeySkillsRoot is the root for skills configuration.
	ConfigKeySkillsRoot = ConfigRoot + ".skills"
	// ConfigKeySkillsDirectories is additional skill directories to load.
	ConfigKeySkillsDirectories = ConfigKeySkillsRoot + ".directories"
	// ConfigKeySkillsDisabled is skills to disable in agent sessions.
	ConfigKeySkillsDisabled = ConfigKeySkillsRoot + ".disabled"

	// -- MCP --

	// ConfigKeyMCPRoot is the root for MCP (Model Context Protocol) configuration.
	ConfigKeyMCPRoot = ConfigRoot + ".mcp"
	// ConfigKeyMCPServers is additional MCP servers to load, merged with built-in servers.
	ConfigKeyMCPServers = ConfigKeyMCPRoot + ".servers"

	// -- Top-level settings --

	// ConfigKeyReasoningEffort is the reasoning effort level (low, medium, high).
	ConfigKeyReasoningEffort = ConfigRoot + ".reasoningEffort"
	// ConfigKeySystemMessage is a custom system message appended to the default prompt.
	ConfigKeySystemMessage = ConfigRoot + ".systemMessage"
	// ConfigKeyConsent is the consent rules configuration for tool execution.
	ConfigKeyConsent = ConfigRoot + ".consent"
	// ConfigKeyLogLevel is the log level for the Copilot SDK client.
	ConfigKeyLogLevel = ConfigRoot + ".logLevel"
	// ConfigKeyMode is the default agent mode (interactive, autopilot, plan).
	ConfigKeyMode = ConfigRoot + ".mode"

	// -- Error Handling --

	// ConfigKeyErrorHandlingRoot is the root for error handling preferences.
	ConfigKeyErrorHandlingRoot = ConfigRoot + ".errorHandling"
	// ConfigKeyErrorHandlingFix controls auto-approval of agent-applied fixes.
	ConfigKeyErrorHandlingFix = ConfigKeyErrorHandlingRoot + ".fix"
	// ConfigKeyErrorHandlingTroubleshootSkip controls skipping error troubleshooting.
	ConfigKeyErrorHandlingTroubleshootSkip = ConfigKeyErrorHandlingRoot + ".troubleshooting.skip"
)
