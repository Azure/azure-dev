// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"fmt"
	"os"
)

// ReportError sends a structured extension error to the azd host via gRPC.
// It creates a temporary gRPC client using the AZD_SERVER environment variable.
// Returns nil if AZD_SERVER is not set (extension running outside azd).
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
	if _, rpcErr := client.Extension().ReportError(ctx, req); rpcErr != nil {
		return fmt.Errorf("report error via gRPC: %w", rpcErr)
	}

	return nil
}
