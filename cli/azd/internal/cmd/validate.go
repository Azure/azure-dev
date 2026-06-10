// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/validate"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// ValidateFlags holds the flags for the validate command.
type ValidateFlags struct {
	global *internal.GlobalCommandOptions
	// gate filters the pipeline to run only the specified gate.
	gate string
	*internal.EnvFlag
}

// Bind registers the validate flags on the given flag set.
func (f *ValidateFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.EnvFlag = &internal.EnvFlag{}
	f.EnvFlag.Bind(local, global)
	local.StringVar(
		&f.gate,
		"gate",
		"",
		"Run only the specified validation gate (e.g. \"local-preflight\").",
	)
	f.global = global
}

// NewValidateFlags creates and binds flags for the validate command.
func NewValidateFlags(
	cmd *cobra.Command, global *internal.GlobalCommandOptions,
) *ValidateFlags {
	flags := &ValidateFlags{}
	flags.Bind(cmd.Flags(), global)
	return flags
}

// NewValidateCmd creates the cobra command for "azd validate".
func NewValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate your project configuration and Azure readiness.",
		Long: `Validate runs a pipeline of validation gates against your project
and environment to detect issues before provisioning. Each gate performs
a set of checks (e.g. role assignments, resource quotas, configuration)
and reports warnings or errors with actionable suggestions.

Use --gate to run a specific gate in isolation.`,
	}
}

// ValidateAction implements the azd validate command.
type ValidateAction struct {
	flags          *ValidateFlags
	projectConfig  *project.ProjectConfig
	projectManager project.ProjectManager
	env            *environment.Environment
	console        input.Console
	formatter      output.Formatter
	writer         io.Writer
	gates          []validate.Gate
}

// NewValidateAction creates a new ValidateAction with all dependencies
// injected via IoC.
func NewValidateAction(
	flags *ValidateFlags,
	projectConfig *project.ProjectConfig,
	projectManager project.ProjectManager,
	env *environment.Environment,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
) actions.Action {
	action := &ValidateAction{
		flags:          flags,
		projectConfig:  projectConfig,
		projectManager: projectManager,
		env:            env,
		console:        console,
		formatter:      formatter,
		writer:         writer,
	}

	// Register built-in gates
	action.RegisterGate(validate.NewProjectConfigGate())

	return action
}

// RegisterGate adds a validation gate to be executed by this action.
// Gates are executed in the order they are registered.
func (a *ValidateAction) RegisterGate(gate validate.Gate) {
	a.gates = append(a.gates, gate)
}

// Run executes the validation pipeline and displays results.
func (a *ValidateAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	a.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title:     "Validating project (azd validate)",
		TitleNote: "Running validation checks against your project and environment",
	})

	if err := a.projectManager.Initialize(ctx, a.projectConfig); err != nil {
		return nil, err
	}

	pipeline := validate.NewPipeline(validate.PipelineOptions{
		OnError: validate.OnErrorContinue,
	})

	// Register gates, optionally filtering by --gate flag
	gateFilter := a.flags.gate
	registered := 0
	for _, gate := range a.gates {
		if gateFilter != "" && gate.Name() != gateFilter {
			continue
		}
		pipeline.AddGate(gate)
		registered++
	}

	if gateFilter != "" && registered == 0 {
		return nil, fmt.Errorf(
			"unknown gate %q; available gates: %s",
			gateFilter, a.availableGateNames(),
		)
	}

	pCtx := &validate.PipelineContext{
		Console:     a.console,
		Environment: a.env,
		Project:     a.projectConfig,
	}

	result, err := pipeline.Run(ctx, pCtx)
	if err != nil {
		return nil, err
	}

	// Display the validation report
	report := &validate.ValidationReport{Result: result}
	a.console.MessageUxItem(ctx, report)

	// Format output for --output json
	if a.formatter.Kind() == output.JsonFormat {
		if err := a.formatter.Format(result, a.writer, nil); err != nil {
			return nil, fmt.Errorf("formatting output: %w", err)
		}
	}

	if result.HasErrors() {
		return &actions.ActionResult{
			Message: &actions.ResultMessage{
				Header: fmt.Sprintf(
					"Validation found %d error(s) and %d warning(s).",
					result.TotalErrors(), result.TotalWarnings(),
				),
			},
		}, nil
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: "Validation completed successfully.",
		},
	}, nil
}

// availableGateNames returns a comma-separated list of registered gate names.
func (a *ValidateAction) availableGateNames() string {
	if len(a.gates) == 0 {
		return "(none registered)"
	}
	names := make([]string, len(a.gates))
	for i, g := range a.gates {
		names[i] = g.Name()
	}
	return fmt.Sprintf("%v", names)
}
