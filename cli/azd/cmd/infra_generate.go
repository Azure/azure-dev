// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
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
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
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

	force := a.flags.force
	if !force {
		// When the user accepts, generators (including extensions) may overwrite existing files.
		var err error
		force, err = a.confirmOverwrite(ctx)
		if err != nil {
			return nil, err
		}
	}

	cleanup, err := a.activateInfraGenerationProviders(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	spinnerMessage := "Generating infrastructure"

	a.console.ShowSpinner(ctx, spinnerMessage, input.Step)
	generated, err := a.generationManager.Generate(ctx, a.projectConfig, &v1beta.InfraGenerateOptions{Force: force})
	if err != nil {
		a.console.StopSpinner(ctx, spinnerMessage, input.StepFailed)
		return nil, err
	}
	a.console.StopSpinner(ctx, spinnerMessage, input.StepDone)
	synthFS := generated.Files
	if synthFS == nil {
		return nil, nil
	}

	root, err := os.OpenRoot(a.azdCtx.ProjectDirectory())
	if err != nil {
		return nil, fmt.Errorf("opening infrastructure destination: %w", err)
	}
	defer root.Close()
	if err := writeGeneratedInfrastructureFiles(ctx, root, synthFS); err != nil {
		return nil, err
	}
	return nil, nil
}

// activateInfraGenerationProviders activates extensions for configured non-built-in infrastructure providers
// using the environment name from the flags. On success, the caller must invoke the returned cleanup function,
// which is a no-op when no extensions are started.
func (a *infraGenerateAction) activateInfraGenerationProviders(ctx context.Context) (func(), error) {
	var providerNames []string
	for _, entry := range a.projectConfig.InfrastructureConfigs() {
		if entry.Provider != provisioning.NotSpecified &&
			!slices.Contains(provisioning.BuiltInProviderKinds(), entry.Provider) {
			providerNames = append(providerNames, string(entry.Provider))
		}
	}
	if len(providerNames) == 0 {
		return func() {}, nil
	}

	return a.extensionActivator.EnsureInfraGenerationProviders(ctx, providerNames, a.flags.EnvironmentName)
}

// writeGeneratedInfrastructureFiles writes files from synthFS to root, overwriting existing files.
// Errors or cancellation can leave previously written files in place.
func writeGeneratedInfrastructureFiles(ctx context.Context, root *os.Root, synthFS fs.FS) error {
	if err := fs.WalkDir(synthFS, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		contents, err := fs.ReadFile(synthFS, name)
		if err != nil {
			return err
		}
		if err := root.MkdirAll(filepath.Dir(name), osutil.PermissionDirectory); err != nil {
			return err
		}
		return root.WriteFile(name, contents, osutil.PermissionFile)
	}); err != nil {
		return fmt.Errorf("writing generated infrastructure: %w", err)
	}

	return nil
}

// confirmOverwrite checks whether any folder that generation writes into already contains files, and if so,
// asks the user whether to overwrite. It returns true only when the user accepted overwriting, false when every
// folder is empty or missing, and [internal.ErrAbortedByUser] when the user declines. In --no-prompt mode, a
// non-empty folder is an error.
func (a *infraGenerateAction) confirmOverwrite(ctx context.Context) (bool, error) {
	projectDir := a.azdCtx.ProjectDirectory()

	var nonEmpty []string

	for _, folder := range a.generationManager.OutputPaths(a.projectConfig) {
		full := folder
		if !filepath.IsAbs(full) {
			full = filepath.Join(projectDir, full)
		}

		hasFiles, err := folderHasEntries(full)
		if err != nil {
			return false, fmt.Errorf("checking for existing infrastructure: %w", err)
		}

		if hasFiles {
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

// folderHasEntries returns true if dir exists and contains at least one entry.
func folderHasEntries(dir string) (bool, error) {
	f, err := os.Open(dir)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()

	_, err = f.Readdirnames(1)
	if errors.Is(err, io.EOF) {
		return false, nil
	}

	return err == nil, err
}
