// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"time"

	"azureaieval/internal/pkg/eval_api"

	"github.com/spf13/cobra"
)

// comparePollBudget bounds the wait for a comparison. The probe returned in
// about a second, so this is generous headroom rather than an expected wait.
const (
	comparePollInterval = 3 * time.Second
	comparePollAttempts = 100
)

func newResultsCompareCommand() *cobra.Command {
	var (
		baseline    string
		treatments  []string
		displayName string
		endpointFlg string
		groupName   string
	)

	cmd := &cobra.Command{
		Use:   "compare [eval-id]",
		Short: "Compare runs of an eval group against a baseline.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()

			ec, err := newEvalContext(ctx, endpointFlg)
			if err != nil {
				return err
			}
			defer ec.Close()

			evalID, err := resolveEvalID(cmd, ec, args, groupName)
			if err != nil {
				return err
			}

			baseline, treatments, err = ec.resolveComparisonRuns(ctx, evalID, baseline, treatments)
			if err != nil {
				return err
			}

			if displayName == "" {
				displayName = fmt.Sprintf("compare-%s", time.Now().UTC().Format("20060102-150405"))
			}

			insight, err := ec.evalClient.CreateInsight(ctx, &eval_api.CreateInsightRequest{
				DisplayName: displayName,
				Request: &eval_api.InsightRequest{
					Type:            eval_api.InsightTypeEvaluationComparison,
					EvalID:          evalID,
					BaselineRunID:   baseline,
					TreatmentRunIDs: treatments,
				},
			}, ProjectEndpointAPIVersion)
			if err != nil {
				return fmt.Errorf("starting the comparison: %w", err)
			}

			if !isJSON(cmd) {
				fmt.Fprintf(out, "Comparing %d run(s) against %s...\n", len(treatments), baseline)
			}

			completed, err := ec.pollInsight(ctx, insight.ID)
			if err != nil {
				return err
			}
			if isJSON(cmd) {
				return emitJSON(out, completed)
			}
			return renderComparison(out, completed)
		},
	}

	cmd.Flags().StringVar(&baseline, "baseline", "",
		"Run to compare against. Defaults to the second most recent completed run.")
	cmd.Flags().StringArrayVar(&treatments, "treatment", nil,
		"Run to measure, repeatable. Defaults to the most recent completed run.")
	cmd.Flags().StringVar(&displayName, "name", "", "Name for this comparison.")
	addEvalGroupFlags(cmd, &groupName)
	cmd.Flags().StringVar(&endpointFlg, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

// resolveComparisonRuns fills in whichever runs were not named.
//
// Comparing the two most recent completed runs is what "did my change help?"
// means most of the time, so neither flag is required.
func (ec *evalContext) resolveComparisonRuns(
	ctx context.Context,
	evalID, baseline string,
	treatments []string,
) (string, []string, error) {
	if baseline != "" && len(treatments) > 0 {
		return baseline, treatments, nil
	}

	list, err := ec.evalClient.ListOpenAIEvalRuns(ctx, evalID, 0)
	if err != nil {
		return "", nil, fmt.Errorf("listing runs of eval group %s: %w", evalID, err)
	}

	completed := make([]string, 0, 2)
	if list == nil {
		return "", nil, fmt.Errorf("eval group %s has no runs", evalID)
	}
	for _, run := range list.Data {
		if run.Status == "completed" {
			completed = append(completed, run.ID)
		}
	}

	if len(treatments) == 0 {
		if len(completed) == 0 {
			return "", nil, fmt.Errorf(
				"eval group %s has no completed runs to compare", evalID)
		}
		treatments = []string{completed[0]}
	}
	if baseline == "" {
		if len(completed) < 2 {
			return "", nil, fmt.Errorf(
				"eval group %s has only one completed run, so there is nothing to compare it "+
					"against; run it again, or name a baseline with --baseline",
				evalID)
		}
		baseline = completed[1]
	}
	return baseline, treatments, nil
}

// pollInsight waits for the comparison to reach a terminal state.
func (ec *evalContext) pollInsight(ctx context.Context, insightID string) (*eval_api.Insight, error) {
	for attempt := 0; attempt < comparePollAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(comparePollInterval):
		}

		insight, err := ec.evalClient.GetInsight(ctx, insightID, ProjectEndpointAPIVersion)
		if err != nil {
			return nil, fmt.Errorf("reading comparison %s: %w", insightID, err)
		}
		if !insight.Terminal() {
			continue
		}
		if !insight.Succeeded() {
			return nil, fmt.Errorf("comparison %s finished with state %q", insightID, insight.State)
		}
		return insight, nil
	}
	return nil, fmt.Errorf("comparison %s did not finish in time", insightID)
}

// renderComparison prints one row per criterion per treatment run.
func renderComparison(w interface{ Write([]byte) (int, error) }, insight *eval_api.Insight) error {
	if insight.Result == nil || len(insight.Result.Comparisons) == 0 {
		fmt.Fprintln(w, "The comparison produced no metrics.")
		return nil
	}

	if insight.Result.Method != "" {
		fmt.Fprintf(w, "Method: %s\n\n", insight.Result.Method)
	}

	rows := [][]string{}
	for _, c := range insight.Result.Comparisons {
		baseAvg := "-"
		if c.BaselineRunSummary != nil {
			baseAvg = fmt.Sprintf("%.3f", c.BaselineRunSummary.Average)
		}
		for _, item := range c.CompareItems {
			treatAvg := "-"
			runID := "-"
			if item.TreatmentRunSummary != nil {
				treatAvg = fmt.Sprintf("%.3f", item.TreatmentRunSummary.Average)
				runID = item.TreatmentRunSummary.RunID
			}
			rows = append(rows, []string{
				c.Metric,
				runID,
				baseAvg,
				treatAvg,
				fmt.Sprintf("%+.3f", item.DeltaEstimate),
				fmt.Sprintf("%.3f", item.PValue),
				item.TreatmentEffect,
			})
		}
	}

	return emitTable(w,
		[]string{"METRIC", "TREATMENT RUN", "BASELINE", "TREATMENT", "DELTA", "P-VALUE", "EFFECT"},
		rows)
}
