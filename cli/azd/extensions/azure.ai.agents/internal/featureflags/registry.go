// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore featureflags

package featureflags

// Every agents preview feature is declared here and listed by All.
var (
	// ManagedHarness controls prompt agents using a Copilot harness.
	ManagedHarness = Feature{
		ID:          "agents.managed_harness",
		EnvVar:      "AZD_ALPHA_ENABLE_AGENTS_MANAGED_HARNESS",
		Description: "Prompt agents that run on a Copilot harness",
	}
	// Voice controls managed voice and hosted voice agents.
	Voice = Feature{
		ID:          "agents.voice",
		EnvVar:      "AZD_ALPHA_ENABLE_AGENTS_VOICE",
		Description: "Voice, prompt-voice, and hosted voice agents",
	}
	// A2A controls init selection, explicit A2A wire versions, and the 1.0 default.
	A2A = Feature{
		ID:          "agents.a2a",
		EnvVar:      "AZD_ALPHA_ENABLE_AGENTS_A2A",
		Description: "A2A init selection, wire version selection, and the 1.0 message default",
	}
	// LongRunning controls starting background Responses invocations.
	LongRunning = Feature{
		ID:          "agents.long_running",
		EnvVar:      "AZD_ALPHA_ENABLE_AGENTS_LONG_RUNNING",
		Description: "Long-running Responses invocations",
	}
)

// All returns every declared feature in a stable order.
// The slice is a fresh copy, so callers may change it.
func All() []Feature {
	return []Feature{ManagedHarness, Voice, LongRunning, A2A}
}

// Lookup returns the declared feature with the given ID.
func Lookup(id string) (Feature, bool) {
	for _, feature := range All() {
		if feature.ID == id {
			return feature, true
		}
	}
	return Feature{}, false
}
