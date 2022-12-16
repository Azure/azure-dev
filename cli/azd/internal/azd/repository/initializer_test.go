package repository

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockexec"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/exp/slices"
)

func Test_initializer_Initialize(t *testing.T) {
	tests := []struct {
		name        string
		templateDir string
	}{
		{"RegularTemplate", "template"},
		{"MinimalTemplate", "template-minimal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectDir := t.TempDir()
			azdCtx, err := azdcontext.NewAzdContext()
			require.NoError(t, err)
			azdCtx.SetProjectDirectory(projectDir)

			console := mockinput.NewMockConsole()
			realRunner := exec.NewCommandRunner(os.Stdin, os.Stdout, os.Stderr)
			mockRunner := mockexec.NewMockCommandRunner()
			mockRunner.When(func(args exec.RunArgs, command string) bool { return true }).
				RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
					// Stub out git clone, otherwise run actual command
					if slices.Contains(args.Args, "clone") && slices.Contains(args.Args, "local") {
						stagingDir := args.Args[len(args.Args)-1]
						copyTemplate(t, tt.templateDir, stagingDir)
						return exec.NewRunResult(0, "", ""), nil
					}

					return realRunner.Run(context.Background(), args)
				})

			i := NewInitializer(azdCtx, console, git.NewGitCli(mockRunner))
			err = i.Initialize(context.Background(), "local", "")
			require.NoError(t, err)

			verifyTemplateCopied(t, tt.templateDir, projectDir)

			require.FileExists(t, filepath.Join(projectDir, ".gitignore"))
			require.FileExists(t, azdCtx.ProjectPath())
			require.DirExists(t, azdCtx.EnvironmentDirectory())
		})
	}
}

func Test_initializer_InitializeWithOverwritePrompt(t *testing.T) {
	templateDir := "template"
	tests := []struct {
		name             string
		confirmOverwrite bool
	}{
		{"Confirm", true},
		{"Deny", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectDir := t.TempDir()
			azdCtx, err := azdcontext.NewAzdContext()
			require.NoError(t, err)
			azdCtx.SetProjectDirectory(projectDir)
			// Copy all files to project to set up duplicate files
			copyTemplate(t, templateDir, projectDir)

			console := mockinput.NewMockConsole()
			console.WhenConfirm(func(options input.ConsoleOptions) bool {
				return strings.Contains(options.Message, "Overwrite files with versions from template?")
			}).Respond(tt.confirmOverwrite)

			realRunner := exec.NewCommandRunner(os.Stdin, os.Stdout, os.Stderr)
			mockRunner := mockexec.NewMockCommandRunner()
			mockRunner.When(func(args exec.RunArgs, command string) bool { return true }).
				RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
					// Stub out git clone, otherwise run actual command
					if slices.Contains(args.Args, "clone") && slices.Contains(args.Args, "local") {
						stagingDir := args.Args[len(args.Args)-1]
						copyTemplate(t, templateDir, stagingDir)
						return exec.NewRunResult(0, "", ""), nil
					}

					return realRunner.Run(context.Background(), args)
				})

			i := NewInitializer(azdCtx, console, git.NewGitCli(mockRunner))
			err = i.Initialize(context.Background(), "local", "")

			if !tt.confirmOverwrite {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)

			verifyTemplateCopied(t, templateDir, projectDir)

			require.FileExists(t, filepath.Join(projectDir, ".gitignore"))
			require.FileExists(t, azdCtx.ProjectPath())
			require.DirExists(t, azdCtx.EnvironmentDirectory())
		})
	}
}

// Copy all files from source to target, removing *.txt suffix.
func copyTemplate(t *testing.T, source string, target string) {
	sourceFull := testDataPath(source)
	err := filepath.WalkDir(sourceFull, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			require.NoError(t, err)
		}

		if d.IsDir() {
			relDir, err := filepath.Rel(sourceFull, path)
			if err != nil {
				return fmt.Errorf("computing relative path: %w", err)
			}

			return os.MkdirAll(filepath.Join(target, relDir), 0755)
		}

		rel, err := filepath.Rel(sourceFull, path)
		if err != nil {
			return fmt.Errorf("computing relative path: %w", err)
		}

		relTarget := strings.TrimSuffix(rel, ".txt")

		copyFile(t, source, rel, filepath.Join(target, relTarget))

		return nil
	})

	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(target, ".git"), 0755))
}

// Verify all template code was copied to the destination.
func verifyTemplateCopied(t *testing.T, original string, copied string) {
	originalFull := testDataPath(original)

	err := filepath.WalkDir(originalFull, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			require.NoError(t, err)
		}

		if d.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(originalFull, path)
		if err != nil {
			return fmt.Errorf("computing relative path: %w", err)
		}

		relCopied := strings.TrimSuffix(rel, ".txt")
		verifyFileContent(t, filepath.Join(copied, relCopied), readFile(t, original, rel))

		return nil
	})

	require.NoError(t, err)
}

func Test_initializer_InitializeEmpty(t *testing.T) {
	type setup struct {
		projectFile   string
		gitignoreFile string
	}

	type expected struct {
		projectFile   string
		gitignoreFile string
	}

	tests := []struct {
		name     string
		setup    setup
		expected expected
	}{
		{"CreateAll", setup{"", ""}, expected{projectFile: "azureyaml_created.txt", gitignoreFile: "gitignore_created.txt"}},
		{"AppendGitignore", setup{"azureyaml_existing.txt", "gitignore_existing.txt"}, expected{projectFile: "azureyaml_existing.txt", gitignoreFile: "gitignore_with_env.txt"}},
		{"AppendGitignoreNoTrailing", setup{"azureyaml_existing.txt", "gitignore_existing_notrail.txt"}, expected{projectFile: "azureyaml_existing.txt", gitignoreFile: "gitignore_with_env.txt"}},
		{"Unmodified", setup{"azureyaml_existing.txt", "gitignore_with_env.txt"}, expected{projectFile: "azureyaml_existing.txt", gitignoreFile: "gitignore_with_env.txt"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectDir := t.TempDir()
			azdCtx, err := azdcontext.NewAzdContext()
			require.NoError(t, err)
			azdCtx.SetProjectDirectory(projectDir)

			if tt.setup.gitignoreFile != "" {
				copyFile(t, "empty", tt.setup.gitignoreFile, filepath.Join(projectDir, ".gitignore"))
			}

			if tt.setup.projectFile != "" {
				copyFile(t, "empty", tt.setup.projectFile, azdCtx.ProjectPath())
			}

			console := mockinput.NewMockConsole()
			runner := mockexec.NewMockCommandRunner()
			i := NewInitializer(azdCtx, console, git.NewGitCli(runner))
			err = i.InitializeEmpty(context.Background())
			require.NoError(t, err)

			projectFileContent := readFile(t, "empty", tt.expected.projectFile)
			gitIgnoreFileContent := readFile(t, "empty", tt.expected.gitignoreFile)

			verifyProjectFile(t, azdCtx, projectFileContent)

			gitignore := filepath.Join(projectDir, ".gitignore")
			verifyFileContent(t, gitignore, gitIgnoreFileContent)

			require.DirExists(t, azdCtx.EnvironmentDirectory())
		})
	}
}

func testDataPath(elem ...string) string {
	elem = append([]string{"testdata"}, elem...)
	return filepath.Join(elem...)
}

func copyFile(t *testing.T, testCase string, source string, target string) {
	content := readFile(t, testCase, source)
	err := os.WriteFile(target, []byte(content), 0644)

	require.NoError(t, err)
}

func readFile(t *testing.T, testCase string, file string) string {
	bytes, err := os.ReadFile(testDataPath(testCase, file))
	require.NoError(t, err)
	content := string(bytes)
	// All asset files are stored in LF.
	// By replacing LF with CRLF here, we ensure all line ending tests are covered.
	if runtime.GOOS == "windows" {
		content = strings.ReplaceAll(content, "\n", "\r\n")
	}

	return content
}

func verifyFileContent(t *testing.T, file string, content string) {
	require.FileExists(t, file)

	actualContent, err := os.ReadFile(file)
	require.NoError(t, err)
	require.Equal(t, content, string(actualContent))
}

func verifyProjectFile(t *testing.T, azdCtx *azdcontext.AzdContext, content string) {
	content = strings.Replace(content, "<project>", azdCtx.GetDefaultProjectName(), 1)
	verifyFileContent(t, azdCtx.ProjectPath(), content)

	_, err := project.LoadProjectConfig(azdCtx.ProjectPath(), environment.Ephemeral())
	require.NoError(t, err)
}

func Test_determineDuplicates(t *testing.T) {
	type args struct {
		sourceFiles []string
		targetFiles []string
	}
	tests := []struct {
		name     string
		args     args
		expected []string
	}{
		{"NoDuplicates", args{[]string{"a.txt", "b.txt", "dir1/a.txt"}, []string{"c.txt", "d.txt", "dir2/a.txt"}}, []string{}},
		{"Duplicates", args{
			[]string{
				"a.txt", "b.txt", "c.txt",
				"dir1/a.txt",
				"dir1/dir2/b.txt", "dir1/dir2/d.txt"},
			[]string{
				"a.txt", "c.txt",
				"dir1/a.txt", "dir1/c.txt",
				"dir1/dir2/b.txt"}},
			[]string{
				"a.txt", "c.txt",
				"dir1/a.txt",
				"dir1/dir2/b.txt"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := t.TempDir()
			target := t.TempDir()

			createFiles(t, source, tt.args.sourceFiles)
			createFiles(t, target, tt.args.targetFiles)

			duplicates, err := determineDuplicates(source, target)

			expected := []string{}
			for _, expectedFile := range tt.expected {
				expected = append(expected, filepath.Clean(expectedFile))
			}

			assert.NoError(t, err)
			assert.ElementsMatch(t, duplicates, expected)
		})
	}
}

func createFiles(t *testing.T, dir string, files []string) {
	for _, file := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, file)), 0755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, file), []byte{}, 0644))
	}
}
