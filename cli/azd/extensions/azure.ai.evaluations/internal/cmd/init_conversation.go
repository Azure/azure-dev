// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"

	"azureaieval/internal/exterrors"
	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

const (
	conversationModeStatic     = "static"
	conversationModeSimulation = "simulation"
	modelConnectionType        = "AzureOpenAI"
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
		if !a.cmd.Flags().Changed("conversation-mode") {
			return messages.InitImpliedStaticTargetConflict(noPrompt(a.cmd))
		}
		return messages.InitFlagConflict("target", "cannot be used with --conversation-mode static; no agent is invoked")
	}
	if a.cmd.Flags().Changed("simulation-model") && strings.TrimSpace(a.flags.simulationModel) == "" {
		return messages.SimulationModelRequired()
	}
	if a.cmd.Flags().Changed("simulation-model") {
		if err := (&project.Simulation{Model: a.flags.simulationModel}).Validate(); err != nil {
			return messages.InitFlagConflict("simulation-model", err.Error())
		}
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

func resolveSimulationModel(cmd *cobra.Command, explicit string, authored []string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		return strings.TrimSpace(explicit), nil
	}
	var candidates []string
	for _, model := range authored {
		if err := (&project.Simulation{Model: model}).Validate(); err == nil {
			candidates = append(candidates, model)
		}
	}
	slices.Sort(candidates)
	candidates = slices.Compact(candidates)
	if len(candidates) == 1 {
		if !isJSON(cmd) {
			fmt.Fprint(cmd.OutOrStdout(), messages.DetectedSimulationModel(candidates[0]))
		}
		return candidates[0], nil
	}
	if noPrompt(cmd) {
		if len(candidates) > 1 {
			return "", messages.AmbiguousSimulationModel(candidates)
		}
		return "", messages.SimulationModelRequired()
	}
	if len(candidates) > 1 {
		client, err := azdext.NewAzdClient()
		if err != nil {
			return "", messages.ConnectingToAzd(err)
		}
		defer client.Close()
		choices := make([]*azdext.SelectChoice, 0, len(candidates)+1)
		for _, model := range candidates {
			choices = append(choices, &azdext.SelectChoice{Label: model, Value: model})
		}
		choices = append(choices, &azdext.SelectChoice{Label: messages.EnterAnotherSimulationModel()})
		resp, err := client.Prompt().Select(commandContext(cmd), &azdext.SelectRequest{
			Options: &azdext.SelectOptions{
				Message: messages.SelectSimulationModelPrompt(), Choices: choices,
				EnableFiltering: filteringFor(len(choices)),
			},
		})
		if err != nil {
			return "", fmt.Errorf("selecting simulation model: %w", err)
		}
		if resp == nil || resp.Value == nil || resp.GetValue() < 0 || int(resp.GetValue()) >= len(choices) {
			return "", messages.AmbiguousSimulationModel(candidates)
		}
		if index := int(resp.GetValue()); index < len(candidates) {
			return candidates[index], nil
		}
	}
	model, err := promptInitModel(cmd, messages.SimulationModelPrompt(), messages.SimulationModelHelp(),
		messages.SimulationModelRequired())
	if err != nil {
		return "", err
	}
	if err := (&project.Simulation{Model: model}).Validate(); err != nil {
		return "", messages.InitFlagConflict("simulation-model", err.Error())
	}
	fmt.Fprint(cmd.OutOrStdout(), messages.UnverifiedSimulationModel(model))
	return model, nil
}

func (a *initAction) resolveSimulationModel(cmd *cobra.Command, explicit string, authored []string) (string, error) {
	model := strings.TrimSpace(explicit)
	if model == "" && slices.ContainsFunc(authored, func(value string) bool {
		return (&project.Simulation{Model: value}).Validate() == nil
	}) {
		var err error
		model, err = resolveSimulationModel(cmd, "", authored)
		if err != nil {
			return "", err
		}
	}
	if model != "" {
		if err := validateSimulationModel(model); err != nil {
			return "", err
		}
		connections, err := a.modelConnectionCatalogue(cmd.Context())
		if err != nil {
			return "", messages.ListingSimulationModelConnections(err)
		}
		if err := validateSimulationModelConnection(model, connections); err != nil {
			return "", err
		}
		return model, nil
	}
	if noPrompt(cmd) {
		return "", messages.SimulationModelRequired()
	}
	connections, err := a.modelConnectionCatalogue(cmd.Context())
	if err != nil {
		return "", messages.ListingSimulationModelConnections(err)
	}
	eligible := eligibleSimulationModelConnections(connections)
	if len(eligible) == 0 {
		return "", messages.NoEligibleSimulationModelConnections()
	}
	connection, err := selectSimulationModelConnection(cmd, eligible)
	if err != nil {
		return "", err
	}
	deployment, err := promptInitModel(cmd, messages.SimulationModelDeploymentPrompt(),
		messages.SimulationModelDeploymentHelp(), messages.SimulationModelDeploymentRequired())
	if err != nil {
		return "", err
	}
	model = connection + "/" + deployment
	if err := validateSimulationModel(model); err != nil {
		return "", err
	}
	return model, nil
}

func readModelConnectionCatalogue(ctx context.Context) ([]eval_api.Connection, error) {
	ec, err := newEvalContext(ctx, "")
	if err != nil {
		return nil, err
	}
	defer ec.Close()
	catalogue, err := ec.evalClient.ListConnections(ctx, ProjectConnectionsAPIVersion)
	if err != nil {
		return nil, err
	}
	return catalogue.Value, nil
}

func validateSimulationModel(model string) error {
	if err := (&project.Simulation{Model: model}).Validate(); err != nil {
		return exterrors.Validation(exterrors.CodeInvalidParameter, fmt.Sprintf("--simulation-model: %v", err),
			"Use connection-name/model-deployment for the simulated user, independently of the judge and generation models.")
	}
	return nil
}

func validateSimulationModelConnection(model string, connections []eval_api.Connection) error {
	connectionName := strings.SplitN(model, "/", 2)[0]
	var found *eval_api.Connection
	for i := range connections {
		connection := &connections[i]
		if connection.Name != connectionName {
			continue
		}
		if connection.Type == modelConnectionType {
			return nil
		}
		found = connection
	}
	if found == nil {
		return messages.SimulationModelConnectionNotFound(connectionName)
	}
	return messages.SimulationModelConnectionWrongKind(connectionName, found.Type)
}

func eligibleSimulationModelConnections(connections []eval_api.Connection) []string {
	names := make(map[string]struct{})
	for _, connection := range connections {
		if connection.Type != modelConnectionType || !validSimulationConnectionName(connection.Name) {
			continue
		}
		names[connection.Name] = struct{}{}
	}
	return slices.Sorted(maps.Keys(names))
}

func validSimulationConnectionName(name string) bool {
	return name != "" && name == strings.TrimSpace(name) &&
		!strings.Contains(name, "/") && !strings.ContainsFunc(name, unicode.IsSpace)
}

func selectSimulationModelConnection(cmd *cobra.Command, connections []string) (string, error) {
	client, err := azdext.NewAzdClient()
	if err != nil {
		return "", messages.ConnectingToAzd(err)
	}
	defer client.Close()
	choices := make([]*azdext.SelectChoice, 0, len(connections))
	for _, connection := range connections {
		choices = append(choices, &azdext.SelectChoice{Label: connection, Value: connection})
	}
	resp, err := client.Prompt().Select(commandContext(cmd), &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: messages.SimulationModelConnectionPrompt(), Choices: choices,
			SelectedIndex: preselect(0), EnableFiltering: filteringFor(len(choices)),
		},
	})
	if err != nil {
		return "", exterrors.FromPrompt(err, "selecting a simulation model connection")
	}
	if resp == nil || resp.Value == nil || int(resp.GetValue()) < 0 || int(resp.GetValue()) >= len(connections) {
		return "", messages.SimulationModelConnectionRequired()
	}
	return connections[resp.GetValue()], nil
}
