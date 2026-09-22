// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"io"
	"strconv"

	"azureaieval/internal/pkg/eval_api"
)

const (
	metaRunMode               = "azd_run_mode"
	metaSimulationSeeds       = "azd_simulation_seed_count"
	metaSimulationRepetitions = "azd_simulation_repetitions"
	metaSimulationMaxTurns    = "azd_simulation_max_turns"
	simulationMode            = "conversation_simulation"
	serviceDefaultTurns       = "service_default"
)

func recordSimulationMetadata(metadata map[string]string, source *eval_api.EvalRunDataSource) {
	if source == nil || source.Type != eval_api.EvalRunDataSourceTypeUserConversationSimulation {
		return
	}
	metadata[metaRunMode] = simulationMode
	if source.SimulationSeedCount != nil {
		metadata[metaSimulationSeeds] = strconv.Itoa(*source.SimulationSeedCount)
	}
	if settings := source.DefaultSimulationConfiguration; settings != nil {
		metadata[metaSimulationRepetitions] = strconv.Itoa(settings.ConversationRepetitions)
		metadata[metaSimulationMaxTurns] = serviceDefaultTurns
		if settings.MaxNumTurns > 0 {
			metadata[metaSimulationMaxTurns] = strconv.Itoa(settings.MaxNumTurns)
		}
	}
}

func isSimulationRun(run *eval_api.OpenAIEvalRun) bool {
	if run.DataSource != nil && run.DataSource.Type != "" {
		return run.DataSource.Type == eval_api.EvalRunDataSourceTypeUserConversationSimulation
	}
	return run.Metadata[metaRunMode] == simulationMode
}

func simulationCount(value string, minimum, maximum int) string {
	n, err := strconv.Atoi(value)
	if err != nil || n < minimum || (maximum > 0 && n > maximum) {
		return "not reported"
	}
	return strconv.Itoa(n)
}

func renderSimulationSettings(out io.Writer, run *eval_api.OpenAIEvalRun) {
	if !isSimulationRun(run) {
		return
	}
	repetitions := run.Metadata[metaSimulationRepetitions]
	maxTurns := run.Metadata[metaSimulationMaxTurns]
	if source := run.DataSource; source != nil && source.DefaultSimulationConfiguration != nil {
		settings := source.DefaultSimulationConfiguration
		if repetitions == "" {
			repetitions = strconv.Itoa(settings.ConversationRepetitions)
		}
		if maxTurns == "" {
			maxTurns = serviceDefaultTurns
			if settings.MaxNumTurns != 0 {
				maxTurns = strconv.Itoa(settings.MaxNumTurns)
			}
		}
	}
	turns := simulationCount(maxTurns, 1, 20)
	if maxTurns == serviceDefaultTurns {
		turns = "service default (not specified)"
	}
	fmt.Fprint(out, "\nSIMULATION CONFIGURATION (REQUESTED)\n")
	for _, row := range []field{
		{"Seed scenarios", simulationCount(run.Metadata[metaSimulationSeeds], 0, 0)},
		{"Repetitions per seed", simulationCount(repetitions, 1, 5)},
		{"Maximum turns", turns},
	} {
		fmt.Fprintf(out, "%-22s %s\n", row.Key, row.Value)
	}
	fmt.Fprint(out, "\nSIMULATION EXECUTION (OBSERVED)\n")
	// result_counts describes evaluation outcomes, not successful generation.
	// The service contract has no separate generation or turn counters.
	for _, label := range []string{"Conversations generated", "Conversations completed", "Conversation turns"} {
		fmt.Fprintf(out, "%-24s not reported\n", label)
	}
}

func renderConversationResults(out io.Writer, run *eval_api.OpenAIEvalRun) {
	counts := run.ReportedResultCounts()
	if len(counts) == 0 {
		fmt.Fprint(out, "\nCONVERSATION EVALUATION RESULTS\nNot reported by the service.\n")
		return
	}
	fmt.Fprint(out, "\nCONVERSATION EVALUATION RESULTS\n")
	for _, row := range []field{
		{"Total", "total"}, {"Passed", "passed"}, {"Failed", "failed"},
		{"Errored", "errored"}, {"Skipped", "skipped"},
	} {
		if count, ok := counts[row.Value]; ok {
			fmt.Fprintf(out, "%-10s %4d\n", row.Key, count)
		} else {
			fmt.Fprintf(out, "%-10s not reported\n", row.Key)
		}
	}
	passed, passedKnown := counts["passed"]
	failed, failedKnown := counts["failed"]
	if passedKnown && failedKnown {
		fmt.Fprintf(out, "%-10s %s (%d passed / (%d passed + %d failed))\n",
			"Pass rate", formatRate(passed, passed+failed), passed, passed, failed)
	} else {
		fmt.Fprint(out, "Pass rate  not reported\n")
	}
}
