// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
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

var recordedScrubberNames = []string{
	"scrub-request-headers-sas", "scrub-response-text", "scrub-response-bytes",
}

type recordedHygieneCase struct {
	Name         string          `json:"name"`
	Source       string          `json:"source"`
	BodyEncoding string          `json:"body_encoding"`
	Raw          json.RawMessage `json:"raw"`
	Scrubbed     json.RawMessage `json:"scrubbed"`
}

type recordedHygieneFixture struct {
	SchemaVersion int `json:"schema_version"`
	Source        struct {
		Repository       string            `json:"repository"`
		Commit           string            `json:"commit"`
		License          string            `json:"license"`
		Files            map[string]string `json:"files"`
		SensitiveHeaders []string          `json:"sensitive_headers"`
		SensitiveQuery   []string          `json:"sensitive_query"`
	} `json:"source"`
	Normalizations     []string              `json:"normalizations"`
	Cases              []recordedHygieneCase `json:"cases"`
	Helpers            []recordedHygieneCase `json:"helpers"`
	ExpectedRecordings []string              `json:"expected_recordings"`
}

func loadRecordedHygiene(t *testing.T) recordedHygieneFixture {
	t.Helper()
	body, err := os.ReadFile("../../testdata/parity/hygiene_scenarios.json")
	require.NoError(t, err)
	inventory := loadParityInventory(t)
	require.Equal(t, "testdata/parity/hygiene_scenarios.json", inventory.Hygiene.File)
	require.Equal(t, inventory.Hygiene.SHA256, sha256Text(body))
	var fixture recordedHygieneFixture
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	require.NoError(t, decoder.Decode(&fixture))
	require.Equal(t, 2, fixture.SchemaVersion)
	require.Equal(t, "microsoft-foundry/content-understanding-cli", fixture.Source.Repository)
	require.Equal(t, paritySourceCommit, fixture.Source.Commit)
	require.Equal(t, "MIT", fixture.Source.License)
	require.Len(t, fixture.Source.Files, 3)
	for _, path := range []string{
		"tests/playback/recording.py", "tests/playback/test_hygiene_scenarios.py", "tests/playback/test_recording.py",
	} {
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
	require.Len(t, fixture.Helpers, 3)
	helperNodes := []string{
		"tests/playback/test_recording.py::test_request_scrubber_removes_host_headers_and_sas_values",
		"tests/playback/test_recording.py::test_response_scrubber_removes_host_and_sensitive_query[False]",
		"tests/playback/test_recording.py::test_response_scrubber_removes_host_and_sensitive_query[True]",
	}
	for index, row := range fixture.Helpers {
		require.Equal(t, recordedScrubberNames[index], row.Name)
		require.Equal(t, helperNodes[index], row.Source)
		encoding := "text"
		if row.Name == "scrub-response-bytes" {
			encoding = "utf8-bytes"
		}
		require.Equal(t, encoding, row.BodyEncoding)
		require.True(t, json.Valid(row.Raw))
		require.True(t, json.Valid(row.Scrubbed))
	}
	require.Len(t, fixture.ExpectedRecordings, 19)
	require.True(t, slices.IsSorted(fixture.ExpectedRecordings))
	return fixture
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
	for _, row := range loadRecordedHygiene(t).Cases {
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

func TestRecordedScrubberHelpers(t *testing.T) {
	for _, row := range loadRecordedHygiene(t).Helpers {
		t.Run(row.Name, func(t *testing.T) {
			require.Error(t, recordingHygiene(row.Raw))
			require.Error(t, recordingHygiene(row.Scrubbed), "native deliberately rejects REDACTED metadata")
			require.NoError(t, recordingHygiene(reviewedHygieneControl(t, row.Name)))
			if row.Name == "scrub-request-headers-sas" {
				var request struct {
					URI     string            `json:"uri"`
					Headers map[string]string `json:"headers"`
					Body    string            `json:"body"`
				}
				require.NoError(t, json.Unmarshal(row.Scrubbed, &request))
				require.NotContains(t, request.URI, "real-resource")
				require.Equal(t, "REDACTED", request.Headers["api-key"])
				require.NotContains(t, request.Body, "secret")
				require.Contains(t, request.Body, "sig=REDACTED")
				require.Contains(t, request.Body, "sv=REDACTED")
			} else {
				var response struct {
					Headers map[string][]string `json:"headers"`
					Body    struct {
						String string `json:"string"`
					} `json:"body"`
				}
				require.NoError(t, json.Unmarshal(row.Scrubbed, &response))
				require.Equal(t, []string{"REDACTED"}, response.Headers["x-ms-request-id"])
				require.NotContains(t, response.Body.String, "real-resource")
				require.NotContains(t, response.Body.String, "secret")
				require.Contains(t, response.Body.String, "code=REDACTED")
				require.Contains(t, response.Body.String, "sv=REDACTED")
			}
		})
	}
}

func recordedImportCompleteness(expected []string, imports []parityRecording, files []string) error {
	wanted := slices.Clone(expected)
	slices.Sort(wanted)
	imported := make([]string, 0, len(imports))
	for _, entry := range imports {
		imported = append(imported, entry.Name+".yaml")
	}
	slices.Sort(imported)
	present := make([]string, 0, len(files))
	for _, file := range files {
		present = append(present, strings.TrimSuffix(filepath.Base(file), ".json")+".yaml")
	}
	slices.Sort(present)
	if !slices.Equal(wanted, imported) || !slices.Equal(wanted, present) {
		return errors.New("Recording import/file set differs from the pinned source expectation.")
	}
	return nil
}

func TestRecordedImportCompleteness(t *testing.T) {
	fixture := loadRecordedHygiene(t)
	imports := loadParityInventory(t).Recordings
	files, err := filepath.Glob("../../testdata/playback/*.json")
	require.NoError(t, err)
	require.NoError(t, recordedImportCompleteness(fixture.ExpectedRecordings, imports, files))
	for _, entry := range imports {
		t.Run(entry.Name, func(t *testing.T) { _ = loadRecordedFixture(t, entry.Name) })
	}
}

func TestRecordedImportCompletenessRejectsDrift(t *testing.T) {
	expected := loadRecordedHygiene(t).ExpectedRecordings
	imports := loadParityInventory(t).Recordings
	files, err := filepath.Glob("../../testdata/playback/*.json")
	require.NoError(t, err)
	for _, test := range []struct {
		name    string
		imports []parityRecording
		files   []string
	}{
		{"missing import", imports[1:], files},
		{"duplicate import", append(slices.Clone(imports), imports[0]), files},
		{"extra import", append(slices.Clone(imports), parityRecording{Name: "unreviewed"}), files},
		{"missing file", imports, files[1:]},
		{"duplicate file", imports, append(slices.Clone(files), files[0])},
		{"extra file", imports, append(slices.Clone(files), "../../testdata/playback/unreviewed.json")},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Error(t, recordedImportCompleteness(expected, test.imports, test.files))
		})
	}
}

func TestRecordedRequestOrigins(t *testing.T) {
	for _, raw := range []string{
		"https://other.example.test/input", "http://sanitized.services.ai.azure.com/input",
		"https://sanitized.services.ai.azure.com:443/input", "https://user@sanitized.services.ai.azure.com/input",
		"//sanitized.services.ai.azure.com/input", "https://sanitized.services.ai.azure.com.evil/input",
		"https://sanitized.services.ai.azure.com/%ZZ",
	} {
		err := recordingRequestOrigin(raw)
		require.Error(t, err)
		require.NotContains(t, err.Error(), raw)
	}
	require.NoError(t, recordingRequestOrigin("https://sanitized.services.ai.azure.com/contentunderstanding/analyze"))
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

func TestRecordedSourceArtifactMarkers(t *testing.T) {
	reject := func(t *testing.T, text string) {
		t.Helper()
		body, err := json.Marshal(map[string]any{"payload": []any{map[string]string{"content": text}}})
		require.NoError(t, err)
		err = recordingHygiene(body)
		require.Error(t, err, "reject isolated source marker %q", text)
		assert.NotContains(t, err.Error(), text)
	}
	t.Run("private", func(t *testing.T) {
		for _, text := range []string{
			"/home/customer/input.pdf", "/Users/customer/input.pdf", `C:\Users\customer\input.pdf`,
			"https://dev.azure.com/synthetic", "https://unit.visualstudio.com/synthetic",
			"coreai-microsoft", "msazure", "https://v\u0130sualstud\u0131o.com/synthetic",
		} {
			reject(t, text)
			reject(t, strings.ToUpper(text))
		}
	})
	for _, key := range []string{
		"api-key", "api_key", "apikey", "authorization", "sig",
		"subscription-key", "subscription_key", "subscriptionkey", "ap\u0130key", "s\u0131g",
	} {
		t.Run("secret/"+key, func(t *testing.T) {
			for _, separator := range []string{
				" ", "\t", "\n", "\r", "\f", "\v", "'", "\"", "=", ":", " \t\"'=: ",
				"\u001c", "\u001d", "\u001e", "\u001f", "\u0085", "\u00a0", "\u1680",
				"\u2000", "\u2001", "\u2002", "\u2003", "\u2004", "\u2005", "\u2006",
				"\u2007", "\u2008", "\u2009", "\u200a", "\u2028", "\u2029", "\u202f", "\u205f", "\u3000",
			} {
				for _, value := range []string{
					"abcdefgh", "12345678", "%+/_-ABC", "a\u0130b\u0131cdef", "a\u017fb\u212acdef", "REDACTED",
				} {
					reject(t, key+separator+value)
					reject(t, strings.ToUpper(key)+separator+value)
				}
			}
		})
	}
	body, err := json.Marshal(map[string]any{
		"payload": []any{map[string]string{
			"content": "https://docs.example.test/public?api-version=2025-11-01 signature public",
		}},
	})
	require.NoError(t, err)
	require.NoError(t, recordingHygiene(body))
}
