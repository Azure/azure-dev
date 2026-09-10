// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRootCommand_PublicPreviewCommandsVisible(t *testing.T) {
	cmd := NewRootCommand()

	var visible []string
	for _, sub := range cmd.Commands() {
		if !sub.Hidden {
			visible = append(visible, sub.Name())
		}
	}

	for _, name := range []string{
		"add",
		"code",
		"delete",
		"deploy",
		"doctor",
		"endpoint",
		"eval",
		"files",
		"init",
		"invoke",
		"monitor",
		"optimize",
		"pack",
		"publish",
		"run",
		"sample",
		"sessions",
		"show",
	} {
		if !slices.Contains(visible, name) {
			t.Fatalf("expected visible root subcommand %q in %v", name, visible)
		}
	}
}

func TestVoicePublicPreviewHelp(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, tt := range []struct {
		name     string
		path     []string
		contains []string
		absent   []string
	}{
		{
			name: "root",
			contains: []string{
				"Ship hosted and voice agents", "Initialize a new hosted or voice agent project",
				"Show the status of a hosted or voice agent", "Delete a hosted or voice agent",
			},
		},
		{
			name: "init", path: []string{"init"},
			contains: []string{
				"--kind", "--voice", "--kind prompt-voice", "azure.yaml", "azd provision", "azd deploy",
				"managed", "self_deployed", "hosted_agent", "targetAgent", "audio input/output",
				"structured inputs", "tools", "greeting", "avatar", "handoff", "telephony", "acs", "twilio",
				"For existing voice services, edit azure.yaml",
			},
			absent: []string{"New voice services use kind: voice", "adopted manifest"},
		},
		{
			name: "deploy", path: []string{"deploy"},
			contains: []string{"hosted source-code deployment only", "azure.yaml", "azd provision", "azd deploy"},
			absent:   []string{"Deploy a hosted or voice agent"},
		},
		{
			name: "invoke", path: []string{"invoke"},
			contains: []string{
				"Microsoft Foundry portal", "https://ai.azure.com", "prompt voice agents", "hosted voice wrappers",
				"Voice Live client", "voice WebSocket endpoint", "HTTP-based",
			},
		},
		{
			name: "show", path: []string{"show"},
			contains: []string{"hosted or voice agent"},
		},
		{
			name: "delete", path: []string{"delete"},
			contains: []string{"hosted or voice agent", "telephony bindings", "does not guarantee binding cleanup"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := NewRootCommand()
			var buf bytes.Buffer
			root.SetOut(&buf)
			root.SetErr(&buf)
			command, _, err := root.Find(tt.path)
			require.NoError(t, err)
			require.False(t, command.Hidden)
			require.NoError(t, command.Help())
			text := strings.Join(strings.Fields(buf.String()), " ")
			for _, want := range tt.contains {
				require.Contains(t, text, want)
			}
			for _, unwanted := range tt.absent {
				require.NotContains(t, text, unwanted)
			}
			require.NotContains(t, strings.ToLower(text), "private preview")
		})
	}
}
