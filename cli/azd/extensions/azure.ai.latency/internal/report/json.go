// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package report

import (
	"encoding/json"
	"fmt"
	"io"

	"azure.ai.latency/internal/model"
)

// RenderJSON writes the public assessment contract as indented JSON to writer.
func RenderJSON(writer io.Writer, result *model.AssessmentResult) error {
	if writer == nil {
		return fmt.Errorf("JSON report writer is required")
	}
	if result == nil {
		return fmt.Errorf("assessment result is required")
	}

	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return fmt.Errorf("write JSON report: %w", err)
	}
	return nil
}
