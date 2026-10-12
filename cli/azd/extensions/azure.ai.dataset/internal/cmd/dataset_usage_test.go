// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"azureaidataset/internal/pkg/dataset_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// A version is reported when one was published, and the publish is the whole
// condition: these drive the write against a stand-in service and read what
// reached the telemetry service. The builder tests pin the wire shape; what
// they cannot see is a report that survives a failed publish, or one that
// stops being sent at all.

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

// serveStandInAzd runs an azd whose only answer is the telemetry service, and
// points azdext.NewAzdClient at it. Everything else the write asks of azd --
// persisting the version to an environment -- is a convenience the command
// already treats as allowed to fail.
func serveStandInAzd(t *testing.T) *usageRecorder {
	t.Helper()

	recorder := &usageRecorder{}
	server := grpc.NewServer()
	azdext.RegisterTelemetryServiceServer(server, recorder)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	t.Setenv("AZD_SERVER", listener.Addr().String())
	return recorder
}

// publishStub answers the three-step publish and the version listing.
type publishStub struct {
	mu sync.Mutex

	// listing is the set of versions the service reports for the name.
	listing []string
	// listingStatus, when set, is returned for the listing instead of a body.
	listingStatus int
	// failPublish makes startPendingUpload refuse, which is a publish that
	// did not happen.
	failPublish bool

	published []string
	// faults are anything that went wrong inside the handler. They are kept
	// rather than asserted here, because this runs on the server's goroutine
	// and FailNow is only meaningful on the test's own.
	faults []error
}

func (s *publishStub) handler(base func() string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")

		// write answers with a JSON body, recording rather than failing on an
		// encoder error.
		write := func(body map[string]any) {
			if err := json.NewEncoder(w).Encode(body); err != nil {
				s.faults = append(s.faults, err)
			}
		}

		switch {
		case strings.HasSuffix(r.URL.Path, "/startPendingUpload"):
			if s.failPublish {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":{"code":"InternalServerError"}}`))
				return
			}
			write(map[string]any{
				"blobReference": map[string]any{
					"blobUri":             base() + "/c",
					"storageAccountArmId": "id",
					"credential":          map[string]any{"sasUri": base() + "/c?sig=x"},
				},
			})

		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/versions"):
			if s.listingStatus != 0 {
				w.WriteHeader(s.listingStatus)
				_, _ = w.Write([]byte(`{"error":{"code":"NotFound"}}`))
				return
			}
			values := []map[string]any{}
			for _, v := range s.listing {
				values = append(values, map[string]any{"name": "ds", "version": v})
			}
			write(map[string]any{"value": values})

		// The presence probe reads one version directly, which is how a listing
		// that has not caught up is checked. Answering it from anything but the
		// listing makes every name look taken.
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/versions/"):
			version := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			if slices.Contains(s.listing, version) {
				write(map[string]any{"name": "ds", "version": version})
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"NotFound"}}`))

		// The blob write is a PUT as well, and has to be matched before the
		// finalize branch or the blob name is recorded as a published version.
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/c/"):
			if _, err := io.ReadAll(r.Body); err != nil {
				s.faults = append(s.faults, err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusCreated)

		case r.Method == http.MethodPut:
			version := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			s.published = append(s.published, version)
			write(map[string]any{"name": "ds", "version": version})

		default:
			w.WriteHeader(http.StatusCreated)
		}
	}
}

func (s *publishStub) publishedVersions() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.published...)
}

func (s *publishStub) handlerFaults() []error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]error(nil), s.faults...)
}

// writeHarness is a stand-in service plus the action that writes against it.
type writeHarness struct {
	usage *usageRecorder
	stub  *publishStub
	ec    *datasetContext
}

func newWriteHarness(t *testing.T, stub *publishStub) *writeHarness {
	t.Helper()

	usage := serveStandInAzd(t)

	httpServer := func() *httptest.Server {
		var s *httptest.Server
		s = httptest.NewServer(stub.handler(func() string { return s.URL }))
		return s
	}()
	t.Cleanup(httpServer.Close)

	// Anything the handler could not do is reported here, on the test's own
	// goroutine, rather than from the server's.
	t.Cleanup(func() {
		assert.Empty(t, stub.handlerFaults(), "the stand-in service failed to answer")
	})

	client := dataset_api.NewDatasetClientFromPipeline(
		httpServer.URL, runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil))

	azdClient, err := azdext.NewAzdClient()
	require.NoError(t, err)
	t.Cleanup(azdClient.Close)

	return &writeHarness{
		usage: usage,
		stub:  stub,
		ec: &datasetContext{
			azdClient:     azdClient,
			endpoint:      httpServer.URL,
			datasetClient: client,
		},
	}
}

// write runs the half of the command that needs a resolved context.
func (h *writeHarness) write(t *testing.T, verb, version string) error {
	t.Helper()

	cmd := &cobra.Command{Use: verb}
	cmd.Flags().String("output", "", "")
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetContext(t.Context())

	action := &datasetWriteAction{
		cmd:   cmd,
		flags: &datasetWriteFlags{version: version},
		verb:  verb,
		name:  "ds",
	}
	return action.publishAndReport(t.Context(), h.ec, "{\"query\":\"q\"}\n")
}

// assertOneDatasetPublished fails unless exactly one dataset.published arrived
// naming the write that produced the version.
func assertOneDatasetPublished(t *testing.T, h *writeHarness, wantOperation string) {
	t.Helper()

	reported := h.usage.reported()
	require.Len(t, reported, 1, "one version published is one event")
	assert.Equal(t, "dataset.published", reported[0].GetEventName())
	assert.Equal(t, map[string]string{"operation": wantOperation}, reported[0].GetAttributes(),
		"the write that published it, and nothing that could carry content")
}

// A first publish reports the write that made it.
func TestCreateReportsThePublishedVersion(t *testing.T) {
	// Nothing registered under the name, stated as an answer rather than an
	// empty listing, which create refuses to treat as proof of absence.
	h := newWriteHarness(t, &publishStub{listingStatus: http.StatusNotFound})

	require.NoError(t, h.write(t, "create", "1.0"))

	assertOneDatasetPublished(t, h, "create")
	assert.Equal(t, []string{"1.0"}, h.stub.publishedVersions(),
		"the test is worthless if nothing was actually published")
}

// And a further version reports as an update, which is the only thing that
// distinguishes the two on the wire.
func TestUpdateReportsThePublishedVersion(t *testing.T) {
	h := newWriteHarness(t, &publishStub{listing: []string{"1.0"}})

	require.NoError(t, h.write(t, "update", "2.0"))

	assertOneDatasetPublished(t, h, "update")
	assert.Equal(t, []string{"2.0"}, h.stub.publishedVersions())
}

// A publish that failed is not a publish.
//
// This is the guard the builder tests cannot provide: move the report above
// the publish, or report regardless of the error, and this is what notices.
func TestAFailedPublishReportsNothing(t *testing.T) {
	h := newWriteHarness(t, &publishStub{
		listingStatus: http.StatusNotFound,
		failPublish:   true,
	})

	require.Error(t, h.write(t, "create", "1.0"), "the service refused the upload")

	assert.Empty(t, h.usage.reported(),
		"no version exists, so there is nothing to have published")
	assert.Empty(t, h.stub.publishedVersions())
}

// A write refused before it starts reports nothing either: `create` against a
// name that already exists never reaches the publish.
func TestAWriteRefusedBeforePublishingReportsNothing(t *testing.T) {
	h := newWriteHarness(t, &publishStub{listing: []string{"1.0"}})

	require.Error(t, h.write(t, "create", ""), "the name is taken")

	assert.Empty(t, h.usage.reported())
	assert.Empty(t, h.stub.publishedVersions())
}

// The failures the command answers on its own terms, before it opens a
// connection, are still failures that publish nothing.
func TestARefusalBeforeTheServiceReportsNothing(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"a name the service would reject", []string{"not a valid name", "--from-file", "rows.jsonl"}},
		{"no rows to publish", []string{"ds"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			usage := serveStandInAzd(t)
			t.Setenv("FOUNDRY_PROJECT_ENDPOINT", "https://dataset-usage.invalid/api/projects/none")

			cmd := newDatasetWriteCommand("create", "Register a dataset.")
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			cmd.Flags().Bool("no-prompt", false, "")
			cmd.Flags().String("output", "", "")
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs(tc.args)
			cmd.SetContext(t.Context())

			require.Error(t, cmd.Execute())
			assert.Empty(t, usage.reported(), "nothing was published")
		})
	}
}
