// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"
	"testing"
)

func TestPromptManagedAgentInstructionsFlagWins(t *testing.T) {
	flags := &initFlags{instructions: " flag instructions "}

	got, err := promptManagedAgentInstructions(t.Context(), nil, flags)
	if err != nil {
		t.Fatalf("promptManagedAgentInstructions: %v", err)
	}
	if got != "flag instructions" {
		t.Errorf("instructions: got %q", got)
	}
}

// Instructions are written inline into the azure.yaml service, so a scaffold
// with nothing authored still produces a deployable agent rather than an empty prompt.
func TestPromptScaffoldInstructions_DefaultsWhenBlank(t *testing.T) {
	if got := promptScaffoldInstructions("   "); got != "You are a helpful AI assistant." {
		t.Errorf("default instructions: got %q", got)
	}
	if got := promptScaffoldInstructions(" authored \n"); got != "authored" {
		t.Errorf("authored instructions: got %q", got)
	}
}

func TestPromptManagedAgentDescriptionReferencesAzureYaml(t *testing.T) {
	promptServer := &helpersPromptServer{promptValue: "summary"}
	client := newHelpersTestAzdClient(
		t,
		&helpersProjectServer{},
		promptServer,
	)

	got, err := promptManagedAgentDescription(t.Context(), client, &initFlags{})
	if err != nil {
		t.Fatalf("promptManagedAgentDescription: %v", err)
	}
	if got != "summary" {
		t.Fatalf("description: got %q", got)
	}
	help := promptServer.lastPrompt.GetOptions().GetHelpMessage()
	if !strings.Contains(help, "agent service in azure.yaml") {
		t.Errorf("help does not reference azure.yaml service: %q", help)
	}
	if strings.Contains(help, "agent.yaml") {
		t.Errorf("help references legacy agent.yaml: %q", help)
	}
}
