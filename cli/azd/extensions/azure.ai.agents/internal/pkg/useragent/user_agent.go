// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package useragent builds User-Agent values for the azure.ai.agents extension.
package useragent

import (
	"os"
	"strings"

	"azureaiagent/internal/version"
)

const (
	azureDevUserAgentEnv  = "AZURE_DEV_USER_AGENT"
	foundrySkillEnvValue  = "microsoft_foundry_skill"
	foundrySkillUserAgent = "microsoft-foundry-skill/1.0"
	defaultUserAgent      = "azd-ext-azure-ai-agents"
	connectionUserAgent   = "azd-ext-azure-ai-connection/0.1.0"
	doctorUserAgent       = "azd-ai-agent-doctor"
)

// Default returns the default azure.ai.agents extension User-Agent value.
func Default() string {
	return withExecutionEnv(defaultUserAgent + "/" + version.Version)
}

// Connection returns the User-Agent for Foundry connection requests.
func Connection() string {
	return withExecutionEnv(connectionUserAgent)
}

// Doctor returns the User-Agent for Foundry doctor requests.
func Doctor() string {
	return withExecutionEnv(doctorUserAgent)
}

func withExecutionEnv(userAgent string) string {
	return withSkill(userAgent)
}

func withSkill(userAgent string) string {
	if !strings.Contains(os.Getenv(azureDevUserAgentEnv), foundrySkillEnvValue) {
		return userAgent
	}
	if userAgent == "" {
		return foundrySkillUserAgent
	}
	return userAgent + "," + foundrySkillUserAgent
}
