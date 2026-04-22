// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dotnet

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockexec"
	"github.com/stretchr/testify/require"
)

// newCliWithMock returns a Cli backed by a mock command runner.
func newCliWithMock(t *testing.T) (*Cli, *mockexec.MockCommandRunner) {
	t.Helper()
	mockCtx := mocks.NewMockContext(t.Context())
	return NewCli(mockCtx.CommandRunner), mockCtx.CommandRunner
}

// matchDotnetArg0 returns a predicate matching `dotnet <arg0>` invocations.
func matchDotnetArg0(arg0 string) mockexec.CommandWhenPredicate {
	return func(args exec.RunArgs, command string) bool {
		return args.Cmd == "dotnet" && len(args.Args) > 0 && args.Args[0] == arg0
	}
}

func Test_Cli_NameAndInstallUrl(t *testing.T) {
	t.Parallel()
	cli, _ := newCliWithMock(t)
	require.Equal(t, ".NET CLI", cli.Name())
	require.Equal(t, "https://dotnet.microsoft.com/download", cli.InstallUrl())
}

func Test_newDotNetRunArgs_SetsBaseEnv(t *testing.T) {
	t.Parallel()
	args := newDotNetRunArgs("build", "foo.csproj")
	require.Equal(t, "dotnet", args.Cmd)
	require.Equal(t, []string{"build", "foo.csproj"}, args.Args)
	require.Contains(t, args.Env, "DOTNET_CLI_WORKLOAD_UPDATE_NOTIFY_DISABLE=1")
	require.Contains(t, args.Env, "DOTNET_NOLOGO=1")
}

func Test_Cli_CheckInstalled(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		runner.MockToolInPath("dotnet", nil)
		runner.When(matchDotnetArg0("--version")).Respond(exec.NewRunResult(0, "8.0.100\n", ""))

		require.NoError(t, cli.CheckInstalled(t.Context()))
	})

	t.Run("tool not in path", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		wantErr := errors.New("dotnet not found")
		runner.MockToolInPath("dotnet", wantErr)

		err := cli.CheckInstalled(t.Context())
		require.ErrorIs(t, err, wantErr)
	})

	t.Run("version command fails", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		runner.MockToolInPath("dotnet", nil)
		runner.When(matchDotnetArg0("--version")).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				return exec.RunResult{}, errors.New("boom")
			})

		err := cli.CheckInstalled(t.Context())
		require.Error(t, err)
		require.Contains(t, err.Error(), "checking .NET CLI version")
	})

	t.Run("version parse error", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		runner.MockToolInPath("dotnet", nil)
		runner.When(matchDotnetArg0("--version")).Respond(exec.NewRunResult(0, "not-a-version", ""))

		err := cli.CheckInstalled(t.Context())
		require.Error(t, err)
	})

	t.Run("version below minimum", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		runner.MockToolInPath("dotnet", nil)
		runner.When(matchDotnetArg0("--version")).Respond(exec.NewRunResult(0, "5.0.0", ""))

		err := cli.CheckInstalled(t.Context())
		require.Error(t, err)
	})
}

func Test_Cli_SdkVersion(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		runner.When(matchDotnetArg0("--version")).Respond(exec.NewRunResult(0, "8.0.203", ""))

		ver, err := cli.SdkVersion(t.Context())
		require.NoError(t, err)
		require.Equal(t, uint64(8), ver.Major)
		require.Equal(t, uint64(0), ver.Minor)
		require.Equal(t, uint64(203), ver.Patch)
	})

	t.Run("run error", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		runner.When(matchDotnetArg0("--version")).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				return exec.RunResult{}, errors.New("fail")
			})

		_, err := cli.SdkVersion(t.Context())
		require.Error(t, err)
	})

	t.Run("parse error", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		runner.When(matchDotnetArg0("--version")).Respond(exec.NewRunResult(0, "bogus", ""))

		_, err := cli.SdkVersion(t.Context())
		require.Error(t, err)
	})
}

func Test_Cli_Restore(t *testing.T) {
	t.Parallel()

	t.Run("success without env", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		var captured exec.RunArgs
		runner.When(matchDotnetArg0("restore")).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				captured = args
				return exec.NewRunResult(0, "", ""), nil
			})

		require.NoError(t, cli.Restore(t.Context(), "my.csproj", nil))
		require.Equal(t, []string{"restore", "my.csproj"}, captured.Args)
		require.Contains(t, captured.Env, "DOTNET_NOLOGO=1")
	})

	t.Run("success with env preserves base", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		var captured exec.RunArgs
		runner.When(matchDotnetArg0("restore")).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				captured = args
				return exec.NewRunResult(0, "", ""), nil
			})

		require.NoError(t, cli.Restore(t.Context(), "my.csproj", []string{"FOO=BAR"}))
		require.Contains(t, captured.Env, "FOO=BAR")
		require.Contains(t, captured.Env, "DOTNET_NOLOGO=1")
	})

	t.Run("error", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		runner.When(matchDotnetArg0("restore")).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				return exec.RunResult{}, errors.New("boom")
			})

		err := cli.Restore(t.Context(), "my.csproj", nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "dotnet restore on project 'my.csproj' failed")
	})
}

func Test_Cli_Build(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		project       string
		configuration string
		output        string
		env           []string
		expectArgs    []string
	}{
		{
			name:       "basic",
			project:    "p.csproj",
			expectArgs: []string{"build", "p.csproj"},
		},
		{
			name:          "with configuration",
			project:       "p.csproj",
			configuration: "Release",
			expectArgs:    []string{"build", "p.csproj", "-c", "Release"},
		},
		{
			name:       "with output",
			project:    "p.csproj",
			output:     "./bin",
			expectArgs: []string{"build", "p.csproj", "--output", "./bin"},
		},
		{
			name:          "with all",
			project:       "p.csproj",
			configuration: "Debug",
			output:        "./out",
			env:           []string{"X=1"},
			expectArgs:    []string{"build", "p.csproj", "-c", "Debug", "--output", "./out"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cli, runner := newCliWithMock(t)
			var captured exec.RunArgs
			runner.When(matchDotnetArg0("build")).
				RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
					captured = args
					return exec.NewRunResult(0, "", ""), nil
				})

			require.NoError(t, cli.Build(t.Context(), tc.project, tc.configuration, tc.output, tc.env))
			require.Equal(t, tc.expectArgs, captured.Args)
			if len(tc.env) > 0 {
				for _, e := range tc.env {
					require.Contains(t, captured.Env, e)
				}
				require.Contains(t, captured.Env, "DOTNET_NOLOGO=1")
			}
		})
	}

	t.Run("error", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		runner.When(matchDotnetArg0("build")).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				return exec.RunResult{}, errors.New("oops")
			})

		err := cli.Build(t.Context(), "p.csproj", "", "", nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "dotnet build on project 'p.csproj' failed")
	})
}

func Test_Cli_Publish(t *testing.T) {
	t.Parallel()

	t.Run("with all params", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		var captured exec.RunArgs
		runner.When(matchDotnetArg0("publish")).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				captured = args
				return exec.NewRunResult(0, "", ""), nil
			})

		require.NoError(t, cli.Publish(t.Context(), "p.csproj", "Release", "./pub", []string{"K=V"}))
		require.Equal(t, []string{"publish", "p.csproj", "-c", "Release", "--output", "./pub"}, captured.Args)
		require.Contains(t, captured.Env, "K=V")
	})

	t.Run("minimal", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		var captured exec.RunArgs
		runner.When(matchDotnetArg0("publish")).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				captured = args
				return exec.NewRunResult(0, "", ""), nil
			})

		require.NoError(t, cli.Publish(t.Context(), "p.csproj", "", "", nil))
		require.Equal(t, []string{"publish", "p.csproj"}, captured.Args)
	})

	t.Run("error", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		runner.When(matchDotnetArg0("publish")).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				return exec.RunResult{}, errors.New("x")
			})

		err := cli.Publish(t.Context(), "p.csproj", "", "", nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "dotnet publish on project 'p.csproj' failed")
	})
}

func Test_Cli_PublishAppHostManifest(t *testing.T) {
	t.Run("project based", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		hostProject := filepath.Join("some", "dir", "AppHost.csproj")
		var captured exec.RunArgs
		runner.When(matchDotnetArg0("run")).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				captured = args
				return exec.NewRunResult(0, "", ""), nil
			})

		require.NoError(t, cli.PublishAppHostManifest(t.Context(), hostProject, "out.json", "Development"))
		require.Equal(t, filepath.Dir(hostProject), captured.Cwd)
		require.Contains(t, captured.Args, "--project")
		require.Contains(t, captured.Args, filepath.Base(hostProject))
		require.Contains(t, captured.Args, "--publisher")
		require.Contains(t, captured.Args, "manifest")
		require.Contains(t, captured.Args, "--output-path")
		require.Contains(t, captured.Args, "out.json")
		require.Contains(t, captured.Env, "DOTNET_ENVIRONMENT=Development")
	})

	t.Run("single file apphost", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		hostProject := filepath.Join("some", "dir", "apphost.cs")
		var captured exec.RunArgs
		runner.When(matchDotnetArg0("run")).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				captured = args
				return exec.NewRunResult(0, "", ""), nil
			})

		require.NoError(t, cli.PublishAppHostManifest(t.Context(), hostProject, "out.json", ""))
		require.Equal(t, filepath.Dir(hostProject), captured.Cwd)
		require.Equal(t, "apphost.cs", captured.Args[1])
		require.NotContains(t, captured.Args, "--project")
	})

	t.Run("run error", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		runner.When(matchDotnetArg0("run")).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				return exec.RunResult{}, errors.New("boom")
			})

		err := cli.PublishAppHostManifest(t.Context(), filepath.Join("d", "AppHost.csproj"), "out.json", "")
		require.Error(t, err)
		require.Contains(t, err.Error(), "dotnet run --publisher manifest")
	})

	t.Run("fixed manifest debug", func(t *testing.T) {
		cli, _ := newCliWithMock(t)
		dir := t.TempDir()
		hostProject := filepath.Join(dir, "AppHost.csproj")
		manifestSrc := filepath.Join(dir, "apphost-manifest.json")
		require.NoError(t, os.WriteFile(manifestSrc, []byte(`{"resources":{}}`), osutil.PermissionFile))

		outPath := filepath.Join(dir, "out.json")
		t.Setenv("AZD_DEBUG_DOTNET_APPHOST_USE_FIXED_MANIFEST", "true")

		require.NoError(t, cli.PublishAppHostManifest(t.Context(), hostProject, outPath, ""))
		data, err := os.ReadFile(outPath)
		require.NoError(t, err)
		require.Equal(t, `{"resources":{}}`, string(data))
	})

	t.Run("fixed manifest missing file", func(t *testing.T) {
		cli, _ := newCliWithMock(t)
		dir := t.TempDir()
		hostProject := filepath.Join(dir, "AppHost.csproj")

		t.Setenv("AZD_DEBUG_DOTNET_APPHOST_USE_FIXED_MANIFEST", "1")

		err := cli.PublishAppHostManifest(t.Context(), hostProject, filepath.Join(dir, "out.json"), "")
		require.Error(t, err)
		require.Contains(t, err.Error(), "apphost-manifest.json")
	})
}

const successContainerOutput = `{"config":{"ExposedPorts":{"8080/tcp":{}}}}`

func Test_Cli_BuildContainerLocal(t *testing.T) {
	t.Parallel()

	t.Run("project-based default tag", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		var captured exec.RunArgs
		runner.When(matchDotnetArg0("publish")).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				captured = args
				return exec.NewRunResult(0, successContainerOutput, ""), nil
			})

		port, image, err := cli.BuildContainerLocal(t.Context(), "p.csproj", "Release", "myrepo")
		require.NoError(t, err)
		require.Equal(t, 8080, port)
		require.Equal(t, "myrepo:latest", image)
		require.Equal(t, "", captured.Cwd)
		joined := strings.Join(captured.Args, " ")
		require.Contains(t, joined, "/t:PublishContainer")
		require.Contains(t, joined, "-p:ContainerRepository=myrepo")
		require.Contains(t, joined, "-p:ContainerImageTag=latest")
		require.Contains(t, joined, "-p:PublishProfile=DefaultContainer")
	})

	t.Run("single-file project", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		var captured exec.RunArgs
		runner.When(matchDotnetArg0("publish")).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				captured = args
				return exec.NewRunResult(0, successContainerOutput, ""), nil
			})

		proj := filepath.Join("some", "dir", "apphost.cs")
		_, _, err := cli.BuildContainerLocal(t.Context(), proj, "Release", "img:v1")
		require.NoError(t, err)
		require.Equal(t, filepath.Dir(proj), captured.Cwd)
		require.Equal(t, "apphost.cs", captured.Args[1])
		require.Contains(t, strings.Join(captured.Args, " "), "-p:ContainerImageTag=v1")
	})

	t.Run("publish error", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		runner.When(matchDotnetArg0("publish")).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				return exec.RunResult{}, errors.New("nope")
			})

		_, _, err := cli.BuildContainerLocal(t.Context(), "p.csproj", "Release", "img")
		require.Error(t, err)
		require.Contains(t, err.Error(), "dotnet publish on project 'p.csproj' failed")
	})

	t.Run("bad output port", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		runner.When(matchDotnetArg0("publish")).
			Respond(exec.NewRunResult(0, "", ""))

		_, _, err := cli.BuildContainerLocal(t.Context(), "p.csproj", "Release", "img")
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to get dotnet target port")
	})
}

func Test_Cli_PublishContainer(t *testing.T) {
	t.Parallel()

	t.Run("success project-based", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		var captured exec.RunArgs
		runner.When(matchDotnetArg0("publish")).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				captured = args
				return exec.NewRunResult(0, successContainerOutput, ""), nil
			})

		port, err := cli.PublishContainer(
			t.Context(), "p.csproj", "Release", "repo", "registry.example.com", "user", "secret",
		)
		require.NoError(t, err)
		require.Equal(t, 8080, port)
		joined := strings.Join(captured.Args, " ")
		require.Contains(t, joined, "-p:ContainerRegistry=registry.example.com")
		require.Contains(t, captured.Env, "DOTNET_CONTAINER_REGISTRY_UNAME=user")
		require.Contains(t, captured.Env, "DOTNET_CONTAINER_REGISTRY_PWORD=secret")
		require.Contains(t, captured.Env, "SDK_CONTAINER_REGISTRY_UNAME=user")
		require.Contains(t, captured.Env, "SDK_CONTAINER_REGISTRY_PWORD=secret")
	})

	t.Run("single-file default tag", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		var captured exec.RunArgs
		runner.When(matchDotnetArg0("publish")).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				captured = args
				return exec.NewRunResult(0, successContainerOutput, ""), nil
			})

		proj := filepath.Join("d", "apphost.cs")
		_, err := cli.PublishContainer(t.Context(), proj, "Release", "img", "r", "u", "p")
		require.NoError(t, err)
		require.Equal(t, filepath.Dir(proj), captured.Cwd)
		require.Equal(t, "apphost.cs", captured.Args[1])
		require.Contains(t, strings.Join(captured.Args, " "), "-p:ContainerImageTag=latest")
	})

	t.Run("publish error", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		runner.When(matchDotnetArg0("publish")).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				return exec.RunResult{}, errors.New("boom")
			})

		_, err := cli.PublishContainer(t.Context(), "p.csproj", "Release", "img", "r", "u", "p")
		require.Error(t, err)
		require.Contains(t, err.Error(), "dotnet publish on project 'p.csproj' failed")
	})

	t.Run("empty output port error", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		runner.When(matchDotnetArg0("publish")).
			Respond(exec.NewRunResult(0, "", ""))

		_, err := cli.PublishContainer(t.Context(), "p.csproj", "Release", "img", "r", "u", "p")
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to get dotnet target port")
	})
}

func Test_Cli_getTargetPort_Branches(t *testing.T) {
	t.Parallel()

	cli := &Cli{}

	t.Run("port with protocol", func(t *testing.T) {
		t.Parallel()
		port, err := cli.getTargetPort(`{"config":{"ExposedPorts":{"9090/tcp":{}}}}`, "p")
		require.NoError(t, err)
		require.Equal(t, 9090, port)
	})

	t.Run("port without protocol", func(t *testing.T) {
		t.Parallel()
		port, err := cli.getTargetPort(`{"config":{"ExposedPorts":{"1234":{}}}}`, "p")
		require.NoError(t, err)
		require.Equal(t, 1234, port)
	})

	t.Run("invalid json", func(t *testing.T) {
		t.Parallel()
		_, err := cli.getTargetPort(`{"config":"not-an-object"}`, "p")
		require.Error(t, err)
	})

	t.Run("non-integer port", func(t *testing.T) {
		t.Parallel()
		_, err := cli.getTargetPort(`{"config":{"ExposedPorts":{"abc/tcp":{}}}}`, "p")
		require.Error(t, err)
		require.Contains(t, err.Error(), "convert port")
	})
}

func Test_Cli_InitializeSecret(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		var captured exec.RunArgs
		runner.When(matchDotnetArg0("user-secrets")).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				captured = args
				return exec.NewRunResult(0, "", ""), nil
			})

		require.NoError(t, cli.InitializeSecret(t.Context(), "p.csproj"))
		require.Equal(t, []string{"user-secrets", "init", "--project", "p.csproj"}, captured.Args)
	})

	t.Run("error", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		runner.When(matchDotnetArg0("user-secrets")).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				return exec.RunResult{}, errors.New("oops")
			})

		err := cli.InitializeSecret(t.Context(), "p.csproj")
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to initialize secrets")
	})
}

func Test_Cli_SetSecrets(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		var captured exec.RunArgs
		runner.When(matchDotnetArg0("user-secrets")).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				captured = args
				return exec.NewRunResult(0, "", ""), nil
			})

		require.NoError(t, cli.SetSecrets(t.Context(), map[string]string{"A": "1"}, "p.csproj"))
		require.Equal(t, []string{"user-secrets", "set", "--project", "p.csproj"}, captured.Args)
		require.NotNil(t, captured.StdIn)
		data, err := io.ReadAll(captured.StdIn)
		require.NoError(t, err)
		require.JSONEq(t, `{"A":"1"}`, string(data))
	})

	t.Run("run error", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		runner.When(matchDotnetArg0("user-secrets")).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				return exec.RunResult{}, errors.New("no")
			})

		err := cli.SetSecrets(t.Context(), map[string]string{}, "p.csproj")
		require.Error(t, err)
		require.Contains(t, err.Error(), "secret set")
	})
}

func Test_Cli_GetMsBuildProperty(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		var captured exec.RunArgs
		runner.When(matchDotnetArg0("msbuild")).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				captured = args
				return exec.NewRunResult(0, "MyVal\n", ""), nil
			})

		val, err := cli.GetMsBuildProperty(t.Context(), "p.csproj", "AssemblyName")
		require.NoError(t, err)
		require.Equal(t, "MyVal\n", val)
		require.Contains(t, captured.Args, "--ignore:.sln")
		require.Contains(t, captured.Args, "--getProperty:AssemblyName")
	})

	t.Run("error", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		runner.When(matchDotnetArg0("msbuild")).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				return exec.RunResult{}, errors.New("boom")
			})

		_, err := cli.GetMsBuildProperty(t.Context(), "p.csproj", "X")
		require.Error(t, err)
	})
}

func Test_Cli_IsAspireHostProject_ProjectBased(t *testing.T) {
	t.Parallel()

	t.Run("run error", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		runner.When(matchDotnetArg0("msbuild")).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				return exec.RunResult{}, errors.New("boom")
			})

		_, err := cli.IsAspireHostProject(t.Context(), "p.csproj")
		require.Error(t, err)
		require.Contains(t, err.Error(), "running dotnet msbuild")
	})

	t.Run("invalid json", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		runner.When(matchDotnetArg0("msbuild")).
			Respond(exec.NewRunResult(0, "not-json", ""))

		_, err := cli.IsAspireHostProject(t.Context(), "p.csproj")
		require.Error(t, err)
		require.Contains(t, err.Error(), "unmarshal")
	})
}

func Test_Cli_GitIgnore(t *testing.T) {
	t.Parallel()

	t.Run("skip when exists default", func(t *testing.T) {
		t.Parallel()
		cli, _ := newCliWithMock(t)
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("bin/"), osutil.PermissionFile))

		// Should not invoke command runner because file exists and strategy is Skip.
		require.NoError(t, cli.GitIgnore(t.Context(), dir, nil))
	})

	t.Run("creates when missing", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		dir := t.TempDir()

		var captured exec.RunArgs
		runner.When(matchDotnetArg0("new")).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				captured = args
				return exec.NewRunResult(0, "", ""), nil
			})

		require.NoError(t, cli.GitIgnore(t.Context(), dir, nil))
		require.Contains(t, captured.Args, "gitignore")
		require.Contains(t, captured.Args, "--project")
		require.Contains(t, captured.Args, dir)
		require.False(t, slices.Contains(captured.Args, "--force"))
	})

	t.Run("overwrite adds --force", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("x"), osutil.PermissionFile))

		var captured exec.RunArgs
		runner.When(matchDotnetArg0("new")).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				captured = args
				return exec.NewRunResult(0, "", ""), nil
			})

		err := cli.GitIgnore(t.Context(), dir, &GitIgnoreOptions{
			IfExistsStrategy: GitIgnoreIfExistsStrategyOverwrite,
		})
		require.NoError(t, err)
		require.Contains(t, captured.Args, "--force")
	})

	t.Run("invalid strategy", func(t *testing.T) {
		t.Parallel()
		cli, _ := newCliWithMock(t)
		err := cli.GitIgnore(t.Context(), t.TempDir(), &GitIgnoreOptions{
			IfExistsStrategy: GitIgnoreIfExistsStrategy("bogus"),
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid IfExistsStrategy")
	})

	t.Run("run error propagates", func(t *testing.T) {
		t.Parallel()
		cli, runner := newCliWithMock(t)
		dir := t.TempDir()
		wantErr := fmt.Errorf("no")
		runner.When(matchDotnetArg0("new")).
			RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				return exec.RunResult{}, wantErr
			})

		err := cli.GitIgnore(t.Context(), dir, nil)
		require.ErrorIs(t, err, wantErr)
	})
}
