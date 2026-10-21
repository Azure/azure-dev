// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azure.ai.content-understanding/internal/analysis"
	"azure.ai.content-understanding/internal/cuclient"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type authoringGoldens struct {
	Source      string                     `json:"source"`
	Templates   map[string]json.RawMessage `json:"templates"`
	Suggestions []struct {
		Name     string             `json:"name"`
		Contents []cuclient.Content `json:"contents"`
		Expected json.RawMessage    `json:"expected"`
		Found    bool               `json:"found"`
	} `json:"suggestions"`
}

func readAuthoringGoldens(t *testing.T) authoringGoldens {
	t.Helper()
	body, err := os.ReadFile("testdata/authoring-python.json")
	require.NoError(t, err)
	var goldens authoringGoldens
	require.NoError(t, json.Unmarshal(body, &goldens))
	require.Contains(t, goldens.Source, "25d39b30116a840491b7b6f393d6db792b38d4a4")
	return goldens
}

func exactJSON(t *testing.T, raw []byte) any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var result any
	require.NoError(t, decoder.Decode(&result))
	return result
}

func TestSchemaStarterPythonParity(t *testing.T) {
	goldens := readAuthoringGoldens(t)
	for kind, golden := range goldens.Templates {
		for _, modality := range []string{"document", "image", "audio", "video"} {
			for _, version := range []string{cuclient.DefaultAPIVersion, cuclient.PreviewAPIVersion} {
				t.Run(kind+"/"+modality+"/"+version, func(t *testing.T) {
					result, err := Starter(AuthorOptions{
						Name: " invoice_v1 ", Modality: modality, Type: kind,
					}, version)
					require.NoError(t, err)
					expected, ok := exactJSON(t, golden).(map[string]any)
					require.True(t, ok)
					expected["baseAnalyzerId"], expected["apiVersion"] = "prebuilt-"+modality, version
					if kind == "extraction" {
						expected["description"] = "TODO: one-sentence description of what this " +
							modality + " analyzer extracts."
					}
					body, err := json.Marshal(result)
					require.NoError(t, err)
					assert.Equal(t, expected, exactJSON(t, body))
					validation, err := Validate(body, version, true)
					require.NoError(t, err)
					assert.Empty(t, validation.Errors)
					assert.Empty(t, validation.Warnings)
				})
			}
		}
	}
	result, err := Starter(AuthorOptions{
		Name: "custom_v1", Base: "prebuilt-documentSearch", Modality: "image", Type: "extraction",
	}, "")
	require.NoError(t, err)
	assert.Equal(t, "prebuilt-documentSearch", result.BaseAnalyzerID)
	assert.Equal(t, cuclient.DefaultAPIVersion, result.APIVersion)
	assert.Equal(t, "custom_v1_schema", result.FieldSchema.Name)
	result.FieldSchema.Fields["example_summary"] = json.RawMessage(`null`)
	fresh, err := Starter(AuthorOptions{Name: "other", Modality: "document", Type: "extraction"}, "")
	require.NoError(t, err)
	assert.NotEqual(t, result.FieldSchema.Fields["example_summary"], fresh.FieldSchema.Fields["example_summary"])
}

type suggestionClient struct {
	cuclient.Client
	call func(context.Context, cuclient.AnalyzeRequest) (cuclient.AnalyzeResponse, error)
}

func (c suggestionClient) Analyze(ctx context.Context, request cuclient.AnalyzeRequest) (cuclient.AnalyzeResponse, error) {
	return c.call(ctx, request)
}

func authorSample(t *testing.T, name string) (string, analysis.Input) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte("sample bytes"), 0o600))
	input, err := analysis.DiscoverFile(t.Context(), path)
	require.NoError(t, err)
	return path, input
}

func TestSchemaSuggestionPythonParityAndReaderOwnership(t *testing.T) {
	goldens := readAuthoringGoldens(t)
	for _, vector := range goldens.Suggestions {
		t.Run(vector.Name, func(t *testing.T) {
			path, input := authorSample(t, "invoice[1].pdf")
			var reader io.ReadSeeker
			client := suggestionClient{call: func(
				_ context.Context, request cuclient.AnalyzeRequest,
			) (cuclient.AnalyzeResponse, error) {
				assert.Equal(t, SuggestionAnalyzer, request.AnalyzerID)
				assert.Equal(t, cuclient.AnalysisModeAsync, request.Mode)
				assert.Empty(t, request.Input.URL)
				assert.Equal(t, "application/pdf", request.Input.ContentType)
				reader = request.Input.Body
				body, err := io.ReadAll(reader)
				require.NoError(t, err)
				assert.Equal(t, "sample bytes", string(body))
				return cuclient.AnalyzeResponse{Result: cuclient.AnalysisResult{
					Contents: vector.Contents, Warnings: []cuclient.Diagnostic{{Code: "Review", Message: "private warning"}},
				}}, nil
			}}
			suggestion, err := Suggest(t.Context(), client, input, AuthorOptions{
				Name: "invoice_v1", Modality: "document", Type: "extraction", Sample: path,
			}, cuclient.DefaultAPIVersion)
			require.NoError(t, err)
			body, err := json.Marshal(suggestion.Definition)
			require.NoError(t, err)
			assert.Equal(t, exactJSON(t, vector.Expected), exactJSON(t, body))
			assert.Equal(t, vector.Found, suggestion.FoundFields)
			assert.Equal(t, "Review", suggestion.Warnings[0].Code)
			if vector.Name == "nested-placeholders" {
				assert.Contains(t, string(body), "9007199254740993123456789")
			}
			_, err = reader.Read(make([]byte, 1))
			require.ErrorIs(t, err, os.ErrClosed)
		})
	}
}

func TestSchemaAuthoringValidationAndFormats(t *testing.T) {
	for _, options := range []AuthorOptions{
		{Name: "", Modality: "document", Type: "extraction"},
		{Name: "invalid-id", Modality: "document", Type: "extraction"},
		{Name: strings.Repeat("a", 65), Modality: "document", Type: "extraction"},
		{Name: "invoice", Base: "bad/base", Modality: "document", Type: "extraction"},
		{Name: "invoice", Modality: "unknown", Type: "extraction"},
		{Name: "invoice", Modality: "document", Type: "invalid"},
		{Name: "invoice", Modality: "document", Type: "classification", Sample: "one.pdf"},
		{Name: "invoice", Modality: "image", Type: "extraction", Sample: "one.pdf"},
		{Name: "invoice", Base: "prebuilt-document", Modality: "document", Type: "extraction", Sample: "one.pdf"},
		{Name: "invoice", Modality: "document", Type: "extraction", Sample: "one.pdf", FromTemplate: true},
	} {
		_, err := Starter(options, "")
		assert.Error(t, err, "%+v", options)
	}
	_, err := Starter(AuthorOptions{Name: "invoice", Modality: "document", Type: "extraction"}, "invalid")
	assert.Error(t, err)
	for _, extension := range []string{
		".pdf", ".PDF", ".tiff", ".docx", ".xlsx", ".pptx", ".docm", ".xlsm", ".pptm", ".doc", ".xls", ".ppt",
		".odt", ".ods", ".odp", ".epub", ".txt", ".html", ".md", ".rtf", ".xml", ".json", ".csv", ".tsv",
		".kml", ".eml", ".msg",
	} {
		assert.NoError(t, ValidateSample("sample"+extension))
	}
	for _, extension := range []string{"", ".jpg", ".mp3", ".mp4", ".zip", ".tif"} {
		assert.Error(t, ValidateSample("sample"+extension))
	}
}

func TestSchemaSuggestionFailuresCancellationAndInputReplacement(t *testing.T) {
	for _, mode := range []string{"service", "cancelled", "malformed", "removed", "changed", "closed"} {
		t.Run(mode, func(t *testing.T) {
			path, input := authorSample(t, "sample.pdf")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			privateErr := errors.New("private backend diagnostic")
			var reader io.ReadSeeker
			client := suggestionClient{call: func(
				_ context.Context, request cuclient.AnalyzeRequest,
			) (cuclient.AnalyzeResponse, error) {
				reader = request.Input.Body
				if mode == "cancelled" {
					cancel()
					return cuclient.AnalyzeResponse{}, nil
				}
				if mode == "malformed" {
					return cuclient.AnalyzeResponse{}, nil
				}
				if mode == "closed" {
					require.NoError(t, request.Input.Body.(*os.File).Close())
				}
				return cuclient.AnalyzeResponse{}, privateErr
			}}
			if mode == "removed" || mode == "changed" {
				require.NoError(t, os.Rename(path, path+".original"))
				if mode == "changed" {
					require.NoError(t, os.WriteFile(path, []byte("replacement"), 0o600))
				}
			}
			result, err := Suggest(ctx, client, input, AuthorOptions{
				Name: "invoice", Modality: "document", Type: "extraction", Sample: path,
			}, "")
			require.Error(t, err)
			assert.Empty(t, result.Definition.AnalyzerID)
			switch mode {
			case "cancelled":
				assert.ErrorIs(t, err, context.Canceled)
			case "service", "closed":
				assert.ErrorIs(t, err, privateErr)
			}
			if reader != nil {
				_, err = reader.Read(make([]byte, 1))
				assert.ErrorIs(t, err, os.ErrClosed)
			}
		})
	}
	path, input := authorSample(t, "sample.txt")
	client := suggestionClient{call: func(
		_ context.Context, request cuclient.AnalyzeRequest,
	) (cuclient.AnalyzeResponse, error) {
		assert.Equal(t, "text/plain", request.Input.ContentType)
		return cuclient.AnalyzeResponse{Result: cuclient.AnalysisResult{Contents: []cuclient.Content{}}}, nil
	}}
	options := AuthorOptions{Name: "invoice", Modality: "document", Type: "extraction", Sample: path}
	result, err := Suggest(t.Context(), client, input, options, "")
	require.NoError(t, err)
	assert.False(t, result.FoundFields)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = Suggest(ctx, client, input, options, "")
	assert.ErrorIs(t, err, context.Canceled)
	for _, invalid := range []AuthorOptions{
		{Name: "invalid-id", Modality: "document", Type: "extraction", Sample: path},
		{Name: "invoice", Modality: "document", Type: "extraction"},
		{Name: "invoice", Modality: "document", Type: "extraction", Sample: "sample.zip"},
	} {
		_, err = Suggest(t.Context(), client, input, invalid, "")
		assert.Error(t, err)
	}
	_, err = Suggest(t.Context(), nil, input, options, "")
	assert.Error(t, err)
	remote := input
	remote.URL = "https://example.com/sample.pdf"
	_, err = Suggest(t.Context(), client, remote, options, "")
	assert.Error(t, err)
}
