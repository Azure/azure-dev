// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"testing"

	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/cuclient"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

var normalizedParityNames = []string{"list", "delete", "conflicting-delete"}

type normalizedParityError struct {
	Category   string  `json:"category"`
	Message    string  `json:"message"`
	Hint       *string `json:"hint"`
	StatusCode *int    `json:"status_code"`
}

type normalizedObservation struct {
	Request  json.RawMessage        `json:"request"`
	Result   json.RawMessage        `json:"result"`
	Warnings []string               `json:"warnings"`
	Error    *normalizedParityError `json:"error"`
}

type normalizedBackendCall struct {
	Operation string          `json:"operation"`
	Args      []string        `json:"args"`
	Result    json.RawMessage `json:"result"`
}

type normalizedFrontend struct {
	Observation normalizedObservation   `json:"observation"`
	Calls       []normalizedBackendCall `json:"calls"`
}

type normalizedParityCase struct {
	Name        string             `json:"name"`
	Source      string             `json:"source"`
	Azure       normalizedFrontend `json:"azure"`
	Azd         normalizedFrontend `json:"azd"`
	Native      normalizedFrontend `json:"native"`
	Differences struct {
		Syntax       []string `json:"syntax"`
		Presentation []string `json:"presentation"`
	} `json:"differences"`
	NativeErrorCode string `json:"native_error_code"`
}

func loadNormalizedParity(t *testing.T) []normalizedParityCase {
	t.Helper()
	inventory := loadParityInventory(t)
	require.Equal(t, "testdata/parity/normalized_adapter.json", inventory.Normalized.File)
	body, err := os.ReadFile("../../testdata/parity/normalized_adapter.json")
	require.NoError(t, err)
	require.Equal(t, inventory.Normalized.SHA256, sha256Text(body))
	require.NoError(t, recordingHygiene(body))
	_, err = recordedJSON(body)
	require.NoError(t, err)
	var fixture struct {
		SchemaVersion int `json:"schema_version"`
		Source        struct {
			Repository string            `json:"repository"`
			Commit     string            `json:"commit"`
			License    string            `json:"license"`
			Files      map[string]string `json:"files"`
			Runtime    map[string]string `json:"runtime"`
		} `json:"source"`
		Normalizations []string               `json:"normalizations"`
		Cases          []normalizedParityCase `json:"cases"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	require.NoError(t, decoder.Decode(&fixture))
	require.Equal(t, 1, fixture.SchemaVersion)
	require.Equal(t, inventory.Source.Repository, fixture.Source.Repository)
	require.Equal(t, paritySourceCommit, fixture.Source.Commit)
	require.Equal(t, "MIT", fixture.Source.License)
	require.Len(t, fixture.Source.Files, 9)
	for path, digest := range fixture.Source.Files {
		require.Regexp(t, "^(tests|packages)/.*\\.py$", path)
		require.NotContains(t, path, "..")
		require.Regexp(t, "^[0-9a-f]{64}$", digest)
	}
	require.Len(t, fixture.Source.Runtime, 3)
	require.Len(t, fixture.Normalizations, 3)
	require.Len(t, fixture.Cases, 3)
	for index, row := range fixture.Cases {
		require.Equal(t, normalizedParityNames[index], row.Name)
		require.Contains(t, row.Source, "tests/parity/test_request_result_parity.py::test_")
		assertNormalizedJSON(t, row.Azure, row.Azd)
		require.Equal(t, row.Name == "conflicting-delete", row.Native.Observation.Error != nil)
		require.Equal(t, row.Name == "conflicting-delete", row.NativeErrorCode != "")
	}
	return fixture.Cases
}

func assertNormalizedJSON(t *testing.T, expected, actual any) {
	t.Helper()
	wanted, err := json.Marshal(expected)
	require.NoError(t, err)
	got, err := json.Marshal(actual)
	require.NoError(t, err)
	left, err := recordedJSON(wanted)
	require.NoError(t, err)
	right, err := recordedJSON(got)
	require.NoError(t, err)
	require.Equal(t, left, right)
}

type normalizedSDKObserver struct {
	cuclient.Client
	request json.RawMessage
	calls   []normalizedBackendCall
	kind    string
	sortBy  string
	yes     bool
}

func (o *normalizedSDKObserver) ListAnalyzers(ctx context.Context) ([]cuclient.Analyzer, error) {
	var err error
	o.request, err = json.Marshal(map[string]string{"kind": o.kind, "sort_by": o.sortBy})
	if err != nil {
		return nil, err
	}
	items, err := o.Client.ListAnalyzers(ctx)
	if err != nil {
		return nil, err
	}
	raw := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		value, err := analyzerJSON(item)
		if err != nil {
			return nil, err
		}
		raw = append(raw, value)
	}
	result, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	o.calls = append(o.calls, normalizedBackendCall{"list", []string{}, result})
	return items, nil
}

func (o *normalizedSDKObserver) GetAnalyzer(ctx context.Context, name string) (cuclient.Analyzer, error) {
	request, err := json.Marshal(map[string]any{"name": name, "yes": o.yes})
	if err != nil {
		return cuclient.Analyzer{}, err
	}
	o.request = request
	value, err := o.Client.GetAnalyzer(ctx, name)
	if err != nil {
		return cuclient.Analyzer{}, err
	}
	raw, err := analyzerJSON(value)
	if err != nil {
		return cuclient.Analyzer{}, err
	}
	o.calls = append(o.calls, normalizedBackendCall{"get", []string{name}, raw})
	return value, nil
}

func (o *normalizedSDKObserver) DeleteAnalyzer(ctx context.Context, name string) error {
	if err := o.Client.DeleteAnalyzer(ctx, name); err != nil {
		return err
	}
	o.calls = append(o.calls, normalizedBackendCall{"delete", []string{name}, json.RawMessage("null")})
	return nil
}

func normalizedHTTPFixture(t *testing.T, calls []normalizedBackendCall) recordedFixture {
	t.Helper()
	var fixture recordedFixture
	for _, call := range calls {
		var step recordedInteraction
		step.Request.Method = http.MethodGet
		step.Request.URL = "https://sanitized.services.ai.azure.com/contentunderstanding/analyzers"
		step.Request.Headers = map[string]string{"Accept": "application/json"}
		step.Request.BodyMode = "none"
		step.Request.Body = json.RawMessage("null")
		step.Response.Status = http.StatusOK
		step.Response.Headers = map[string]string{"Content-Type": "application/json"}
		step.Response.Body = call.Result
		switch call.Operation {
		case "list":
			require.Empty(t, call.Args)
			body, err := json.Marshal(map[string]json.RawMessage{"value": call.Result})
			require.NoError(t, err)
			step.Response.Body = body
		case "get", "delete":
			require.Len(t, call.Args, 1)
			step.Request.URL += "/" + call.Args[0]
			if call.Operation == "delete" {
				step.Request.Method = http.MethodDelete
				step.Response.Status = http.StatusNoContent
			}
		default:
			t.Fatalf("unknown normalized source backend operation")
		}
		step.Request.URL += "?api-version=" + cuclient.DefaultAPIVersion
		fixture.Interactions = append(fixture.Interactions, step)
	}
	return fixture
}

func TestNormalizedAdapterParity(t *testing.T) {
	for _, row := range loadNormalizedParity(t) {
		t.Run(row.Name, func(t *testing.T) {
			var forms [][]string
			switch row.Name {
			case "list":
				forms = [][]string{
					{"analyzer", "list"},
					{"analyzer", "list", "--kind", "all", "--sort-by", "analyzerId"},
				}
			case "delete":
				for _, operand := range [][]string{
					{"invoice-v1"}, {"--name", "invoice-v1"}, {"-n", "invoice-v1"}, {"-a", "invoice-v1"},
				} {
					forms = append(forms, append(append([]string{"analyzer", "delete"}, operand...), "--yes"))
				}
			case "conflicting-delete":
				forms = [][]string{
					{"analyzer", "delete", "two", "--name", "one", "--yes"},
					{"analyzer", "delete", "two", "-a", "one", "--yes"},
				}
			}
			for index, args := range forms {
				for _, format := range []string{"default", "json", "none"} {
					t.Run(fmt.Sprintf("%d/%s", index, format), func(t *testing.T) {
						executeNormalizedParity(t, row, args, format)
					})
				}
			}
		})
	}
}

func executeNormalizedParity(t *testing.T, row normalizedParityCase, args []string, format string) {
	t.Helper()
	_ = newDefaultsFixture(t)
	transport := &recordedTransport{fixture: normalizedHTTPFixture(t, row.Native.Calls)}
	credential := &recordedCredential{}
	observer := &normalizedSDKObserver{request: json.RawMessage("null"), calls: []normalizedBackendCall{}}
	opens, closes, prompts := 0, 0, 0
	root := newRootCommand(func(
		cmd *cobra.Command, _ *azdext.ExtensionContext, options config.Options,
	) (*serviceSession, error) {
		opens++
		require.Equal(t, "https://sanitized.services.ai.azure.com", options.Endpoint)
		require.Equal(t, cuclient.DefaultAPIVersion, options.APIVersion)
		if cmd.Name() == "list" {
			var err error
			observer.kind, err = cmd.Flags().GetString("kind")
			require.NoError(t, err)
			observer.sortBy, err = cmd.Flags().GetString("sort-by")
			require.NoError(t, err)
		} else {
			var err error
			observer.yes, err = cmd.Flags().GetBool("yes")
			require.NoError(t, err)
		}
		client, err := cuclient.NewSDKTransport(options.Endpoint, credential,
			&cuclient.TransportOptions{APIVersion: options.APIVersion, Transport: transport})
		if err != nil {
			return nil, err
		}
		observer.Client = client
		return &serviceSession{
			client: observer, apiVersion: options.APIVersion, close: func() { closes++ },
			confirm: func(context.Context, string) (bool, error) {
				prompts++
				return false, errors.New("Unexpected normalized parity prompt.")
			},
		}, nil
	})
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	invocation := slices.Clone(args)
	invocation = append(invocation, "-o", format, "--no-prompt",
		"--endpoint", "https://sanitized.services.ai.azure.com", "--api-version", cuclient.DefaultAPIVersion)
	root.SetArgs(invocation)
	err := root.ExecuteContext(t.Context())
	if row.Native.Observation.Error == nil {
		require.NoError(t, err)
	} else {
		require.Error(t, err)
	}
	require.Empty(t, stderr.String())
	require.Zero(t, prompts)
	require.NoError(t, transport.complete())
	require.Equal(t, len(row.Native.Calls), credential.calls)
	assertNormalizedJSON(t, row.Native.Calls, observer.calls)
	actual := normalizedObservation{Request: observer.request, Result: json.RawMessage("null"), Warnings: []string{}}
	expected := row.Native.Observation
	if expected.Error != nil {
		require.Error(t, err)
		local, ok := errors.AsType[*azdext.LocalError](err)
		require.True(t, ok)
		actual.Error = &normalizedParityError{
			Category: string(local.Category), Message: local.Message, Hint: &local.Suggestion,
		}
		require.Equal(t, row.NativeErrorCode, local.Code)
		require.Zero(t, opens)
		require.Zero(t, closes)
		require.Empty(t, stdout.String())
	} else {
		require.NoError(t, err)
		require.Equal(t, 1, opens)
		require.Equal(t, 1, closes)
		if format != "none" {
			actual.Result = stdout.Bytes()
		}
	}
	if format == "none" {
		require.Empty(t, stdout.String())
		expected.Result = json.RawMessage("null")
	}
	assertNormalizedJSON(t, expected, actual)
}

func TestNormalizedSourceModelsRequireWireStatus(t *testing.T) {
	for _, row := range loadNormalizedParity(t) {
		if row.Name == "conflicting-delete" {
			continue
		}
		t.Run(row.Name, func(t *testing.T) {
			transport := &recordedTransport{fixture: normalizedHTTPFixture(t, row.Azure.Calls[:1])}
			root := newRecordedCommand(t, transport, "")
			args := []string{"analyzer", "list"}
			if row.Name == "delete" {
				args = []string{"analyzer", "delete", "invoice-v1", "--yes"}
			}
			root.SetArgs(append(args, "-o", "json", "--no-prompt",
				"--endpoint", "https://sanitized.services.ai.azure.com", "--api-version", cuclient.DefaultAPIVersion))
			var stdout, stderr bytes.Buffer
			root.SetOut(&stdout)
			root.SetErr(&stderr)
			err := root.ExecuteContext(t.Context())
			require.Error(t, err)
			service, ok := errors.AsType[*azdext.ServiceError](err)
			require.True(t, ok)
			require.Equal(t, "malformed_response", service.ErrorCode)
			require.Empty(t, stdout.String())
			require.Empty(t, stderr.String())
		})
	}
}
