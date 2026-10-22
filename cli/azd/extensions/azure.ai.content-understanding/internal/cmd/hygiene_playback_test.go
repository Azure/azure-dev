// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var recordedHygieneNames = []string{
	"scrub-request-sas-basic",
	"scrub-request-sas-apostrophe-path",
	"scrub-request-sas-apostrophe-query",
	"scrub-response-sas-basic-text",
	"scrub-response-sas-apostrophe-path-text",
	"scrub-response-sas-apostrophe-query-text",
	"scrub-response-sas-basic-bytes",
	"scrub-response-sas-apostrophe-path-bytes",
	"scrub-response-sas-apostrophe-query-bytes",
	"scrub-response-host-and-query",
	"scrub-response-bytes-payload",
}

type recordedHygieneCase struct {
	Name         string          `json:"name"`
	Source       string          `json:"source"`
	BodyEncoding string          `json:"body_encoding"`
	Raw          json.RawMessage `json:"raw"`
	Scrubbed     json.RawMessage `json:"scrubbed"`
}

func loadRecordedHygiene(t *testing.T) []recordedHygieneCase {
	t.Helper()
	body, err := os.ReadFile("../../testdata/parity/hygiene_scenarios.json")
	require.NoError(t, err)
	inventory := loadParityInventory(t)
	require.Equal(t, "testdata/parity/hygiene_scenarios.json", inventory.Hygiene.File)
	require.Equal(t, inventory.Hygiene.SHA256, sha256Text(body))
	var fixture struct {
		SchemaVersion int `json:"schema_version"`
		Source        struct {
			Repository       string            `json:"repository"`
			Commit           string            `json:"commit"`
			License          string            `json:"license"`
			Files            map[string]string `json:"files"`
			SensitiveHeaders []string          `json:"sensitive_headers"`
			SensitiveQuery   []string          `json:"sensitive_query"`
		} `json:"source"`
		Normalizations []string              `json:"normalizations"`
		Cases          []recordedHygieneCase `json:"cases"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	require.NoError(t, decoder.Decode(&fixture))
	require.Equal(t, 1, fixture.SchemaVersion)
	require.Equal(t, "microsoft-foundry/content-understanding-cli", fixture.Source.Repository)
	require.Equal(t, paritySourceCommit, fixture.Source.Commit)
	require.Equal(t, "MIT", fixture.Source.License)
	require.Len(t, fixture.Source.Files, 2)
	for _, path := range []string{"tests/playback/recording.py", "tests/playback/test_hygiene_scenarios.py"} {
		require.Regexp(t, "^[0-9a-f]{64}$", fixture.Source.Files[path])
	}
	require.Equal(t, []string{
		"api-key", "authorization", "ocp-apim-subscription-key", "set-cookie",
		"subscription-key", "x-ms-client-request-id", "x-ms-request-id",
	}, fixture.Source.SensitiveHeaders)
	queryKeys := make([]string, 0, len(recordingSensitiveQuery))
	for key := range recordingSensitiveQuery {
		queryKeys = append(queryKeys, key)
	}
	slices.Sort(queryKeys)
	require.Equal(t, fixture.Source.SensitiveQuery, queryKeys, "cover every source sensitive query key")
	require.Len(t, fixture.Normalizations, 3)
	require.Len(t, fixture.Cases, 11)
	for index, row := range fixture.Cases {
		require.Equal(t, recordedHygieneNames[index], row.Name)
		require.Equal(t, "tests/playback/test_hygiene_scenarios.py::"+
			"test_all_11_offline_recording_hygiene_scenarios["+row.Name+"]", row.Source)
		encoding := "text"
		if strings.HasSuffix(row.Name, "-bytes") || row.Name == "scrub-response-bytes-payload" {
			encoding = "utf8-bytes"
		}
		require.Equal(t, encoding, row.BodyEncoding)
		require.True(t, json.Valid(row.Raw))
		require.True(t, json.Valid(row.Scrubbed))
	}
	return fixture.Cases
}

func reviewedHygieneControl(t *testing.T, name string) []byte {
	t.Helper()
	path := "/container/input.pdf"
	query := url.Values{}
	if strings.Contains(name, "apostrophe-path") {
		path = "/container/customer's-file.pdf"
	} else if strings.Contains(name, "apostrophe-query") {
		query.Set("name", "customer's-file")
	}
	public := "https://storage.example.test" + path
	if len(query) != 0 {
		public += "?" + query.Encode()
	}
	var control any
	if strings.HasPrefix(name, "scrub-request") {
		input, err := json.Marshal(map[string]any{"inputs": []any{map[string]string{"url": public}}})
		require.NoError(t, err)
		control = map[string]any{
			"uri":     "https://sanitized.services.ai.azure.com/contentunderstanding/analyzers/prebuilt-layout:analyze",
			"headers": map[string]string{"Accept": "application/json"}, "body": string(input),
		}
	} else {
		control = map[string]any{
			"headers": map[string]any{
				"operation-location": []string{"https://sanitized.services.ai.azure.com/operations/1"},
			},
			"body": map[string]string{"string": "https://sanitized.services.ai.azure.com/result?source=" +
				url.QueryEscape(public)},
		}
	}
	body, err := json.Marshal(control)
	require.NoError(t, err)
	return body
}

func TestRecordedHygieneScenarios(t *testing.T) {
	for _, row := range loadRecordedHygiene(t) {
		t.Run(row.Name, func(t *testing.T) {
			require.Error(t, recordingHygiene(row.Raw), "reject this exact source unsafe artifact")
			for _, marker := range []string{
				"customer-resource", "customer-secret", "customer-token", "customer-request-id",
			} {
				require.NotContains(t, string(row.Scrubbed), marker,
					"source scrubber oracle satisfies its own postconditions")
			}
			require.Error(t, recordingHygiene(row.Scrubbed),
				"native deliberately also rejects REDACTED credential markers; it does not implement a recorder")
			require.NoError(t, recordingHygiene(reviewedHygieneControl(t, row.Name)),
				"accept reviewed credential-free public controls, including harmless apostrophes and nested URLs")
		})
	}
}

func TestRecordedHygieneIndependentMarkers(t *testing.T) {
	for _, key := range []string{
		"api-key", "authorization", "ocp-apim-subscription-key", "set-cookie",
		"subscription-key", "x-ms-client-request-id", "x-ms-request-id",
	} {
		t.Run("header/"+key, func(t *testing.T) {
			for _, value := range []string{"synthetic-private-value", "REDACTED"} {
				body, err := json.Marshal(map[string]any{"headers": map[string]string{strings.ToUpper(key): value}})
				require.NoError(t, err)
				require.Error(t, recordingHygiene(body), "reject isolated header without a second leak hiding the assertion")
			}
		})
	}
	for key := range recordingSensitiveQuery {
		t.Run("query/"+key, func(t *testing.T) {
			for _, value := range []string{"synthetic-private-value", "REDACTED", ""} {
				query := url.Values{strings.ToUpper(key): {value}}
				body, err := json.Marshal(map[string]string{
					"url": "https://storage.example.test/input.pdf?" + query.Encode(),
				})
				require.NoError(t, err)
				require.Error(t, recordingHygiene(body), "reject isolated source query key without sig/header/host markers")
			}
		})
	}
	for _, text := range []string{
		"https://customer-resource.services.ai.azure.com/result",
		"https://CUSTOMER-RESOURCE.services.ai.azure.com/result",
		"https://storage.example.test/input.pdf?s%76=1",
		"https://storage.example.test/input.pdf?source=https%3A%2F%2Fstorage.example.test%2Finner.pdf%3Fcode%3Dprivate",
		"https://storage.example.test/input.pdf?code=%ZZ",
	} {
		body, err := json.Marshal(map[string]string{"content": text})
		require.NoError(t, err)
		err = recordingHygiene(body)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "customer-resource")
	}
	body, err := json.Marshal(map[string]string{
		"content": "https://sanitized.services.ai.azure.com/result?api-version=2025-11-01&stringEncoding=codePoint",
	})
	require.NoError(t, err)
	require.NoError(t, recordingHygiene(body))
}
