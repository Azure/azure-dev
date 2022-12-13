package repository

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/otiai10/copy"
)

type Initializer interface {
	Initialize(ctx context.Context, templateUrl string, templateBranch string) error
	InitializeEmpty(ctx context.Context) error
}

type initializer struct {
	azdCtx  *azdcontext.AzdContext
	console input.Console
	gitCli  git.GitCli
}

func NewInitializer(azdCtx *azdcontext.AzdContext,
	console input.Console,
	gitCli git.GitCli) Initializer {
	return &initializer{
		azdCtx:  azdCtx,
		console: console,
		gitCli:  gitCli,
	}
}

func (i *initializer) Initialize(ctx context.Context, templateUrl string, templateBranch string) error {
	var err error
	stepMessage := fmt.Sprintf("Downloading template code to: %s", output.WithLinkFormat("%s", i.azdCtx.ProjectDirectory()))
	i.console.ShowSpinner(ctx, stepMessage, input.Step)
	defer i.console.StopSpinner(ctx, stepMessage+"\n", input.GetStepResultFormat(err))

	staging, err := os.MkdirTemp("", "az-dev-template")

	if err != nil {
		return fmt.Errorf("creating temp folder: %w", err)
	}

	// Attempt to remove the temporary directory we cloned the template into, but don't fail the
	// overall operation if we can't.
	defer func() {
		_ = os.RemoveAll(staging)
	}()

	target := i.azdCtx.ProjectDirectory()

	err = i.gitCli.FetchCode(ctx, templateUrl, templateBranch, staging)
	if err != nil {
		return fmt.Errorf("\nfetching template: %w", err)
	}

	log.Printf(
		"template init, checking for duplicates. source: %s target: %s",
		staging,
		target,
	)

	duplicateFiles, err := determineDuplicates(staging, target)
	if err != nil {
		return fmt.Errorf("checking for overwrites: %s", err)
	}

	if len(duplicateFiles) > 0 {
		i.console.Message(ctx, "warning: the following files will be overwritten with the versions from the template:")
		for _, file := range duplicateFiles {
			i.console.Message(ctx, fmt.Sprintf(" * %s\n", file))
		}

		overwrite, err := i.console.Confirm(ctx, input.ConsoleOptions{
			Message:      "Overwrite files with versions from template?",
			DefaultValue: false,
		})

		if err != nil {
			return fmt.Errorf("prompting to overwrite: %w", err)
		}

		if !overwrite {
			return errors.New("confirmation declined")
		}
	}

	if err := copy.Copy(staging, target); err != nil {
		return fmt.Errorf("copying template contents: %w", err)
	}

	i.console.StopSpinner(ctx, stepMessage+"\n", input.GetStepResultFormat(err))
	err = i.writeAzdAssets(ctx)
	if err != nil {
		return err
	}

	return nil
}

// Initializes an empty (bare minimum) azd repository.
func (i *initializer) InitializeEmpty(ctx context.Context) error {
	return i.writeAzdAssets(ctx)
}

func (i *initializer) writeAzdAssets(ctx context.Context) error {
	// Check to see if `azure.yaml` exists, and if it doesn't, create it.
	if _, err := os.Stat(i.azdCtx.ProjectPath()); errors.Is(err, os.ErrNotExist) {
		stepMessage := fmt.Sprintf("Creating a new %s file.", azdcontext.ProjectFileName)

		i.console.ShowSpinner(ctx, stepMessage, input.Step)
		_, err = project.NewProject(i.azdCtx.ProjectPath(), i.azdCtx.GetDefaultProjectName())
		i.console.StopSpinner(ctx, stepMessage, input.GetStepResultFormat(err))

		if err != nil {
			return fmt.Errorf("failed to create a project file: %w", err)
		}
	}

	//create .azure when running azd init
	err := os.MkdirAll(
		filepath.Join(i.azdCtx.ProjectDirectory(), azdcontext.EnvironmentDirectoryName),
		osutil.PermissionDirectory,
	)
	if err != nil {
		return fmt.Errorf("failed to create a directory: %w", err)
	}

	//create .gitignore or open existing .gitignore file, and contains .azure
	gitignoreFile, err := os.OpenFile(
		filepath.Join(i.azdCtx.ProjectDirectory(), ".gitignore"),
		os.O_APPEND|os.O_RDWR|os.O_CREATE,
		osutil.PermissionFile,
	)
	if err != nil {
		return fmt.Errorf("fail to create or open .gitignore: %w", err)
	}
	defer gitignoreFile.Close()

	writeGitignoreFile := true
	//bufio scanner splits on new lines by default
	scanner := bufio.NewScanner(gitignoreFile)
	for scanner.Scan() {
		if azdcontext.EnvironmentDirectoryName == scanner.Text() {
			writeGitignoreFile = false
		}
	}

	if writeGitignoreFile {
		newLine := osutil.GetNewLineSeparator()
		_, err := gitignoreFile.WriteString(newLine + azdcontext.EnvironmentDirectoryName + newLine)
		if err != nil {
			return fmt.Errorf("fail to write '%s' in .gitignore: %w", azdcontext.EnvironmentDirectoryName, err)
		}
	}

	return nil
}

// Returns files that are both present in source and target.
// The returned files are full paths to the target files.
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
