// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"strings"

	"azureaieval/internal/exterrors"
	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"
)

// Seed-row fields. A scenario describes a conversation to create; it carries no
// question, because nobody has asked one yet.
const (
	seedDescriptionField = "test_case_description"
	seedTurnsField       = "desired_num_turns"
	completedRowsField   = "messages"
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
	items, version, err := ec.readRegisteredDataset(
		ctx, group.Dataset, pinnedVersion)
	if err != nil {
		return nil, "", err
	}
	if err := refuseUnusableSeedRows(group, items); err != nil {
		return nil, "", err
	}

	// A seed dataset is referenced, never copied. The spec is explicit that
	// inline rows are not equivalent for a registered dataset, and a version the
	// service will not describe is not one a run can be pinned to.
	id, err := ec.datasetResourceID(ctx, group.Dataset, version)
	if err != nil {
		return nil, "", err
	}

	ds := eval_api.NewSimulationDataSource(
		agent,
		group.Simulation.Model,
		group.Simulation.Conversations(),
		group.Simulation.MaxTurns,
	)
	ds.SetFileID(id)
	ds.SimulationSeedCount = new(len(items))
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
	return checkDesiredTurns(group, item, index)
}

// checkDesiredTurns refuses a per-row turn count that is not a positive whole
// number. JSON numbers decode as float64, so a fractional value is a real
// possibility rather than a theoretical one.
func checkDesiredTurns(group *project.Eval, item map[string]any, index int) error {
	raw, present := item[seedTurnsField]
	if !present {
		return nil
	}

	turns, ok := wholeNumber(raw)
	if !ok || turns < 1 {
		return simulationError(group,
			fmt.Sprintf("row %d has %s = %v, which is not a positive whole number of turns",
				index+1, seedTurnsField, raw),
			fmt.Sprintf("%q is how many turns that one conversation should run for.", seedTurnsField))
	}

	// The per-row count is a request, and the eval's own bound is the ceiling.
	// Saying so here beats a conversation silently ending early.
	if group.Simulation.MaxTurns > 0 && turns > group.Simulation.MaxTurns {
		return simulationError(group,
			fmt.Sprintf("row %d asks for %d turns, but simulation.max_turns is %d",
				index+1, turns, group.Simulation.MaxTurns),
			fmt.Sprintf("Raise simulation.max_turns to at least %d, or lower %s on that row.",
				turns, seedTurnsField))
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
