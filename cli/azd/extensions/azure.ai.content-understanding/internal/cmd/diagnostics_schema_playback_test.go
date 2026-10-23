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
	"azure.ai.content-understanding/internal/schema"
	"azure.ai.content-understanding/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var recordedDoctorCases = []string{"healthy", "synthetic-empty", "synthetic-unset", "synthetic-not-found"}
var recordedSuggestionCases = []string{"recorded", "synthetic-warning-usage", "synthetic-empty"}

func recordedCommandObject(t *testing.T, body []byte) map[string]any {
	t.Helper()
	value, err := recordedJSON(body)
	require.NoError(t, err)
	object, ok := value.(map[string]any)
	require.True(t, ok)
	return object
}

func TestRecordedDoctorCommands(t *testing.T) {
	for _, name := range recordedDoctorCases {
		t.Run(name, func(t *testing.T) {
			for _, environment := range []string{"projectless", "recorded-env"} {
				t.Run(environment, func(t *testing.T) {
					for _, format := range []string{"default", "json", "none"} {
						t.Run(format, func(t *testing.T) {
							fixture := loadRecordedFixture(t, "doctor")
							require.Len(t, fixture.Interactions, 1)
							require.Equal(t, "GET", fixture.Interactions[0].Request.Method)
							azure := recordedCommandObject(t, fixture.CommandExpectation.Results["azure"])
							expected := recordedCommandObject(t, fixture.CommandExpectation.Results["azd"])
							require.Equal(t, "resource key", azure["authentication"])
							require.Equal(t, "playback", azure["profile"])
							require.Equal(t, "API key", expected["authentication"])
							require.Equal(t, "projectless", expected["profile"])
							azure["authentication"], azure["profile"] = "API key", "projectless"
							require.Equal(t, azure, expected, "no other source report members may differ")
							require.Equal(t, "https://sanitized.services.ai.azure.com/", expected["endpoint"])
							expected["authentication"] = "Microsoft Entra ID"
							expected["profile"] = environment
							expected["endpoint"] = "https://sanitized.services.ai.azure.com"
							// This factory supplies service results, not a config resolver.
							expected["sources"] = nil
							if name != "healthy" {
								if name == "synthetic-empty" {
									fixture.Interactions[0].Response.Body = json.RawMessage(`{"modelDeployments":{}}`)
								} else {
									fixture.Interactions[0].Response.Status = 404
									code := "DefaultsNotSet"
									if name == "synthetic-not-found" {
										code = "ResourceNotFound"
									}
									body, err := json.Marshal(map[string]any{
										"error": map[string]any{"code": code, "message": "synthetic-private-doctor-detail"},
									})
									require.NoError(t, err)
									fixture.Interactions[0].Response.Body = body
								}
								expected["ready"] = false
								expected["modelDeployments"] = map[string]any{}
								expected["missingRequirements"] = []any{
									"an embeddings model (for example text-embedding-3-large)",
									"a supported large language model (LLM) deployment",
									"Content Understanding's prebuilt analyzer mapping for the selected LLM",
								}
							}
							transport := &recordedTransport{fixture: fixture}
							root := newRecordedCommand(t, transport, "")
							args := []string{"doctor", "-o", format, "--debug", "--no-prompt",
								"--endpoint", "https://sanitized.services.ai.azure.com",
								"--api-version", cuclient.DefaultAPIVersion}
							if environment != "projectless" {
								args = append(args, "--environment", environment)
							}
							var stdout, stderr bytes.Buffer
							root.SetOut(&stdout)
							root.SetErr(&stderr)
							require.NoError(t, ValidateInvocation(root, args))
							root.SetArgs(args)
							err := root.ExecuteContext(t.Context())
							require.NoError(t, transport.complete())
							if name == "synthetic-not-found" {
								require.Error(t, err, "an unrelated 404 is not missing generative configuration")
								assert.NotContains(t, err.Error(), "synthetic-private-doctor-detail")
								assert.Empty(t, stdout.String())
							} else {
								require.NoError(t, err)
								if format == "none" {
									assert.Empty(t, stdout.String())
								} else if format == "json" {
									assert.Equal(t, expected, recordedCommandObject(t, stdout.Bytes()),
										"compare the complete report, not readiness or model names alone")
								} else {
									text := stdout.String()
									assert.False(t, json.Valid(stdout.Bytes()))
									assert.Contains(t, text, "Context: "+environment)
									assert.Contains(t, text, "CU_ENDPOINT: https://sanitized.services.ai.azure.com")
									assert.Contains(t, text, "CU_API_VERSION: "+cuclient.DefaultAPIVersion)
									assert.Contains(t, text, "CU_DEFAULT_ANALYZER: (not set)")
									assert.Contains(t, text,
										"[OK] Authentication and CU service access (Microsoft Entra ID)")
									if name == "healthy" {
										assert.Contains(t, text, "[OK] Generative readiness: ready")
										models, ok := expected["modelDeployments"].(map[string]any)
										require.True(t, ok)
										for model, deployment := range models {
											value, ok := deployment.(string)
											require.True(t, ok)
											assert.Contains(t, text, model+": "+value)
										}
									} else {
										assert.Contains(t, text, "[WARN] Generative readiness: not ready")
										assert.Contains(t, text, "(none configured)")
										missing, ok := expected["missingRequirements"].([]any)
										require.True(t, ok)
										for _, requirement := range missing {
											value, ok := requirement.(string)
											require.True(t, ok)
											assert.Contains(t, text, value)
										}
										assert.Contains(t, text, "azd ai cu defaults set --model MODEL=DEPLOYMENT")
									}
								}
							}
							assert.Empty(t, stderr.String())
							assert.NotContains(t, stdout.String(), "synthetic-private-doctor-detail")
						})
					}
				})
			}
		})
	}
}

func TestRecordedSchemaSuggestion(t *testing.T) {
	for _, name := range recordedSuggestionCases {
		t.Run(name, func(t *testing.T) {
			for _, format := range []string{"default", "json", "none"} {
				t.Run(format, func(t *testing.T) {
					synctest.Test(t, func(t *testing.T) {
						fixture := loadRecordedFixture(t, "schema_suggest")
						require.Len(t, fixture.Interactions, 51)
						require.Equal(t, 51, fixture.Source.InteractionCount)
						require.Empty(t, fixture.Source.UnusedInteractions)
						require.Contains(t, fixture.Interactions[0].Request.URL,
							"/prebuilt-documentFieldSchema:analyzeBinary?")
						require.Equal(t, "application/pdf", fixture.Interactions[0].Request.Headers["Content-Type"])
						azure := recordedCommandObject(t, fixture.CommandExpectation.Results["azure"])
						expected := recordedCommandObject(t, fixture.CommandExpectation.Results["azd"])
						azureModels, ok := azure["models"].(map[string]any)
						require.True(t, ok)
						nativeModels, ok := expected["models"].(map[string]any)
						require.True(t, ok)
						require.Equal(t, "gpt-4.1", azureModels["completion"])
						require.Equal(t, "gpt-5.2", nativeModels["completion"])
						azureModels["completion"] = "gpt-5.2"
						require.Equal(t, azure, expected,
							"preserve all source schema content except the declared model choice")
						fieldSchema, ok := expected["fieldSchema"].(map[string]any)
						require.True(t, ok)
						fields, ok := fieldSchema["fields"].(map[string]any)
						require.True(t, ok)
						require.Len(t, fields, 20)
						expectedWarning := ""
						if name != "recorded" {
							last := &fixture.Interactions[len(fixture.Interactions)-1]
							envelope := recordedCommandObject(t, last.Response.Body)
							result, ok := envelope["result"].(map[string]any)
							require.True(t, ok)
							if name == "synthetic-warning-usage" {
								result["warnings"] = []any{map[string]any{
									"code": "SyntheticWarning", "message": "synthetic-private-suggestion-detail",
								}}
								envelope["usage"] = map[string]any{"documentPagesStandard": 9}
								expectedWarning = "Warning: CU returned 1 schema-suggestion warning(s); " +
									"review the generated schema.\n"
							} else {
								contents, ok := result["contents"].([]any)
								require.True(t, ok)
								require.Len(t, contents, 1)
								content, ok := contents[0].(map[string]any)
								require.True(t, ok)
								resultFields, ok := content["fields"].(map[string]any)
								require.True(t, ok)
								suggestion, ok := resultFields["schema"].(map[string]any)
								require.True(t, ok)
								suggestion["valueJson"] = map[string]any{}
								starter := recordedTemplateGolden(t, "extraction", "my_analyzer_v1")
								expected = recordedCommandObject(t, starter)
								expectedWarning = "Warning: No suggested fields were returned; " +
									"using the default extraction template.\n"
							}
							body, err := json.Marshal(envelope)
							require.NoError(t, err)
							last.Response.Body = body
						}
						directory := testutil.TempDir(t)
						input := filepath.Join(directory, "sample_invoice.pdf")
						output := filepath.Join(directory, "suggested.json")
						require.NoError(t, os.WriteFile(input, []byte(recordedBinaryInput), 0o600))
						transport := &recordedTransport{fixture: fixture, binaryBody: []byte(recordedBinaryInput)}
						root := newRecordedCommand(t, transport, schema.SuggestionAnalyzer)
						args := []string{"analyzer", "schema", "create", "--from-sample", input,
							"--name", "my_analyzer_v1", "--output-file", output, "-o", format, "--debug", "--no-prompt",
							"--endpoint", "https://sanitized.services.ai.azure.com",
							"--api-version", cuclient.DefaultAPIVersion}
						var stdout, stderr bytes.Buffer
						root.SetOut(&stdout)
						root.SetErr(&stderr)
						require.NoError(t, ValidateInvocation(root, args))
						root.SetArgs(args)
						start := time.Now()
						require.NoError(t, root.ExecuteContext(t.Context()))
						require.NoError(t, transport.complete())
						assert.Equal(t, 49*time.Second, time.Since(start), "all 49 Running polls retain their intervals")
						// #nosec G304 -- Reads only this test's private temporary schema output.
						body, err := os.ReadFile(output)
						require.NoError(t, err)
						assert.Equal(t, expected, recordedCommandObject(t, body),
							"compare every saved field, nested description, model and configuration value")
						if format == "none" {
							assert.Empty(t, stdout.String())
						} else {
							assert.Equal(t, expected, recordedCommandObject(t, stdout.Bytes()))
						}
						assert.Equal(t, expectedWarning, stderr.String())
						for _, text := range []string{string(body), stdout.String(), stderr.String()} {
							assert.NotContains(t, text, "synthetic-private-suggestion-detail")
							assert.NotContains(t, text, `"usage"`)
							assert.NotContains(t, text, `"warnings"`)
						}
					})
				})
			}
		})
	}
}
