// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import "testing"

func TestPromptManagedAgentInstructionsFlagWins(t *testing.T) {
	flags := &initFlags{instructions: " flag instructions "}
	manifest := &promptAgentManifest{}
	manifest.definition.Instructions = "manifest instructions"

	got, err := promptManagedAgentInstructions(t.Context(), nil, flags, manifest)
	if err != nil {
		t.Fatalf("promptManagedAgentInstructions: %v", err)
	}
	if got != "flag instructions" {
		t.Errorf("instructions: got %q", got)
	}
}

// Instructions are written inline into agent.yaml, so a scaffold with nothing
// authored still produces a deployable agent rather than an empty prompt.
func TestPromptScaffoldInstructions_DefaultsWhenBlank(t *testing.T) {
	if got := promptScaffoldInstructions("   "); got != "You are a helpful AI assistant." {
		t.Errorf("default instructions: got %q", got)
	}
	if got := promptScaffoldInstructions(" authored \n"); got != "authored" {
		t.Errorf("authored instructions: got %q", got)
	}
}
