// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"strings"

	"azureaieval/internal/messages"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

const (
	conversationModeStatic     = "static"
	conversationModeSimulation = "simulation"
)

func (a *initAction) validateConversationFlags(source, level, mode string) error {
	level = strings.ToLower(strings.TrimSpace(level))
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != "" && mode != conversationModeStatic && mode != conversationModeSimulation ||
		a.cmd.Flags().Changed("conversation-mode") && mode == "" {
		return messages.ConversationModeNotAChoice(mode)
	}
	if mode != "" {
		if source != "" && source != initSourceDataset {
			return messages.InitFlagConflict("conversation-mode", "requires --source dataset, not --source "+source)
		}
		if level != "" && level != project.EvaluationLevelConversation {
			return messages.InitFlagConflict("conversation-mode",
				"requires --evaluation-level conversation, not --evaluation-level "+level)
		}
	}
	for _, flag := range []string{"simulation-model", "num-conversations", "max-turns"} {
		if a.cmd.Flags().Changed(flag) && mode != conversationModeSimulation {
			return messages.InitFlagConflict(flag, "requires --conversation-mode simulation")
		}
	}
	if mode == conversationModeStatic && a.cmd.Flags().Changed("target") {
		return messages.InitFlagConflict("target", "cannot be used with --conversation-mode static; no agent is invoked")
	}
	if a.cmd.Flags().Changed("simulation-model") && strings.TrimSpace(a.flags.simulationModel) == "" {
		return messages.SimulationModelRequired()
	}
	for _, bound := range []struct {
		flag                string
		value, minimum, max int
	}{
		{"num-conversations", a.flags.numConversations, project.MinNumConversations, project.MaxNumConversations},
		{"max-turns", a.flags.maxTurns, project.MinSimulationTurns, project.MaxSimulationTurns},
	} {
		if a.cmd.Flags().Changed(bound.flag) && (bound.value < bound.minimum || bound.value > bound.max) {
			return messages.InitFlagRange(bound.flag, bound.value, bound.minimum, bound.max)
		}
	}
	return nil
}

func resolveConversationMode(cmd *cobra.Command, explicit string) (string, error) {
	if explicit != "" {
		return strings.ToLower(strings.TrimSpace(explicit)), nil
	}
	if noPrompt(cmd) {
		return conversationModeStatic, nil
	}
	client, err := azdext.NewAzdClient()
	if err != nil {
		return "", messages.ConnectingToAzd(err)
	}
	defer client.Close()

	labels := messages.ConversationModeChoices()
	modes := []string{conversationModeStatic, conversationModeSimulation}
	resp, err := client.Prompt().Select(commandContext(cmd), &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: messages.SelectConversationModePrompt(),
			Choices: []*azdext.SelectChoice{
				{Label: labels[0], Value: modes[0]},
				{Label: labels[1], Value: modes[1]},
			},
			SelectedIndex:   preselect(0),
			EnableFiltering: filteringFor(len(modes)),
		},
	})
	if err != nil {
		return "", fmt.Errorf("selecting conversation mode: %w", err)
	}
	if resp == nil || resp.Value == nil || int(resp.GetValue()) < 0 || int(resp.GetValue()) >= len(modes) {
		return "", messages.ConversationModeNotAChoice("")
	}
	return modes[resp.GetValue()], nil
}

func resolveSimulationModel(cmd *cobra.Command, explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		return strings.TrimSpace(explicit), nil
	}
	if noPrompt(cmd) {
		return "", messages.SimulationModelRequired()
	}
	return promptInitModel(cmd, messages.SimulationModelPrompt(), messages.SimulationModelHelp(),
		messages.SimulationModelRequired())
}
