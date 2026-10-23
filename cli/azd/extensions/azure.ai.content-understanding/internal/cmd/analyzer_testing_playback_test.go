// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var recordedFieldReportCases = []struct {
	name             string
	warnings, failed bool
}{
	{"recorded", false, false},
	{"synthetic-warning-usage", true, false},
	{"synthetic-service-failure", false, true},
}

func normalizeRecordedReport(t *testing.T, body []byte, input string) map[string]any {
	t.Helper()
	decoded, err := recordedJSON(body)
	require.NoError(t, err)
	report, ok := decoded.(map[string]any)
	require.True(t, ok)
	samples, ok := report["samples"].([]any)
	require.True(t, ok)
	require.Len(t, samples, 1)
	sample, ok := samples[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, input, sample["input"], "verify the real input before the only path normalization")
	sample["input"] = "sample_invoice.pdf"
	return report
}

func TestRecordedAnalyzerFieldReport(t *testing.T) {
	for _, test := range recordedFieldReportCases {
		t.Run(test.name, func(t *testing.T) {
			for _, format := range []string{"default", "json", "none"} {
				t.Run(format, func(t *testing.T) {
					synctest.Test(t, func(t *testing.T) {
						fixture := loadRecordedFixture(t, "analyzer_test")
						require.Len(t, fixture.Interactions, 45)
						require.Equal(t, 45, fixture.Source.InteractionCount)
						require.Empty(t, fixture.Source.UnusedInteractions)
						expected := normalizeRecordedReport(t, fixture.ReportExpectation.Report, "sample_invoice.pdf")
						expectedSummary, ok := expected["summary"].(map[string]any)
						require.True(t, ok)
						fields, ok := expectedSummary["fields"].(map[string]any)
						require.True(t, ok)
						require.Len(t, fields, 56, "compare all flattened fields, not just AmountDue.Amount")
						if test.warnings || test.failed {
							last := &fixture.Interactions[len(fixture.Interactions)-1]
							var envelope map[string]json.RawMessage
							require.NoError(t, json.Unmarshal(last.Response.Body, &envelope))
							if test.warnings {
								var result map[string]json.RawMessage
								require.NoError(t, json.Unmarshal(envelope["result"], &result))
								result["warnings"] = json.RawMessage(`[{
									"code":"SyntheticWarning","message":"synthetic-warning-not-for-field-report"
								}]`)
								body, err := json.Marshal(result)
								require.NoError(t, err)
								envelope["result"] = body
								envelope["usage"] = json.RawMessage(`{"documentPagesStandard":9}`)
							} else {
								envelope["status"] = json.RawMessage(`"Failed"`)
								envelope["error"] = json.RawMessage(`{
									"code":"InvalidRequest","message":"synthetic-private-message-not-for-field-report"
								}`)
							}
							body, err := json.Marshal(envelope)
							require.NoError(t, err)
							last.Response.Body = body
						}
						directory := testutil.TempDir(t)
						input := filepath.Join(directory, "sample_invoice.pdf")
						output := filepath.Join(directory, "report.json")
						require.NoError(t, os.WriteFile(input, []byte(recordedBinaryInput), 0o600))
						transport := &recordedTransport{fixture: fixture, binaryBody: []byte(recordedBinaryInput)}
						root := newRecordedCommand(t, transport, "prebuilt-invoice")
						args := []string{"analyzer", "test", "prebuilt-invoice", "--file", input,
							"--output-file", output, "--yes", "-j", "4", "-o", format, "--debug", "--no-prompt",
							"--endpoint", "https://sanitized.services.ai.azure.com",
							"--api-version", cuclient.DefaultAPIVersion}
						var stdout, stderr bytes.Buffer
						root.SetOut(&stdout)
						root.SetErr(&stderr)
						require.NoError(t, ValidateInvocation(root, args))
						root.SetArgs(args)
						start := time.Now()
						runErr := root.ExecuteContext(t.Context())
						require.NoError(t, transport.complete())
						assert.Equal(t, 43*time.Second, time.Since(start), "all 43 Running polls retain their intervals")
						if test.failed {
							require.ErrorContains(t, runErr, "1 analyzer test sample(s) failed")
							failure, err := json.Marshal(map[string]any{
								"analyzerId": "prebuilt-invoice",
								"summary": map[string]any{
									"samplesTotal": 1, "samplesOk": 0, "samplesFailed": 1,
									"disclaimer": expectedSummary["disclaimer"], "fields": map[string]any{},
									"lowConfidenceThreshold": 0.6,
								},
								"samples": []any{map[string]any{
									"input": "sample_invoice.pdf", "status": "error",
									"error": "The analysis operation failed.", "fields": map[string]any{},
								}},
							})
							require.NoError(t, err)
							expected = normalizeRecordedReport(t, failure, "sample_invoice.pdf")
						} else {
							require.NoError(t, runErr)
						}
						// #nosec G304 -- Reads only this test's private temporary report.
						saved, err := os.ReadFile(output)
						require.NoError(t, err)
						assert.Equal(t, expected, normalizeRecordedReport(t, saved, input),
							"independent Python oracle includes every value, confidence, statistic, null and disclaimer")
						if format == "none" || test.failed {
							assert.Empty(t, stdout.String())
						} else {
							assert.Equal(t, expected, normalizeRecordedReport(t, stdout.Bytes(), input))
						}
						assert.Empty(t, stderr.String())
						for _, text := range []string{string(saved), stdout.String(), stderr.String()} {
							assert.NotContains(t, text, "synthetic-warning-not-for-field-report")
							assert.NotContains(t, text, "synthetic-private-message-not-for-field-report")
							assert.NotContains(t, text, `"usage"`)
							assert.NotContains(t, text, `"warnings"`)
						}
					})
				})
			}
		})
	}
}
