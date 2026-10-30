// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/inittelemetry"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type remoteTimingAiServer struct {
	azdext.UnimplementedAiModelServiceServer
	checkpoint func()
}

func (s *remoteTimingAiServer) ListModels(
	context.Context, *azdext.ListModelsRequest,
) (*azdext.ListModelsResponse, error) {
	s.checkpoint()
	return nil, status.Error(codes.Unimplemented, "stopped after template loading")
}

func TestInitExplicitRemoteTemplateLoadingTiming(t *testing.T) {
	const (
		source = "https://github.com/example/repo/blob/main/samples/azure.yaml"
		apiDir = "/repos/example/repo/contents/samples"
		valid  = "name: remote-template\nservices:\n  agent:\n    host: azure.ai.agent\n" +
			"    kind: prompt\n    name: remote-agent\n    model: gpt-4.1-mini\n    instructions: Help.\n"
	)
	for _, test := range []struct {
		name        string
		content     string
		downloadErr bool
		wantErr     string
		loaded      bool
	}{
		{name: "loaded before later Init failure", content: valid, loaded: true,
			wantErr: "incompatible with your installed version"},
		{name: "download failure", downloadErr: true, wantErr: "status 503"},
		{name: "invalid YAML", content: "services: [", wantErr: "parsing"},
		{name: "missing agent", content: "services:\n  web:\n    host: containerapp\n",
			wantErr: "does not declare an agent service"},
		{name: "invalid staged definition", content: "services:\n  agent:\n    host: azure.ai.agent\n" +
			"    $ref: ./missing.yaml\n", wantErr: "missing.yaml"},
		{name: "preview validation failure", content: "services:\n  agent:\n    host: azure.ai.agent\n" +
			"    kind: prompt-voice\n    name: voice-agent\n    modelType: managed\n    model:\n      id: gpt-realtime\n",
			wantErr: "agents.voice"},
	} {
		t.Run(test.name, func(t *testing.T) {
			clearPreviewProcess(t)
			t.Chdir(t.TempDir())
			t.Setenv("AZD_EXEC_PROJECT_DIR", "")
			t.Setenv("AZD_EXT_DEBUG", "")
			t.Setenv("NO_COLOR", "1")
			t.Setenv(templateCacheDirEnv, t.TempDir())
			tempRoot := t.TempDir()
			for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
				t.Setenv(name, tempRoot)
			}

			downloadContext := make(chan context.Context, 1)
			previous := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = previous })
			requests := 0
			http.DefaultTransport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
				require.Equal(t, "api.github.com", request.URL.Host)
				require.Equal(t, "ref=main", request.URL.RawQuery)
				requests++
				body, code := test.content, http.StatusOK
				switch request.URL.Path {
				case apiDir:
					downloadContext <- request.Context()
					body = `[{"name":"azure.yaml","type":"file"}]`
				case apiDir + "/azure.yaml":
					if test.downloadErr {
						code = http.StatusServiceUnavailable
					}
				default:
					t.Fatalf("unexpected template request %s", request.URL.Path)
				}
				return &http.Response{
					StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)),
				}, nil
			})

			capture := &operationTelemetryServer{}
			checkpoint := make(chan []*azdext.ReportUsageRequest, 1)
			server := grpc.NewServer()
			azdext.RegisterTelemetryServiceServer(server, capture)
			azdext.RegisterAiModelServiceServer(server, &remoteTimingAiServer{checkpoint: func() {
				// Snapshot completed timings before later Init work starts, then
				// compare with the normal final report to check the exact boundary.
				inittelemetry.Report(<-downloadContext)
				capture.mu.Lock()
				checkpoint <- capture.events
				capture.events = nil
				capture.mu.Unlock()
			}})
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			done := make(chan error, 1)
			go func() { done <- server.Serve(listener) }()
			t.Cleanup(func() { server.Stop(); require.NoError(t, <-done) })
			t.Setenv("AZD_SERVER", listener.Addr().String())

			root := NewRootCommand()
			var output bytes.Buffer
			root.SetOut(&output)
			root.SetErr(&output)
			root.SetArgs([]string{"init", "-t", source, "--no-prompt"})
			require.ErrorContains(t, root.ExecuteContext(t.Context()), test.wantErr)
			require.Equal(t, 2, requests, "each remote template must be staged only once")

			recorder := &initTimingRecorder{}
			capture.mu.Lock()
			for _, event := range capture.events {
				recorder.events = append(recorder.events, foundryTelemetry.Event{
					Name: event.EventName, Attributes: event.Attributes,
				})
			}
			capture.mu.Unlock()
			loads := recordedStepDurations(t, recorder, inittelemetry.TemplateLoading)
			staging := recordedStepDurations(t, recorder, inittelemetry.TemplateStaging)
			require.Len(t, loads, 1)
			require.Len(t, staging, 1, "preserve the existing staging sample without duplicating it")
			require.GreaterOrEqual(t, loads[0], staging[0])
			if test.loaded {
				before := &initTimingRecorder{}
				for _, event := range <-checkpoint {
					before.events = append(before.events, foundryTelemetry.Event{
						Name: event.EventName, Attributes: event.Attributes,
					})
				}
				require.Equal(t, loads, recordedStepDurations(t, before, inittelemetry.TemplateLoading),
					"loading must already be complete, with unchanged duration at the final report")
				require.Len(t, recordedStepDurations(t, recorder, inittelemetry.AiServiceCheck), 1)
			} else {
				require.Empty(t, checkpoint, "loading/validation errors must stop before later Init work")
				require.Empty(t, recordedStepDurations(t, recorder, inittelemetry.AiServiceCheck))
			}
			require.Empty(t, recordedStepDurations(t, recorder, inittelemetry.ProjectScaffolding))
			staged, err := filepath.Glob(filepath.Join(tempRoot, "azd-foundry-adopt-*"))
			require.NoError(t, err)
			require.Empty(t, staged)
			entries, err := os.ReadDir(".")
			require.NoError(t, err)
			require.Empty(t, entries, "loading must not scaffold project files")
		})
	}
}
