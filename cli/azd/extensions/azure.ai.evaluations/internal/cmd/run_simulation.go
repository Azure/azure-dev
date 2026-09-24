// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"azureaieval/internal/exterrors"
	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"
)

// Seed-row fields. A scenario describes a conversation to create; it carries no
// question, because nobody has asked one yet.
const (
	seedDescriptionField     = "test_case_description"
	seedConfigField          = "simulation_configuration"
	seedTurnsField           = "desired_num_turns"
	completedRowsField       = "messages"
	defaultSimulationTurns   = 20
	maxSeedDescriptionLength = 2500
)

// simulationDataSource builds the run for an eval that creates its
// conversations from scenario seeds.
//
// Every rule is applied before the data source is returned, because the caller
// creates the run immediately afterwards and a run is billed whether or not the
// declaration made sense.
func (ec *evalContext) simulationDataSource(
	ctx context.Context,
	group *project.Eval,
	pinnedVersion string,
	maxSamples int,
) (*eval_api.EvalRunDataSource, string, error) {
	// The declaration rules are project.ValidateRunnable's, so `azd up` refuses
	// the same shapes this does; the caller has already applied them. What is
	// left here is the resolved cap, which --max-samples can introduce for a
	// declaration that carries none.
	if maxSamples > 0 {
		return nil, "", messages.InEval(group.Name, messages.SimulationCannotBeSampled(maxSamples))
	}

	// The azure.yaml service key is a local label; the agent answers to what its
	// service declares. The turn path resolves the same way.
	agent, err := ec.remoteAgentName(ctx, group.Target.Name)
	if err != nil {
		return nil, "", simulationError(group, err.Error(), "")
	}

	// Read whole: the run is bound to the registered version, so a cap here
	// would validate a prefix of what the service is about to simulate from.
	version, err := ec.resolveRunDatasetVersion(ctx, group.Dataset, pinnedVersion, false)
	if err != nil {
		return nil, "", err
	}
	id, err := ec.datasetResourceID(ctx, group.Dataset, version)
	if err != nil {
		return nil, "", err
	}
	items, err := ec.readDatasetVersion(ctx, group.Dataset, version)
	if err != nil {
		return nil, "", err
	}
	if err := refuseUnusableSeedRows(group, items); err != nil {
		return nil, "", err
	}

	ds := eval_api.NewSimulationDataSource(
		agent,
		group.Simulation.Model,
		group.Simulation.Conversations(),
		group.Simulation.MaxTurns,
	)
	ds.SetFileID(id)
	return ds, version, nil
}

// refuseUnusableSeedRows checks every row before anything is created.
//
// The whole set is examined rather than the first row: a single unusable
// scenario in the middle of a file would otherwise be discovered only once the
// run had been billed for the ones before it.
func refuseUnusableSeedRows(group *project.Eval, items []map[string]any) error {
	for i, item := range items {
		if err := refuseUnusableSeedRow(group, item, i); err != nil {
			return err
		}
	}
	return nil
}

func refuseUnusableSeedRow(group *project.Eval, item map[string]any, index int) error {
	if _, isCompleted := item[completedRowsField]; isCompleted {
		return simulationError(group,
			fmt.Sprintf("row %d carries %q, which is a completed conversation rather than a scenario to simulate",
				index+1, completedRowsField),
			"A dataset holds either conversations to score or scenarios to simulate, not both. "+
				"Remove the simulation block to score these conversations as they stand.")
	}
	for _, field := range []string{"query", "response"} {
		if _, present := item[field]; present {
			return simulationError(group,
				fmt.Sprintf("row %d carries %q, which is a turn-level field rather than a scenario to simulate",
					index+1, field),
				"Simulation seeds cannot mix with query/response rows. Remove the query and response fields "+
					"to simulate scenarios, or use a separate non-simulation eval for turn-level rows.")
		}
	}

	description, present := item[seedDescriptionField]
	if !present {
		return simulationError(group,
			fmt.Sprintf("row %d has no %q, so there is no scenario to simulate", index+1, seedDescriptionField),
			fmt.Sprintf("Every seed row needs a %q. Generate seeds with "+
				"`azd ai eval generate --evaluation-level conversation`.", seedDescriptionField))
	}
	text, isString := description.(string)
	if !isString || strings.TrimSpace(text) == "" {
		return simulationError(group,
			fmt.Sprintf("row %d has an empty or non-text %q", index+1, seedDescriptionField),
			fmt.Sprintf("%q describes the conversation to create, so it has to be a non-empty string.",
				seedDescriptionField))
	}
	if length := utf8.RuneCountInString(text); length > maxSeedDescriptionLength {
		return simulationError(group,
			fmt.Sprintf("row %d has %s with %d characters; the maximum is %d",
				index+1, seedDescriptionField, length, maxSeedDescriptionLength),
			fmt.Sprintf("Shorten %s to at most %d characters and publish a new dataset version.",
				seedDescriptionField, maxSeedDescriptionLength))
	}
	return checkDesiredTurns(group, item, index)
}

// checkDesiredTurns refuses a per-row turn count that is not a positive whole
// number. JSON numbers decode as float64, so a fractional value is a real
// possibility rather than a theoretical one.
func checkDesiredTurns(group *project.Eval, item map[string]any, index int) error {
	if _, flat := item[seedTurnsField]; flat {
		return simulationError(group,
			fmt.Sprintf("row %d has %s outside %s; the service does not read this flat field",
				index+1, seedTurnsField, seedConfigField),
			fmt.Sprintf("Move %s into %s.%s and publish a new dataset version before running.",
				seedTurnsField, seedConfigField, seedTurnsField))
	}
	raw, present := item[seedConfigField]
	if !present {
		return nil
	}
	config, ok := raw.(map[string]any)
	if !ok {
		return simulationError(group,
			fmt.Sprintf("row %d has a non-object %s", index+1, seedConfigField),
			fmt.Sprintf("%s must be an object containing optional turn settings, or be omitted.", seedConfigField))
	}

	maxTurns := group.Simulation.MaxTurns
	if maxTurns == 0 {
		maxTurns = defaultSimulationTurns
	}
	maxField := "simulation.max_turns"
	turns := 0
	for _, field := range []string{"max_num_turns", seedTurnsField} {
		value, present := config[field]
		if !present {
			continue
		}
		n, ok := wholeNumber(value)
		if !ok || n < 1 {
			return simulationError(group,
				fmt.Sprintf("row %d has %s.%s = %v, which is not a positive whole number of turns",
					index+1, seedConfigField, field, value),
				"Use a positive whole number, or omit the setting to keep the default.")
		}
		if field == "max_num_turns" {
			// Per-case settings override the run defaults in the service contract.
			maxTurns = n
			maxField = seedConfigField + "." + field
		} else {
			turns = n
		}
	}
	if turns > maxTurns {
		suggestion := fmt.Sprintf("Raise %s to at least %d, or lower %s.%s on that row.",
			maxField, turns, seedConfigField, seedTurnsField)
		if maxField == "simulation.max_turns" && turns > project.MaxSimulationTurns {
			suggestion = fmt.Sprintf("Lower %s.%s to at most %d on that row. simulation.max_turns accepts %d to %d.",
				seedConfigField, seedTurnsField, maxTurns, project.MinSimulationTurns, project.MaxSimulationTurns)
		}
		return simulationError(group,
			fmt.Sprintf("row %d asks for %d turns, but effective %s is %d",
				index+1, turns, maxField, maxTurns),
			suggestion)
	}

	return nil
}

// wholeNumber reads a JSON number that has to be an integer.
func wholeNumber(raw any) (int, bool) {
	switch v := raw.(type) {
	case float64:
		if v != float64(int(v)) {
			return 0, false
		}
		return int(v), true
	case int:
		return v, true
	case int64:
		return int(v), true
	default:
		return 0, false
	}
}

// simulationError names the eval inside the structured message. azd serializes
// a structured error's own message, so a wrapper prefix would not be shown.
func simulationError(group *project.Eval, message, suggestion string) error {
	return exterrors.Validation(
		exterrors.CodeInvalidParameter,
		fmt.Sprintf("eval %q: %s", group.Name, message),
		suggestion,
	)
}
