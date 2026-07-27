// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package maven

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockexec"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
)

func Test_getMavenPath(t *testing.T) {
	rootPath := os.TempDir()
	sourcePath := filepath.Join(rootPath, "src")
	projectPath := filepath.Join(sourcePath, "api")

	pathDir := os.TempDir()

	require.NoError(t, os.MkdirAll(projectPath, 0755))
	ostest.Unsetenv(t, "PATH")

	type args struct {
		projectPath     string
		rootProjectPath string
	}

	tests := []struct {
		name         string
		mvnwPath     []string
		mvnwRelative bool
		mvnPath      []string
		envVar       map[string]string
		want         string
		wantErr      bool
	}{
		{name: "MvnwProjectPath", mvnwPath: []string{projectPath}, want: filepath.Join(projectPath, mvnwWithExt())},
		{name: "MvnwSrcPath", mvnwPath: []string{sourcePath}, want: filepath.Join(sourcePath, mvnwWithExt())},
		{name: "MvnwRootPath", mvnwPath: []string{rootPath}, want: filepath.Join(rootPath, mvnwWithExt())},
		{name: "MvnwFirst", mvnwPath: []string{rootPath}, want: filepath.Join(rootPath, mvnwWithExt()),
			mvnPath: []string{pathDir}, envVar: map[string]string{"PATH": pathDir}},
		{
			name:         "MvnwProjectPathRelative",
			mvnwPath:     []string{projectPath},
			want:         filepath.Join(projectPath, mvnwWithExt()),
			mvnwRelative: true,
		},
		{
			name:         "MvnwSrcPathRelative",
			mvnwPath:     []string{sourcePath},
			want:         filepath.Join(sourcePath, mvnwWithExt()),
			mvnwRelative: true,
		},
		{
			name:         "MvnwRootPathRelative",
			mvnwPath:     []string{rootPath},
			want:         filepath.Join(rootPath, mvnwWithExt()),
			mvnwRelative: true,
		},
		{
			name:    "Mvn",
			mvnPath: []string{pathDir},
			envVar:  map[string]string{"PATH": pathDir},
			want:    filepath.Join(pathDir, mvnWithExt()),
		},
		{name: "NotFound", want: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			placeExecutable(t, mvnwWithExt(), tt.mvnwPath...)
			placeExecutable(t, mvnWithExt(), tt.mvnPath...)
			ostest.Setenvs(t, tt.envVar)

			args := args{}
			if tt.mvnwRelative {
				ostest.Chdir(t, rootPath)
				// Set PWD directly to avoid symbolic links

				t.Setenv("PWD", rootPath)
				projectPathRel, err := filepath.Rel(rootPath, projectPath)
				require.NoError(t, err)
				args.projectPath = projectPathRel
				args.rootProjectPath = ""
			} else {
				args.projectPath = projectPath
				args.rootProjectPath = rootPath
			}

			wd, err := os.Getwd()
			require.NoError(t, err)
			log.Printf("rootPath: %s, cwd: %s, getMavenPath(%s, %s)\n", rootPath, wd, args.projectPath, args.rootProjectPath)
			actual, err := getMavenPath(args.projectPath, args.rootProjectPath)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			assert.Equal(t, tt.want, actual)
		})
	}
}

func Test_extractVersion(t *testing.T) {
	execMock := mockexec.NewMockCommandRunner().
		When(func(a exec.RunArgs, command string) bool { return a.Args[0] == "--version" }).
		RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, heredoc.Doc(`
			Apache Maven 3.9.1 (2e178502fcdbffc201671fb2537d0cb4b4cc58f8)
			Maven home: C:\Tools\apache-maven-3.9.1
			Java version: 17.0.6, vendor: Microsoft, runtime: C:\Program Files\Microsoft\jdk-17.0.6.10-hotspot
			Default locale: en_US, platform encoding: Cp1252
			OS name: "windows 11", version: "10.0", arch: "amd64", family: "windows"
			`), ""), nil
		})

	mvn := NewCli(execMock)
	placeExecutable(t, mvnwWithExt(), mvn.projectPath)
	ver, err := mvn.extractVersion(t.Context())
	require.NoError(t, err)
	require.Equal(t, "3.9.1", ver)
}

func placeExecutable(t *testing.T, name string, dirs ...string) {
	for _, createPath := range dirs {
		toCreate := filepath.Join(createPath, name)
		ostest.Create(t, toCreate)

		err := os.Chmod(toCreate, 0755)
		require.NoError(t, err)
	}
}

func mvnWithExt() string {
	if runtime.GOOS == "windows" {
		// For Windows, we want to test EXT resolution behavior
		return "mvn.cmd"
	} else {
		return "mvn"
	}
}

func mvnwWithExt() string {
	if runtime.GOOS == "windows" {
		// For Windows, we want to test EXT resolution behavior
		return "mvnw.cmd"
	} else {
		return "mvnw"
	}
}

// newTestCli creates a Cli with a mock command runner
// and marks the lazy maven-path init as done so that
// mvnCmd() returns "mvn" without searching the disk.
func newTestCli(
	runner exec.CommandRunner,
) *Cli {
	cli := &Cli{
		commandRunner: runner,
		mvnCmdStr:     "mvn",
	}
	// Mark lazy init as done; the no-op succeeds so
	// subsequent mvnCmd() calls skip getMavenPath.
	cli.mvnCmdInit.Do(func() error { return nil })
	return cli
}

func TestName(t *testing.T) {
	cli := NewCli(nil)
	require.Equal(t, "Maven", cli.Name())
}

func TestInstallUrl(t *testing.T) {
	cli := NewCli(nil)
	require.Equal(
		t, "https://maven.apache.org", cli.InstallUrl(),
	)
}

func TestSetPath(t *testing.T) {
	cli := NewCli(nil)
	cli.SetPath("/project", "/root")
	require.Equal(t, "/project", cli.projectPath)
	require.Equal(t, "/root", cli.rootProjectPath)
}

func TestMavenVersionRegexp(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		matches bool
	}{
		{
			name: "Standard",
			input: "Apache Maven 3.9.1 " +
				"(2e178502fcdbffc201671fb2537d0cb4b4cc58f8)",
			want:    "3.9.1",
			matches: true,
		},
		{
			name: "OlderVersion",
			input: "Apache Maven 3.6.3 " +
				"(cecedd343002696d0abb50b32b541b8a6ba2883f)",
			want:    "3.6.3",
			matches: true,
		},
		{
			name:    "NoMatch",
			input:   "Gradle 8.0.1",
			matches: false,
		},
		{
			name:    "PartialMatch",
			input:   "Apache Maven",
			matches: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matches := mavenVersionRegexp.FindStringSubmatch(
				tt.input,
			)

			if !tt.matches {
				require.Empty(t, matches)
				return
			}

			require.Len(t, matches, 2)
			require.Equal(t, tt.want, matches[1])
		})
	}
}

func TestGetEffectivePomStringFromConsoleOutput(
	t *testing.T,
) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name: "Standard",
			input: "[INFO] Scanning for projects...\n" +
				"[INFO]\n" +
				"<project xmlns=\"http://maven.apache.org" +
				"/POM/4.0.0\">\n" +
				"  <modelVersion>4.0.0</modelVersion>\n" +
				"</project>\n" +
				"[INFO] Done\n",
			want: "<project xmlns=\"http://maven.apache.org" +
				"/POM/4.0.0\">" +
				"  <modelVersion>4.0.0</modelVersion>" +
				"</project>",
		},
		{
			name:    "EmptyOutput",
			input:   "",
			wantErr: true,
		},
		{
			name: "NoProjectTag",
			input: "[INFO] Scanning for projects...\n" +
				"[INFO] Done\n",
			wantErr: true,
		},
		{
			name: "ProjectStartOnly",
			input: "<project xmlns=\"http://maven.apache.org" +
				"/POM/4.0.0\">\n" +
				"  <modelVersion>4.0.0</modelVersion>\n",
			want: "<project xmlns=\"http://maven.apache.org" +
				"/POM/4.0.0\">" +
				"  <modelVersion>4.0.0</modelVersion>",
		},
		{
			name: "IndentedProjectTags",
			input: "  <project xmlns=\"http://example.com\">\n" +
				"    <id>test</id>\n" +
				"  </project>\n",
			want: "  <project xmlns=\"http://example.com\">" +
				"    <id>test</id>" +
				"  </project>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getEffectivePomStringFromConsoleOutput(
				tt.input,
			)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestCompile(t *testing.T) {
	tests := []struct {
		name    string
		runErr  error
		wantErr bool
	}{
		{
			name: "Success",
		},
		{
			name:    "Failure",
			runErr:  errors.New("compilation failed"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var captured exec.RunArgs
			runner := mockexec.NewMockCommandRunner()
			runner.When(func(
				args exec.RunArgs, _ string,
			) bool {
				return slices.Contains(args.Args, "compile")
			}).RespondFn(func(
				args exec.RunArgs,
			) (exec.RunResult, error) {
				captured = args
				return exec.RunResult{}, tt.runErr
			})

			cli := newTestCli(runner)
			err := cli.Compile(
				t.Context(), "/project",
				[]string{"ENV=val"},
			)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Contains(t, captured.Args, "compile")
			require.Equal(t, "/project", captured.Cwd)
		})
	}
}

func TestPackage(t *testing.T) {
	var captured exec.RunArgs
	runner := mockexec.NewMockCommandRunner()
	runner.When(func(
		args exec.RunArgs, _ string,
	) bool {
		return slices.Contains(args.Args, "package")
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		captured = args
		return exec.RunResult{}, nil
	})

	cli := newTestCli(runner)
	err := cli.Package(t.Context(), "/project", nil)
	require.NoError(t, err)
	require.Contains(t, captured.Args, "package")
	require.Contains(t, captured.Args, "-DskipTests")
}

func TestResolveDependencies(t *testing.T) {
	var captured exec.RunArgs
	runner := mockexec.NewMockCommandRunner()
	runner.When(func(
		args exec.RunArgs, _ string,
	) bool {
		return slices.Contains(
			args.Args, "dependency:resolve",
		)
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		captured = args
		return exec.RunResult{}, nil
	})

	cli := newTestCli(runner)
	err := cli.ResolveDependencies(
		t.Context(), "/project", nil,
	)
	require.NoError(t, err)
	require.Contains(
		t, captured.Args, "dependency:resolve",
	)
}

func TestGetProperty(t *testing.T) {
	tests := []struct {
		name    string
		stdout  string
		runErr  error
		want    string
		wantErr error
	}{
		{
			name:   "Success",
			stdout: "  com.example.myapp  ",
			want:   "com.example.myapp",
		},
		{
			name:    "PropertyNotFound",
			stdout:  "null object or invalid expression",
			wantErr: ErrPropertyNotFound,
		},
		{
			name:    "RunError",
			runErr:  errors.New("mvn failed"),
			wantErr: errors.New("mvn help:evaluate"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			runner := mockexec.NewMockCommandRunner()
			runner.When(func(
				args exec.RunArgs, _ string,
			) bool {
				return slices.Contains(
					args.Args, "help:evaluate",
				)
			}).RespondFn(func(
				args exec.RunArgs,
			) (exec.RunResult, error) {
				return exec.RunResult{
					Stdout: tt.stdout,
				}, tt.runErr
			})

			cli := newTestCli(runner)
			got, err := cli.GetProperty(
				t.Context(),
				"project.groupId",
				"/project",
			)

			if tt.wantErr != nil {
				require.Error(t, err)
				if errors.Is(tt.wantErr, ErrPropertyNotFound) {
					require.ErrorIs(t, err, ErrPropertyNotFound)
				}
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestGetPropertyArgs(t *testing.T) {
	var captured exec.RunArgs
	runner := mockexec.NewMockCommandRunner()
	runner.When(func(
		args exec.RunArgs, _ string,
	) bool {
		return slices.Contains(args.Args, "help:evaluate")
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		captured = args
		return exec.RunResult{
			Stdout: "result-value",
		}, nil
	})

	cli := newTestCli(runner)
	_, err := cli.GetProperty(
		t.Context(), "project.version", "/proj",
	)
	require.NoError(t, err)
	require.Contains(t, captured.Args, "help:evaluate")
	require.Contains(
		t, captured.Args, "-Dexpression=project.version",
	)
	require.Contains(t, captured.Args, "-q")
	require.Contains(t, captured.Args, "-DforceStdout")
}

func TestEffectivePom(t *testing.T) {
	pomOutput := "[INFO] Scanning\n" +
		"<project xmlns=\"http://maven.apache.org\">\n" +
		"  <modelVersion>4.0.0</modelVersion>\n" +
		"</project>\n" +
		"[INFO] Done\n"

	runner := mockexec.NewMockCommandRunner()
	runner.When(func(
		args exec.RunArgs, _ string,
	) bool {
		return slices.Contains(
			args.Args, "help:effective-pom",
		)
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		return exec.RunResult{
			Stdout: pomOutput,
		}, nil
	})

	cli := newTestCli(runner)
	got, err := cli.EffectivePom(
		t.Context(), "/project/pom.xml",
	)
	require.NoError(t, err)
	require.Contains(t, got, "<project")
	require.Contains(t, got, "</project>")
}

func TestEffectivePomError(t *testing.T) {
	runner := mockexec.NewMockCommandRunner()
	runner.When(func(
		args exec.RunArgs, _ string,
	) bool {
		return slices.Contains(
			args.Args, "help:effective-pom",
		)
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		return exec.RunResult{},
			errors.New("pom failed")
	})

	cli := newTestCli(runner)
	_, err := cli.EffectivePom(
		t.Context(), "/project/pom.xml",
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "help:effective-pom")
}

func TestErrPropertyNotFound(t *testing.T) {
	require.EqualError(
		t, ErrPropertyNotFound, "property not found",
	)
}
