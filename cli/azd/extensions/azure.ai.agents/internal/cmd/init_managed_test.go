// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"strings"
	"testing"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
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

func TestResolveDeferredManagedAgentModel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		flags   initFlags
		want    string
		wantErr string
	}{
		{
			name:  "model-deployment takes precedence over model",
			flags: initFlags{modelDeployment: " my-deployment ", model: "gpt-5.4-mini"},
			want:  "my-deployment",
		},
		{
			name:  "model flag used when model-deployment empty",
			flags: initFlags{model: " gpt-5.4-mini "},
			want:  "gpt-5.4-mini",
		},
		{
			name:    "missing both flags fails with invalid_parameter",
			flags:   initFlags{noPrompt: true},
			wantErr: exterrors.CodeInvalidParameter,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolveDeferredManagedAgentModel(&tt.flags)
			if tt.wantErr != "" {
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				require.Equal(t, tt.wantErr, localErr.Code)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
