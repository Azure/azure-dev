// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/eval_api"

	"github.com/spf13/cobra"
)

// addRunSubcommands attaches the atomic run operations.
//
// `azd ai eval run` stays the composite that creates the group if needed and
// starts a run; these expose the individual operations so every one is
// reachable without the config file.
func addRunSubcommands(cmd *cobra.Command) {
	cmd.AddCommand(
		newRunListCommand(),
		newRunShowCommand(),
		newRunCancelCommand(),
		newRunDeleteCommand(),
		newRunOutputCommand(),
	)
}

func newRunListCommand() *cobra.Command {
	var (
		endpointFlg string
		groupName   string
		limit       int
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List runs for an eval.",
		Long: "List runs for an eval.\n\n" +
			"The table carries one pass rate per run. A per-evaluator breakdown " +
			"cannot fit a column each and stay readable when runs score different " +
			"evaluators, so `-o json` carries it instead, under " +
			"`per_testing_criteria_results` on every run.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			evalID, err := resolveEvalID(cmd, ec, nil, groupName)
			if err != nil {
				return err
			}

			list, err := ec.evalClient.ListOpenAIEvalRuns(ctx, evalID, limit)
			if err != nil {
				if eval_api.IsNotFound(err) {
					return messages.EvalNotDeployed(evalID, ec.deployCommand(ctx))
				}
				return messages.ListingRuns(evalID, err)
			}
			if isJSON(cmd) {
				// Emitted whole, unlike `run start`, which hands back a small
				// handoff. The table cannot show a per-evaluator breakdown, so
				// this is the only place a script can read one; narrowing these
				// to the table's columns would drop it silently.
				var runs []eval_api.OpenAIEvalRun
				if list != nil {
					runs = list.Data
				}
				return emitJSONList(cmd.OutOrStdout(), runs)
			}
			if list == nil || len(list.Data) == 0 {
				fmt.Fprint(cmd.OutOrStdout(), messages.EvalHasNoRunsLine(evalID))
				return nil
			}

			rows := make([][]string, 0, len(list.Data))
			for _, run := range list.Data {
				rows = append(rows, []string{
					run.ID,
					runDataset(run.Metadata),
					timestampString(run.CreatedAt),
					run.Status,
					sampleCount(run.ResultCounts),
					runPassRate(run.ResultCounts),
				})
			}
			return emitTable(cmd.OutOrStdout(),
				[]string{"RUN", "DATASET", "STARTED", "STATUS", "SAMPLES", "PASS RATE"}, rows)
		},
	}
	addEvalFlag(cmd, &groupName)
	// Registered wherever a declared name is resolved, so a configuration
	// outside ./evals can be addressed by every command, not just un start.
	addEvalPathFlag(cmd, new(string))
	cmd.Flags().IntVar(&limit, "limit", 0,
		"Return at most this many runs. Omit for the service default.")
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func newRunShowCommand() *cobra.Command {
	var (
		endpointFlg string
		groupName   string
		wait        bool
		failOn      string
	)

	cmd := &cobra.Command{
		Use:   "show [run]",
		Short: "Show a single run.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			threshold, err := parseGate(failOn)
			if err != nil {
				return err
			}

			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			evalID, err := resolveEvalID(cmd, ec, nil, groupName)
			if err != nil {
				return err
			}

			runID := firstArg(args)
			run, err := ec.latestOrNamedRun(cmd, evalID, runID, runID != "")
			if err != nil {
				return err
			}
			run = ec.withPortalLink(ctx, evalID, run)

			// Reattaching to a run started asynchronously: the pipeline that
			// gates on it is often not the one that started it.
			//
			// Only a caller that waited is told a bad status through the exit
			// code. Without --wait this is an inspection command: it was asked
			// what happened, and answering that is a success whatever the
			// answer.
			gateOnStatus := wait
			if wait {
				run, err = ec.pollRun(ctx, evalID, run.ID, cmd.OutOrStdout(), isJSON(cmd))
				if err != nil {
					return err
				}
			}

			// The spec puts --fail-on on the commands that wait. Gating a run
			// that is still moving would read partial counts; ignoring the flag
			// would leave a pipeline believing it is gated when it is not.
			if threshold.set && !runIsTerminal(run) {
				return messages.GateNeedsATerminalRun(run.ID, run.Status)
			}

			if isJSON(cmd) {
				if err := emitJSON(cmd.OutOrStdout(), run); err != nil {
					return err
				}
				if gateOnStatus {
					if err := runCompleted(run); err != nil {
						return err
					}
				}
				applyGate(cmd, threshold, run)
				return nil
			}

			out := cmd.OutOrStdout()
			fmt.Fprint(out, messages.RunHeading(run.ID))
			fmt.Fprint(out, messages.RunNameLine(run.Name))
			fmt.Fprint(out, messages.RunStatusDetail(run.Status))
			if counts := summarizeCounts(run.ResultCounts); counts != "" {
				fmt.Fprint(out, messages.RunResultsLine(counts))
			}
			if run.ReportURL != "" {
				fmt.Fprint(out, messages.RunReportLine(run.ReportURL))
			}
			writePortalLink(out, run.PortalURL)
			if gateOnStatus {
				if err := runCompleted(run); err != nil {
					return err
				}
			}
			applyGate(cmd, threshold, run)
			return nil
		},
	}
	cmd.Flags().BoolVar(&wait, "wait", false,
		"Block until the run reaches a terminal state before reporting.")
	addFailOnFlag(cmd, &failOn)
	addEvalFlag(cmd, &groupName)
	// Registered wherever a declared name is resolved, so a configuration
	// outside ./evals can be addressed by every command, not just un start.
	addEvalPathFlag(cmd, new(string))
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

// firstArg returns the positional argument, or empty when none was given.
func firstArg(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return ""
}

func newRunCancelCommand() *cobra.Command {
	var (
		endpointFlg string
		groupName   string
	)

	cmd := &cobra.Command{
		Use:   "cancel [run]",
		Short: "Cancel an in-flight run.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			evalID, err := resolveEvalID(cmd, ec, nil, groupName)
			if err != nil {
				return err
			}

			runID := firstArg(args)
			target, err := ec.latestOrNamedRun(cmd, evalID, runID, runID != "")
			if err != nil {
				return err
			}
			// Cancelling a run that already finished is a no-op worth naming,
			// since the service reports success either way. Lowercased to match
			// the polling path: the service's casing is not guaranteed.
			if terminalRunStates[strings.ToLower(target.Status)] {
				return messages.RunAlreadyFinished(target.ID, target.Status)
			}

			canceled, err := ec.evalClient.CancelOpenAIEvalRun(ctx, evalID, target.ID)
			if err != nil {
				return messages.CancellingRun(target.ID, err)
			}
			if isJSON(cmd) {
				return emitJSON(cmd.OutOrStdout(), canceled)
			}
			status := canceled.Status
			if status == "" {
				status = "cancelling"
			}
			fmt.Fprint(cmd.OutOrStdout(), messages.RunIsNow(target.ID, status))
			return nil
		},
	}
	addEvalFlag(cmd, &groupName)
	// Registered wherever a declared name is resolved, so a configuration
	// outside ./evals can be addressed by every command, not just un start.
	addEvalPathFlag(cmd, new(string))
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

// newRunDeleteCommand removes a run.
//
// Runs accumulate — every `run start` adds one — and a run that evaluated the
// wrong dataset or target is noise in every later listing. The run is required
// rather than defaulted to the most recent, because deleting is not undoable
// and "the latest one" is a poor thing to guess at.
func newRunDeleteCommand() *cobra.Command {
	var (
		endpointFlg string
		groupName   string
	)

	cmd := &cobra.Command{
		Use:   "delete <run>",
		Short: "Delete a run.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			runID := args[0]
			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			evalID, err := resolveEvalID(cmd, ec, nil, groupName)
			if err != nil {
				return err
			}

			if err := ec.evalClient.DeleteOpenAIEvalRun(ctx, evalID, runID); err != nil {
				if eval_api.IsNotFound(err) {
					return messages.RunNotFound(runID, evalID)
				}
				return messages.DeletingRun(runID, err)
			}

			if isJSON(cmd) {
				return emitJSON(cmd.OutOrStdout(), map[string]string{
					"id": runID, "eval_id": evalID, "status": "deleted",
				})
			}
			fmt.Fprint(cmd.OutOrStdout(), messages.RunDeleted(runID))
			return nil
		},
	}
	addEvalFlag(cmd, &groupName)
	// Registered wherever a declared name is resolved, so a configuration
	// outside ./evals can be addressed by every command, not just un start.
	addEvalPathFlag(cmd, new(string))
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func summarizeCounts(counts *eval_api.EvalRunResultCounts) string {
	if counts == nil {
		return ""
	}
	return messages.CountsSummary(counts.Passed, counts.Failed, counts.Errored)
}

// metaDataset and metaDatasetVersion record which rows a run scored. The run's
// own data source cannot answer it: the rows travel inline, so the name that
// selected them is not in the request the service keeps.
const (
	metaDataset        = "azd_dataset"
	metaDatasetVersion = "azd_dataset_version"
	// metaEvalName is the eval's declared name, recorded on the run because a
	// run is read on its own and an id is not what the author called it.
	metaEvalName = "azd_eval"
	// metaAgent is the agent an eval targets.
	metaAgent = "azd_agent"
	// metaDescription carries an eval's description: the create request has no
	// field of its own for it.
	metaDescription = "azd_description"
)

// runDataset renders the dataset a run scored, versioned when a version was
// recorded with it.
//
// A run started before this was recorded shows nothing rather than the name in
// the configuration today, which is the one thing the column exists to detect
// having changed.
func runDataset(metadata map[string]string) string {
	name := metadata[metaDataset]
	if name == "" {
		return ""
	}
	if version := metadata[metaDatasetVersion]; version != "" {
		return fmt.Sprintf("%s (v%s)", name, version)
	}
	return name
}

// runDatasetLine is the same fact spelled for a detail view, where there is
// room for the whole word.
func runDatasetLine(metadata map[string]string) string {
	name := metadata[metaDataset]
	if name == "" {
		return ""
	}
	if version := metadata[metaDatasetVersion]; version != "" {
		return fmt.Sprintf("%s (version %s)", name, version)
	}
	return name
}

// sampleCount is how many rows the run scored, which is what makes two rows of
// `run list` comparable: a rate over 15 samples and one over 200 are not the
// same claim.
func sampleCount(counts *eval_api.EvalRunResultCounts) string {
	if counts == nil {
		return ""
	}
	return strconv.Itoa(counts.Total)
}

// runPassRate is the same passed/total the gate uses, so a row a reader gates
// on cannot disagree with the gate.
func runPassRate(counts *eval_api.EvalRunResultCounts) string {
	if counts == nil || counts.Total == 0 {
		return ""
	}
	return formatRate(counts.Passed, counts.Total)
}
