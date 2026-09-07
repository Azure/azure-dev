// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package telemetry reports best-effort usage events from Microsoft Foundry extensions.
package telemetry

import (
	"context"
	"maps"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

const defaultReportTimeout = time.Second

// Event is an extension-owned usage event.
type Event struct {
	Name       string
	Attributes map[string]string
}

// Reporter records usage events without affecting the caller's command result.
type Reporter interface {
	Report(ctx context.Context, event Event)
}

// Client is the telemetry RPC used by Reporter.
type Client interface {
	ReportUsage(
		ctx context.Context,
		request *azdext.ReportUsageRequest,
		options ...grpc.CallOption,
	) (*azdext.ReportUsageResponse, error)
}

// Options configures a Reporter.
type Options struct {
	Timeout time.Duration
	Logger  *azdext.Logger
}

type reporter struct {
	client  Client
	timeout time.Duration
	logger  *azdext.Logger
}

// NewReporter creates a best-effort usage reporter backed by the azd host.
func NewReporter(client Client, options *Options) Reporter {
	timeout := defaultReportTimeout
	logger := azdext.NewLogger("foundry.telemetry")
	if options != nil {
		if options.Timeout > 0 {
			timeout = options.Timeout
		}
		if options.Logger != nil {
			logger = options.Logger
		}
	}

	return &reporter{client: client, timeout: timeout, logger: logger}
}

func (r *reporter) Report(ctx context.Context, event Event) {
	if r.client == nil {
		return
	}

	reportCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	response, err := r.client.ReportUsage(reportCtx, &azdext.ReportUsageRequest{
		EventName:  event.Name,
		Attributes: maps.Clone(event.Attributes),
	})
	if err != nil {
		r.logger.Debug(
			"telemetry event was not reported",
			"event", event.Name,
			"code", status.Code(err).String(),
		)
		return
	}
	if response == nil {
		r.logger.Debug("telemetry event returned an empty response", "event", event.Name)
	}
}
