// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockexec"
)

func TestBuildGateRoundTrip(t *testing.T) {
	mu := &sync.Mutex{}
	ctx := ContextWithBuildGate(context.Background(), mu)
	got := BuildGateFromContext(ctx)
	require.Same(t, mu, got)
}

func TestBuildGateFromContext_NilWhenAbsent(t *testing.T) {
	require.Nil(t, BuildGateFromContext(context.Background()))
}

func TestSanitizeTempDirName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"alphanumeric passthrough", "webfrontend", "webfrontend"},
		{"hyphens preserved", "my-api-service", "my-api-service"},
		{"underscores preserved", "my_api", "my_api"},
		{"dots replaced", "my.service", "my_service"},
		{"slashes replaced", "path/to/svc", "path_to_svc"},
		{"spaces replaced", "my service", "my_service"},
		{"mixed unsafe chars", "svc@1.0/beta", "svc_1_0_beta"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, sanitizeTempDirName(tt.in))
		})
	}
}

func TestRunIsolatedDotNetBuild_NoGate(t *testing.T) {
	ctx := t.Context()
	called := false

	err := runIsolatedDotNetBuild(ctx, "api", nil, func(buildCtx context.Context) error {
		called = true
		require.Equal(t, ctx, buildCtx)
		require.Empty(t, dotnet.ArtifactsPathFromContext(buildCtx))
		return nil
	})

	require.NoError(t, err)
	require.True(t, called)
}

func TestRunIsolatedDotNetBuild_ArtifactsPathsAllowConcurrency(t *testing.T) {
	cli := newDotNetCliForIsolationTest(t, "8.0.100", nil)
	ctx := ContextWithBuildGate(t.Context(), &sync.Mutex{})
	entered := make(chan string, 2)
	release := make(chan struct{})
	errs := make(chan error, 2)

	for _, serviceName := range []string{"api", "web"} {
		go func() {
			errs <- runIsolatedDotNetBuild(ctx, serviceName, cli, func(buildCtx context.Context) error {
				artifactsPath := dotnet.ArtifactsPathFromContext(buildCtx)
				if artifactsPath == "" {
					return errors.New("artifacts path was not set")
				}
				if _, err := os.Stat(artifactsPath); err != nil {
					return err
				}
				entered <- artifactsPath
				<-release
				return nil
			})
		}()
	}

	paths := make([]string, 0, 2)
	for range 2 {
		select {
		case path := <-entered:
			paths = append(paths, path)
		case <-time.After(time.Second):
			close(release)
			t.Fatal("timed out waiting for concurrent .NET builds")
		}
	}
	close(release)

	for range 2 {
		require.NoError(t, <-errs)
	}
	require.NotEqual(t, paths[0], paths[1])
	for _, path := range paths {
		_, err := os.Stat(path)
		require.ErrorIs(t, err, os.ErrNotExist)
	}
}

func TestRunIsolatedDotNetBuild_OlderSdkSerializes(t *testing.T) {
	cli := newDotNetCliForIsolationTest(t, "7.0.410", nil)
	ctx := ContextWithBuildGate(t.Context(), &sync.Mutex{})

	assertDotNetBuildsSerialized(t, func(serviceName string, build func(context.Context) error) error {
		return runIsolatedDotNetBuild(ctx, serviceName, cli, build)
	})
}

func TestRunIsolatedDotNetBuild_ProbeFailureSerializes(t *testing.T) {
	probeErr := errors.New("SDK probe failed")
	cli := newDotNetCliForIsolationTest(t, "", probeErr)
	ctx := ContextWithBuildGate(t.Context(), &sync.Mutex{})

	assertDotNetBuildsSerialized(t, func(serviceName string, build func(context.Context) error) error {
		return runIsolatedDotNetBuild(ctx, serviceName, cli, build)
	})
}

func TestRunIsolatedDotNetBuild_TempDirFailureSerializes(t *testing.T) {
	cli := newDotNetCliForIsolationTest(t, "8.0.100", nil)
	ctx := ContextWithBuildGate(t.Context(), &sync.Mutex{})
	mkdirErr := errors.New("temp directory unavailable")

	assertDotNetBuildsSerialized(t, func(serviceName string, build func(context.Context) error) error {
		return runIsolatedDotNetBuildWithTempDir(
			ctx,
			serviceName,
			cli,
			func(string, string) (string, error) {
				return "", mkdirErr
			},
			build,
		)
	})
}

func TestRunIsolatedDotNetBuild_CleansArtifactsAfterBuild(t *testing.T) {
	cli := newDotNetCliForIsolationTest(t, "8.0.100", nil)
	buildErr := errors.New("publish failed")

	tests := []struct {
		name      string
		returnErr error
		cancel    bool
	}{
		{name: "success"},
		{name: "failure with canceled context", returnErr: buildErr, cancel: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(ContextWithBuildGate(t.Context(), &sync.Mutex{}))
			defer cancel()

			var artifactsPath string
			err := runIsolatedDotNetBuild(ctx, "api", cli, func(buildCtx context.Context) error {
				artifactsPath = dotnet.ArtifactsPathFromContext(buildCtx)
				require.NotEmpty(t, artifactsPath)
				require.NoError(t, os.WriteFile(
					artifactsPath+string(os.PathSeparator)+"output.txt",
					[]byte("output"),
					0600,
				))
				if tt.cancel {
					cancel()
				}
				return tt.returnErr
			})

			if tt.returnErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tt.returnErr)
			}
			_, statErr := os.Stat(artifactsPath)
			require.ErrorIs(t, statErr, os.ErrNotExist)
		})
	}
}

func TestRunIsolatedDotNetBuild_ErrorReleasesGate(t *testing.T) {
	cli := newDotNetCliForIsolationTest(t, "7.0.410", nil)
	ctx := ContextWithBuildGate(t.Context(), &sync.Mutex{})
	buildErr := errors.New("publish failed")

	err := runIsolatedDotNetBuild(ctx, "api", cli, func(context.Context) error {
		return buildErr
	})
	require.ErrorIs(t, err, buildErr)

	completed := make(chan error, 1)
	go func() {
		completed <- runIsolatedDotNetBuild(ctx, "web", cli, func(context.Context) error {
			return nil
		})
	}()

	select {
	case err := <-completed:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("build gate remained locked after a failed build")
	}
}

func newDotNetCliForIsolationTest(t *testing.T, version string, probeErr error) *dotnet.Cli {
	t.Helper()

	runner := mockexec.NewMockCommandRunner()
	runner.When(func(args exec.RunArgs, _ string) bool {
		return args.Cmd == "dotnet" && len(args.Args) == 1 && args.Args[0] == "--version"
	}).RespondFn(func(exec.RunArgs) (exec.RunResult, error) {
		if probeErr != nil {
			return exec.RunResult{}, probeErr
		}
		return exec.NewRunResult(0, version, ""), nil
	})

	return dotnet.NewCli(runner)
}

func assertDotNetBuildsSerialized(
	t *testing.T,
	run func(string, func(context.Context) error) error,
) {
	t.Helper()

	firstEntered := make(chan string)
	releaseFirst := make(chan struct{})
	secondEntered := make(chan string)
	errs := make(chan error, 2)

	go func() {
		errs <- run("api", func(buildCtx context.Context) error {
			firstEntered <- dotnet.ArtifactsPathFromContext(buildCtx)
			<-releaseFirst
			return nil
		})
	}()
	firstArtifactsPath := <-firstEntered

	go func() {
		errs <- run("web", func(buildCtx context.Context) error {
			secondEntered <- dotnet.ArtifactsPathFromContext(buildCtx)
			return nil
		})
	}()

	serialized := false
	secondArtifactsPath := ""
	select {
	case secondArtifactsPath = <-secondEntered:
	case <-time.After(50 * time.Millisecond):
		serialized = true
	}

	close(releaseFirst)
	if serialized {
		select {
		case secondArtifactsPath = <-secondEntered:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for the second .NET build")
		}
	}

	for range 2 {
		require.NoError(t, <-errs)
	}
	require.True(t, serialized, ".NET builds overlapped instead of using the fallback gate")
	require.Empty(t, firstArtifactsPath)
	require.Empty(t, secondArtifactsPath)
}
