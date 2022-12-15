package repository

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	mockinput "github.com/azure/azure-dev/cli/azd/test/mocks/console"
	mockexec "github.com/azure/azure-dev/cli/azd/test/mocks/exec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_initializer_Initialize(t *testing.T) {
	type args struct {
		ctx            context.Context
		templateUrl    string
		templateBranch string
	}
	tests := []struct {
		name    string
		i       *initializer
		args    args
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.i.Initialize(tt.args.ctx, tt.args.templateUrl, tt.args.templateBranch); (err != nil) != tt.wantErr {
				t.Errorf("initializer.Initialize() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
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
		{"Unmodified", setup{"azureyaml_existing.txt", "gitignore_with_env.txt"}, expected{projectFile: "azureyaml_existing.txt", gitignoreFile: "gitignore_with_env.txt"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectDir := t.TempDir()
			azdCtx, err := azdcontext.NewAzdContext()
			require.NoError(t, err)
			azdCtx.SetProjectDirectory(projectDir)

			if tt.setup.gitignoreFile != "" {
				copyFile(t, "empty", tt.setup.gitignoreFile, filepath.Join(azdCtx.ProjectDirectory(), ".gitignore"))
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

func copyFile(t *testing.T, testCase string, source string, target string) {
	err := os.WriteFile(target, []byte(readFile(t, testCase, source)), 0644)
	require.NoError(t, err)
}

func readFile(t *testing.T, testCase string, file string) string {
	bytes, err := os.ReadFile(filepath.Join("testdata", testCase, file))
	require.NoError(t, err)
	content := string(bytes)
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
