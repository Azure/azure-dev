// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"text/tabwriter"

	"azure.ai.customtraining/internal/utils"
	"azure.ai.customtraining/pkg/client"
	"azure.ai.customtraining/pkg/models"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/spf13/cobra"
)

func newJobShowCommand() *cobra.Command {
	var name string
	var outputFormat string

	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show details of a specific training job",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())

			if name == "" {
				return fmt.Errorf("--name is required")
			}

			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			envValues, err := utils.GetEnvironmentValues(ctx, azdClient)
			if err != nil {
				return fmt.Errorf("failed to get environment values: %w", err)
			}

			accountName := envValues[utils.EnvAzureAccountName]
			projectName := envValues[utils.EnvAzureProjectName]
			tenantID := envValues[utils.EnvAzureTenantID]

			if accountName == "" || projectName == "" {
				return fmt.Errorf(
					"environment not configured. Run 'azd ai training init' first",
				)
			}

			credential, err := azidentity.NewAzureDeveloperCLICredential(
				&azidentity.AzureDeveloperCLICredentialOptions{
					TenantID:                   tenantID,
					AdditionallyAllowedTenants: []string{"*"},
				},
			)
			if err != nil {
				return fmt.Errorf("failed to create azure credential: %w", err)
			}

			endpoint := buildProjectEndpoint(accountName, projectName)
			apiClient, err := client.NewClient(endpoint, credential)
			if err != nil {
				return fmt.Errorf("failed to create API client: %w", err)
			}

			// Always fetch job details first — required for both formats
			spinner := ux.NewSpinner(&ux.SpinnerOptions{
				Text: "Fetching job details...",
			})
			_ = spinner.Start(ctx)

			job, err := apiClient.GetJob(ctx, name)
			if err != nil {
				_ = spinner.Stop(ctx)
				return fmt.Errorf("failed to get job: %w", err)
			}

			// JSON mode: return raw job response only (backward compatible)
			if utils.OutputFormat(outputFormat) == utils.FormatJSON {
				_ = spinner.Stop(ctx)
				return utils.PrintObject(job, utils.FormatJSON)
			}

			// Rich display: fetch supplementary data with progress updates
			details := fetchJobDetails(ctx, apiClient, name, spinner)
			details.Job = job

			_ = spinner.Stop(ctx)
			printJobDetails(details)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Job name/ID (required)")
	cmd.Flags().StringVarP(
		&outputFormat, "output", "o", "table",
		"Output format (table|json)",
	)

	return cmd
}

// jobDetails aggregates all data needed for the rich job show display.
type jobDetails struct {
	Job       *models.JobResource
	History   *models.RunHistory
	Metrics   map[string]*models.MetricsFullResponse
	Artifacts *models.ArtifactList
}

// fetchJobDetails fetches run history, metrics, and artifacts concurrently
// while updating the spinner text to show progress.
func fetchJobDetails(
	ctx context.Context, apiClient *client.Client, jobID string, spinner *ux.Spinner,
) *jobDetails {
	details := &jobDetails{
		Metrics: make(map[string]*models.MetricsFullResponse),
	}
	debug := rootFlags.Debug

	type step struct {
		name string
		done bool
	}
	steps := []*step{
		{name: "run history"},
		{name: "metrics"},
		{name: "artifacts"},
	}

	var mu sync.Mutex

	updateSpinner := func(completed string) {
		var pending []string
		for _, s := range steps {
			if s.name == completed {
				s.done = true
			}
			if !s.done {
				pending = append(pending, s.name)
			}
		}
		if len(pending) == 0 {
			spinner.UpdateText("Finalizing...")
		} else {
			spinner.UpdateText("Fetching " + strings.Join(pending, ", ") + "...")
		}
	}

	spinner.UpdateText("Fetching run history, metrics, artifacts...")

	var wg sync.WaitGroup

	// Fetch run history
	wg.Add(1)
	go func() {
		defer wg.Done()
		history, err := apiClient.GetRunHistory(ctx, jobID)
		if debug {
			if err != nil {
				fmt.Fprintf(os.Stderr, "[DEBUG] run history error: %v\n", err)
			} else if history == nil {
				fmt.Fprintf(os.Stderr, "[DEBUG] run history: not found (404)\n")
			} else {
				fmt.Fprintf(
					os.Stderr, "[DEBUG] run history: status=%s duration=%s\n",
					history.Status, history.Duration,
				)
			}
		}
		mu.Lock()
		details.History = history
		updateSpinner("run history")
		mu.Unlock()
	}()

	// Fetch artifacts
	wg.Add(1)
	go func() {
		defer wg.Done()
		artifacts, err := apiClient.ListArtifacts(ctx, jobID)
		if debug {
			if err != nil {
				fmt.Fprintf(os.Stderr, "[DEBUG] artifacts error: %v\n", err)
			} else if artifacts == nil {
				fmt.Fprintf(os.Stderr, "[DEBUG] artifacts: not found (404)\n")
			} else {
				fmt.Fprintf(
					os.Stderr, "[DEBUG] artifacts: %d file(s)\n",
					len(artifacts.Value),
				)
			}
		}
		mu.Lock()
		details.Artifacts = artifacts
		updateSpinner("artifacts")
		mu.Unlock()
	}()

	// Fetch metric names, then fetch each metric's latest values
	wg.Add(1)
	go func() {
		defer wg.Done()
		metricsList, err := apiClient.ListMetrics(ctx, jobID)
		if err != nil && debug {
			fmt.Fprintf(os.Stderr, "[DEBUG] metrics list: %v\n", err)
		}
		if metricsList == nil || len(metricsList.Value) == 0 {
			if debug {
				fmt.Fprintf(os.Stderr, "[DEBUG] no metrics available\n")
			}
			mu.Lock()
			updateSpinner("metrics")
			mu.Unlock()
			return
		}

		if debug {
			fmt.Fprintf(
				os.Stderr, "[DEBUG] found %d metric definition(s)\n",
				len(metricsList.Value),
			)
		}

		var metricsWg sync.WaitGroup
		for _, def := range metricsList.Value {
			for colName := range def.Columns {
				metricsWg.Add(1)
				go func() {
					defer metricsWg.Done()
					full, mErr := apiClient.GetMetricsFull(
						ctx, jobID, colName,
					)
					if mErr != nil && debug {
						fmt.Fprintf(
							os.Stderr,
							"[DEBUG] metric %s: %v\n", colName, mErr,
						)
					}
					if full != nil {
						mu.Lock()
						details.Metrics[colName] = full
						mu.Unlock()
					}
				}()
			}
		}
		metricsWg.Wait()

		mu.Lock()
		updateSpinner("metrics")
		mu.Unlock()
	}()

	wg.Wait()
	return details
}

// statusIndicator returns a colored status indicator.
func statusIndicator(status string) string {
	switch strings.ToLower(status) {
	case "completed":
		return "✓ Completed"
	case "failed":
		return "✗ Failed"
	case "running", "starting", "preparing":
		return "● " + status
	case "canceled", "cancelled":
		return "○ Canceled"
	case "notstarted", "queued":
		return "◌ " + status
	default:
		return status
	}
}

// printJobDetails renders the rich job display to stdout.
func printJobDetails(d *jobDetails) {
	job := d.Job
	props := job.Properties

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	// Header
	fmt.Fprintf(w, "Job:\t%s\n", job.Name)
	if props.DisplayName != "" && props.DisplayName != job.Name {
		fmt.Fprintf(w, "Display Name:\t%s\n", props.DisplayName)
	}
	fmt.Fprintf(w, "Status:\t%s\n", statusIndicator(props.Status))
	if props.Description != "" {
		fmt.Fprintf(w, "Description:\t%s\n", props.Description)
	}
	w.Flush()
	fmt.Println()

	// Timing — prefer run history (more detailed), fall back to job properties
	printTimingSection(d)

	// Compute
	printComputeSection(d)

	// Environment & Code
	w = tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "Environment:\t%s\n", valueOrDash(props.EnvironmentID))

	// Show code ID from job props, or fall back to run history inputs
	codeID := props.CodeID
	if codeID == "" && d.History != nil {
		if codeAsset, ok := d.History.Inputs["code"]; ok {
			codeID = codeAsset.AssetID
		}
	}
	if codeID != "" {
		fmt.Fprintf(w, "Code:\t%s\n", codeID)
	}
	w.Flush()

	// Distribution
	if props.Distribution != nil {
		fmt.Println()
		w = tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintf(w, "Distribution:\t%s\n", props.Distribution.DistributionType)
		if props.Distribution.ProcessCountPerInstance > 0 {
			fmt.Fprintf(
				w, "Processes/Node:\t%d\n",
				props.Distribution.ProcessCountPerInstance,
			)
		}
		w.Flush()
	}

	// Resources
	if props.Resources != nil {
		fmt.Println()
		w = tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		if props.Resources.InstanceCount > 0 {
			fmt.Fprintf(
				w, "Instance Count:\t%d\n", props.Resources.InstanceCount,
			)
		}
		if props.Resources.InstanceType != "" {
			fmt.Fprintf(
				w, "Instance Type:\t%s\n", props.Resources.InstanceType,
			)
		}
		if props.Resources.ShmSize != "" {
			fmt.Fprintf(w, "Shared Memory:\t%s\n", props.Resources.ShmSize)
		}
		w.Flush()
	}

	// Inputs — merge job inputs with run history inputs for asset IDs
	printInputsSection(props.Inputs, d.History)

	// Outputs — merge job outputs with run history outputs for asset IDs
	printOutputsSection(props.Outputs, d.History)

	// Error (from run history)
	printErrorSection(d)

	// Metrics
	if len(d.Metrics) > 0 {
		printMetricsSection(d.Metrics)
	}

	// Artifacts
	if d.Artifacts != nil && len(d.Artifacts.Value) > 0 {
		printArtifactsSection(d.Artifacts)
	}
}

func printTimingSection(d *jobDetails) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	if d.History != nil {
		h := d.History
		if h.CreatedUTC != "" {
			fmt.Fprintf(w, "Created:\t%s\n", h.CreatedUTC)
		}
		if h.StartTimeUTC != "" {
			fmt.Fprintf(w, "Started:\t%s\n", h.StartTimeUTC)
		}
		if h.EndTimeUTC != "" {
			fmt.Fprintf(w, "Ended:\t%s\n", h.EndTimeUTC)
		}
		if h.Duration != "" && h.Duration != "00:00:00" {
			fmt.Fprintf(w, "Duration:\t%s\n", h.Duration)
		}
		if h.ComputeDuration != "" && h.ComputeDuration != "00:00:00" {
			fmt.Fprintf(w, "Compute Time:\t%s\n", h.ComputeDuration)
		}
		if h.CreatedBy != nil && h.CreatedBy.UserName != "" {
			fmt.Fprintf(w, "Created By:\t%s\n", h.CreatedBy.UserName)
		}
	} else if d.Job.SystemData != nil {
		sd := d.Job.SystemData
		if sd.CreatedAt != "" {
			fmt.Fprintf(w, "Created:\t%s\n", sd.CreatedAt)
		}
		if sd.CreatedBy != "" {
			fmt.Fprintf(w, "Created By:\t%s\n", sd.CreatedBy)
		}
	}

	w.Flush()
	fmt.Println()
}

func printComputeSection(d *jobDetails) {
	computeID := d.Job.Properties.ComputeID
	if computeID == "" && d.History == nil {
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	if computeID != "" {
		// Extract just the compute name from the full ARM ID
		parts := strings.Split(computeID, "/")
		computeName := parts[len(parts)-1]
		fmt.Fprintf(w, "Compute:\t%s\n", computeName)
	}

	if d.History != nil && d.History.Compute != nil {
		c := d.History.Compute
		if c.VMSize != "" {
			fmt.Fprintf(w, "VM Size:\t%s\n", c.VMSize)
		}
		if c.InstanceCount > 0 {
			fmt.Fprintf(w, "Nodes:\t%d\n", c.InstanceCount)
		}
		if c.GPUCount > 0 {
			fmt.Fprintf(w, "GPUs:\t%d\n", c.GPUCount)
		}
		if c.Priority != "" {
			fmt.Fprintf(w, "Priority:\t%s\n", c.Priority)
		}
	}

	w.Flush()
	fmt.Println()
}

func printInputsSection(inputs map[string]models.JobInput, history *models.RunHistory) {
	// Merge: job inputs + any extra inputs from run history not in the job response
	type mergedInput struct {
		Name     string
		Type     string
		Mode     string
		Value    string // URI or literal value
		AssetID  string // from run history
	}

	seen := make(map[string]bool)
	var merged []mergedInput

	names := slices.Sorted(func(yield func(string) bool) {
		for name := range inputs {
			if !yield(name) {
				return
			}
		}
	})

	for _, name := range names {
		input := inputs[name]
		seen[name] = true
		m := mergedInput{
			Name: name,
			Type: input.JobInputType,
			Mode: input.Mode,
		}
		if input.JobInputType == "literal" {
			m.Value = input.Value
		} else {
			m.Value = input.URI
		}
		// Enrich with run history asset ID if job URI is empty
		if m.Value == "" && history != nil {
			if ha, ok := history.Inputs[name]; ok {
				m.AssetID = ha.AssetID
			}
		}
		merged = append(merged, m)
	}

	// Add inputs only present in run history (e.g., synthetic "_code_" input)
	if history != nil {
		histNames := slices.Sorted(func(yield func(string) bool) {
			for name := range history.Inputs {
				if !yield(name) {
					return
				}
			}
		})
		for _, name := range histNames {
			if seen[name] || name == "code" {
				continue // skip "code" — shown separately
			}
			ha := history.Inputs[name]
			merged = append(merged, mergedInput{
				Name:    name,
				Type:    ha.Type,
				AssetID: ha.AssetID,
			})
		}
	}

	if len(merged) == 0 {
		return
	}

	fmt.Println()
	fmt.Println("Inputs:")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "  NAME\tTYPE\tMODE\tVALUE\n")
	fmt.Fprintf(w, "  ----\t----\t----\t-----\n")

	for _, m := range merged {
		val := m.Value
		if val == "" {
			val = m.AssetID
		}
		fmt.Fprintf(
			w, "  %s\t%s\t%s\t%s\n",
			m.Name,
			valueOrDash(m.Type),
			valueOrDash(m.Mode),
			valueOrDash(val),
		)
	}
	w.Flush()
}

func printOutputsSection(outputs map[string]models.JobOutput, history *models.RunHistory) {
	type mergedOutput struct {
		Name    string
		Type    string
		Mode    string
		URI     string
		AssetID string
	}

	seen := make(map[string]bool)
	var merged []mergedOutput

	names := slices.Sorted(func(yield func(string) bool) {
		for name := range outputs {
			if !yield(name) {
				return
			}
		}
	})

	for _, name := range names {
		output := outputs[name]
		seen[name] = true
		m := mergedOutput{
			Name: name,
			Type: output.JobOutputType,
			Mode: output.Mode,
			URI:  output.URI,
		}
		if m.URI == "" && history != nil {
			if ha, ok := history.Outputs[name]; ok {
				m.AssetID = ha.AssetID
			}
		}
		merged = append(merged, m)
	}

	// Add outputs only in run history
	if history != nil {
		histNames := slices.Sorted(func(yield func(string) bool) {
			for name := range history.Outputs {
				if !yield(name) {
					return
				}
			}
		})
		for _, name := range histNames {
			if seen[name] {
				continue
			}
			ha := history.Outputs[name]
			merged = append(merged, mergedOutput{
				Name:    name,
				Type:    ha.Type,
				AssetID: ha.AssetID,
			})
		}
	}

	if len(merged) == 0 {
		return
	}

	fmt.Println()
	fmt.Println("Outputs:")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "  NAME\tTYPE\tMODE\tURI\n")
	fmt.Fprintf(w, "  ----\t----\t----\t---\n")

	for _, m := range merged {
		val := m.URI
		if val == "" {
			val = m.AssetID
		}
		fmt.Fprintf(
			w, "  %s\t%s\t%s\t%s\n",
			m.Name,
			valueOrDash(m.Type),
			valueOrDash(m.Mode),
			valueOrDash(val),
		)
	}
	w.Flush()
}

func printErrorSection(d *jobDetails) {
	if d.History == nil || d.History.Error == nil || d.History.Error.Error == nil {
		return
	}

	e := d.History.Error.Error
	if e.Message == "" {
		return
	}

	fmt.Println()
	fmt.Println("Error:")
	if e.Code != "" {
		fmt.Printf("  Code:    %s\n", e.Code)
	}
	fmt.Printf("  Message: %s\n", e.Message)
}

func printMetricsSection(metrics map[string]*models.MetricsFullResponse) {
	fmt.Println()
	fmt.Println("Metrics:")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "  NAME\tLATEST VALUE\tSTEP\n")
	fmt.Fprintf(w, "  ----\t------------\t----\n")

	names := slices.Sorted(func(yield func(string) bool) {
		for name := range metrics {
			if !yield(name) {
				return
			}
		}
	})

	for _, name := range names {
		m := metrics[name]
		if len(m.Value) == 0 {
			continue
		}
		// Get the last data point
		latest := m.Value[len(m.Value)-1]
		val := "-"
		if v, ok := latest.Data[name]; ok {
			val = fmt.Sprintf("%v", v)
		} else if len(latest.Data) > 0 {
			// Use first available value if key doesn't match metric name
			for _, v := range latest.Data {
				val = fmt.Sprintf("%v", v)
				break
			}
		}
		fmt.Fprintf(w, "  %s\t%s\t%d\n", name, val, latest.Step)
	}
	w.Flush()
}

func printArtifactsSection(artifacts *models.ArtifactList) {
	fmt.Println()
	fmt.Printf("Artifacts: %d file(s)\n", len(artifacts.Value))
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	for _, a := range artifacts.Value {
		fmt.Fprintf(w, "  %s\n", a.Path)
	}
	w.Flush()
}

func valueOrDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
