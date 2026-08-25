// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
)

func isTerminalResponseStatus(status string) bool {
	switch status {
	case "completed", "failed", "incomplete", "cancelled":
		return true
	default:
		return false
	}
}

func persistAndPrintBackgroundProgress(
	ctx context.Context,
	store responseStateStore,
	agentKey string,
	record savedBackgroundResponse,
	printedResponseID *string,
	writer io.Writer,
) error {
	if err := store.Save(ctx, agentKey, record); err != nil {
		return err
	}
	if *printedResponseID == "" {
		if _, err := fmt.Fprintf(writer, "Response:     %s\n", record.ResponseID); err != nil {
			return err
		}
		*printedResponseID = record.ResponseID
	}
	return nil
}
