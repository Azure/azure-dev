// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package maven

import (
	"errors"
	"slices"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockexec"
	"github.com/stretchr/testify/require"
)

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
