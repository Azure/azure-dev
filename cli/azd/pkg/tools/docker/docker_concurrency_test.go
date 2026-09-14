// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package docker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockexec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContainerEngineConcurrent(t *testing.T) {
	tests := []struct {
		name       string
		override   string
		dockerErr  error
		engine     tools.ContainerEngine
		engineName string
		version    string
	}{
		{
			name: "docker first", engine: tools.ContainerEngineDocker, engineName: "Docker",
			version: "Docker version 20.10.17, build 100c701",
		},
		{
			name: "podman discovery", dockerErr: errors.New("docker not found"),
			engine: tools.ContainerEnginePodman, engineName: "Podman", version: "podman version 4.3.1",
		},
		{
			name: "podman override", override: "podman",
			engine: tools.ContainerEnginePodman, engineName: "Podman", version: "podman version 4.3.1",
		},
		{
			name: "docker override", override: "docker",
			engine: tools.ContainerEngineDocker, engineName: "Docker", version: "Docker version 20.10.17, build 100c701",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AZD_CONTAINER_RUNTIME", tt.override)
			runner := mockexec.NewMockCommandRunner()
			runner.MockToolInPath("docker", tt.dockerErr)
			runner.MockToolInPath("podman", nil)
			runner.When(func(args exec.RunArgs, command string) bool {
				return command == string(tt.engine)+" --version"
			}).Respond(exec.RunResult{Stdout: tt.version})
			runner.When(func(args exec.RunArgs, command string) bool {
				return command == string(tt.engine)+" ps" || command == string(tt.engine)+" pull image"
			}).Respond(exec.RunResult{})

			countingRunner := &engineLookupRunner{CommandRunner: runner}
			cli := NewCli(countingRunner)

			// Register all mocks before starting concurrent readers.
			start := make(chan struct{})
			var wg sync.WaitGroup
			for range 8 {
				wg.Go(func() {
					<-start
					for range 10 {
						assert.Equal(t, tt.engineName, cli.Name())
						assert.Equal(t, tt.engine, cli.ContainerEngine())
						assert.NoError(t, cli.CheckInstalled(t.Context()))
						assert.Equal(t, "https://aka.ms/azure-dev/"+string(tt.engine)+"-install", cli.InstallUrl())
						assert.NoError(t, cli.Pull(t.Context(), "image"))
					}
				})
			}
			close(start)
			wg.Wait()
			wantLookups := int32(0)
			if tt.override == "" {
				wantLookups = 1
				if tt.dockerErr != nil {
					wantLookups++
				}
			}
			require.Equal(t, wantLookups, countingRunner.lookups.Load())
		})
	}
}

type engineLookupRunner struct {
	exec.CommandRunner
	lookups atomic.Int32
}

func (r *engineLookupRunner) ToolInPath(name string) error {
	r.lookups.Add(1)
	return r.CommandRunner.ToolInPath(name)
}

func TestCheckInstalledConcurrentReadiness(t *testing.T) {
	tests := []struct {
		name           string
		blockedCommand string
		firstVersion   string
		firstErr       error
		wantError      string
	}{
		{
			name: "ready", blockedCommand: "--version", firstVersion: "podman version 4.3.1",
		},
		{
			name: "version failure", blockedCommand: "--version", firstErr: errors.New("version unavailable"),
			wantError: "checking podman version: version unavailable",
		},
		{
			name: "canceled check", blockedCommand: "--version", firstErr: context.Canceled,
			wantError: "checking podman version: context canceled",
		},
		{
			name: "unsupported version", blockedCommand: "--version", firstVersion: "podman version 2.9.0",
			wantError: "need at least version 3.0.0 or later of Podman installed",
		},
		{
			name: "daemon failure", blockedCommand: "ps", firstErr: errors.New("daemon unavailable"),
			wantError: "the podman service is not running, please start it: daemon unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AZD_CONTAINER_RUNTIME", "podman")
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()

			started := make(chan struct{})
			release := make(chan struct{})
			defer close(release)
			var calls atomic.Int32
			runner := mockexec.NewMockCommandRunner()
			cli := NewCli(runner)
			runner.When(func(args exec.RunArgs, command string) bool {
				return command == "podman --version"
			}).Respond(exec.RunResult{Stdout: "podman version 4.3.1"})
			runner.When(func(args exec.RunArgs, command string) bool {
				return command == "podman ps" || command == "podman pull image"
			}).Respond(exec.RunResult{})
			runner.When(func(args exec.RunArgs, command string) bool {
				return command == "podman "+tt.blockedCommand
			}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				if calls.Add(1) == 1 {
					close(started)
					select {
					case <-release:
						return exec.RunResult{Stdout: tt.firstVersion}, tt.firstErr
					case <-ctx.Done():
						return exec.RunResult{}, ctx.Err()
					}
				}
				return exec.RunResult{Stdout: "podman version 4.3.1"}, nil
			})
			runner.When(func(args exec.RunArgs, command string) bool {
				return args.Cmd == "podman" && len(args.Args) > 0 && args.Args[0] == "build"
			}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				return exec.RunResult{}, errors.New("build stopped")
			})

			done := make(chan error, 1)
			go func() {
				done <- cli.CheckInstalled(ctx)
			}()
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("readiness check did not reach the subprocess")
			}

			// Selection is immutable, but readiness checks and operations must
			// remain independent of the blocked readiness subprocess.
			t.Setenv("AZD_CONTAINER_RUNTIME", "docker")
			require.NoError(t, cli.CheckInstalled(ctx))
			require.Equal(t, tools.ContainerEnginePodman, cli.ContainerEngine())
			require.Equal(t, "Podman", cli.Name())
			require.Equal(t, "https://aka.ms/azure-dev/podman-install", cli.InstallUrl())
			require.NoError(t, cli.Pull(ctx, "image"))
			_, err := cli.Build(ctx, ".", "Dockerfile", "", "", ".", "", nil, nil, nil, "", nil)
			require.EqualError(t, err, "building image: build stopped")
			require.NoError(t, ctx.Err(), "runtime operations waited for the blocked readiness subprocess")

			select {
			case release <- struct{}{}:
			case <-ctx.Done():
				t.Fatal("readiness check did not resume")
			}
			err = <-done
			if tt.wantError == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tt.wantError)
			}
			if tt.firstErr != nil {
				require.ErrorIs(t, err, tt.firstErr)
			}
			require.Equal(t, tools.ContainerEnginePodman, cli.ContainerEngine())

			require.NoError(t, cli.CheckInstalled(ctx))
			require.Equal(t, int32(3), calls.Load(), "readiness checks must run again after success or failure")
			require.Equal(t, tools.ContainerEnginePodman, cli.ContainerEngine())
		})
	}
}

func TestContainerEngineSelectionErrors(t *testing.T) {
	for _, tt := range []struct {
		name      string
		override  string
		wantError string
	}{
		{name: "invalid override", override: "invalid", wantError: "unsupported container runtime"},
		{name: "neither installed", wantError: "neither docker nor podman is installed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AZD_CONTAINER_RUNTIME", tt.override)
			runner := mockexec.NewMockCommandRunner()
			runner.MockToolInPath("docker", errors.New("not found"))
			runner.MockToolInPath("podman", errors.New("not found"))
			cli := NewCli(runner)

			require.Equal(t, tools.ContainerEngineDocker, cli.ContainerEngine())
			err := cli.CheckInstalled(t.Context())
			require.ErrorContains(t, err, tt.wantError)

			t.Setenv("AZD_CONTAINER_RUNTIME", "podman")
			runner.MockToolInPath("podman", nil)
			require.ErrorIs(t, cli.CheckInstalled(t.Context()), err)
			require.Equal(t, tools.ContainerEngineDocker, cli.ContainerEngine())
			require.Equal(t, tools.ContainerEnginePodman, NewCli(runner).ContainerEngine())
		})
	}
}
