// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azure.ai.content-understanding/internal/cuclient"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiagnosticRedactionDoesNotChangeIntentionalResultPayloads(t *testing.T) {
	envelope := strings.ReplaceAll(analyzeEnvelope, "Picture", "Authorization: Custom private-customer-content")
	for _, fileOutput := range []bool{false, true} {
		fixture := newAnalyzeFixture(t)
		fixture.client.call = func(context.Context, cuclient.AnalyzeRequest) (cuclient.AnalyzeResponse, error) {
			return cuclient.AnalyzeResponse{JSON: json.RawMessage(envelope)}, nil
		}
		args := []string{"https://example.com/input.pdf", "--debug"}
		path := filepath.Join(t.TempDir(), "result.json")
		if fileOutput {
			args = append(args, "--output-file", path)
		}
		stdout, stderr, err := fixture.execute(t.Context(), args...)
		require.NoError(t, err)
		assert.NotContains(t, stderr, "private-customer-content")
		if fileOutput {
			// #nosec G304 -- Reads only this test's intentional result file.
			body, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.JSONEq(t, envelope, string(body))
		} else {
			assert.JSONEq(t, envelope, stdout)
		}
	}
}
