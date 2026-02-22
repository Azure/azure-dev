// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package repository

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/platform"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockexec"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type testCase struct {
	name        string
	templateDir string
	// Files that will be mocked to be executable when fetched remotely.
	// Equally, these files are asserted to be executable after init.
	executableFiles []string
}

func Test_Initializer_Initialize(t *testing.T) {
	tests := []testCase{
		{"RegularTemplate", "template", []string{"script/test.sh"}},
		{"MinimalTemplate", "template-minimal", []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectDir := t.TempDir()
			azdCtx := azdcontext.NewAzdContextWithDirectory(projectDir)
			mockContext := mocks.NewMockContext(context.Background())
			mockGitClone(t, mockContext, "https://github.com/Azure-Samples/local", tt)

			mockEnv := &mockenv.MockEnvManager{}
			mockEnv.On("Save", mock.Anything, mock.Anything).Return(nil)

			i := NewInitializer(
				mockContext.Console,
				git.NewCli(mockContext.CommandRunner),
				dotnet.NewCli(mockContext.CommandRunner),
				mockContext.AlphaFeaturesManager,
				lazy.From[environment.Manager](mockEnv),
			)
			err := i.Initialize(*mockContext.Context, azdCtx, &templates.Template{RepositoryPath: "local"}, "")
			require.NoError(t, err)

			verifyTemplateCopied(t, testDataPath(tt.templateDir), projectDir, verifyOptions{})
			verifyExecutableFilePermissions(t, *mockContext.Context, i.gitCli, projectDir, tt.executableFiles)

			require.FileExists(t, filepath.Join(projectDir, ".gitignore"))
			require.FileExists(t, azdCtx.ProjectPath())
			require.DirExists(t, azdCtx.EnvironmentDirectory())
		})
	}
}

func Test_Initializer_DevCenter(t *testing.T) {
	projectDir := t.TempDir()
	azdCtx := azdcontext.NewAzdContextWithDirectory(projectDir)
	mockContext := mocks.NewMockContext(context.Background())
	testMetadata := testCase{
		name:        "devcenter",
		templateDir: "template",
	}
	mockGitClone(t, mockContext, "https://github.com/Azure-Samples/local", testMetadata)

	template := &templates.Template{
		RepositoryPath: "local",
		Metadata: templates.Metadata{
			Project: map[string]string{
				"platform.type":                         "devcenter",
				"platform.config.name":                  "DEVCENTER_NAME",
				"platform.config.project":               "DEVCENTER_PROJECT",
				"platform.config.environmentDefinition": "DEVCENTER_ENV_DEFINITION",
			},
		},
	}

	mockEnv := &mockenv.MockEnvManager{}
	mockEnv.On("Save", mock.Anything, mock.Anything).Return(nil)

	i := NewInitializer(
		mockContext.Console,
		git.NewCli(mockContext.CommandRunner),
		dotnet.NewCli(mockContext.CommandRunner),
		mockContext.AlphaFeaturesManager,
		lazy.From[environment.Manager](mockEnv),
	)
	err := i.Initialize(*mockContext.Context, azdCtx, template, "")
	require.NoError(t, err)

	prj, err := project.Load(*mockContext.Context, azdCtx.ProjectPath())
	require.NoError(t, err)
	require.Equal(t, prj.Platform.Type, platform.PlatformKind("devcenter"))
	require.Equal(t, prj.Platform.Config["name"], "DEVCENTER_NAME")
	require.Equal(t, prj.Platform.Config["project"], "DEVCENTER_PROJECT")
	require.Equal(t, prj.Platform.Config["environmentDefinition"], "DEVCENTER_ENV_DEFINITION")
}

func Test_Initializer_InitializeWithOverwritePrompt(t *testing.T) {
	templateDir := "template"
	tests := []struct {
		name      string
		selection int
	}{
		{"Overwrite", 0},
		{"Keep", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalReadme := "ORIGINAL"
			originalProgram := "Console.WriteLine(\"Hello, Original World!\");"
			projectDir := t.TempDir()
			azdCtx := azdcontext.NewAzdContextWithDirectory(projectDir)
			// set up duplicate files
			err := os.WriteFile(filepath.Join(projectDir, "README.md"), []byte(originalReadme), osutil.PermissionFile)
			require.NoError(t, err, "setting up duplicate readme.md")
			err = os.MkdirAll(filepath.Join(projectDir, "src"), osutil.PermissionDirectory)
			require.NoError(t, err, "setting up src folder")
			err = os.WriteFile(
				filepath.Join(projectDir, "src", "Program.cs"),
				[]byte(originalProgram),
				osutil.PermissionFile,
			)
			require.NoError(t, err, "setting up duplicate program.cs")

			console := mockinput.NewMockConsole()
			console.WhenSelect(func(options input.ConsoleOptions) bool {
				return strings.Contains(options.Message, "What would you like to do with these files?")
			}).Respond(tt.selection)

			realRunner := exec.NewCommandRunner(nil)
			mockRunner := mockexec.NewMockCommandRunner()
			mockRunner.When(func(args exec.RunArgs, command string) bool { return true }).
				RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
					// Stub out git clone, otherwise run actual command
					if slices.Contains(args.Args, "clone") &&
						slices.Contains(args.Args, "https://github.com/Azure-Samples/local") {
						stagingDir := args.Args[len(args.Args)-1]
						copyTemplate(t, testDataPath(templateDir), stagingDir)
						_, err := realRunner.Run(context.Background(), exec.NewRunArgs("git", "-C", stagingDir, "init"))
						require.NoError(t, err)

						return exec.NewRunResult(0, "", ""), nil
					}

					return realRunner.Run(context.Background(), args)
				})

			mockEnv := &mockenv.MockEnvManager{}
			mockEnv.On("Save", mock.Anything, mock.Anything).Return(nil)

			i := NewInitializer(
				console,
				git.NewCli(mockRunner),
				dotnet.NewCli(mockRunner),
				alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
				lazy.From[environment.Manager](mockEnv),
			)
			err = i.Initialize(context.Background(), azdCtx, &templates.Template{RepositoryPath: "local"}, "")
			require.NoError(t, err)

			switch tt.selection {
			case 0:
				// overwrite
				verifyTemplateCopied(t, testDataPath(templateDir), projectDir, verifyOptions{})
			case 1:
				// keep
				content, err := os.ReadFile(filepath.Join(projectDir, "README.md"))
				require.NoError(t, err)
				require.Equal(t, originalReadme, string(content))

				content, err = os.ReadFile(filepath.Join(projectDir, "src", "Program.cs"))
				require.NoError(t, err)
				require.Equal(t, originalProgram, string(content))

				verifyTemplateCopied(t, testDataPath(templateDir), projectDir, verifyOptions{
					Skip: func(src string) (bool, error) {
						return src == testDataPath(templateDir, "README.md.txt") ||
							src == testDataPath(templateDir, "src", "Program.cs.txt"), nil
					},
				})
			default:
				require.Fail(t, "unhandled user selection")
			}

			require.FileExists(t, filepath.Join(projectDir, ".gitignore"))
			require.FileExists(t, azdCtx.ProjectPath())
			require.DirExists(t, azdCtx.EnvironmentDirectory())
		})
	}
}

// Copy all files from source to target, removing *.txt suffix.
func copyTemplate(t *testing.T, source string, target string) {
	err := filepath.WalkDir(source, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			require.NoError(t, err)
		}

		if d.IsDir() {
			relDir, err := filepath.Rel(source, path)
			if err != nil {
				return fmt.Errorf("computing relative path: %w", err)
			}

			return os.MkdirAll(filepath.Join(target, relDir), 0755)
		}

		rel, err := filepath.Rel(source, path)
		if err != nil {
			return fmt.Errorf("computing relative path: %w", err)
		}

		relTarget := strings.TrimSuffix(rel, ".txt")
		copyFile(t, filepath.Join(source, rel), filepath.Join(target, relTarget))

		return nil
	})

	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(target, ".git"), 0755))
}

type verifyOptions struct {
	// skip verification for a given file.
	Skip func(src string) (bool, error)
}

// Verify all template code was copied to the destination.
func verifyTemplateCopied(
	t *testing.T,
	original string,
	copied string,
	options verifyOptions) {
	err := filepath.WalkDir(original, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			require.NoError(t, err)
		}

		if d.IsDir() {
			return nil
		}

		if options.Skip != nil {
			skip, err := options.Skip(path)
			if err != nil {
				return err
			}

			if skip {
				return nil
			}
		}

		rel, err := filepath.Rel(original, path)
		if err != nil {
			return fmt.Errorf("computing relative path: %w", err)
		}

		relCopied := strings.TrimSuffix(rel, ".txt")

		verifyFileContent(
			t,
			filepath.Join(copied, relCopied),
			readFile(t, filepath.Join(original, rel)))

		return nil
	})

	require.NoError(t, err)
}

func verifyExecutableFilePermissions(t *testing.T,
	ctx context.Context,
	git *git.Cli,
	repoPath string,
	expectedFiles []string) {
	output, err := git.ListStagedFiles(ctx, repoPath)
	require.NoError(t, err)

	// On windows, since the file system doesn't keep track of executable file permissions,
	// we have to query git instead for the tracked permissions.
	if runtime.GOOS == "windows" {
		actual, err := parseExecutableFiles(output)
		require.NoError(t, err)

		require.ElementsMatch(t, actual, expectedFiles)

	} else {
		for _, file := range expectedFiles {
			fi, err := os.Stat(filepath.Join(repoPath, file))
			require.NoError(t, err)
			mode := fi.Mode()
			isExecutable := mode&0111 == 0111
			require.Truef(t, isExecutable, "file is not executable for all, fileMode: %s", mode)
		}
	}
}

func Test_Initializer_WriteCoreAssets(t *testing.T) {
	type setup struct {
		projectFile   string
		gitignoreFile string
		gitIgnoreCrlf bool
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
		{"CreateAll",
			setup{"", "", false},
			expected{projectFile: "azureyaml_created.txt", gitignoreFile: "gitignore_created.txt"}},
		{"AppendGitignore",
			setup{"azureyaml_existing.txt", "gitignore_existing.txt", false},
			expected{projectFile: "azureyaml_existing.txt", gitignoreFile: "gitignore_with_env.txt"}},
		{"AppendGitignoreNoTrailing",
			setup{"azureyaml_existing.txt", "gitignore_existing_notrail.txt", false},
			expected{projectFile: "azureyaml_existing.txt", gitignoreFile: "gitignore_with_env.txt"}},
		{"AppendGitignoreCrlf",
			setup{"azureyaml_existing.txt", "gitignore_existing.txt", true},
			expected{projectFile: "azureyaml_existing.txt", gitignoreFile: "gitignore_with_env.txt"}},
		{"AppendGitignoreNoTrailingCrlf",
			setup{"azureyaml_existing.txt", "gitignore_existing_notrail.txt", true},
			expected{projectFile: "azureyaml_existing.txt", gitignoreFile: "gitignore_with_env.txt"}},
		{"Unmodified",
			setup{"azureyaml_existing.txt", "gitignore_with_env.txt", false},
			expected{projectFile: "azureyaml_existing.txt", gitignoreFile: "gitignore_with_env.txt"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectDir := t.TempDir()
			azdCtx := azdcontext.NewAzdContextWithDirectory(projectDir)

			if tt.setup.gitignoreFile != "" {
				if tt.setup.gitIgnoreCrlf {
					copyFileCrlf(t, testDataPath("empty", tt.setup.gitignoreFile), filepath.Join(projectDir, ".gitignore"))
				} else {
					copyFile(t, testDataPath("empty", tt.setup.gitignoreFile), filepath.Join(projectDir, ".gitignore"))
				}
			}

			if tt.setup.projectFile != "" {
				copyFile(t, testDataPath("empty", tt.setup.projectFile), azdCtx.ProjectPath())
			}

			console := mockinput.NewMockConsole()
			realRunner := exec.NewCommandRunner(nil)

			envManager := &mockenv.MockEnvManager{}
			envManager.On("Save", mock.Anything, mock.Anything).Return(nil)

			i := NewInitializer(
				console, git.NewCli(realRunner), nil,
				alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
				lazy.From[environment.Manager](envManager))
			err := i.writeCoreAssets(context.Background(), azdCtx)
			require.NoError(t, err)

			projectFileContent := readFile(t, testDataPath("empty", tt.expected.projectFile))
			gitIgnoreFileContent := readFile(t, testDataPath("empty", tt.expected.gitignoreFile))
			if tt.setup.gitIgnoreCrlf {
				gitIgnoreFileContent = crlf(gitIgnoreFileContent)
			}

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

func copyFile(t *testing.T, source string, target string) {
	content := readFile(t, source)
	err := os.WriteFile(target, []byte(content), 0600)

	require.NoError(t, err)
}

func copyFileCrlf(t *testing.T, source string, target string) {
	content := crlf(readFile(t, source))
	err := os.WriteFile(target, []byte(content), 0600)

	require.NoError(t, err)
}

func crlf(lfContent string) string {
	return strings.ReplaceAll(lfContent, "\n", "\r\n")
}

func readFile(t *testing.T, file string) string {
	bytes, err := os.ReadFile(file)
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
	content = strings.Replace(content, "<project>", azdcontext.ProjectName(azdCtx.ProjectDirectory()), 1)
	verifyFileContent(t, azdCtx.ProjectPath(), content)

	_, err := project.Load(context.Background(), azdCtx.ProjectPath())
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
		{
			"NoDuplicates",
			args{[]string{"a.txt", "b.txt", "dir1/a.txt"}, []string{"c.txt", "d.txt", "dir2/a.txt"}},
			[]string{},
		},
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
		require.NoError(t, os.WriteFile(filepath.Join(dir, file), []byte{}, 0600))
	}
}

func Test_parseExecutableFiles(t *testing.T) {
	tests := []struct {
		name              string
		stagedFilesOutput string
		expected          []string
		expectErr         bool
	}{
		{
			"ParseSome",
			heredoc.Doc(`
				100755 0744dc7835515b7f6246969cc3a6d5ae69490db9 0	init.sh
				100755 0684640b0dad4297b21109f2a39a73f4b1e3ca41 0	script/script1.sh
				100644 8b41c35f177e442a80c3a9c3bac826d14628e6b4 0	readme.md
				100644 53f096183482e39868eecd1d1a54a2a17cbe72e6 0	src/any1.txt
				100755 0684640b0dad4297b21109f2a39a73f4b1e3ca41 0	script/script2.sh
				100644 7c6cfd932637e4e89ce03c79563ad4044bf5c030 0	src/any2.json
				100644 9b69faf15e1ba7232aa2004940ac3419bfe8192e 0	src/any3.csv
				100644 0a5ec605ae4bdfdf384780e1b713f9404d41d97f 0	src/any4.txt
				100755 de6afa7b4a15f3ef63a1756160a026e2284c514d 0	script/script3.sh
				100644 21df4a08f368817971d2b3da7f471b97874f572f 0	doc.md`),
			[]string{
				"init.sh",
				"script/script1.sh",
				"script/script2.sh",
				"script/script3.sh",
			},
			false,
		},
		{
			"ParseNone",
			heredoc.Doc(`
				100644 8b41c35f177e442a80c3a9c3bac826d14628e6b4 0	readme.md
				100644 53f096183482e39868eecd1d1a54a2a17cbe72e6 0	src/any1.txt
				100644 7c6cfd932637e4e89ce03c79563ad4044bf5c030 0	src/any2.json
				100644 9b69faf15e1ba7232aa2004940ac3419bfe8192e 0	src/any3.csv
				100644 0a5ec605ae4bdfdf384780e1b713f9404d41d97f 0	src/any4.txt
				100644 21df4a08f368817971d2b3da7f471b97874f572f 0	doc.md`),
			[]string{},
			false,
		},
		{"ParseEmpty", "", []string{}, false},
		{"ErrorInvalidFormat", "100755 de6afa7b4a15f3ef63a1756160a026e2284c514d", []string{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual, err := parseExecutableFiles(tt.stagedFilesOutput)

			if tt.expectErr {
				require.Error(t, err)
			} else {
				assert.Equal(t, tt.expected, actual)
			}
		})
	}
}

func TestInitializer_PromptIfNonEmpty(t *testing.T) {
	type dirSetup struct {
		// whether the directory is a git repository
		isGitRepo bool
		// filenames to create in the directory before running tests
		files []string
	}
	tests := []struct {
		name           string
		dir            dirSetup
		userConfirm    bool
		declinedOutput string
	}{
		{
			"EmptyDir",
			dirSetup{false, []string{}},
			false,
			"",
		},
		{
			"NonEmptyDir",
			dirSetup{false, []string{"a.txt"}},
			true,
			"",
		},
		{
			"NonEmptyDir_Declined",
			dirSetup{false, []string{"a.txt"}},
			false,
			"confirmation declined; app was not initialized",
		},
		{
			"NonEmptyGitDir",
			dirSetup{true, []string{"a.txt"}},
			true,
			"",
		},
		{
			"NonEmptyGitDir_Declined",
			dirSetup{true, []string{"a.txt"}},
			false,
			"confirmation declined; app was not initialized",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			console := mockinput.NewMockConsole()
			cmdRun := mockexec.NewMockCommandRunner()
			gitCli := git.NewCli(cmdRun)

			// create files
			for _, file := range tt.dir.files {
				require.NoError(t, os.WriteFile(filepath.Join(dir, file), []byte{}, 0600))
			}

			// mock git branch command
			gitBranchImpl := cmdRun.When(func(args exec.RunArgs, command string) bool {
				return slices.Contains(args.Args, "branch") &&
					slices.Contains(args.Args, "--show-current")
			})
			if tt.dir.isGitRepo {
				gitBranchImpl.Respond(exec.RunResult{ExitCode: 0})
			} else {
				gitBranchImpl.Respond(exec.RunResult{ExitCode: 128, Stderr: "fatal: not a git repository"})
			}

			// mock console input
			console.WhenConfirm(func(options input.ConsoleOptions) bool { return true }).
				Respond(tt.userConfirm)

			i := &Initializer{
				console: console,
				gitCli:  gitCli,
			}
			azdCtx := azdcontext.NewAzdContextWithDirectory(dir)

			// we only test if declinedOutput is empty
			// if confirmation is declined and app is not initialized
			// we skip the test as it will exit with code 1
			if tt.declinedOutput == "" {
				err := i.PromptIfNonEmpty(context.Background(), azdCtx)
				require.NoError(t, err)
			}
		})
	}
}

func TestInitializer_writeFileSafe(t *testing.T) {
	const nameNoExt = "test"
	const ext = ".txt"
	const name = nameNoExt + ext
	const infix = "renamed"

	type file struct {
		path    string
		content string
	}

	tests := []struct {
		name     string
		existing []file // existing files in the directory
		args     file   // the file to write
		expect   []file // expected files after writing
	}{
		{
			name:     "Empty",
			existing: []file{},
			args:     file{name, "content"},
			expect:   []file{{name, "content"}},
		},
		{
			name:     "WhenExisting_Renamed",
			existing: []file{{name, "existing"}},
			args:     file{name, "content"},
			expect:   []file{{name, "existing"}, {nameNoExt + infix + ext, "content"}},
		},
		{
			name:     "BothExisting_NotModified",
			existing: []file{{name, "existing"}, {nameNoExt + infix + ext, "existing2"}},
			args:     file{name, "content"},
			expect:   []file{{name, "existing"}, {nameNoExt + infix + ext, "existing2"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := Initializer{
				console: mockinput.NewMockConsole(),
			}

			dir := t.TempDir()
			for _, f := range tt.existing {
				require.NoError(t, os.WriteFile(filepath.Join(dir, f.path), []byte(f.content), osutil.PermissionFile))
			}

			err := i.writeFileSafe(
				context.Background(),
				filepath.Join(dir, tt.args.path),
				infix,
				[]byte(tt.args.content),
				osutil.PermissionFile,
			)
			require.NoError(t, err)

			for _, expect := range tt.expect {
				require.FileExists(t, filepath.Join(dir, expect.path))
				content, err := os.ReadFile(filepath.Join(dir, expect.path))
				require.NoError(t, err)
				require.Equal(t, expect.content, string(content))
			}
		})
	}
}

func mockGitClone(t *testing.T, mockContext *mocks.MockContext, templatePath string, testCase testCase) {
	realRunner := exec.NewCommandRunner(nil)

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool { return true }).
		RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			// Stub out git clone, otherwise run actual command
			if slices.Contains(args.Args, "clone") && slices.Contains(args.Args, templatePath) {
				stagingDir := args.Args[len(args.Args)-1]
				copyTemplate(t, testDataPath(testCase.templateDir), stagingDir)

				gitArgs := exec.NewRunArgs("git", "-C", stagingDir)

				// Mock clone by creating a git repository locally
				_, err := realRunner.Run(*mockContext.Context, gitArgs.AppendParams("init"))
				require.NoError(t, err)

				_, err = realRunner.Run(*mockContext.Context, gitArgs.AppendParams("add", "*"))
				require.NoError(t, err)

				for _, file := range testCase.executableFiles {
					_, err = realRunner.Run(
						*mockContext.Context,
						gitArgs.AppendParams("update-index", "--chmod=+x", file),
					)
					require.NoError(t, err)

					// Mocks the correct behavior in *nix when the file lands on the filesystem.
					// git would have automatically set the correct file executable permissions.
					//
					// Note that `git update-index --chmod=+x` simply updates the tracked permissions in git,
					// but does not update the files directly, hence this is needed.
					if runtime.GOOS != "windows" {
						err = os.Chmod(filepath.Join(stagingDir, file), 0755)
						require.NoError(t, err)
					}
				}

				return exec.NewRunResult(0, "", ""), nil
			}

			return realRunner.Run(*mockContext.Context, args)
		})
}

// createLocalTemplateDir creates a local template directory that mimics what a real template
// looks like on the filesystem (without .txt suffixes used in testdata).
// Returns the path to the created template directory.
func createLocalTemplateDir(t *testing.T, sourceTestData string) string {
	t.Helper()
	dir := t.TempDir()

	err := filepath.WalkDir(sourceTestData, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			relDir, err := filepath.Rel(sourceTestData, path)
			if err != nil {
				return err
			}
			return os.MkdirAll(filepath.Join(dir, relDir), 0755)
		}

		rel, err := filepath.Rel(sourceTestData, path)
		if err != nil {
			return err
		}
		// Remove the .txt suffix used in testdata
		relTarget := strings.TrimSuffix(rel, ".txt")

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, relTarget), content, 0600)
	})
	require.NoError(t, err)

	return dir
}

func Test_Initializer_Initialize_LocalTemplate(t *testing.T) {
	// Create a local template directory (NOT a git repo) with real files
	localTemplateDir := createLocalTemplateDir(t, testDataPath("template"))

	// Add an uncommitted-only file to verify it gets copied
	// (this is the key advantage over git clone)
	uncommittedContent := "this file is not committed to any git repo"
	require.NoError(t, os.WriteFile(
		filepath.Join(localTemplateDir, "uncommitted.txt"),
		[]byte(uncommittedContent),
		0600,
	))

	projectDir := t.TempDir()
	azdCtx := azdcontext.NewAzdContextWithDirectory(projectDir)

	// Use real command runner (no git clone mocking needed for local templates)
	realRunner := exec.NewCommandRunner(nil)

	mockEnv := &mockenv.MockEnvManager{}
	mockEnv.On("Save", mock.Anything, mock.Anything).Return(nil)

	i := NewInitializer(
		mockinput.NewMockConsole(),
		git.NewCli(realRunner),
		dotnet.NewCli(realRunner),
		alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
		lazy.From[environment.Manager](mockEnv),
	)

	err := i.Initialize(context.Background(), azdCtx, &templates.Template{
		RepositoryPath: localTemplateDir,
	}, "")
	require.NoError(t, err)

	// Verify template files were copied
	require.FileExists(t, filepath.Join(projectDir, "azure.yaml"))
	require.FileExists(t, filepath.Join(projectDir, "README.md"))
	require.FileExists(t, filepath.Join(projectDir, "src", "Program.cs"))
	require.FileExists(t, filepath.Join(projectDir, "script", "test.sh"))

	// Verify the uncommitted file was also copied
	require.FileExists(t, filepath.Join(projectDir, "uncommitted.txt"))
	content, err := os.ReadFile(filepath.Join(projectDir, "uncommitted.txt"))
	require.NoError(t, err)
	require.Equal(t, uncommittedContent, string(content))

	// Verify .git directory from source template was NOT copied
	// (the project may have its own .git from azd init, but it should not contain the source marker)
	require.NoFileExists(t, filepath.Join(projectDir, ".git", "source-template-marker"))

	// Verify standard azd assets were created
	require.FileExists(t, filepath.Join(projectDir, ".gitignore"))
	require.DirExists(t, azdCtx.EnvironmentDirectory())
}

func Test_Initializer_Initialize_LocalTemplateWithGitDir(t *testing.T) {
	// Create a local template that also has a .git directory
	localTemplateDir := createLocalTemplateDir(t, testDataPath("template-minimal"))

	// Create a fake .git directory with a unique marker file
	require.NoError(t, os.MkdirAll(filepath.Join(localTemplateDir, ".git"), 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(localTemplateDir, ".git", "source-template-marker"),
		[]byte("this should not be copied"),
		0600,
	))

	projectDir := t.TempDir()
	azdCtx := azdcontext.NewAzdContextWithDirectory(projectDir)

	realRunner := exec.NewCommandRunner(nil)

	mockEnv := &mockenv.MockEnvManager{}
	mockEnv.On("Save", mock.Anything, mock.Anything).Return(nil)

	i := NewInitializer(
		mockinput.NewMockConsole(),
		git.NewCli(realRunner),
		dotnet.NewCli(realRunner),
		alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
		lazy.From[environment.Manager](mockEnv),
	)

	err := i.Initialize(context.Background(), azdCtx, &templates.Template{
		RepositoryPath: localTemplateDir,
	}, "")
	require.NoError(t, err)

	// Verify template files were copied
	require.FileExists(t, filepath.Join(projectDir, "README.md"))

	// Verify the source template's .git was NOT copied.
	// Our marker file should NOT exist in the project's .git directory.
	require.NoFileExists(t, filepath.Join(projectDir, ".git", "source-template-marker"))
}

func Test_Initializer_Initialize_LocalTemplateRespectsGitignore(t *testing.T) {
	// Create a local template directory with a .gitignore that excludes certain files
	localTemplateDir := createLocalTemplateDir(t, testDataPath("template"))

	// Create a .gitignore in the template
	gitignoreContent := "node_modules/\n*.log\nbuild/\n.env\n"
	require.NoError(t, os.WriteFile(
		filepath.Join(localTemplateDir, ".gitignore"),
		[]byte(gitignoreContent),
		0600,
	))

	// Create files that SHOULD be ignored by .gitignore
	require.NoError(t, os.MkdirAll(filepath.Join(localTemplateDir, "node_modules", "some-pkg"), 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(localTemplateDir, "node_modules", "some-pkg", "index.js"),
		[]byte("module.exports = {}"),
		0600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(localTemplateDir, "debug.log"),
		[]byte("some log output"),
		0600,
	))
	require.NoError(t, os.MkdirAll(filepath.Join(localTemplateDir, "build"), 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(localTemplateDir, "build", "output.js"),
		[]byte("compiled code"),
		0600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(localTemplateDir, ".env"),
		[]byte("SECRET=value"),
		0600,
	))

	// Create a file that should NOT be ignored
	require.NoError(t, os.WriteFile(
		filepath.Join(localTemplateDir, "important.txt"),
		[]byte("keep me"),
		0600,
	))

	projectDir := t.TempDir()
	azdCtx := azdcontext.NewAzdContextWithDirectory(projectDir)

	realRunner := exec.NewCommandRunner(nil)

	mockEnv := &mockenv.MockEnvManager{}
	mockEnv.On("Save", mock.Anything, mock.Anything).Return(nil)

	i := NewInitializer(
		mockinput.NewMockConsole(),
		git.NewCli(realRunner),
		dotnet.NewCli(realRunner),
		alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
		lazy.From[environment.Manager](mockEnv),
	)

	err := i.Initialize(context.Background(), azdCtx, &templates.Template{
		RepositoryPath: localTemplateDir,
	}, "")
	require.NoError(t, err)

	// Verify tracked template files were copied
	require.FileExists(t, filepath.Join(projectDir, "azure.yaml"))
	require.FileExists(t, filepath.Join(projectDir, "README.md"))
	require.FileExists(t, filepath.Join(projectDir, "important.txt"))

	// Verify .gitignore itself was copied (it's a tracked file)
	require.FileExists(t, filepath.Join(projectDir, ".gitignore"))

	// Verify .gitignored files were NOT copied (matching git clone behavior)
	require.NoDirExists(t, filepath.Join(projectDir, "node_modules"))
	require.NoFileExists(t, filepath.Join(projectDir, "debug.log"))
	require.NoDirExists(t, filepath.Join(projectDir, "build"))
	require.NoFileExists(t, filepath.Join(projectDir, ".env"))
}

func Test_Initializer_Initialize_LocalTemplateGitignoreNegation(t *testing.T) {
	// Verify that .gitignore negation patterns (e.g., !important.log) are respected
	localTemplateDir := createLocalTemplateDir(t, testDataPath("template"))

	// Create a .gitignore with a negation pattern
	gitignoreContent := "*.log\n!important.log\n"
	require.NoError(t, os.WriteFile(
		filepath.Join(localTemplateDir, ".gitignore"),
		[]byte(gitignoreContent),
		0600,
	))

	// Create log files — one should be ignored, one should be kept via negation
	require.NoError(t, os.WriteFile(
		filepath.Join(localTemplateDir, "debug.log"),
		[]byte("debug output"),
		0600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(localTemplateDir, "important.log"),
		[]byte("keep this"),
		0600,
	))

	projectDir := t.TempDir()
	azdCtx := azdcontext.NewAzdContextWithDirectory(projectDir)

	realRunner := exec.NewCommandRunner(nil)

	mockEnv := &mockenv.MockEnvManager{}
	mockEnv.On("Save", mock.Anything, mock.Anything).Return(nil)

	i := NewInitializer(
		mockinput.NewMockConsole(),
		git.NewCli(realRunner),
		dotnet.NewCli(realRunner),
		alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
		lazy.From[environment.Manager](mockEnv),
	)

	err := i.Initialize(context.Background(), azdCtx, &templates.Template{
		RepositoryPath: localTemplateDir,
	}, "")
	require.NoError(t, err)

	// debug.log should be excluded by *.log pattern
	require.NoFileExists(t, filepath.Join(projectDir, "debug.log"))

	// important.log should be included via negation pattern !important.log
	require.FileExists(t, filepath.Join(projectDir, "important.log"))
}

func Test_Initializer_Initialize_LocalTemplateGitFile(t *testing.T) {
	// In git worktrees and submodules, .git is a file (not a directory)
	// pointing to an external gitdir. It must be excluded just like a .git directory.
	localTemplateDir := createLocalTemplateDir(t, testDataPath("template-minimal"))

	// Create a .git *file* like in a worktree/submodule
	require.NoError(t, os.WriteFile(
		filepath.Join(localTemplateDir, ".git"),
		[]byte("gitdir: /some/external/gitdir"),
		0600,
	))

	projectDir := t.TempDir()
	azdCtx := azdcontext.NewAzdContextWithDirectory(projectDir)

	realRunner := exec.NewCommandRunner(nil)

	mockEnv := &mockenv.MockEnvManager{}
	mockEnv.On("Save", mock.Anything, mock.Anything).Return(nil)

	i := NewInitializer(
		mockinput.NewMockConsole(),
		git.NewCli(realRunner),
		dotnet.NewCli(realRunner),
		alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
		lazy.From[environment.Manager](mockEnv),
	)

	err := i.Initialize(context.Background(), azdCtx, &templates.Template{
		RepositoryPath: localTemplateDir,
	}, "")
	require.NoError(t, err)

	// Verify template files were copied
	require.FileExists(t, filepath.Join(projectDir, "README.md"))

	// Verify the .git *file* was NOT copied
	require.NoFileExists(t, filepath.Join(projectDir, ".git"))
}

func Test_Initializer_Initialize_LocalTemplateNestedGitignore(t *testing.T) {
	// Nested .gitignore files in subdirectories should be respected,
	// not just the root .gitignore.
	localTemplateDir := createLocalTemplateDir(t, testDataPath("template"))

	// Create a subdirectory with its own .gitignore
	subDir := filepath.Join(localTemplateDir, "subproject")
	require.NoError(t, os.MkdirAll(subDir, 0755))

	// Create a .gitignore in the subdirectory that ignores *.tmp files
	require.NoError(t, os.WriteFile(
		filepath.Join(subDir, ".gitignore"),
		[]byte("*.tmp\n"),
		0600,
	))

	// Create files: one should be ignored by nested .gitignore, one should be kept
	require.NoError(t, os.WriteFile(
		filepath.Join(subDir, "data.tmp"),
		[]byte("temporary data"),
		0600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(subDir, "keep.txt"),
		[]byte("keep this"),
		0600,
	))

	projectDir := t.TempDir()
	azdCtx := azdcontext.NewAzdContextWithDirectory(projectDir)

	realRunner := exec.NewCommandRunner(nil)

	mockEnv := &mockenv.MockEnvManager{}
	mockEnv.On("Save", mock.Anything, mock.Anything).Return(nil)

	i := NewInitializer(
		mockinput.NewMockConsole(),
		git.NewCli(realRunner),
		dotnet.NewCli(realRunner),
		alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
		lazy.From[environment.Manager](mockEnv),
	)

	err := i.Initialize(context.Background(), azdCtx, &templates.Template{
		RepositoryPath: localTemplateDir,
	}, "")
	require.NoError(t, err)

	// Verify the kept file was copied
	require.FileExists(t, filepath.Join(projectDir, "subproject", "keep.txt"))

	// Verify the nested .gitignore itself was copied
	require.FileExists(t, filepath.Join(projectDir, "subproject", ".gitignore"))

	// Verify the .tmp file was excluded by nested .gitignore
	require.NoFileExists(t, filepath.Join(projectDir, "subproject", "data.tmp"))
}
