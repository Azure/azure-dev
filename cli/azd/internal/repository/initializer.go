// Package repository provides handling of files in the user's code repository.
package repository

import (
	"bufio"
	"bytes"
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
	"text/template"

	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/azure/azure-dev/cli/azd/resources"
	"github.com/otiai10/copy"
)

type DatabaseOption string

const (
	DatabaseNone   DatabaseOption = "none"
	DatabaseCosmos DatabaseOption = "cosmos"
	DatabaseSql    DatabaseOption = "sql"
)

func DatabaseDisplayOptions() map[string]DatabaseOption {
	return map[string]DatabaseOption{
		"Azure Cosmos DB (MongoDB API)":   DatabaseCosmos,
		"Azure SQL DB":                    DatabaseSql,
		"No, I would not like a database": DatabaseNone,
	}
}

type ScaffoldContext struct {
	DatabaseName string
	Database     map[string]string
}

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

type ProjectSpec struct {
	Language   string
	Host       string
	Path       string
	OutputPath string
	HackIsWeb  bool
}

type InfraUseOptions struct {
	Language     string
	DatabaseName string
	Database     DatabaseOption
	Projects     []ProjectSpec
}

func LanguageDisplayOptions() map[string]string {
	return map[string]string{
		".NET / C# / F#": "dotnet",
		"Python":         "python",
		"NodeJS":         "node",
		"Java":           "java",
	}
}

type TemplateRules struct {
	Includes []string
	Excludes []string
	Rewrites map[string]string
}

func getRules(appType string, useOptions InfraUseOptions) TemplateRules {
	switch appType {
	case "api-db":
		return TemplateRules{
			Includes: []string{
				fmt.Sprintf("app/api-%s.bicep", mapLanguage(useOptions.Language)),
				fmt.Sprintf("app/db-%s.bicep.template", string(useOptions.Database)),
			},
			Excludes: []string{
				"app/api-*.bicep",
				"app/db-*.bicep.template",
			},
			Rewrites: map[string]string{
				fmt.Sprintf("app/api-%s.bicep", mapLanguage(useOptions.Language)):    "app/api.bicep",
				fmt.Sprintf("app/db-%s.bicep.template", string(useOptions.Database)): "app/db.bicep",
				"main.bicep.template": "main.bicep",
			},
		}
	}

	return TemplateRules{}
}

func (i *Initializer) ScaffoldProject(
	ctx context.Context,
	name string,
	azdCtx *azdcontext.AzdContext,
	projects []ProjectSpec) error {
	prj := project.ProjectConfig{}
	prj.Name = azdCtx.GetDefaultProjectName()
	prj.Services = map[string]*project.ServiceConfig{}
	for _, spec := range projects {
		// TODO: This is a hack while prompts are not yet supported.
		serviceName := "api"
		if spec.HackIsWeb {
			serviceName = "web"
		}
		rel, err := filepath.Rel(azdCtx.ProjectDirectory(), spec.Path)
		if err != nil {
			return fmt.Errorf("creating %s: %w", name, err)
		}

		prj.Services[serviceName] = &project.ServiceConfig{
			RelativePath: rel,
			OutputPath:   spec.OutputPath,
			// TODO: should default based on project
			Host:     spec.Host,
			Language: spec.Language,
		}
	}

	err := project.Save(filepath.Join(azdCtx.ProjectDirectory(), name), prj)
	if err != nil {
		return fmt.Errorf("creating %s: %w", name, err)
	}
	return nil
}

func (i *Initializer) InitializeInfra(ctx context.Context,
	azdCtx *azdcontext.AzdContext,
	templateUrl string,
	templateBranch string,
	useOptions InfraUseOptions) error {
	var err error
	stepMessage := fmt.Sprintf(
		"Generating infrastructure-as-code (IaC) files under the %s folder",
		output.WithLinkFormat("infra"))
	i.console.ShowSpinner(ctx, stepMessage, input.Step)
	defer i.console.StopSpinner(ctx, "", input.GetStepResultFormat(err))

	err = copyTemplateFS(resources.AppTypes, useOptions, templateUrl, azdCtx.ProjectDirectory())
	if err != nil {
		return fmt.Errorf("copying from template : %w", err)
	}

	err = copyCoreFS(resources.AppTypes, useOptions, azdCtx.ProjectDirectory())
	if err != nil {
		return fmt.Errorf("copying core lib : %w", err)
	}
	i.console.StopSpinner(ctx, stepMessage, input.GetStepResultFormat(err))

	err = i.writeAzdAssets(ctx, azdCtx)
	if err != nil {
		return err
	}

	return nil
}

// copyTemplate copies the given infrastructure template.
func copyTemplateFS(templateFs embed.FS, useOptions InfraUseOptions, appType string, target string) error {
	root := path.Join("app-types", appType)
	infraRoot := path.Join(root, "infra")
	projectContent, err := templateFs.ReadFile(path.Join(root, azdcontext.ProjectFileName))
	if err != nil {
		return fmt.Errorf("missing azure.yaml, %w", err)
	}

	_, err = project.ParseProjectConfig(string(projectContent))
	if err != nil {
		return err
	}

	rules := getRules(appType, useOptions)

	scaffoldCtx := ScaffoldContext{
		DatabaseName: useOptions.DatabaseName,
		Database:     map[string]string{},
	}

	if useOptions.Database != DatabaseNone {
		directory := path.Join("snippets", "database")
		snippets, err := resources.Snippets.ReadDir(directory)
		if err != nil {
			return fmt.Errorf("reading database snippets: %w", err)
		}

		for _, snippet := range snippets {
			prefix := string(useOptions.Database) + ".main."
			if strings.HasPrefix(snippet.Name(), prefix) {
				contents, err := resources.Snippets.ReadFile(path.Join(directory, snippet.Name()))
				if err != nil {
					return fmt.Errorf("reading file snippet: %w", err)
				}
				resource := strings.TrimPrefix(snippet.Name(), prefix)
				scaffoldCtx.Database[resource] = string(contents)
			}
		}
	}

	return fs.WalkDir(templateFs, infraRoot, func(name string, d fs.DirEntry, err error) error {
		// If there was some error that was preventing is from walking into the directory, just fail now,
		// not much we can do to recover.
		if err != nil {
			return err
		}
		relToInfra := strings.TrimPrefix(name[len(infraRoot):], "/")
		relPath := filepath.Join("infra", name[len(infraRoot):])
		targetPath := filepath.Join(target, relPath)

		if d.IsDir() {
			return os.MkdirAll(targetPath, osutil.PermissionDirectory)
		}

		alwaysInclude := false
		for _, pattern := range rules.Includes {
			if matched, err := filepath.Match(pattern, relToInfra); err != nil {
				return err
			} else if matched {
				alwaysInclude = true
			}
		}

		if !alwaysInclude {
			for _, pattern := range rules.Excludes {
				if matched, err := filepath.Match(pattern, relToInfra); err != nil {
					return err
				} else if matched {
					// An exclude pattern was matched. The file is excluded.
					return nil
				}
			}
		}

		for pattern, rewrite := range rules.Rewrites {
			if matched, err := filepath.Match(pattern, relToInfra); err != nil {
				return err
			} else if matched {
				newName := filepath.Join("infra", rewrite)
				targetPath = filepath.Join(target, newName)
			}
		}

		if filepath.Ext(name) == ".template" {
			contents, err := fs.ReadFile(templateFs, name)
			if err != nil {
				return fmt.Errorf("reading sample file: %w", err)
			}

			t, err := template.New(relPath).Option("missingkey=zero").Parse(string(contents))
			if err != nil {
				return fmt.Errorf("parsing template: %w", err)
			}

			buf := bytes.NewBufferString("")
			err = t.Execute(buf, scaffoldCtx)
			if err != nil {
				return fmt.Errorf("executing template: %w", err)
			}

			return os.WriteFile(targetPath, buf.Bytes(), osutil.PermissionFile)
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
	return fs.WalkDir(templateFs, root, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		targetPath := filepath.Join(target, "infra", "core", name[len(root):])

		if d.IsDir() {
			return os.MkdirAll(targetPath, osutil.PermissionDirectory)
		}

		contents, err := fs.ReadFile(templateFs, name)
		if err != nil {
			return fmt.Errorf("reading sample file: %w", err)
		}
		return os.WriteFile(targetPath, contents, osutil.PermissionFile)
	})
}
func mapLanguage(lang string) string {
	switch lang {
	case "dotnet", "csharp", "fsharp", "":
		return "dotnet"
	case "py", "python":
		return "python"
	case "js", "ts":
		return "node"
	case "java":
		return "java"
	}

	return ""
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

	filesWithExecPerms, err := i.fetchCode(ctx, templateUrl, templateBranch, staging)
	if err != nil {
		return err
	}

	err = i.promptForDuplicates(ctx, staging, target)
	if err != nil {
		return err
	}

	isEmpty, err := isEmptyDir(target)
	if err != nil {
		return err
	}

	if err := copy.Copy(staging, target); err != nil {
		return fmt.Errorf("copying template contents: %w", err)
	}

	err = i.writeAzdAssets(ctx, azdCtx)
	if err != nil {
		return err
	}

	err = i.gitInitialize(ctx, target, filesWithExecPerms, isEmpty)
	if err != nil {
		return err
	}

	i.console.StopSpinner(ctx, stepMessage+"\n", input.GetStepResultFormat(err))

	return nil
}

func (i *Initializer) fetchCode(
	ctx context.Context,
	templateUrl string,
	templateBranch string,
	destination string) (executableFilePaths []string, err error) {
	err = i.gitCli.ShallowClone(ctx, templateUrl, templateBranch, destination)
	if err != nil {
		return nil, fmt.Errorf("fetching template: %w", err)
	}

	stagedFilesOutput, err := i.gitCli.ListStagedFiles(ctx, destination)
	if err != nil {
		return nil, fmt.Errorf("listing files with permissions: %w", err)
	}

	executableFilePaths, err = parseExecutableFiles(stagedFilesOutput)
	if err != nil {
		return nil, fmt.Errorf("parsing file permissions output: %w", err)
	}

	if err := os.RemoveAll(filepath.Join(destination, ".git")); err != nil {
		return nil, fmt.Errorf("removing .git folder after clone: %w", err)
	}

	return executableFilePaths, nil
}

func (i *Initializer) promptForDuplicates(ctx context.Context, staging string, target string) error {
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
		i.console.MessageUxItem(ctx, &ux.WarningMessage{
			Description: "the following files will be overwritten with the versions from the template:",
		})

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
	}

	return nil
}

func (i *Initializer) gitInitialize(ctx context.Context,
	target string,
	executableFilesToRestore []string,
	stageAllFiles bool) error {
	err := i.ensureGitRepository(ctx, target)
	if err != nil {
		return err
	}

	// Set executable files
	for _, executableFile := range executableFilesToRestore {
		err = i.gitCli.AddFileExecPermission(ctx, target, executableFile)
		if err != nil {
			return fmt.Errorf("restoring file permissions: %w", err)
		}
	}

	if stageAllFiles {
		err = i.gitCli.AddFile(ctx, target, "*")
		if err != nil {
			return fmt.Errorf("staging newly fetched template files: %w", err)
		}
	}

	return nil
}

func (i *Initializer) ensureGitRepository(ctx context.Context, repoPath string) error {
	_, err := i.gitCli.GetCurrentBranch(ctx, repoPath)
	if err != nil {
		if !errors.Is(err, git.ErrNotRepository) {
			return fmt.Errorf("determining current git repository state: %w", err)
		}

		err = i.gitCli.InitRepo(ctx, repoPath)
		if err != nil {
			return fmt.Errorf("initializing git repository: %w", err)
		}

		i.console.MessageUxItem(ctx, &ux.DoneMessage{Message: "Initialized git repository"})
	}

	return nil
}

func parseExecutableFiles(stagedFilesOutput string) ([]string, error) {
	scanner := bufio.NewScanner(strings.NewReader(stagedFilesOutput))
	executableFiles := []string{}
	for scanner.Scan() {
		// Format for git ls --stage:
		// <mode> <object> <stage>\t<file>
		// In other words, space delimited for first three properties, tab delimited before filepath is present4ed

		// Scan first word to obtain <mode>
		advance, word, err := bufio.ScanWords(scanner.Bytes(), false)
		if err != nil {
			return nil, err
		}

		// 100755 is the only possible mode for git-tracked executable files
		if string(word) == "100755" {
			// Advance to past '\t', taking the remainder which is <file>
			_, filepath, found := strings.Cut(scanner.Text()[advance:], "\t")
			if !found {
				return nil, errors.New("invalid staged files output format. Missing file path.")
			}

			executableFiles = append(executableFiles, filepath)
		}
	}
	return executableFiles, nil
}

// Initializes an empty (bare minimum) azd repository.
func (i *Initializer) InitializeEmpty(ctx context.Context, azdCtx *azdcontext.AzdContext) error {
	projectFormatted := output.WithLinkFormat("%s", azdCtx.ProjectDirectory())
	var err error
	i.console.ShowSpinner(ctx,
		fmt.Sprintf("Creating minimal project files at: %s", projectFormatted),
		input.Step)
	defer i.console.StopSpinner(ctx,
		fmt.Sprintf("Created minimal project files at: %s", projectFormatted)+"\n",
		input.GetStepResultFormat(err))

	projectDir := azdCtx.ProjectDirectory()
	isEmpty, err := isEmptyDir(projectDir)
	if err != nil {
		return err
	}

	err = i.writeAzdAssets(ctx, azdCtx)
	if err != nil {
		return err
	}

	err = i.gitInitialize(ctx, projectDir, []string{}, isEmpty)
	if err != nil {
		return err
	}

	return nil
}

func (i *Initializer) writeAzdAssets(ctx context.Context, azdCtx *azdcontext.AzdContext) error {
	// Check to see if `azure.yaml` exists, and if it doesn't, create it.
	if _, err := os.Stat(azdCtx.ProjectPath()); errors.Is(err, os.ErrNotExist) {
		_, err = project.NewProject(azdCtx.ProjectPath(), azdCtx.GetDefaultProjectName())
		i.console.MessageUxItem(ctx,
			&ux.DoneMessage{Message: fmt.Sprintf("Created a new %s file", azdcontext.ProjectFileName)})

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

func isEmptyDir(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, fmt.Errorf("determining empty directory: %w", err)
	}

	return len(entries) == 0, nil
}
