// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"

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

	cleanup, err := a.activateInfraGenerationProviders(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	spinnerMessage := "Generating infrastructure"

	a.console.ShowSpinner(ctx, spinnerMessage, input.Step)
	generated, err := a.generationManager.Generate(ctx, a.projectConfig, &v1beta.InfraGenerateOptions{Force: a.flags.force})
	if err != nil {
		a.console.StopSpinner(ctx, spinnerMessage, input.StepFailed)
		return nil, err
	}
	a.console.StopSpinner(ctx, spinnerMessage, input.StepDone)
	synthFS := generated.Files
	if synthFS == nil {
		return nil, nil
	}

	staging, err := os.MkdirTemp("", "infra-generate")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)

	err = fs.WalkDir(synthFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		contents, err := fs.ReadFile(synthFS, path)
		if err != nil {
			return err
		}

		err = os.MkdirAll(filepath.Join(staging, filepath.Dir(path)), osutil.PermissionDirectory)
		if err != nil {
			return err
		}

		return os.WriteFile(filepath.Join(staging, path), contents, osutil.PermissionFile)
	})
	if err != nil {
		return nil, err
	}

	if !a.flags.force {
		if err := a.promptForDuplicates(ctx, staging, a.azdCtx.ProjectDirectory()); err != nil {
			return nil, err
		}
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

// promptForDuplicates asks whether to overwrite existing target paths that match files in staging.
// It returns nil when there are no duplicates or the user accepts the overwrite, and
// [internal.ErrAbortedByUser] when the user declines. In --no-prompt mode, existing files are an error.
func (a *infraGenerateAction) promptForDuplicates(ctx context.Context, staging string, target string) error {
	log.Printf(
		"infrastructure generate, checking for duplicates. source: %s target: %s",
		staging,
		target,
	)

	duplicateFiles, err := determineDuplicates(staging, target)
	if err != nil {
		return fmt.Errorf("checking for overwrites: %w", err)
	}

	if len(duplicateFiles) == 0 {
		return nil
	}

	if a.console.IsNoPromptMode() {
		return &internal.ErrorWithSuggestion{
			Err:        fmt.Errorf("%d generated file(s) already exist in the project", len(duplicateFiles)),
			Message:    "Generated infrastructure files would overwrite existing files.",
			Suggestion: "Rerun with --force to overwrite the existing files.",
		}
	}

	a.console.StopSpinner(ctx, "", input.StepDone)
	a.console.MessageUxItem(ctx, &ux.WarningMessage{
		Description: "The following files would be overwritten by generated versions:",
	})

	for _, file := range duplicateFiles {
		a.console.Message(ctx, fmt.Sprintf(" * %s", file))
	}

	overwrite, err := a.console.Confirm(ctx, input.ConsoleOptions{
		Message:      "Overwrite these files?",
		DefaultValue: false,
	})
	if err != nil {
		return fmt.Errorf("prompting to overwrite: %w", err)
	}

	if !overwrite {
		a.console.Message(ctx, "No files were written.")
		return internal.ErrAbortedByUser
	}

	return nil
}

// determineDuplicates returns paths of source files that also exist under target.
// The returned paths are relative to source and target.
func determineDuplicates(source string, target string) ([]string, error) {
	var duplicateFiles []string
	if err := filepath.WalkDir(source, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if d.IsDir() {
			return nil
		}

		partial, err := filepath.Rel(source, path)
		if err != nil {
			return fmt.Errorf("computing relative path: %w", err)
		}

		if _, err := os.Stat(filepath.Join(target, partial)); err == nil {
			duplicateFiles = append(duplicateFiles, partial)
		}

		return nil
	}); err != nil {
		return nil, fmt.Errorf("enumerating template files: %w", err)
	}
	return duplicateFiles, nil
}
