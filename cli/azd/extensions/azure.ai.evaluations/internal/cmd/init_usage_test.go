// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// The scaffold is only real once azure.yaml references it, so the usage report
// sits after that wiring. These drive the shipped command against a stand-in
// azd and read what arrived at the telemetry service, because the thing worth
// protecting is not that an event can be built -- the builder tests cover that
// -- but that one is sent exactly when an eval was scaffolded, and never when
// the command failed on the way there.

// usageRecorder is the telemetry half of the stand-in azd.
type usageRecorder struct {
	azdext.UnimplementedTelemetryServiceServer

	mu       sync.Mutex
	requests []*azdext.ReportUsageRequest
}

func (r *usageRecorder) ReportUsage(
	_ context.Context, request *azdext.ReportUsageRequest,
) (*azdext.ReportUsageResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, request)
	return &azdext.ReportUsageResponse{Accepted: true}, nil
}

func (r *usageRecorder) reported() []*azdext.ReportUsageRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*azdext.ReportUsageRequest(nil), r.requests...)
}

// initProjectServer answers the two calls `init` makes of the project: reading
// it, and adding the eval service to it.
type initProjectServer struct {
	azdext.UnimplementedProjectServiceServer

	dir           string
	addServiceErr error

	mu         sync.Mutex
	addCalls   int
	addService []string
}

func (s *initProjectServer) Get(
	_ context.Context, _ *azdext.EmptyRequest,
) (*azdext.GetProjectResponse, error) {
	return &azdext.GetProjectResponse{Project: &azdext.ProjectConfig{
		Name:     "usage",
		Path:     s.dir,
		Services: map[string]*azdext.ServiceConfig{"agent": {Name: "agent"}},
	}}, nil
}

func (s *initProjectServer) AddService(
	_ context.Context, request *azdext.AddServiceRequest,
) (*azdext.EmptyResponse, error) {
	s.mu.Lock()
	s.addCalls++
	if request.GetService() != nil {
		s.addService = append(s.addService, request.GetService().GetName())
	}
	s.mu.Unlock()

	if s.addServiceErr != nil {
		return nil, s.addServiceErr
	}
	return &azdext.EmptyResponse{}, nil
}

func (s *initProjectServer) wiringAttempts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addCalls
}

const usageAzureYaml = `name: usage
services:
  agent:
    project: ./agent
    host: containerapp
    language: python
`

// initHarness is a project on disk plus the stand-in azd that serves it.
type initHarness struct {
	dir      string
	usage    *usageRecorder
	project  *initProjectServer
	seedRows string
}

// newInitHarness points the extension at a stand-in azd for one test.
//
// AZD_SERVER is what azdext.NewAzdClient reads, so the command under test
// opens its own connection exactly as it does in production rather than being
// handed one the test built.
func newInitHarness(t *testing.T, addServiceErr error) *initHarness {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "azure.yaml"), []byte(usageAzureYaml), 0o600))

	seed := filepath.Join(dir, "seed.jsonl")
	require.NoError(t, os.WriteFile(
		seed, []byte("{\"query\":\"a\",\"response\":\"b\"}\n"), 0o600))

	harness := &initHarness{
		dir:      dir,
		usage:    &usageRecorder{},
		project:  &initProjectServer{dir: dir, addServiceErr: addServiceErr},
		seedRows: seed,
	}

	server := grpc.NewServer()
	azdext.RegisterProjectServiceServer(server, harness.project)
	azdext.RegisterTelemetryServiceServer(server, harness.usage)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	t.Setenv("AZD_SERVER", listener.Addr().String())

	wd, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(wd) })
	require.NoError(t, os.Chdir(dir))

	return harness
}

// runInit drives the shipped init command the way azd does.
func (h *initHarness) runInit(t *testing.T, args ...string) error {
	t.Helper()

	cmd := newInitCommand()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	// Global flags azd would have supplied.
	cmd.Flags().Bool("no-prompt", false, "")
	cmd.Flags().String("output", "", "")
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(append(args, "--no-prompt"))
	cmd.SetContext(t.Context())

	return cmd.Execute()
}

// assertOneInitCompleted fails unless exactly one init.completed arrived
// carrying the given source and nothing else.
func assertOneInitCompleted(t *testing.T, h *initHarness, wantSource string) {
	t.Helper()

	reported := h.usage.reported()
	require.Len(t, reported, 1, "a scaffold that was written reports once, not twice")
	assert.Equal(t, "init.completed", reported[0].GetEventName())
	assert.Equal(t, map[string]string{"source": wantSource}, reported[0].GetAttributes(),
		"the source the scaffold was written for, and nothing that could carry content")
}

// A trace-backed scaffold reports the source it was written for.
func TestInitReportsTheSourceItScaffoldedFor(t *testing.T) {
	h := newInitHarness(t, nil)

	require.NoError(t, h.runInit(t,
		"--name", "traceeval", "--target", "agent", "--source", "traces",
		"--judge-model", "gpt-4.1-nano"))

	assertOneInitCompleted(t, h, "traces")
}

// And so does a dataset-backed one: the attribute distinguishes them, so both
// values have to be reached by a test that runs the command.
func TestInitReportsADatasetScaffold(t *testing.T) {
	h := newInitHarness(t, nil)

	require.NoError(t, h.runInit(t,
		"--name", "dataseteval", "--target", "agent", "--source", "dataset",
		"--dataset", h.seedRows, "--judge-model", "gpt-4.1-nano"))

	assertOneInitCompleted(t, h, "dataset")
}

// The report follows the wiring, so a scaffold azd never accepted is not an
// init that completed.
//
// This is the placement guard: move the call above ensureRootEvalService and
// this is the test that notices, because the files are on disk by then and
// only the wiring failed.
func TestInitReportsNothingWhenTheWiringFails(t *testing.T) {
	h := newInitHarness(t, errors.New("azure.yaml is read-only"))

	err := h.runInit(t,
		"--name", "unwired", "--target", "agent", "--source", "traces",
		"--judge-model", "gpt-4.1-nano")

	require.Error(t, err, "a scaffold azd cannot see is a failure")
	assert.Positive(t, h.project.wiringAttempts(),
		"the test is worthless if the command never got as far as wiring")
	assert.Empty(t, h.usage.reported(),
		"nothing completed, so nothing is reported")
}

// A command that refuses before it writes anything reports nothing either.
func TestInitReportsNothingWhenItRefusesEarly(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{
			// No --judge-model, and the stand-in project declares no deployment.
			name: "no model to judge with",
			args: []string{"--name", "nomodel", "--target", "agent", "--source", "traces"},
		},
		{
			// A dataset source with no dataset to grade.
			name: "no dataset to grade",
			args: []string{
				"--name", "nodata", "--target", "agent", "--source", "dataset",
				"--judge-model", "gpt-4.1-nano",
			},
		},
		{
			// --max-traces contradicts the source that was asked for.
			name: "a flag the source cannot honour",
			args: []string{
				"--name", "contradiction", "--target", "agent", "--source", "dataset",
				"--max-traces", "50", "--judge-model", "gpt-4.1-nano",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newInitHarness(t, nil)

			require.Error(t, h.runInit(t, tc.args...))
			assert.Empty(t, h.usage.reported(),
				"a refusal is not a completed init")
		})
	}
}

// Reporting is best effort, so a telemetry service that refuses the event must
// not turn a written scaffold into a failed command.
func TestInitSucceedsWhenTheEventIsRefused(t *testing.T) {
	h := newInitHarness(t, nil)
	h.usage.mu.Lock()
	h.usage.requests = nil
	h.usage.mu.Unlock()

	require.NoError(t, h.runInit(t,
		"--name", "besteffort", "--target", "agent", "--source", "traces",
		"--judge-model", "gpt-4.1-nano"),
		"the scaffold is on disk; what telemetry thinks of it is not the caller's problem")
}
