// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package deploy

import (
	"azurespringboot/internal/packaging"
	"azurespringboot/internal/testutil"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/benbjohnson/clock"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	azdexec "github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"google.golang.org/protobuf/proto"
)

type nativePackagingServer struct {
	azdext.UnimplementedContainerServiceServer
	helper  *project.ContainerHelper
	service *project.ServiceConfig
	calls   int
}

type commandRunnerFunc struct {
	run func(context.Context, azdexec.RunArgs) (azdexec.RunResult, error)
}

func (r *commandRunnerFunc) Run(ctx context.Context, args azdexec.RunArgs) (azdexec.RunResult, error) {
	return r.run(ctx, args)
}

func (r *commandRunnerFunc) RunList(
	context.Context, []string, azdexec.RunArgs,
) (azdexec.RunResult, error) {
	return azdexec.RunResult{}, nil
}

func (r *commandRunnerFunc) ToolInPath(string) error { return nil }

func (s *nativePackagingServer) Package(
	ctx context.Context, request *azdext.ContainerPackageRequest,
) (*azdext.ContainerPackageResponse, error) {
	s.calls++
	packaged, err := packaging.RunWithProgress(ctx, nil, func(progress *async.Progress[project.ServiceProgress]) (
		*project.ServicePackageResult, error,
	) {
		return s.helper.Package(ctx, s.service, packaging.NativeServiceContext(request.GetServiceContext()),
			environment.New("dev"), progress)
	})
	if err != nil {
		return nil, err
	}
	return &azdext.ContainerPackageResponse{Result: &azdext.ServicePackageResult{
		Artifacts: packaging.ExtensionArtifacts(packaged.Artifacts),
	}}, nil
}

func TestNativeRuntimeImageUsesIsolatedContextAndCorePackageOnce(t *testing.T) {
	for _, mode := range []string{"h2", "database-free-maven"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("AZD_CONTAINER_RUNTIME", "docker")
			config, service := testutil.CreateMavenProject(t)
			t.Setenv("AZD_EXEC_PROJECT_DIR", config.Path)
			require.NoError(t, os.WriteFile(filepath.Join(config.Path, "Dockerfile"), []byte("FROM authored"), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(config.Path, ".dockerignore"), []byte("*"), 0o600))
			imageID := "sha256:" + strings.Repeat("a", 64)
			entries := [][2]string{
				{"BOOT-INF/classes/application.properties", "spring.datasource.url=jdbc:h2:mem:fixture"},
				{"BOOT-INF/lib/h2-1.jar", ""},
			}
			if mode != "h2" {
				entries = [][2]string{
					{"BOOT-INF/classes/application.properties", "server.port=8080"},
					{"BOOT-INF/lib/spring-boot-starter-web-3.jar", ""},
				}
			}
			jar, err := os.ReadFile(testutil.SpringJarFixture(t, entries))
			require.NoError(t, err)
			builds, tags := 0, 0
			var contextPath string
			runner := &commandRunnerFunc{run: func(_ context.Context, args azdexec.RunArgs) (azdexec.RunResult, error) {
				require.Equal(t, "docker", args.Cmd)
				switch args.Args[0] {
				case "--version":
					return azdexec.RunResult{Stdout: "Docker version 29.0.0, build test"}, nil
				case "ps":
				case "build":
					builds++
					contextPath = filepath.Dir(args.Args[2])
					require.NotEqual(t, config.Path, contextPath)
					require.Equal(t, []string{
						"build", "-f", filepath.Join(contextPath, "Dockerfile"), "--platform", "linux/amd64", contextPath,
					}, args.Args[:6])
					require.Equal(t, contextPath, args.Cwd)
					// #nosec G304 -- contextPath is the isolated temporary directory passed to docker packaging.
					content, err := os.ReadFile(filepath.Join(contextPath, "Dockerfile"))
					require.NoError(t, err)
					require.Equal(t, packaging.SpringDockerfile, string(content))
					// #nosec G304 -- packaging.StagedJarName is a fixed filename inside contextPath.
					content, err = os.ReadFile(filepath.Join(contextPath, packaging.StagedJarName))
					require.NoError(t, err)
					require.Equal(t, jar, content)
					require.NoError(t, os.WriteFile(args.Args[len(args.Args)-1], []byte(imageID), 0o600))
				case "tag":
					tags++
					require.Equal(t, imageID, args.Args[1])
				default:
					t.Fatalf("unexpected container command %v", args.Args)
				}
				return azdexec.RunResult{}, nil
			}}
			native, err := project.Parse(t.Context(), `name: petclinic
services:
  petclinic:
    project: .
    language: java
    host: springbootapp
`)
			require.NoError(t, err)
			native.Path = config.Path
			console := input.NewConsole(true, false,
				input.Writers{Output: io.Discard, Spinner: io.Discard},
				input.ConsoleHandles{Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard}, nil, nil)
			server := &nativePackagingServer{
				helper: project.NewContainerHelper(clock.NewMock(), nil, nil, runner, docker.NewCli(runner),
					nil, console, cloud.AzurePublic()), service: native.Services["petclinic"],
			}
			target := newServiceTarget(packagingTestClient(t, config, server), nil, nil, nil, nil)
			target.images = packaging.NewRuntimeImageBuilder(runner)
			serviceContext := testutil.MavenPackageContext(t, jar)
			result, err := target.Package(t.Context(), service, serviceContext, func(string) {})
			require.NoError(t, err)
			require.Equal(t, 1, builds)
			require.Equal(t, 1, tags)
			require.Equal(t, 1, server.calls)
			require.Len(t, result.Artifacts, 1)
			require.Equal(t, imageID, result.Artifacts[0].Metadata["imageHash"])
			require.Len(t, serviceContext.Build, 1)
			require.NoDirExists(t, contextPath)
			for name, expected := range map[string]string{"Dockerfile": "FROM authored", ".dockerignore": "*"} {
				// #nosec G304 -- fixed test filenames inside t.TempDir().
				actual, err := os.ReadFile(filepath.Join(config.Path, name))
				require.NoError(t, err)
				require.Equal(t, expected, string(actual))
			}
		})
	}
}

type failedRuntimeBuilder struct {
	directory string
}

func (b *failedRuntimeBuilder) Build(_ context.Context, context *packaging.StagedContainerContext) (*azdext.Artifact, error) {
	b.directory = context.Directory()
	return nil, errors.New("failed to retrieve https://registry-user:registry-password@" +
		"registry.example/image?sig=token#fragment")
}

func TestNativeRuntimeBuildFailurePreservesContextAndDoesNotDisclose(t *testing.T) {
	config, service := testutil.CreateMavenProject(t)
	target := newServiceTarget(packagingTestClient(t, config), nil, nil, nil, nil)
	builder := new(failedRuntimeBuilder)
	target.images = builder
	jar, err := os.ReadFile(testutil.SpringJarFixture(t, [][2]string{
		{"BOOT-INF/classes/application.properties", "spring.datasource.url=jdbc:h2:mem:fixture"},
		{"BOOT-INF/lib/h2-1.jar", ""},
	}))
	require.NoError(t, err)
	serviceContext := testutil.MavenPackageContext(t, jar)
	before := proto.Clone(serviceContext)
	_, err = target.Package(t.Context(), service, serviceContext, func(string) {})
	require.Error(t, err)
	for _, secret := range []string{"registry-user", "registry-password", "sig=token", "#fragment"} {
		require.NotContains(t, err.Error(), secret)
	}
	require.Contains(t, err.Error(), "https://registry.example/image")
	require.NoDirExists(t, builder.directory)
	require.True(t, proto.Equal(before, serviceContext))
	serviceContext.Build = coreDockerPackageContext().Build
	_, err = target.Package(t.Context(), service, serviceContext, func(string) {})
	require.ErrorContains(t, err, "cannot reuse")
}
