// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package telemetry

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type recordingClient struct {
	ctx      context.Context
	request  *azdext.ReportUsageRequest
	response *azdext.ReportUsageResponse
	err      error
	calls    atomic.Int64
}

func (c *recordingClient) ReportUsage(
	ctx context.Context,
	request *azdext.ReportUsageRequest,
	_ ...grpc.CallOption,
) (*azdext.ReportUsageResponse, error) {
	c.ctx = ctx
	c.request = request
	c.calls.Add(1)
	return c.response, c.err
}

func TestReporterForwardsEvent(t *testing.T) {
	t.Parallel()

	client := &recordingClient{response: &azdext.ReportUsageResponse{Accepted: true}}
	reporter := NewReporter(client, nil)
	attributes := map[string]string{"mode": "declarative"}
	reporter.Report(t.Context(), Event{Name: "resource.created", Attributes: attributes})
	attributes["mode"] = "changed"

	require.NotNil(t, client.request)
	assert.Equal(t, "resource.created", client.request.EventName)
	assert.Equal(t, map[string]string{"mode": "declarative"}, client.request.Attributes)
	assert.Equal(t, int64(1), client.calls.Load())
	_, hasDeadline := client.ctx.Deadline()
	assert.True(t, hasDeadline)
}

func TestReporterIsBestEffort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		client   Client
		wantLogs bool
	}{
		{name: "not accepted", client: &recordingClient{response: &azdext.ReportUsageResponse{}}},
		{name: "empty response", client: &recordingClient{}, wantLogs: true},
		{name: "RPC error", client: &recordingClient{err: errors.New("secret transport detail")}, wantLogs: true},
		{name: "no client"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var logs bytes.Buffer
			reporter := NewReporter(test.client, &Options{Logger: azdext.NewLogger(
				"test",
				azdext.LoggerOptions{Debug: true, Writer: &logs},
			)})
			reporter.Report(t.Context(), Event{
				Name:       "resource.created",
				Attributes: map[string]string{"credential": "customer-secret"},
			})

			assert.Equal(t, test.wantLogs, logs.Len() > 0)
			assert.NotContains(t, logs.String(), "customer-secret")
			assert.NotContains(t, logs.String(), "secret transport detail")
		})
	}
}

func TestReporterHonorsTimeoutWithoutRetry(t *testing.T) {
	t.Parallel()

	client := &blockingClient{}
	reporter := NewReporter(client, &Options{Timeout: time.Millisecond})
	reporter.Report(t.Context(), Event{Name: "resource.created"})

	assert.Equal(t, int64(1), client.calls.Load())
	assert.ErrorIs(t, client.err, context.DeadlineExceeded)
}

func TestReporterHonorsCallerCancellation(t *testing.T) {
	t.Parallel()

	client := &blockingClient{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	NewReporter(client, nil).Report(ctx, Event{Name: "resource.created"})

	assert.Equal(t, int64(1), client.calls.Load())
	assert.ErrorIs(t, client.err, context.Canceled)
}

func TestReporterSupportsConcurrentCalls(t *testing.T) {
	t.Parallel()

	client := &concurrentClient{}
	reporter := NewReporter(client, nil)
	var waitGroup sync.WaitGroup
	for range 20 {
		waitGroup.Go(func() {
			reporter.Report(t.Context(), Event{Name: "resource.created"})
		})
	}
	waitGroup.Wait()

	assert.Equal(t, int64(20), client.calls.Load())
}

type blockingClient struct {
	calls atomic.Int64
	err   error
}

type concurrentClient struct {
	calls atomic.Int64
}

func (c *concurrentClient) ReportUsage(
	_ context.Context,
	_ *azdext.ReportUsageRequest,
	_ ...grpc.CallOption,
) (*azdext.ReportUsageResponse, error) {
	c.calls.Add(1)
	return &azdext.ReportUsageResponse{Accepted: true}, nil
}

func (c *blockingClient) ReportUsage(
	ctx context.Context,
	_ *azdext.ReportUsageRequest,
	_ ...grpc.CallOption,
) (*azdext.ReportUsageResponse, error) {
	c.calls.Add(1)
	<-ctx.Done()
	c.err = ctx.Err()
	return nil, c.err
}

func TestReporterLogsStatusWithoutSensitiveDetails(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	client := &recordingClient{err: errors.New("secret transport detail")}
	reporter := NewReporter(client, &Options{Logger: azdext.NewLogger(
		"test",
		azdext.LoggerOptions{Debug: true, Writer: &logs},
	)})
	reporter.Report(t.Context(), Event{
		Name:       "resource.created",
		Attributes: map[string]string{"credential": "customer-secret"},
	})

	assert.True(t, strings.Contains(logs.String(), "resource.created"))
	assert.NotContains(t, logs.String(), "customer-secret")
	assert.NotContains(t, logs.String(), "secret transport detail")
}
