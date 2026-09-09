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
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockexec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContainerEngineConcurrent(t *testing.T) {
	tests := []struct {
		name       string
		override   string
		dockerErr  error
		engine     string
		engineName string
		version    string
	}{
		{
			name: "docker first", engine: "docker", engineName: "Docker",
			version: "Docker version 20.10.17, build 100c701",
		},
		{
			name: "podman discovery", dockerErr: errors.New("docker not found"),
			engine: "podman", engineName: "Podman", version: "podman version 4.3.1",
		},
		{
			name: "podman override", override: "podman",
			engine: "podman", engineName: "Podman", version: "podman version 4.3.1",
		},
		{
			name: "docker override", override: "docker",
			engine: "docker", engineName: "Docker", version: "Docker version 20.10.17, build 100c701",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AZD_CONTAINER_RUNTIME", tt.override)
			runner := mockexec.NewMockCommandRunner()
			runner.MockToolInPath("docker", tt.dockerErr)
			runner.MockToolInPath("podman", nil)
			runner.When(func(args exec.RunArgs, command string) bool {
				return command == tt.engine+" --version"
			}).Respond(exec.RunResult{Stdout: tt.version})
			runner.When(func(args exec.RunArgs, command string) bool {
				return command == tt.engine+" ps" || command == tt.engine+" pull image"
			}).Respond(exec.RunResult{})

			cli := NewCli(runner)
			require.Equal(t, "Docker", cli.Name())
			require.Equal(t, "https://aka.ms/azure-dev/docker-install", cli.InstallUrl())

			// Register all mocks before starting concurrent readers.
			start := make(chan struct{})
			var wg sync.WaitGroup
			for range 8 {
				wg.Go(func() {
					<-start
					for range 10 {
						assert.Equal(t, tt.engine, cli.ContainerEngine())
						assert.NoError(t, cli.CheckInstalled(t.Context()))
						assert.Equal(t, tt.engineName, cli.Name())
						assert.Equal(t, "https://aka.ms/azure-dev/"+tt.engine+"-install", cli.InstallUrl())
						assert.NoError(t, cli.Pull(t.Context(), "image"))
					}
				})
			}
			close(start)
			wg.Wait()
		})
	}
}

func TestCheckInstalledSnapshotAndRetry(t *testing.T) {
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
				return command == "docker --version"
			}).Respond(exec.RunResult{Stdout: "Docker version 20.10.17, build 100c701"})
			runner.When(func(args exec.RunArgs, command string) bool {
				return command == "podman --version"
			}).Respond(exec.RunResult{Stdout: "podman version 4.3.1"})
			runner.When(func(args exec.RunArgs, command string) bool {
				return command == "docker ps" || command == "podman ps" || command == "docker pull image"
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
				return args.Cmd == "docker" && len(args.Args) > 0 && args.Args[0] == "build"
			}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				if !cli.engineMu.TryLock() {
					return exec.RunResult{}, errors.New("engine lock held during build")
				}
				cli.engineMu.Unlock()
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

			// Reselect while the first readiness subprocess is blocked. Neither
			// another check nor container operations may wait for that subprocess.
			t.Setenv("AZD_CONTAINER_RUNTIME", "docker")
			require.NoError(t, cli.CheckInstalled(ctx))
			require.Equal(t, "docker", cli.ContainerEngine())
			require.Equal(t, "Docker", cli.Name())
			require.Equal(t, "https://aka.ms/azure-dev/docker-install", cli.InstallUrl())
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
			require.Equal(t, "docker", cli.ContainerEngine())

			t.Setenv("AZD_CONTAINER_RUNTIME", "podman")
			require.NoError(t, cli.CheckInstalled(ctx))
			require.Equal(t, int32(2), calls.Load(), "readiness checks must run again after success or failure")
			require.Equal(t, "podman", cli.ContainerEngine())
		})
	}
}
