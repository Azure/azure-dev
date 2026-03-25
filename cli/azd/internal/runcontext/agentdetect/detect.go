// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agentdetect

import (
	"log"
	"os"
	"sync"
)

var (
	cachedAgent AgentInfo
	detectOnce  sync.Once
)

// GetCallingAgent detects if azd was invoked by a known AI coding agent.
// The result is cached after the first call.
func GetCallingAgent() AgentInfo {
	detectOnce.Do(func() {
		cachedAgent = detectAgent()
		if cachedAgent.Detected {
			log.Printf("Agent detection result: detected=%t, agent=%s, source=%s, details=%s",
				cachedAgent.Detected, cachedAgent.Name, cachedAgent.Source, cachedAgent.Details)
		} else {
			log.Printf("Agent detection result: detected=%t, no AI coding agent detected",
				cachedAgent.Detected)
		}
	})
	return cachedAgent
}

// IsRunningInAgent returns true if azd was invoked by a known AI coding agent.
func IsRunningInAgent() bool {
	return GetCallingAgent().Detected
}

// DisableAgentDetectEnvVar is the environment variable that, when set to any non-empty value,
// disables all agent detection. This is used by functional tests that spawn azd as a child
// process and need to test interactive prompt behavior without the parent process tree
// (which may include an AI agent like Copilot CLI) triggering no-prompt mode.
const DisableAgentDetectEnvVar = "AZD_DISABLE_AGENT_DETECT"

// detectAgent performs the actual agent detection.
// Detection is performed in priority order:
// 0. AZD_DISABLE_AGENT_DETECT env var (test override — skips all detection)
// 1. Environment variables (most reliable)
// 2. User agent string (AZURE_DEV_USER_AGENT)
// 3. Parent process inspection (fallback)
func detectAgent() AgentInfo {
	// Allow tests and tooling to suppress agent detection entirely.
	if v := os.Getenv(DisableAgentDetectEnvVar); v != "" {
		log.Printf("Agent detection disabled via %s=%s", DisableAgentDetectEnvVar, v)
		return NoAgent()
	}

	// Try environment variable detection first (most reliable)
	if agent := detectFromEnvVars(); agent.Detected {
		return agent
	}

	// Try user agent string detection
	if agent := detectFromUserAgent(); agent.Detected {
		return agent
	}

	// Try parent process detection as fallback
	if agent := detectFromParentProcess(); agent.Detected {
		return agent
	}

	return NoAgent()
}

// ResetDetection clears the cached detection result.
// This is primarily useful for testing.
func ResetDetection() {
	detectOnce = sync.Once{}
	cachedAgent = AgentInfo{}
}
