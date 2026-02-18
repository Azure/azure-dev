// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agentdetect

import (
	"log"
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

// detectAgent performs the actual agent detection.
// Detection is performed in priority order:
// 1. Environment variables (most reliable)
// 2. User agent string (AZURE_DEV_USER_AGENT)
// 3. Parent process inspection (fallback)
func detectAgent() AgentInfo {
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
