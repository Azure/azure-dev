package cmd

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/otiai10/copy"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type infraSynthFlags struct {
	global *internal.GlobalCommandOptions
	*internal.EnvFlag
	force bool
}

func newInfraSynthFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *infraSynthFlags {
	flags := &infraSynthFlags{
		EnvFlag: &internal.EnvFlag{},
	}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func (f *infraSynthFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.global = global
	f.EnvFlag.Bind(local, global)
	local.BoolVar(&f.force, "force", false, "Overwrite any existing files without prompting")
}

func newInfraSynthCmd() *cobra.Command {
	return &cobra.Command{
		Use: "synth",
		Short: fmt.Sprintf(
			"Write IaC for your project to disk, allowing you to manage it by hand. %s", output.WithWarningFormat("(Beta)")),
	}
}

type infraSynthAction struct {
	projectConfig *project.ProjectConfig
	importManager *project.ImportManager
	console       input.Console
	azdCtx        *azdcontext.AzdContext
	flags         *infraSynthFlags
	alphaManager  *alpha.FeatureManager
}

func newInfraSynthAction(
	projectConfig *project.ProjectConfig,
	importManager *project.ImportManager,
	flags *infraSynthFlags,
	console input.Console,
	azdCtx *azdcontext.AzdContext,
	alphaManager *alpha.FeatureManager,
) actions.Action {
	return &infraSynthAction{
		projectConfig: projectConfig,
		importManager: importManager,
		flags:         flags,
		console:       console,
		azdCtx:        azdCtx,
		alphaManager:  alphaManager,
	}
}

var infraSynthFeature = alpha.MustFeatureKey("infraSynth")

func (a *infraSynthAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if !a.alphaManager.IsEnabled(infraSynthFeature) {
		return nil, fmt.Errorf(
			"infrastructure synthesis is currently under alpha support and must be explicitly enabled."+
				" Run `%s` to enable this feature.", alpha.GetEnableCommand(infraSynthFeature),
		)
	}

	a.console.WarnForFeature(ctx, infraSynthFeature)

	spinnerMessage := "Synthesizing infrastructure"

	a.console.ShowSpinner(ctx, spinnerMessage, input.Step)
	synthFS, err := a.importManager.SynthAllInfrastructure(ctx, a.projectConfig)
	if err != nil {
		a.console.StopSpinner(ctx, spinnerMessage, input.StepFailed)
		return nil, err
	}
	a.console.StopSpinner(ctx, spinnerMessage, input.StepDone)

	staging, err := os.MkdirTemp("", "infra-synth")
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

		err = os.MkdirAll(filepath.Join(staging, filepath.Dir(path)), osutil.PermissionDirectoryOwnerOnly)
		if err != nil {
			return err
		}

		return os.WriteFile(filepath.Join(staging, path), contents, d.Type().Perm())
	})
	if err != nil {
		return nil, err
	}

	options := copy.Options{}

	if a.flags.force {
		options.Skip = func(fileInfo os.FileInfo, src, dest string) (bool, error) {
			return false, nil
		}

	} else {
		skipStagingFiles, err := a.promptForDuplicates(ctx, staging, a.azdCtx.ProjectDirectory())
		if err != nil {
			return nil, err
		}

		if skipStagingFiles != nil {
			options.Skip = func(fileInfo os.FileInfo, src, dest string) (bool, error) {
				_, skip := skipStagingFiles[src]
				return skip, nil
			}
		}
	}

	if err := copy.Copy(staging, a.azdCtx.ProjectDirectory(), options); err != nil {
		return nil, fmt.Errorf("copying contents from temp staging directory: %w", err)
	}

	return nil, nil
}

func (a *infraSynthAction) promptForDuplicates(
	ctx context.Context, staging string, target string) (skipSourceFiles map[string]struct{}, err error) {
	log.Printf(
		"infrastructure synth, checking for duplicates. source: %s target: %s",
		staging,
		target,
	)

	duplicateFiles, err := determineDuplicates(staging, target)
	if err != nil {
		return nil, fmt.Errorf("checking for overwrites: %w", err)
	}

	if len(duplicateFiles) > 0 {
		a.console.StopSpinner(ctx, "", input.StepDone)
		a.console.MessageUxItem(ctx, &ux.WarningMessage{
			Description: "The following files would be overwritten by synthesized versions:",
		})

		for _, file := range duplicateFiles {
			a.console.Message(ctx, fmt.Sprintf(" * %s", file))
		}

		selection, err := a.console.Select(ctx, input.ConsoleOptions{
			Message: "What would you like to do with these files?",
			Options: []string{
				"Overwrite with the synthesized versions",
				"Keep my existing files unchanged",
			},
		})

		if err != nil {
			return nil, fmt.Errorf("prompting to overwrite: %w", err)
		}

		switch selection {
		case 0: // overwrite
			return nil, nil
		case 1: // keep
			skipSourceFiles = make(map[string]struct{}, len(duplicateFiles))
			for _, file := range duplicateFiles {
				// this also cleans the result, which is important for matching
				sourceFile := filepath.Join(staging, file)
				skipSourceFiles[sourceFile] = struct{}{}
			}
			return skipSourceFiles, nil
		}
	}

	return nil, nil
}

// Returns files that are both present in source and target.
// The files returned are expressed in their relative paths to source/target.
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
