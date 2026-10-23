// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiagnosticRedactionDoesNotChangeIntentionalResultPayloads(t *testing.T) {
	envelope := strings.ReplaceAll(analyzeEnvelope, "Picture", "Authorization: Custom private-customer-content")
	for _, full := range []bool{false, true} {
		for _, fileOutput := range []bool{false, true} {
			t.Run(fmt.Sprintf("full=%t/file=%t", full, fileOutput), func(t *testing.T) {
				fixture := newAnalyzeFixture(t)
				fixture.client.call = func(context.Context, cuclient.AnalyzeRequest) (cuclient.AnalyzeResponse, error) {
					return cuclient.AnalyzeResponse{JSON: json.RawMessage(envelope)}, nil
				}
				args := []string{"https://example.com/input.pdf", "--debug"}
				path := filepath.Join(testutil.TempDir(t), "result")
				if full {
					args = append(args, "--llm-input=false")
				}
				if fileOutput {
					args = append(args, "--output-file", path)
				}
				stdout, stderr, err := fixture.execute(t.Context(), args...)
				require.NoError(t, err)
				assert.NotContains(t, stderr, "private-customer-content")
				actual := stdout
				if fileOutput {
					// #nosec G304 -- Reads only this test's intentional result file.
					body, err := os.ReadFile(path)
					require.NoError(t, err)
					actual = string(body)
				}
				if full {
					assert.JSONEq(t, envelope, actual)
				} else {
					assert.Contains(t, actual, "Authorization: Custom private-customer-content")
					assert.Contains(t, actual, "mimeType: image/png")
					assert.False(t, json.Valid([]byte(actual)))
				}
			})
		}
	}
}
