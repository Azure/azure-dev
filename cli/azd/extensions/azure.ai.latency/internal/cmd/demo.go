// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"strings"

	"azure.ai.latency/internal/contracts"
	"azure.ai.latency/internal/insights"
	"azure.ai.latency/internal/model"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

const defaultDemoReport = "reports/model-latency-demo.html"

type demoFlags struct {
	scenario      string
	listScenarios bool
	reportPath    string
	openReport    bool
}

func newDemoCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	if extCtx == nil {
		extCtx = &azdext.ExtensionContext{}
	}
	flags := &demoFlags{}
	cmd := &cobra.Command{
		Use:   "demo",
		Short: "Explore deterministic model latency assessment scenarios",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDemo(cmd, extCtx, flags)
		},
	}
	cmd.Flags().StringVar(&flags.scenario, "scenario", "within-target", "Demo scenario to display")
	cmd.Flags().BoolVar(&flags.listScenarios, "list-scenarios", false, "List available demo scenarios")
	cmd.Flags().StringVar(&flags.reportPath, "report", defaultDemoReport, "HTML report path; use an empty value to disable")
	cmd.Flags().BoolVar(&flags.openReport, "open", false, "Open the generated HTML report")

	_ = cmd.RegisterFlagCompletionFunc("scenario", completeDemoScenario)
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name:          "output",
		AllowedValues: []string{"terminal", "json"},
		Default:       "terminal",
		Usage:         "Result output format",
	})
	return cmd
}

func runDemo(cmd *cobra.Command, extCtx *azdext.ExtensionContext, flags *demoFlags) error {
	scenarios := insights.DemoScenarios()
	if flags.listScenarios {
		for _, scenario := range scenarios {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", scenario.Name, scenario.Title); err != nil {
				return fmt.Errorf("write demo scenarios: %w", err)
			}
		}
		return nil
	}
	if flags.openReport && strings.TrimSpace(flags.reportPath) == "" {
		return validationError(
			"open_report_path_required",
			"--open requires an HTML report path.",
			"Remove --open or provide --report.",
		)
	}
	selected, ok := insights.FindDemoScenario(flags.scenario)
	if !ok {
		return validationError(
			"invalid_demo_scenario",
			fmt.Sprintf("Unknown demo scenario %q.", flags.scenario),
			"Run 'azd ai latency demo --list-scenarios' to see valid scenarios.",
		)
	}

	catalog, err := contracts.Load()
	if err != nil {
		return err
	}
	results := make([]*model.AssessmentResult, 0, len(scenarios))
	var selectedResult *model.AssessmentResult
	for _, scenario := range scenarios {
		result, err := insights.AssessDemo(cmd.Context(), catalog, scenario)
		if err != nil {
			return fmt.Errorf("assess demo scenario %s: %w", scenario.Name, err)
		}
		if isPrimaryReportScenario(scenario.Name) {
			results = append(results, result)
		}
		if scenario.Name == selected.Name {
			selectedResult = result
		}
	}
	if selectedResult == nil {
		return fmt.Errorf("selected demo scenario %q was not assessed", selected.Name)
	}
	if err := renderResult(cmd, extCtx.OutputFormat, selectedResult); err != nil {
		return err
	}
	return writeAndOpenReport(cmd, flags.reportPath, flags.openReport, results)
}

func isPrimaryReportScenario(name string) bool {
	switch name {
	case "within-target", "workload-explained", "unexplained-gap", "logs-unavailable":
		return true
	default:
		return false
	}
}

func completeDemoScenario(
	_ *cobra.Command,
	_ []string,
	_ string,
) ([]string, cobra.ShellCompDirective) {
	scenarios := insights.DemoScenarios()
	names := make([]string, 0, len(scenarios))
	for _, scenario := range scenarios {
		names = append(names, scenario.Name)
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}
