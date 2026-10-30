// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	azdexec "github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/javac"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/maven"
)

type commandRunnerFunc struct {
	run        func(context.Context, azdexec.RunArgs) (azdexec.RunResult, error)
	runList    func(context.Context, []string, azdexec.RunArgs) (azdexec.RunResult, error)
	toolInPath func(string) error
}

func (r *commandRunnerFunc) Run(ctx context.Context, args azdexec.RunArgs) (azdexec.RunResult, error) {
	return r.run(ctx, args)
}

func (r *commandRunnerFunc) RunList(
	ctx context.Context,
	commands []string,
	args azdexec.RunArgs,
) (azdexec.RunResult, error) {
	if r.runList == nil {
		return azdexec.RunResult{}, nil
	}
	return r.runList(ctx, commands, args)
}

func (r *commandRunnerFunc) ToolInPath(name string) error {
	if r.toolInPath == nil {
		return nil
	}
	return r.toolInPath(name)
}

func TestHasUnbornGitHeadClassifiesOnlyInitialRepositories(t *testing.T) {
	testError := errors.New("git failed")
	tests := []struct {
		name       string
		result     azdexec.RunResult
		err        error
		want       bool
		wantErr    error
		wantAnyErr bool
	}{
		{
			name:   "unborn head",
			result: azdexec.NewRunResult(0, "# branch.oid (initial)\r\n# branch.head main\r\n", ""),
			want:   true,
		},
		{
			name:   "valid head",
			result: azdexec.NewRunResult(0, "# branch.oid 0123456789abcdef\r\n# branch.head main\r\n", ""),
		},
		{
			name:   "detached head",
			result: azdexec.NewRunResult(0, "# branch.oid 0123456789abcdef\n# branch.head (detached)\n", ""),
		},
		{
			name:       "corrupt repository",
			err:        testError,
			wantAnyErr: true,
		},
		{
			name:    "canceled",
			err:     context.Canceled,
			wantErr: context.Canceled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectDir := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(projectDir, ".git"), 0o700))
			runner := &commandRunnerFunc{
				run: func(_ context.Context, args azdexec.RunArgs) (azdexec.RunResult, error) {
					require.Equal(t, "git", args.Cmd)
					require.Equal(t, []string{
						"--no-optional-locks",
						"status",
						"--porcelain=v2",
						"--branch",
						"--untracked-files=no",
					}, args.Args)
					require.Equal(t, []string{"GIT_OPTIONAL_LOCKS=0"}, args.Env)
					return tt.result, tt.err
				},
			}

			got, err := hasUnbornGitHead(t.Context(), runner, projectDir, nil)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			}
			if tt.wantAnyErr {
				require.Error(t, err)
			} else if tt.wantErr == nil {
				require.NoError(t, err)
			}
			require.Equal(t, tt.want, got)
		})
	}
}

func TestHasUnbornGitHeadPreservesNonRepositoryBehavior(t *testing.T) {
	called := false
	runner := &commandRunnerFunc{
		run: func(context.Context, azdexec.RunArgs) (azdexec.RunResult, error) {
			called = true
			return azdexec.RunResult{}, errors.New("git must not run")
		},
	}

	unborn, err := hasUnbornGitHead(t.Context(), runner, t.TempDir(), nil)

	require.NoError(t, err)
	require.False(t, unborn)
	require.False(t, called)
}

func TestHasGitMetadataRecognizesDirectoriesGitfilesAndExplicitDirectories(t *testing.T) {
	t.Run("nearest directory", func(t *testing.T) {
		root := t.TempDir()
		nested := filepath.Join(root, "src", "app")
		require.NoError(t, os.MkdirAll(nested, 0o700))
		require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o700))

		found, err := hasGitMetadata(nested, nil)

		require.NoError(t, err)
		require.True(t, found)
	})

	t.Run("nearest gitfile", func(t *testing.T) {
		root := t.TempDir()
		nested := filepath.Join(root, "src")
		require.NoError(t, os.MkdirAll(nested, 0o700))
		require.NoError(t, os.WriteFile(
			filepath.Join(root, ".git"),
			[]byte("gitdir: C:\\worktrees\\metadata\n"),
			0o600,
		))

		found, err := hasGitMetadata(nested, nil)

		require.NoError(t, err)
		require.True(t, found)
	})

	t.Run("explicit directory", func(t *testing.T) {
		root := t.TempDir()
		gitDir := filepath.Join(root, "metadata")
		require.NoError(t, os.Mkdir(gitDir, 0o700))

		found, err := hasGitMetadata(root, []string{"GIT_DIR=metadata"})

		require.NoError(t, err)
		require.True(t, found)
	})

	t.Run("missing explicit directory", func(t *testing.T) {
		root := t.TempDir()

		found, err := hasGitMetadata(root, []string{"GIT_DIR=missing"})

		require.Error(t, err)
		require.False(t, found)
	})
}

func TestHasUnbornGitHeadPreservesCommandScopedGitEnvironment(t *testing.T) {
	root := t.TempDir()
	worktree := filepath.Join(root, "worktree")
	gitDir := filepath.Join(worktree, "metadata")
	require.NoError(t, os.MkdirAll(gitDir, 0o700))
	commandEnv := []string{
		"GIT_DIR=metadata",
		"GIT_WORK_TREE=.",
		"GIT_COMMON_DIR=common",
	}
	runner := &commandRunnerFunc{
		run: func(_ context.Context, args azdexec.RunArgs) (azdexec.RunResult, error) {
			require.Equal(t, worktree, args.Cwd)
			require.Equal(t, []string{
				"GIT_OPTIONAL_LOCKS=0",
				"GIT_DIR=metadata",
				"GIT_WORK_TREE=.",
				"GIT_COMMON_DIR=common",
			}, args.Env)
			return azdexec.NewRunResult(0, "# branch.oid (initial)\n", ""), nil
		},
	}

	unborn, err := hasUnbornGitHead(t.Context(), runner, worktree, commandEnv)

	require.NoError(t, err)
	require.True(t, unborn)
}

func TestHasUnbornGitHeadWithRealGit(t *testing.T) {
	runner := azdexec.NewCommandRunner(nil)
	if err := runner.ToolInPath("git"); err != nil {
		t.Skipf("git is unavailable: %v", err)
	}

	projectDir := t.TempDir()
	runGit := func(args ...string) {
		result, err := runner.Run(t.Context(), azdexec.NewRunArgs("git", args...).WithCwd(projectDir))
		require.NoError(t, err, result.Stderr)
	}
	runGit("init", "--quiet")

	unborn, err := hasUnbornGitHead(t.Context(), runner, projectDir, nil)
	require.NoError(t, err)
	require.True(t, unborn)

	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "README.md"), []byte("test"), 0o600))
	runGit("add", "README.md")
	runGit("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--quiet", "-m", "initial")

	unborn, err = hasUnbornGitHead(t.Context(), runner, projectDir, nil)
	require.NoError(t, err)
	require.False(t, unborn)

	require.NoError(t, os.WriteFile(filepath.Join(projectDir, ".git", "HEAD"), []byte("invalid head\n"), 0o600))
	unborn, err = hasUnbornGitHead(t.Context(), runner, projectDir, nil)
	require.Error(t, err)
	require.False(t, unborn)
}

func TestMavenPackageCommandRunnerScopesAccommodation(t *testing.T) {
	tests := []struct {
		name          string
		spring        bool
		command       azdexec.RunArgs
		gitResult     azdexec.RunResult
		hasMetadata   bool
		wantGitCheck  bool
		wantArguments []string
		wantProgress  bool
	}{
		{
			name:          "Spring package with unborn head",
			spring:        true,
			command:       azdexec.NewRunArgs("mvnw", "package", "-DskipTests"),
			gitResult:     azdexec.NewRunResult(0, "# branch.oid (initial)\n", ""),
			hasMetadata:   true,
			wantGitCheck:  true,
			wantArguments: []string{"package", "-DskipTests", skipGitMetadataMavenOption},
			wantProgress:  true,
		},
		{
			name:          "Spring package with valid head",
			spring:        true,
			command:       azdexec.NewRunArgs("mvnw", "package", "-DskipTests"),
			gitResult:     azdexec.NewRunResult(0, "# branch.oid abcdef\n", ""),
			hasMetadata:   true,
			wantGitCheck:  true,
			wantArguments: []string{"package", "-DskipTests"},
		},
		{
			name:          "Spring package outside a Git repository",
			spring:        true,
			command:       azdexec.NewRunArgs("mvnw", "package", "-DskipTests"),
			wantArguments: []string{"package", "-DskipTests"},
		},
		{
			name:          "non-Spring Java package",
			command:       azdexec.NewRunArgs("mvnw", "package", "-DskipTests"),
			wantArguments: []string{"package", "-DskipTests"},
		},
		{
			name:          "Spring restore",
			spring:        true,
			command:       azdexec.NewRunArgs("mvnw", "dependency:resolve"),
			wantArguments: []string{"dependency:resolve"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectDir := t.TempDir()
			if tt.hasMetadata {
				require.NoError(t, os.Mkdir(filepath.Join(projectDir, ".git"), 0o700))
			}
			tt.command.Cwd = projectDir
			var calls []azdexec.RunArgs
			base := &commandRunnerFunc{
				run: func(_ context.Context, args azdexec.RunArgs) (azdexec.RunResult, error) {
					calls = append(calls, args)
					if args.Cmd == "git" {
						return tt.gitResult, nil
					}
					return azdexec.NewRunResult(0, "", ""), nil
				},
			}
			var progress []string
			runner := &mavenPackageCommandRunner{
				base:   base,
				spring: tt.spring,
				progress: func(message string) {
					progress = append(progress, message)
				},
			}

			_, err := runner.Run(t.Context(), tt.command)
			require.NoError(t, err)
			if tt.wantGitCheck {
				require.Len(t, calls, 2)
				require.Equal(t, "git", calls[0].Cmd)
			} else {
				require.Len(t, calls, 1)
			}
			require.Equal(t, tt.wantArguments, calls[len(calls)-1].Args)
			require.Equal(t, tt.wantProgress, len(progress) == 1)
		})
	}
}

func TestMavenPackageCommandRunnerPropagatesCancellationAndMavenFailure(t *testing.T) {
	t.Run("Git cancellation", func(t *testing.T) {
		projectDir := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(projectDir, ".git"), 0o700))
		base := &commandRunnerFunc{
			run: func(_ context.Context, args azdexec.RunArgs) (azdexec.RunResult, error) {
				require.Equal(t, "git", args.Cmd)
				return azdexec.RunResult{}, context.Canceled
			},
		}
		runner := &mavenPackageCommandRunner{base: base, spring: true}

		_, err := runner.Run(
			t.Context(),
			azdexec.NewRunArgs("mvnw", "package", "-DskipTests").WithCwd(projectDir),
		)
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("Maven failure", func(t *testing.T) {
		projectDir := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(projectDir, ".git"), 0o700))
		mavenErr := errors.New("Maven failed")
		base := &commandRunnerFunc{
			run: func(_ context.Context, args azdexec.RunArgs) (azdexec.RunResult, error) {
				if args.Cmd == "git" {
					return azdexec.NewRunResult(0, "# branch.oid (initial)\n", ""), nil
				}
				return azdexec.RunResult{}, mavenErr
			},
		}
		runner := &mavenPackageCommandRunner{base: base, spring: true}

		_, err := runner.Run(
			t.Context(),
			azdexec.NewRunArgs("mvnw", "package", "-DskipTests").WithCwd(projectDir),
		)
		require.ErrorContains(t, err, mavenErr.Error())
	})
}

func TestMavenPackageCommandRunnerRejectsUnexpectedGitInspectionFailures(t *testing.T) {
	tests := []struct {
		name   string
		result azdexec.RunResult
		err    error
	}{
		{
			name: "missing Git executable",
			err:  errors.New("executable file not found"),
		},
		{
			name:   "nonzero status without runner error",
			result: azdexec.NewRunResult(128, "", "fatal: invalid HEAD"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectDir := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(projectDir, ".git"), 0o700))
			var calls []azdexec.RunArgs
			base := &commandRunnerFunc{
				run: func(_ context.Context, args azdexec.RunArgs) (azdexec.RunResult, error) {
					calls = append(calls, args)
					require.Equal(t, "git", args.Cmd)
					return tt.result, tt.err
				},
			}
			runner := &mavenPackageCommandRunner{base: base, spring: true}

			_, err := runner.Run(
				t.Context(),
				azdexec.NewRunArgs("mvnw", "package", "-DskipTests").WithCwd(projectDir),
			)

			require.Error(t, err)
			require.Len(t, calls, 1, "Maven must not run when Git inspection fails")
		})
	}
}

func TestFrameworkCommandErrorsRedactCredentialBearingURLs(t *testing.T) {
	tests := []struct {
		name         string
		sensitiveURL string
		sanitizedURL string
	}{
		{
			name:         "HTTPS",
			sensitiveURL: "https://user:password@example.test/repository?sig=secret#fragment",
			sanitizedURL: "https://example.test/repository",
		},
		{
			name:         "Maven scheme",
			sensitiveURL: "maven://user:password@example.test/repository?sig=secret#fragment",
			sanitizedURL: "maven://example.test/repository",
		},
	}
	assertRedacted := func(t *testing.T, err error, sanitizedURL string) {
		t.Helper()
		require.Error(t, err)
		require.NotContains(t, err.Error(), "user")
		require.NotContains(t, err.Error(), "password")
		require.NotContains(t, err.Error(), "sig=")
		require.NotContains(t, err.Error(), "secret")
		require.NotContains(t, err.Error(), "fragment")
		require.Contains(t, err.Error(), sanitizedURL)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Run("Git inspection", func(t *testing.T) {
				projectDir := t.TempDir()
				require.NoError(t, os.Mkdir(filepath.Join(projectDir, ".git"), 0o700))
				calls := 0
				base := &commandRunnerFunc{
					run: func(_ context.Context, args azdexec.RunArgs) (azdexec.RunResult, error) {
						calls++
						require.Equal(t, "git", args.Cmd)
						return azdexec.RunResult{}, fmt.Errorf("reading remote %s failed", tt.sensitiveURL)
					},
				}
				runner := &mavenPackageCommandRunner{base: base, spring: true}

				_, err := runner.Run(
					t.Context(),
					azdexec.NewRunArgs("mvnw", "package", "-DskipTests").WithCwd(projectDir),
				)

				assertRedacted(t, err, tt.sanitizedURL)
				require.Equal(t, 1, calls, "Maven must not run after Git inspection fails")
			})

			t.Run("Maven command", func(t *testing.T) {
				base := &commandRunnerFunc{
					run: func(_ context.Context, _ azdexec.RunArgs) (azdexec.RunResult, error) {
						return azdexec.RunResult{}, fmt.Errorf("downloading %s failed", tt.sensitiveURL)
					},
				}
				runner := &mavenPackageCommandRunner{base: base}

				_, err := runner.Run(
					t.Context(),
					azdexec.NewRunArgs("mvnw", "package", "-DskipTests").WithCwd(t.TempDir()),
				)

				assertRedacted(t, err, tt.sanitizedURL)
			})
		})
	}
}

func TestMavenPackageCommandRunnerIsConcurrentSafe(t *testing.T) {
	projectDir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(projectDir, ".git"), 0o700))
	var mu sync.Mutex
	decorated := 0
	base := &commandRunnerFunc{
		run: func(_ context.Context, args azdexec.RunArgs) (azdexec.RunResult, error) {
			if args.Cmd == "git" {
				return azdexec.NewRunResult(0, "# branch.oid (initial)\n", ""), nil
			}
			mu.Lock()
			defer mu.Unlock()
			if len(args.Args) == 3 && args.Args[2] == skipGitMetadataMavenOption {
				decorated++
			}
			return azdexec.NewRunResult(0, "", ""), nil
		},
	}
	runner := &mavenPackageCommandRunner{base: base, spring: true}

	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			_, err := runner.Run(
				t.Context(),
				azdexec.NewRunArgs("mvnw", "package", "-DskipTests").WithCwd(projectDir),
			)
			require.NoError(t, err)
		})
	}
	wg.Wait()
	require.Equal(t, 16, decorated)
}

func TestJavaFrameworkDelegatesNativeMavenPackaging(t *testing.T) {
	tests := []struct {
		name                 string
		host                 string
		createPackageOutput  func(*testing.T, string)
		wantGitCheck         bool
		wantMavenOption      bool
		wantArtifactLocation func(string) string
		wantMetadata         map[string]string
	}{
		{
			name: "Spring service with unborn head",
			host: SpringHost,
			createPackageOutput: func(t *testing.T, serviceDir string) {
				require.NoError(t, os.MkdirAll(filepath.Join(serviceDir, "target"), 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(serviceDir, "target", "app.jar"), []byte("jar"), 0o600))
			},
			wantGitCheck:    true,
			wantMavenOption: true,
			wantMetadata:    map[string]string{"framework": "maven"},
		},
		{
			name: "legacy host uses native Maven behavior",
			host: "azure.springboot",
			createPackageOutput: func(t *testing.T, serviceDir string) {
				require.NoError(t, os.MkdirAll(filepath.Join(serviceDir, "target"), 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(serviceDir, "target", "app.jar"), []byte("jar"), 0o600))
			},
			wantMetadata: map[string]string{"framework": "maven"},
		},
		{
			name: "non-Spring Azure Functions service",
			host: string(project.AzureFunctionTarget),
			createPackageOutput: func(t *testing.T, serviceDir string) {
				require.NoError(t, os.MkdirAll(
					filepath.Join(serviceDir, "target", "azure-functions", "function-app"),
					0o700,
				))
			},
			wantArtifactLocation: func(serviceDir string) string {
				return filepath.Join(serviceDir, "target", "azure-functions", "function-app")
			},
			wantMetadata: map[string]string{
				"host":       "azure-function",
				"framework":  "maven",
				"funcAppDir": "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectDir := t.TempDir()
			serviceDir := filepath.Join(projectDir, "service")
			require.NoError(t, os.MkdirAll(serviceDir, 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(serviceDir, "mvnw"), []byte("wrapper"), 0o700))
			if tt.host == SpringHost {
				require.NoError(t, os.Mkdir(filepath.Join(projectDir, ".git"), 0o700))
			}
			tt.createPackageOutput(t, serviceDir)

			var commands []azdexec.RunArgs
			base := &commandRunnerFunc{
				run: func(_ context.Context, args azdexec.RunArgs) (azdexec.RunResult, error) {
					commands = append(commands, args)
					if args.Cmd == "git" {
						return azdexec.NewRunResult(0, "# branch.oid (initial)\n", ""), nil
					}
					return azdexec.NewRunResult(0, "", ""), nil
				},
			}
			runner := &mavenPackageCommandRunner{
				base:   base,
				spring: tt.host == SpringHost,
			}
			service := &project.ServiceConfig{
				Project:      &project.ProjectConfig{Path: projectDir},
				Name:         "service",
				RelativePath: "service",
				Host:         project.ServiceTargetKind(tt.host),
				Language:     project.ServiceLanguageJava,
			}
			framework := project.NewMavenProject(
				environment.New("test"),
				maven.NewCli(runner),
				javac.NewCli(runner),
			)
			require.NoError(t, framework.Initialize(t.Context(), service))

			result, err := framework.Package(
				t.Context(),
				service,
				project.NewServiceContext(),
				async.NewNoopProgress[project.ServiceProgress](),
			)
			require.NoError(t, err)
			require.Len(t, result.Artifacts, 1)
			artifact := result.Artifacts[0]
			if tt.wantArtifactLocation != nil {
				require.Equal(t, tt.wantArtifactLocation(serviceDir), artifact.Location)
			} else {
				t.Cleanup(func() {
					require.NoError(t, os.RemoveAll(artifact.Location))
				})
				require.FileExists(t, filepath.Join(artifact.Location, "app.jar"))
			}
			for key, value := range tt.wantMetadata {
				if key == "funcAppDir" {
					value = filepath.Join(serviceDir, "target", "azure-functions", "function-app")
				}
				require.Equal(t, value, artifact.Metadata[key])
			}

			gitChecks := 0
			mavenOptions := 0
			for _, command := range commands {
				if command.Cmd == "git" {
					gitChecks++
				}
				if len(command.Args) == 3 && command.Args[2] == skipGitMetadataMavenOption {
					mavenOptions++
				}
			}
			require.Equal(t, tt.wantGitCheck, gitChecks == 1)
			require.Equal(t, tt.wantMavenOption, mavenOptions == 1)
		})
	}
}

func TestFrameworkArtifactConversionsPreserveContracts(t *testing.T) {
	original := []*azdext.Artifact{
		{
			Kind:         azdext.ArtifactKind_ARTIFACT_KIND_DIRECTORY,
			Location:     "directory",
			LocationKind: azdext.LocationKind_LOCATION_KIND_LOCAL,
			Metadata:     map[string]string{"kind": "directory"},
		},
		{
			Kind:         azdext.ArtifactKind_ARTIFACT_KIND_ARCHIVE,
			Location:     "archive",
			LocationKind: azdext.LocationKind_LOCATION_KIND_REMOTE,
			Metadata:     map[string]string{"kind": "archive"},
		},
		{
			Kind:         azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER,
			Location:     "container",
			LocationKind: azdext.LocationKind_LOCATION_KIND_REMOTE,
			Metadata:     map[string]string{"kind": "container"},
		},
	}

	converted := extensionArtifacts(nativeArtifacts(original))
	require.Equal(t, original, converted)
	converted[0].Metadata["kind"] = "changed"
	require.Equal(t, "directory", original[0].Metadata["kind"])
}

func TestNativeServiceConfigPreservesMixedJavaHosts(t *testing.T) {
	projectDir := t.TempDir()
	t.Setenv("AZD_EXEC_PROJECT_DIR", projectDir)

	for _, host := range []string{SpringHost, string(project.AzureFunctionTarget), "containerapp"} {
		t.Run(host, func(t *testing.T) {
			service, err := nativeServiceConfig(&azdext.ServiceConfig{
				Name:              "service",
				ResourceGroupName: "${AZURE_RESOURCE_GROUP}",
				ResourceName:      "custom-name",
				ApiVersion:        "2025-01-01",
				RelativePath:      "src",
				Host:              host,
				Language:          "java",
				OutputPath:        "dist",
				Image:             "example/image",
				Uses:              []string{"database"},
				Environment:       map[string]string{"SETTING": "${VALUE}"},
			})
			require.NoError(t, err)
			require.Equal(t, projectDir, service.Project.Path)
			require.Equal(t, project.ServiceTargetKind(host), service.Host)
			require.Equal(t, project.ServiceLanguageJava, service.Language)
			require.Equal(t, filepath.Join(projectDir, "src"), service.Path())
			require.Equal(t, "dist", service.OutputPath)
			require.Equal(t, "${VALUE}", service.Environment["SETTING"].Raw())
		})
	}
}

func TestJavaFrameworkRequirementsMatchNativeMaven(t *testing.T) {
	provider := newJavaFrameworkService(nil, &commandRunnerFunc{
		run: func(context.Context, azdexec.RunArgs) (azdexec.RunResult, error) {
			return azdexec.RunResult{}, nil
		},
	})
	got, err := provider.Requirements()
	require.NoError(t, err)

	native := project.NewMavenProject(
		nil,
		maven.NewCli(provider.runner),
		javac.NewCli(provider.runner),
	).Requirements()
	require.Equal(t, native.Package.RequireRestore, got.GetPackage().GetRequireRestore())
	require.Equal(t, native.Package.RequireBuild, got.GetPackage().GetRequireBuild())
}

func TestJavaFrameworkInitializeAndRequirementsAreConcurrentSafe(t *testing.T) {
	projectDir := t.TempDir()
	t.Setenv("AZD_EXEC_PROJECT_DIR", projectDir)
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "mvnw"), []byte("wrapper"), 0o700))

	javaHome := filepath.Join(projectDir, "jdk")
	require.NoError(t, os.MkdirAll(filepath.Join(javaHome, "bin"), 0o700))
	javacName := "javac"
	if runtime.GOOS == "windows" {
		javacName += ".exe"
	}
	require.NoError(t, os.WriteFile(filepath.Join(javaHome, "bin", javacName), []byte("javac"), 0o700))
	t.Setenv("JAVA_HOME", javaHome)

	runner := &commandRunnerFunc{
		run: func(_ context.Context, args azdexec.RunArgs) (azdexec.RunResult, error) {
			switch {
			case len(args.Args) == 1 && args.Args[0] == "--version" &&
				filepath.Base(args.Cmd) == "mvnw":
				return azdexec.NewRunResult(0, "Apache Maven 3.9.9 (test)\n", ""), nil
			case len(args.Args) == 1 && args.Args[0] == "--version":
				return azdexec.NewRunResult(0, "javac 17.0.10\n", ""), nil
			default:
				return azdexec.RunResult{}, fmt.Errorf("unexpected command: %s %v", args.Cmd, args.Args)
			}
		},
	}
	provider := newJavaFrameworkService(nil, runner)
	service := &azdext.ServiceConfig{
		Name:         "service",
		RelativePath: ".",
		Host:         SpringHost,
		Language:     "java",
	}

	start := make(chan struct{})
	errs := make(chan error, 17)
	var wg sync.WaitGroup
	wg.Go(func() {
		<-start
		errs <- provider.Initialize(t.Context(), service)
	})
	for range 16 {
		wg.Go(func() {
			<-start
			_, err := provider.Requirements()
			errs <- err
		})
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
}
