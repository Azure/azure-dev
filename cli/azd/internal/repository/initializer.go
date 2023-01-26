// Package repository provides handling of files in the user's code repository.
package repository

import (
	"bufio"
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/azure/azure-dev/cli/azd/resources"
	"github.com/otiai10/copy"
	"golang.org/x/exp/maps"
)

// Initializer handles the initialization of a local repository.
type Initializer struct {
	console input.Console
	gitCli  git.GitCli
}

func NewInitializer(
	console input.Console,
	gitCli git.GitCli) *Initializer {
	return &Initializer{
		console: console,
		gitCli:  gitCli,
	}
}

type InfraUseOptions struct {
	Language string
	Host     string
}

func LanguageDisplayOptions() map[string]string {
	return map[string]string{
		".NET / C# / F#": "dotnet",
		"Python":         "python",
		"NodeJS":         "node",
		"Java":           "java",
	}
}

func (i *Initializer) InitializeInfra(ctx context.Context,
	azdCtx *azdcontext.AzdContext,
	templateUrl string,
	templateBranch string,
	useOptions InfraUseOptions) error {
	var err error
	stepMessage := fmt.Sprintf("Downloading template code to: %s", output.WithLinkFormat("%s", azdCtx.ProjectDirectory()))
	i.console.ShowSpinner(ctx, stepMessage, input.Step)
	defer i.console.StopSpinner(ctx, stepMessage+"\n", input.GetStepResultFormat(err))

	err = copyTemplateFS(resources.AppTypes, useOptions, templateUrl, azdCtx.ProjectDirectory())
	if err != nil {
		return fmt.Errorf("copying from template : %w", err)
	}

	err = copyCoreFS(resources.AppTypes, useOptions, azdCtx.ProjectDirectory())
	if err != nil {
		return fmt.Errorf("copying core lib : %w", err)
	}
	return nil
}

// copyTemplate copies the given infrastructure template.
func copyTemplateFS(templateFs embed.FS, useOptions InfraUseOptions, template string, target string) error {
	root := path.Join("app-types", template)
	infraRoot := path.Join(root, "infra")
	projectContent, err := templateFs.ReadFile(path.Join(root, azdcontext.ProjectFileName))
	if err != nil {
		return fmt.Errorf("missing azure.yaml, %w", err)
	}

	_, err = project.ParseProjectConfig(string(projectContent))
	if err != nil {
		return err
	}

	possibleLanguages := maps.Values(LanguageDisplayOptions())
	unmatchedLanguageSuffixes := []string{}
	for _, lang := range possibleLanguages {
		if lang != useOptions.Language {
			unmatchedLanguageSuffixes = append(unmatchedLanguageSuffixes, fmt.Sprintf("-%s.bicep", lang))
		}
	}
	langSpecificSuffix := fmt.Sprintf("-%s.bicep", useOptions.Language)

	return fs.WalkDir(templateFs, infraRoot, func(name string, d fs.DirEntry, err error) error {
		// If there was some error that was preventing is from walking into the directory, just fail now,
		// not much we can do to recover.
		if err != nil {
			return err
		}
		targetPath := filepath.Join(target, "infra", name[len(infraRoot):])

		if d.IsDir() {
			return os.MkdirAll(targetPath, osutil.PermissionDirectory)
		}

		// TODO: This currently does not read the project Config to figure out this option
		languageMatched := strings.HasSuffix(name, langSpecificSuffix)
		if languageMatched {
			targetPath = strings.TrimSuffix(targetPath, langSpecificSuffix) + ".bicep"
		}

		// An unmatched language, do not copy
		for _, langSuffix := range unmatchedLanguageSuffixes {
			if strings.HasSuffix(name, langSuffix) {
				return nil
			}
		}

		contents, err := fs.ReadFile(templateFs, name)
		if err != nil {
			return fmt.Errorf("reading sample file: %w", err)
		}
		return os.WriteFile(targetPath, contents, osutil.PermissionFile)
	})
}

func copyCoreFS(templateFs embed.FS, useOptions InfraUseOptions, target string) error {
	root := path.Join("app-types", "core")

	possibleLanguages := maps.Values(LanguageDisplayOptions())
	unmatchedLanguageSuffixes := []string{}
	for _, lang := range possibleLanguages {
		if lang != useOptions.Language {
			unmatchedLanguageSuffixes = append(unmatchedLanguageSuffixes, fmt.Sprintf("-%s.bicep", lang))
		}
	}
	langSpecificSuffix := fmt.Sprintf("-%s.bicep", useOptions.Language)

	return fs.WalkDir(templateFs, root, func(name string, d fs.DirEntry, err error) error {
		// If there was some error that was preventing is from walking into the directory, just fail now,
		// not much we can do to recover.
		if err != nil {
			return err
		}
		targetPath := filepath.Join(target, "infra", "core", name[len(root):])

		if d.IsDir() {
			return os.MkdirAll(targetPath, osutil.PermissionDirectory)
		}

		// TODO: This currently does not read the project Config to figure out this option
		languageMatched := strings.HasSuffix(name, langSpecificSuffix)
		if languageMatched {
			targetPath = strings.TrimSuffix(targetPath, langSpecificSuffix) + ".bicep"
		}

		// An unmatched language, do not copy
		for _, langSuffix := range unmatchedLanguageSuffixes {
			if strings.HasSuffix(name, langSuffix) {
				return nil
			}
		}

		contents, err := fs.ReadFile(templateFs, name)
		if err != nil {
			return fmt.Errorf("reading sample file: %w", err)
		}
		return os.WriteFile(targetPath, contents, osutil.PermissionFile)
	})
}

// Initializes a local repository in the project directory from a remote repository.
//
// A confirmation prompt is displayed for any existing files to be overwritten.
func (i *Initializer) Initialize(ctx context.Context,
	azdCtx *azdcontext.AzdContext,
	templateUrl string,
	templateBranch string) error {
	var err error
	stepMessage := fmt.Sprintf("Downloading template code to: %s", output.WithLinkFormat("%s", azdCtx.ProjectDirectory()))
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

	target := azdCtx.ProjectDirectory()

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
		return fmt.Errorf("checking for overwrites: %w", err)
	}

	if len(duplicateFiles) > 0 {
		i.console.StopSpinner(ctx, "", input.StepDone)
		i.console.Message(
			ctx,
			output.WithWarningFormat(
				"warning: the following files will be overwritten with the versions from the template:",
			),
		)
		for _, file := range duplicateFiles {
			i.console.Message(ctx, fmt.Sprintf(" * %s", file))
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

		i.console.ShowSpinner(ctx, stepMessage, input.Step)
	}

	if err := copy.Copy(staging, target); err != nil {
		return fmt.Errorf("copying template contents: %w", err)
	}

	i.console.StopSpinner(ctx, stepMessage+"\n", input.GetStepResultFormat(err))
	err = i.writeAzdAssets(ctx, azdCtx)
	if err != nil {
		return err
	}

	return nil
}

// Initializes an empty (bare minimum) azd repository.
func (i *Initializer) InitializeEmpty(ctx context.Context, azdCtx *azdcontext.AzdContext) error {
	return i.writeAzdAssets(ctx, azdCtx)
}

func (i *Initializer) writeAzdAssets(ctx context.Context, azdCtx *azdcontext.AzdContext) error {
	// Check to see if `azure.yaml` exists, and if it doesn't, create it.
	if _, err := os.Stat(azdCtx.ProjectPath()); errors.Is(err, os.ErrNotExist) {
		stepMessage := fmt.Sprintf("Creating a new %s file.", azdcontext.ProjectFileName)

		i.console.ShowSpinner(ctx, stepMessage, input.Step)
		_, err = project.NewProject(azdCtx.ProjectPath(), azdCtx.GetDefaultProjectName())
		i.console.StopSpinner(ctx, stepMessage, input.GetStepResultFormat(err))

		if err != nil {
			return fmt.Errorf("failed to create a project file: %w", err)
		}
	}

	//create .azure when running azd init
	err := os.MkdirAll(
		filepath.Join(azdCtx.ProjectDirectory(), azdcontext.EnvironmentDirectoryName),
		osutil.PermissionDirectory,
	)
	if err != nil {
		return fmt.Errorf("failed to create a directory: %w", err)
	}

	//create .gitignore or open existing .gitignore file, and contains .azure
	gitignoreFile, err := os.OpenFile(
		filepath.Join(azdCtx.ProjectDirectory(), ".gitignore"),
		os.O_APPEND|os.O_RDWR|os.O_CREATE,
		osutil.PermissionFile,
	)
	if err != nil {
		return fmt.Errorf("fail to create or open .gitignore: %w", err)
	}
	defer gitignoreFile.Close()

	writeGitignoreFile := true
	// Determines newline based on the last line containing a newline
	useCrlf := false
	// default to true, since if the file is empty, no preceding newline is needed.
	hasTrailingNewLine := true
	//bufio scanner splits on new lines by default
	reader := bufio.NewReader(gitignoreFile)
	for {
		text, err := reader.ReadString('\n')
		if err == nil {
			// reset unless we're on the last line
			useCrlf = false
		}

		if err != nil && len(text) > 0 {
			// err != nil means no delimiter (newline) was found
			// if text is present, that must mean the last line doesn't contain newline
			hasTrailingNewLine = false
		}

		if len(text) > 0 && text[len(text)-1] == '\n' {
			text = text[0 : len(text)-1]
		}

		if len(text) > 0 && text[len(text)-1] == '\r' {
			text = text[0 : len(text)-1]
			useCrlf = true
		}

		// match on entire line
		// gitignore files can't have comments inline
		if azdcontext.EnvironmentDirectoryName == text {
			writeGitignoreFile = false
			break
		}

		// EOF
		if err != nil {
			break
		}
	}

	if writeGitignoreFile {
		newLine := "\n"
		if useCrlf {
			newLine = "\r\n"
		}

		appendContents := azdcontext.EnvironmentDirectoryName + newLine
		if !hasTrailingNewLine {
			appendContents = newLine + appendContents
		}
		_, err := gitignoreFile.WriteString(appendContents)
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
