// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package telemetry

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	calls    int
}

func (c *recordingClient) ReportUsage(
	ctx context.Context,
	request *azdext.ReportUsageRequest,
	_ ...grpc.CallOption,
) (*azdext.ReportUsageResponse, error) {
	c.ctx = ctx
	c.request = request
	c.calls++
	return c.response, c.err
}

func TestReporterForwardsEvent(t *testing.T) {
	t.Parallel()

	client := &recordingClient{response: &azdext.ReportUsageResponse{Accepted: true}}
	reporter := NewReporter(client, nil)
	reporter.Report(t.Context(), Event{
		Name:       "resource.created",
		Attributes: map[string]string{"mode": "declarative"},
	})

	require.NotNil(t, client.request)
	assert.Equal(t, "resource.created", client.request.EventName)
	assert.Equal(t, map[string]string{"mode": "declarative"}, client.request.Attributes)
	assert.Equal(t, 1, client.calls)
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
			var logs []string
			reporter := NewReporter(test.client, &Options{
				Debugf: func(format string, args ...any) {
					logs = append(logs, fmt.Sprintf(format, args...))
				},
			})
			reporter.Report(t.Context(), Event{
				Name:       "resource.created",
				Attributes: map[string]string{"credential": "customer-secret"},
			})

			assert.Equal(t, test.wantLogs, len(logs) > 0)
			assert.NotContains(t, strings.Join(logs, " "), "customer-secret")
			assert.NotContains(t, strings.Join(logs, " "), "secret transport detail")
		})
	}
}

func TestReporterHonorsTimeoutWithoutRetry(t *testing.T) {
	t.Parallel()

	client := &blockingClient{}
	reporter := NewReporter(client, &Options{Timeout: time.Millisecond})
	reporter.Report(t.Context(), Event{Name: "resource.created"})

	assert.Equal(t, 1, client.calls)
	assert.ErrorIs(t, client.err, context.DeadlineExceeded)
}

type blockingClient struct {
	calls int
	err   error
}

func (c *blockingClient) ReportUsage(
	ctx context.Context,
	_ *azdext.ReportUsageRequest,
	_ ...grpc.CallOption,
) (*azdext.ReportUsageResponse, error) {
	c.calls++
	<-ctx.Done()
	c.err = ctx.Err()
	return nil, c.err
}
