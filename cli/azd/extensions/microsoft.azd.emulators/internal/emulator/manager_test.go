// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package emulator

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
)

type dockerCall struct {
	args  []string
	input string
}

type fakeDocker struct {
	calls       []dockerCall
	version     string
	status      string
	info        string
	failCommand string
	stderr      string
	missing     bool
}

func (f *fakeDocker) ToolInPath(_ string) error {
	if f.missing {
		return errors.New("docker not found")
	}
	return nil
}

func (f *fakeDocker) Run(_ context.Context, args exec.RunArgs) (exec.RunResult, error) {
	input, err := io.ReadAll(args.StdIn)
	if err != nil {
		return exec.RunResult{}, err
	}
	f.calls = append(f.calls, dockerCall{args: slices.Clone(args.Args), input: string(input)})
	if f.failCommand != "" && slices.Contains(args.Args, f.failCommand) {
		return exec.RunResult{ExitCode: 1, Stderr: f.stderr}, errors.New("exit status 1")
	}
	if slices.Contains(args.Args, "version") {
		version := f.version
		if version == "" {
			version = "2.23.1"
		}
		return exec.RunResult{Stdout: version}, nil
	}
	if slices.Contains(args.Args, "info") {
		if f.info != "" {
			return exec.RunResult{Stdout: f.info}, nil
		}
		return exec.RunResult{Stdout: "linux x86_64"}, nil
	}
	if slices.Contains(args.Args, "ps") {
		status := f.status
		if status == "" {
			status = `{"Service":"emulator","State":"running","ExitCode":0}`
		}
		return exec.RunResult{Stdout: status}, nil
	}
	return exec.RunResult{}, nil
}

func (f *fakeDocker) RunList(_ context.Context, _ []string, _ exec.RunArgs) (exec.RunResult, error) {
	return exec.RunResult{}, errors.New("unexpected RunList call")
}

func readyDefinition(t *testing.T, status int) (Definition, Request) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(status)
	}))
	t.Cleanup(server.Close)
	_, portText, err := net.SplitHostPort(server.Listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	definition := Definition{
		Name: "test", DisplayName: "Test emulator", Ports: []Port{{Flag: "port", Default: port}},
		Build: func(o Options) (Spec, error) {
			return Spec{
				Compose: Compose{Services: map[string]Service{
					"emulator": {Image: "test/image:1", Ports: []string{o.Binding("port", 8080)}},
				}},
				Connections: []Connection{{Name: "ENDPOINT", Value: o.URL("port")}},
				Probes:      []Probe{{URL: o.URL("port") + "/ready", Status: 200}},
				Secrets:     []string{"generated-password"},
			}, nil
		},
	}
	return definition, Request{
		Options: Options{Ports: map[string]int{"port": port}},
		Changed: map[string]bool{}, Timeout: time.Second,
	}
}

func TestManagerStartsReusesAndStopsInstance(t *testing.T) {
	definition, request := readyDefinition(t, 200)
	buildCount := 0
	build := definition.Build
	definition.Build = func(o Options) (Spec, error) {
		buildCount++
		return build(o)
	}
	runner := &fakeDocker{}
	directory := t.TempDir()
	manager := NewManager(runner, directory)

	first, err := manager.Run(t.Context(), definition, request)
	require.NoError(t, err)
	require.Equal(t, "running", first.Status)
	require.Equal(t, request.Options.URL("port"), first.Connections[0].Value)
	statePath := filepath.Join(directory, "test.json")
	before, err := os.ReadFile(statePath)
	require.NoError(t, err)

	// Unspecified defaults must not replace the saved instance's chosen ports.
	request.Options.Ports = map[string]int{"port": 1234}
	again, err := manager.Run(t.Context(), definition, request)
	require.NoError(t, err)
	require.Equal(t, first, again)
	require.Equal(t, 1, buildCount)
	after, err := os.ReadFile(statePath)
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after))

	stopped, err := manager.Run(t.Context(), definition, Request{Stop: true, Timeout: time.Second})
	require.NoError(t, err)
	require.Equal(t, "stopped", stopped.Status)
	require.Equal(t, []string{"ENDPOINT"}, stopped.RemovedConnectionNames)
	require.Empty(t, stopped.Connections)
	_, err = os.Stat(statePath)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.Contains(t, runner.calls[len(runner.calls)-1].args, "down")
	require.Contains(t, runner.calls[len(runner.calls)-1].args, "--volumes")
}

func TestManagerRejectsExplicitChangesToSavedInstance(t *testing.T) {
	definition, request := readyDefinition(t, 200)
	runner := &fakeDocker{}
	manager := NewManager(runner, t.TempDir())
	_, err := manager.Run(t.Context(), definition, request)
	require.NoError(t, err)
	count := len(runner.calls)

	request.Options.Ports = map[string]int{"port": 1234}
	request.Changed["port"] = true
	_, err = manager.Run(t.Context(), definition, request)
	require.ErrorContains(t, err, "--port differs")
	require.Len(t, runner.calls, count)
}

func TestManagerRequiresLicenseOnlyForNewInstances(t *testing.T) {
	definition, request := readyDefinition(t, 200)
	definition.LicenseURLs = []string{"https://example.com/license"}
	runner := &fakeDocker{}
	manager := NewManager(runner, t.TempDir())

	_, err := manager.Run(t.Context(), definition, request)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	require.Equal(t, codeLicenseNeeded, localErr.Code)
	require.Empty(t, runner.calls)

	request.AcceptLicense = true
	_, err = manager.Run(t.Context(), definition, request)
	require.NoError(t, err)
	request.AcceptLicense = false
	_, err = manager.Run(t.Context(), definition, request)
	require.NoError(t, err)
}

func TestManagerWaitsForHealthyEndpoints(t *testing.T) {
	definition, request := readyDefinition(t, 503)
	request.Timeout = 50 * time.Millisecond
	manager := NewManager(&fakeDocker{}, t.TempDir())

	result, err := manager.Run(t.Context(), definition, request)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "HTTP 503")
	require.Empty(t, result.Connections)
}

func TestManagerDoesNotMistakeExistingListenerForMissingContainer(t *testing.T) {
	definition, request := readyDefinition(t, 200)
	request.Timeout = 50 * time.Millisecond
	manager := NewManager(&fakeDocker{status: "[]"}, t.TempDir())

	_, err := manager.Run(t.Context(), definition, request)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "waiting for all emulator containers")
}

func TestManagerReportsExitedDependency(t *testing.T) {
	definition, request := readyDefinition(t, 200)
	manager := NewManager(&fakeDocker{
		status: `{"Service":"dependency","State":"exited","ExitCode":42}`,
	}, t.TempDir())

	_, err := manager.Run(t.Context(), definition, request)
	require.ErrorContains(t, err, "dependency stopped with exit code 42")
}

func TestManagerKeepsStateWhenLaunchOrStopFails(t *testing.T) {
	definition, request := readyDefinition(t, 200)
	runner := &fakeDocker{failCommand: "up", stderr: "image not found"}
	directory := t.TempDir()
	manager := NewManager(runner, directory)

	_, err := manager.Run(t.Context(), definition, request)
	require.ErrorContains(t, err, "image not found")
	statePath := filepath.Join(directory, "test.json")
	require.FileExists(t, statePath)

	runner.failCommand = "down"
	_, err = manager.Run(t.Context(), definition, Request{Stop: true, Timeout: time.Second})
	require.Error(t, err)
	require.FileExists(t, statePath)

	runner.failCommand = ""
	_, err = manager.Run(t.Context(), definition, Request{Stop: true, Timeout: time.Second})
	require.NoError(t, err)
	require.NoFileExists(t, statePath)
}

func TestManagerRejectsMissingDockerAndOldCompose(t *testing.T) {
	for _, test := range []struct {
		name   string
		runner *fakeDocker
		want   string
	}{
		{name: "missing docker", runner: &fakeDocker{missing: true}, want: "Docker is required"},
		{name: "missing compose", runner: &fakeDocker{failCommand: "version"}, want: "`docker compose` is unavailable"},
		{name: "old compose", runner: &fakeDocker{version: "2.22.0"}, want: "Compose 2.23.1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			definition, request := readyDefinition(t, 200)
			_, err := NewManager(test.runner, t.TempDir()).Run(t.Context(), definition, request)
			require.ErrorContains(t, err, test.want)
			for _, call := range test.runner.calls {
				require.Equal(t, []string{"compose", "version", "--short"}, call.args)
			}
		})
	}
}

func TestManagerDoesNotInterpolateConfiguration(t *testing.T) {
	definition, request := readyDefinition(t, 200)
	build := definition.Build
	definition.Build = func(o Options) (Spec, error) {
		spec, err := build(o)
		spec.Compose.Services["emulator"] = Service{
			Image: "test/image:1", Environment: map[string]string{"LITERAL": "${SECRET}"},
		}

		return spec, err
	}
	runner := &fakeDocker{}
	_, err := NewManager(runner, t.TempDir()).Run(t.Context(), definition, request)
	require.NoError(t, err)
	var config Compose
	for _, call := range runner.calls {
		if slices.Contains(call.args, "up") {
			require.NoError(t, json.Unmarshal([]byte(call.input), &config))
		}
	}
	require.Equal(t, "$${SECRET}", config.Services["emulator"].Environment["LITERAL"])
}

func TestManagerRejectsAMD64EmulatorBeforeComposeUpOnARM64(t *testing.T) {
	definition, request := readyDefinition(t, 200)
	definition.RequiresAMD64 = true
	runner := &fakeDocker{info: "linux aarch64"}
	_, err := NewManager(runner, t.TempDir()).Run(t.Context(), definition, request)
	require.ErrorContains(t, err, "requires an AMD64 Docker host")
	require.Len(t, runner.calls, 2)
	require.Equal(t, []string{"compose", "version", "--short"}, runner.calls[0].args)
	require.Equal(t, "info", runner.calls[1].args[0])
}

func TestManagerStagesOnlyDockerfileInIsolatedBuildContext(t *testing.T) {
	definition, request := readyDefinition(t, 200)
	build := definition.Build
	definition.Build = func(o Options) (Spec, error) {
		spec, err := build(o)
		service := spec.Compose.Services["emulator"]
		service.Build = &Build{Context: "build/test"}
		spec.Compose.Services["emulator"] = service
		spec.Dockerfile = "FROM test/image:1\n"
		return spec, err
	}
	directory := t.TempDir()
	manager := NewManager(&fakeDocker{}, directory)
	_, err := manager.Run(t.Context(), definition, request)
	require.NoError(t, err)
	contextDirectory := filepath.Join(directory, "build", "test")
	entries, err := os.ReadDir(contextDirectory)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "Dockerfile", entries[0].Name())
	content, err := os.ReadFile(filepath.Join(contextDirectory, "Dockerfile"))
	require.NoError(t, err)
	require.Equal(t, "FROM test/image:1\n", string(content))
}

func TestManagerRedactsToolDiagnostics(t *testing.T) {
	definition, request := readyDefinition(t, 200)
	// #nosec G101 -- Fake credentials verify that diagnostics never disclose URL secrets.
	manager := NewManager(&fakeDocker{
		failCommand: "up",
		stderr:      "pull https://alice:password@example.com/image?sig=sas-secret#private-fragment generated-password",
	}, t.TempDir())

	_, err := manager.Run(t.Context(), definition, request)
	require.Error(t, err)
	for _, secret := range []string{"alice", "password", "sas-secret", "private-fragment"} {
		require.NotContains(t, err.Error(), secret)
	}
	require.Contains(t, err.Error(), "https://example.com/image")
}

func TestManagerRejectsCorruptStateAndTraversal(t *testing.T) {
	definition, request := readyDefinition(t, 200)
	directory := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(directory, "test.json"), []byte("{"), 0o600))
	runner := &fakeDocker{}
	manager := NewManager(runner, directory)
	_, err := manager.Run(t.Context(), definition, request)
	require.ErrorContains(t, err, "decode emulator state")
	require.Empty(t, runner.calls)

	definition.Name = "../outside"
	_, err = manager.Run(t.Context(), definition, request)
	require.ErrorContains(t, err, "invalid emulator name")
}

func TestManagerExecutesSQLReadinessCommand(t *testing.T) {
	definition, request := readyDefinition(t, 200)
	build := definition.Build
	definition.Build = func(o Options) (Spec, error) {
		spec, err := build(o)
		spec.Probes = nil
		spec.ExecProbe = []string{"sqlcmd", "-Q", "SELECT 1"}
		return spec, err
	}
	runner := &fakeDocker{}
	manager := NewManager(runner, t.TempDir())
	_, err := manager.Run(t.Context(), definition, request)
	require.NoError(t, err)
	require.True(t, slices.ContainsFunc(runner.calls, func(call dockerCall) bool {
		return strings.HasSuffix(strings.Join(call.args, " "), "exec -T emulator sqlcmd -Q SELECT 1")
	}))
	_, err = manager.Run(t.Context(), definition, request)
	require.NoError(t, err)

	runner.failCommand = "exec"
	request.Timeout = 50 * time.Millisecond
	_, err = manager.Run(t.Context(), definition, request)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestValidateRequestRejectsInvalidAndConflictingOptions(t *testing.T) {
	definition := Definition{Ports: []Port{{Flag: "port"}, {Flag: "other-port"}}}
	for _, test := range []struct {
		name    string
		request Request
		want    string
	}{
		{
			name: "invalid port", want: "between 1 and 65535",
			request: Request{Options: Options{Ports: map[string]int{"port": 0}}, Timeout: time.Second},
		},
		{
			name: "duplicate ports", want: "conflicts",
			request: Request{
				Options: Options{Ports: map[string]int{"port": 1234, "other-port": 1234}}, Timeout: time.Second,
			},
		},
		{
			name: "stop ignores port", want: "--port cannot be used with --stop",
			request: Request{Stop: true, Changed: map[string]bool{"port": true}, Timeout: time.Second},
		},
		{name: "invalid timeout", want: "--timeout", request: Request{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.ErrorContains(t, ValidateRequest(definition, test.request), test.want)
		})
	}
}
