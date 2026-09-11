// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"azure.ai.latency/internal/azureprovider"
	"azure.ai.latency/internal/contracts"
	"azure.ai.latency/internal/insights"
	"azure.ai.latency/internal/model"
	"azure.ai.latency/internal/report"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/cli/browser"
	"github.com/spf13/cobra"
)

const defaultAssessReport = "reports/latency-report.html"

var openReportURL = openURL

type assessFlags struct {
	deploymentFlags
	latencyGoal string
	last        string
	startTime   string
	endTime     string
	reportPath  string
	openReport  bool
	noProgress  bool
}

func newAssessCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	if extCtx == nil {
		extCtx = &azdext.ExtensionContext{}
	}
	flags := &assessFlags{}
	cmd := &cobra.Command{
		Use:   "assess",
		Short: "Assess a deployed model workload and explain observed latency",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAssess(cmd, extCtx, flags)
		},
	}

	cmd.Flags().StringVar(&flags.deploymentID, "deployment-id", "", "Complete Azure model deployment resource ID")
	cmd.Flags().StringVar(&flags.subscription, "subscription", "", "Azure subscription ID")
	cmd.Flags().StringVar(&flags.resourceGroup, "resource-group", "", "Resource group containing the account")
	cmd.Flags().StringVar(&flags.accountName, "account-name", "", "Azure OpenAI account name")
	cmd.Flags().StringVar(&flags.deploymentName, "deployment-name", "", "Model deployment name")
	cmd.Flags().StringVar(
		&flags.latencyGoal,
		"latency-goal",
		"",
		"Optional latency goal (supported: lower-or-predictable)",
	)
	cmd.Flags().StringVar(&flags.last, "last", "24h", "Relative lookback duration, such as 30m, 24h, or 7d")
	cmd.Flags().StringVar(&flags.startTime, "start-time", "", "Exact UTC range start in RFC3339 format")
	cmd.Flags().StringVar(&flags.endTime, "end-time", "", "Exact UTC range end in RFC3339 format")
	cmd.Flags().StringVar(
		&flags.reportPath,
		"report",
		defaultAssessReport,
		"HTML report path; use an empty value to disable",
	)
	cmd.Flags().BoolVar(&flags.openReport, "open", false, "Open the generated HTML report")
	cmd.Flags().BoolVar(&flags.noProgress, "no-progress", false, "Suppress assessment progress messages")

	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name:          "output",
		AllowedValues: []string{"terminal", "json"},
		Default:       "terminal",
		Usage:         "Result output format",
	})
	return cmd
}

func runAssess(cmd *cobra.Command, extCtx *azdext.ExtensionContext, flags *assessFlags) error {
	if flags.openReport && strings.TrimSpace(flags.reportPath) == "" {
		return validationError(
			"open_report_path_required",
			"--open requires an HTML report path.",
			"Remove --open or provide --report.",
		)
	}
	userGoal := ""
	switch flags.latencyGoal {
	case "":
	case "lower-or-predictable":
		userGoal = model.GoalLowerOrPredictable
	default:
		return validationError(
			"invalid_latency_goal",
			fmt.Sprintf("Unsupported --latency-goal %q.", flags.latencyGoal),
			"Use --latency-goal lower-or-predictable.",
		)
	}

	window, err := resolveTimeWindow(
		flags.last,
		flags.startTime,
		flags.endTime,
		cmd.Flags().Changed("last"),
		time.Now(),
	)
	if err != nil {
		return err
	}
	if extCtx.NoPrompt && flags.deploymentID == "" {
		reference := model.DeploymentReference{
			Subscription:   strings.TrimSpace(flags.subscription),
			ResourceGroup:  strings.TrimSpace(flags.resourceGroup),
			AccountName:    strings.TrimSpace(flags.accountName),
			DeploymentName: strings.TrimSpace(flags.deploymentName),
		}
		if !completeReference(reference) {
			return missingDeploymentFieldsError(reference)
		}
	}
	if !flags.noProgress {
		if _, err := fmt.Fprintln(cmd.ErrOrStderr(), "[model-latency] Starting assessment..."); err != nil {
			return fmt.Errorf("write assessment progress: %w", err)
		}
	}

	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return fmt.Errorf("connect to azd: %w", err)
	}
	defer azdClient.Close()
	if err := azdext.WaitForDebugger(cmd.Context(), azdClient); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, azdext.ErrDebuggerAborted) {
			return nil
		}
		return fmt.Errorf("wait for debugger: %w", err)
	}

	reference, err := resolveDeploymentReference(
		cmd.Context(),
		flags.deploymentFlags,
		extCtx.NoPrompt,
		azdClient,
	)
	if err != nil {
		return err
	}
	credential, err := azdext.NewTokenProvider(cmd.Context(), azdClient, &azdext.TokenProviderOptions{
		TenantID: reference.UserTenantID,
	})
	if err != nil {
		return fmt.Errorf("create Azure credential: %w", err)
	}
	provider, err := azureprovider.New(reference.Subscription, credential)
	if err != nil {
		return fmt.Errorf("create Azure latency provider: %w", err)
	}
	catalog, err := contracts.Load()
	if err != nil {
		return err
	}
	if !flags.noProgress {
		if _, err := fmt.Fprintln(cmd.ErrOrStderr(), "Evaluating the current offer target"); err != nil {
			return fmt.Errorf("write assessment progress: %w", err)
		}
	}
	service := insights.NewService(catalog, provider, provider)
	result, err := service.Assess(cmd.Context(), insights.AssessmentRequest{
		Reference:  reference,
		TimeWindow: window,
		UserGoal:   userGoal,
		Mode:       model.ModeAssess,
	})
	if err != nil {
		return err
	}
	if err := renderResult(cmd, extCtx.OutputFormat, result); err != nil {
		return err
	}
	return writeAndOpenReport(cmd, flags.reportPath, flags.openReport, []*model.AssessmentResult{result})
}

func renderResult(cmd *cobra.Command, outputFormat string, result *model.AssessmentResult) error {
	switch outputFormat {
	case "json":
		return report.RenderJSON(cmd.OutOrStdout(), result)
	case "terminal", "default", "":
		return report.RenderTerminal(cmd.OutOrStdout(), result)
	default:
		return validationError(
			"invalid_output_format",
			fmt.Sprintf("Unsupported output format %q.", outputFormat),
			"Use --output terminal or --output json.",
		)
	}
}

func writeAndOpenReport(
	cmd *cobra.Command,
	reportPath string,
	open bool,
	results []*model.AssessmentResult,
) error {
	if strings.TrimSpace(reportPath) == "" {
		return nil
	}
	if err := report.WriteHTML(reportPath, results); err != nil {
		return fmt.Errorf("write HTML report: %w", err)
	}
	if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "HTML report: %s\n", reportPath); err != nil {
		return fmt.Errorf("write report path: %w", err)
	}
	if !open {
		return nil
	}
	reportURL, err := reportFileURL(reportPath)
	if err != nil {
		return fmt.Errorf("resolve HTML report path: %w", err)
	}
	if err := openReportURL(cmd.Context(), reportURL); err != nil {
		return fmt.Errorf("open HTML report: %w", err)
	}
	return nil
}

func openURL(ctx context.Context, reportURL string) error {
	if runtime.GOOS == "windows" {
		// Explorer delegates the file URL through the existing Windows shell process,
		// so browser startup is not tied to the extension process job object.
		command := exec.CommandContext( //nolint:gosec
			ctx,
			"explorer.exe",
			reportURL,
		)
		return normalizeExplorerOpenError(ctx, command.Run())
	}
	return browser.OpenURL(reportURL)
}

func normalizeExplorerOpenError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	// Explorer commonly exits with status 1 after successfully handing a
	// document URL to the existing shell. Python's os.startfile likewise
	// treats a successful shell dispatch as success.
	if _, ok := errors.AsType[*exec.ExitError](err); ok {
		return nil
	}
	return err
}

func reportFileURL(path string) (string, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	slashPath := filepath.ToSlash(absolutePath)
	if after, ok := strings.CutPrefix(slashPath, "//"); ok {
		hostAndPath := after
		host, urlPath, found := strings.Cut(hostAndPath, "/")
		if found {
			return (&url.URL{Scheme: "file", Host: host, Path: "/" + urlPath}).String(), nil
		}
	}
	if filepath.VolumeName(absolutePath) != "" && !strings.HasPrefix(slashPath, "/") {
		slashPath = "/" + slashPath
	}
	return (&url.URL{Scheme: "file", Path: slashPath}).String(), nil
}
