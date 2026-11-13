// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type infraGenerateFlags struct {
	global *internal.GlobalCommandOptions
	*internal.EnvFlag
	force bool
}

// newInfraGenerateFlags binds environment and force flags to cmd and associates them with global options.
func newInfraGenerateFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *infraGenerateFlags {
	flags := &infraGenerateFlags{
		EnvFlag: &internal.EnvFlag{},
	}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func (f *infraGenerateFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.global = global
	f.EnvFlag.Bind(local, global)
	local.BoolVar(&f.force, "force", false, "Overwrite any existing files without prompting")
}

// newInfraGenerateCmd creates the infrastructure generate command with its aliases.
func newInfraGenerateCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "generate",
		Aliases: []string{"gen", "synth"},
		Short:   "Write IaC for your project to disk, allowing you to manually manage it.",
	}
}

type infraGenerationProviderActivator interface {
	EnsureInfraGenerationProviders(context.Context, []string, string) (func(), error)
}

type infraGenerateAction struct {
	projectConfig      *project.ProjectConfig
	generationManager  *project.InfraGenerationManager
	extensionActivator infraGenerationProviderActivator
	console            input.Console
	azdCtx             *azdcontext.AzdContext
	flags              *infraGenerateFlags
	alphaManager       *alpha.FeatureManager
	calledAs           CmdCalledAs
}

// newInfraGenerateAction creates an infrastructure generation action with the supplied dependencies.
// calledAs identifies the invoked command name for alias-specific warnings.
func newInfraGenerateAction(
	projectConfig *project.ProjectConfig,
	generationManager *project.InfraGenerationManager,
	extensionActivator *middleware.ExtensionActivator,
	flags *infraGenerateFlags,
	console input.Console,
	azdCtx *azdcontext.AzdContext,
	alphaManager *alpha.FeatureManager,
	calledAs CmdCalledAs,
) actions.Action {
	return &infraGenerateAction{
		projectConfig:      projectConfig,
		flags:              flags,
		console:            console,
		azdCtx:             azdCtx,
		alphaManager:       alphaManager,
		calledAs:           calledAs,
		generationManager:  generationManager,
		extensionActivator: extensionActivator,
	}
}

func (a *infraGenerateAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	// Track infra provider from project configuration.
	// Emit "auto" when the provider is unset, so we know auto-detection was used; bucket any
	// non-built-in (extension) provider to "custom" so a user-chosen provider name — which may
	// embed project or organization identifiers — is never emitted (mirrors the provision/up/down
	// path, see provisioning.InfraProviderTelemetryValue).
	// Set it directly on the command span (not the process-global usage bag) so it stays scoped to
	// this cmd.infra.generate span (both `infra generate` and its `synth` alias resolve to that
	// canonical command name) and cannot leak onto — or overwrite — the span-scoped infra.provider
	// recorded by a sibling in-process command (for example a custom `workflows.up` step running
	// provision/up alongside generate/synth).
	if a.projectConfig != nil {
		provider := "auto"
		if kind := a.projectConfig.Infra.Provider; kind != provisioning.NotSpecified {
			provider = provisioning.InfraProviderTelemetryValue(kind)
		}
		tracing.SetAttributesInContext(ctx, fields.InfraProviderKey.String(provider))
	}

	if a.calledAs == "synth" {
		fmt.Fprintln(
			a.console.Handles().Stderr,
			output.WithWarningFormat(
				"WARNING: `azd infra synth` has been renamed and may be removed in a future release."))
		fmt.Fprintf(
			a.console.Handles().Stderr,
			"Next time use %s or %s.\n",
			output.WithHighLightFormat("azd infra gen"),
			output.WithHighLightFormat("azd infra generate"))
	}

	extensionProviderNames := extensionInfraProviderNames(a.projectConfig)

	if len(extensionProviderNames) == 0 { // ie, legacy generation (aspire, old 'resources')
		return nil, a.runLegacyGeneration(ctx)
	}

	return nil, a.runExtensionGeneration(ctx, extensionProviderNames)
}

// extensionInfraProviderNames returns referenced providers that aren't
// built into azd. (ex: azure.resources, azure.foundry, etc...)
func extensionInfraProviderNames(config *project.ProjectConfig) []string {
	var providerNames []string
	for _, entry := range config.InfrastructureConfigs() {
		if !provisioning.IsBuiltInProvider(entry.Provider) {
			providerNames = append(providerNames, string(entry.Provider))
		}
	}

	return providerNames
}

func (a *infraGenerateAction) runLegacyGeneration(ctx context.Context) error {
	const spinnerMessage = "Generating infrastructure"

	a.console.ShowSpinner(ctx, spinnerMessage, input.Step)
	generatedFiles, err := a.generationManager.GenerateLegacy(ctx, a.projectConfig)
	if err != nil {
		a.console.StopSpinner(ctx, spinnerMessage, input.StepFailed)
		return err
	}
	a.console.StopSpinner(ctx, spinnerMessage, input.StepDone)

	if generatedFiles == nil {
		return nil
	}

	return a.writeLegacyGeneratedFiles(ctx, a.azdCtx.ProjectDirectory(), generatedFiles)
}

func (a *infraGenerateAction) runExtensionGeneration(ctx context.Context, providerNames []string) error {
	force := a.flags.force
	if !force {
		// Extensions write their own files, so check for existing output before running them.
		var err error
		force, err = a.confirmExtensionOverwrite(ctx)
		if err != nil {
			return err
		}
	}

	//nolint:lll
	cleanupActivatedProviders, err := a.extensionActivator.EnsureInfraGenerationProviders(
		ctx, providerNames, a.flags.EnvironmentName)
	if err != nil {
		return err
	}
	defer cleanupActivatedProviders()

	const spinnerMessage = "Generating infrastructure"

	a.console.ShowSpinner(ctx, spinnerMessage, input.Step)
	err = a.generationManager.GenerateExtensions(
		ctx, a.projectConfig, &v1beta.InfraGenerateOptions{Force: force})
	if err != nil {
		a.console.StopSpinner(ctx, spinnerMessage, input.StepFailed)
		return err
	}
	a.console.StopSpinner(ctx, spinnerMessage, input.StepDone)
	return nil
}

// confirmExtensionOverwrite checks whether any extension output folder already contains files and, if so, asks whether
// to overwrite. It returns false when every folder is empty or missing, and [internal.ErrAbortedByUser] when declined.
// In --no-prompt mode, a non-empty folder is an error.
func (a *infraGenerateAction) confirmExtensionOverwrite(ctx context.Context) (bool, error) {
	projectDir := a.azdCtx.ProjectDirectory()

	var nonEmpty []string

	for _, folder := range a.generationManager.ExtensionOutputPaths(a.projectConfig) {
		full := folder
		if !filepath.IsAbs(full) {
			full = filepath.Join(projectDir, full)
		}

		empty, err := folderEmpty(full)
		if err != nil {
			return false, fmt.Errorf("checking for existing infrastructure: %w", err)
		}

		if !empty {
			nonEmpty = append(nonEmpty, folder)
		}
	}

	if len(nonEmpty) == 0 {
		return false, nil
	}

	if a.console.IsNoPromptMode() {
		return false, &internal.ErrorWithSuggestion{
			Err:        fmt.Errorf("infrastructure output folders are not empty: %s", strings.Join(nonEmpty, ", ")),
			Message:    "Generated infrastructure would be written into folders that already contain files.",
			Suggestion: "Rerun with --force to overwrite the existing files.",
		}
	}

	a.console.MessageUxItem(ctx, &ux.WarningMessage{
		Description: "The following folders already contain files that generated versions may overwrite:",
	})

	for _, folder := range nonEmpty {
		a.console.Message(ctx, fmt.Sprintf(" * %s", folder))
	}

	overwrite, err := a.console.Confirm(ctx, input.ConsoleOptions{
		Message:      "Overwrite existing files?",
		DefaultValue: false,
	})
	if err != nil {
		return false, fmt.Errorf("prompting to overwrite: %w", err)
	}

	if !overwrite {
		a.console.Message(ctx, "No files were written.")
		return false, internal.ErrAbortedByUser
	}

	return true, nil
}

// folderEmpty returns true if dir has no entries or does not exist.
func folderEmpty(dir string) (bool, error) {
	f, err := os.Open(dir)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()

	_, err = f.Readdirnames(1)
	if errors.Is(err, io.EOF) {
		return true, nil
	}

	if err != nil {
		return false, err
	}

	return false, nil
}
