// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"fmt"
	"os"
	"time"
)

const errorReportTimeout = 2 * time.Second

// ReportError sends a structured extension error to the azd host via gRPC.
// It creates a temporary gRPC client using the AZD_SERVER environment variable.
// Returns an error if AZD_SERVER is not set.
func ReportError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}

	server := os.Getenv("AZD_SERVER")
	if server == "" {
		return fmt.Errorf("AZD_SERVER not set")
	}

	extErr := WrapError(err)
	if extErr == nil {
		return nil
	}

	client, clientErr := NewAzdClient(WithAddress(server))
	if clientErr != nil {
		return fmt.Errorf("create gRPC client for error report: %w", clientErr)
	}
	defer client.Close()

	req := &ReportErrorRequest{Error: extErr}
	reportCtx, cancel := newErrorReportContext(ctx)
	defer cancel()
	if _, rpcErr := client.Extension().ReportError(reportCtx, req); rpcErr != nil {
		return fmt.Errorf("report error via gRPC: %w", rpcErr)
	}

	return nil
}

func newErrorReportContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), errorReportTimeout)
}
