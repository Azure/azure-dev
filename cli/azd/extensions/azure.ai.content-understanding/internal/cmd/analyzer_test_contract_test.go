// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/testutil"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

type analyzerTestFixture struct {
	Input struct {
		Name string `json:"name"`
		Body string `json:"body"`
	} `json:"input"`
	Envelope json.RawMessage `json:"envelope"`
	Report   json.RawMessage `json:"report"`
}

type analysisRequestObserver struct {
	cuclient.Client
	request  cuclient.AnalyzeRequest
	body     []byte
	response cuclient.AnalyzeResponse
	failure  error
	calls    int
}

func (o *analysisRequestObserver) Analyze(
	ctx context.Context, request cuclient.AnalyzeRequest,
) (cuclient.AnalyzeResponse, error) {
	o.calls++
	o.request = request
	if request.Input.Body == nil {
		return cuclient.AnalyzeResponse{}, errors.New("Synthetic sample did not reach the REST client as binary input.")
	}
	position, err := request.Input.Body.Seek(0, io.SeekCurrent)
	if err != nil {
		return cuclient.AnalyzeResponse{}, err
	}
	o.body, err = io.ReadAll(request.Input.Body)
	if err != nil {
		return cuclient.AnalyzeResponse{}, err
	}
	if _, err = request.Input.Body.Seek(position, io.SeekStart); err != nil {
		return cuclient.AnalyzeResponse{}, err
	}
	o.response, o.failure = o.Client.Analyze(ctx, request)
	return o.response, o.failure
}

func TestAnalyzerTestContracts(t *testing.T) {
	var fixture analyzerTestFixture
	loadCommandFixture(t, loadParityInventory(t).CoreTestBinding, &fixture)
	for index, args := range analyzerNameForms("test", "invoice-v1") {
		for _, format := range []string{"default", "json", "none"} {
			t.Run(fmt.Sprintf("%d/%s", index, format), func(t *testing.T) {
				executeAnalyzerTestContract(t, fixture, args, format)
			})
		}
	}
}

func executeAnalyzerTestContract(
	t *testing.T, fixture analyzerTestFixture, args []string, format string,
) {
	t.Helper()
	_ = newDefaultsFixture(t)
	directory := filepath.Join(testutil.TempDir(t), "sample folder with spaces")
	require.NoError(t, os.Mkdir(directory, 0o700))
	input := filepath.Join(directory, fixture.Input.Name)
	output := filepath.Join(directory, "report.json")
	require.NoError(t, os.WriteFile(input, []byte(fixture.Input.Body), 0o600))
	var interaction recordedInteraction
	interaction.Request.Method = "POST"
	interaction.Request.URL = "https://sanitized.services.ai.azure.com/contentunderstanding/analyzers/" +
		"invoice-v1:analyzeBinary?api-version=" + cuclient.DefaultAPIVersion + "&stringEncoding=codePoint"
	interaction.Request.Headers = map[string]string{
		"Content-Type": "application/octet-stream", "Accept": "application/json",
	}
	interaction.Request.BodyMode = "scrubbed-binary"
	interaction.Request.Body = json.RawMessage(`"<binary-scrubbed>"`)
	interaction.Response.Status = 202
	interaction.Response.Headers = map[string]string{
		"Content-Type": "application/json",
		"Operation-Location": "https://sanitized.services.ai.azure.com/contentunderstanding/" +
			"analyzerResults/synthetic-core-test?api-version=" + cuclient.DefaultAPIVersion,
	}
	interaction.Response.Body = fixture.Envelope
	transport := &recordedTransport{
		fixture:    recordedFixture{Interactions: []recordedInteraction{interaction}},
		binaryBody: []byte(fixture.Input.Body),
	}
	credential := &recordedCredential{}
	observer := &analysisRequestObserver{}
	opens, closes, prompts, concurrency := 0, 0, 0, 0
	root := newRootCommand(func(
		cmd *cobra.Command, _ *azdext.ExtensionContext, options config.Options,
	) (*serviceSession, error) {
		opens++
		var err error
		concurrency, err = cmd.Flags().GetInt("concurrency")
		require.NoError(t, err)
		require.False(t, cmd.Flags().Changed("concurrency"))
		require.Equal(t, 4, concurrency)
		require.Equal(t, "invoice-v1", options.DefaultAnalyzer)
		require.Equal(t, "https://sanitized.services.ai.azure.com", options.Endpoint)
		require.Equal(t, cuclient.DefaultAPIVersion, options.APIVersion)
		client, err := cuclient.NewRESTTransport(options.Endpoint, credential,
			&cuclient.TransportOptions{APIVersion: options.APIVersion, Transport: transport})
		if err != nil {
			return nil, err
		}
		observer.Client = client
		return &serviceSession{
			client: observer, apiVersion: options.APIVersion, close: func() { closes++ },
			confirm: func(context.Context, string) (bool, error) {
				prompts++
				return false, errors.New("Unexpected analyzer-test prompt.")
			},
		}, nil
	})
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	invocation := slices.Clone(args)
	if format != "default" {
		invocation = append(invocation, "--file")
	}
	root.SetArgs(append(invocation, input, "--output-file", output, "-o", format, "--no-prompt",
		"--endpoint", "https://sanitized.services.ai.azure.com", "--api-version", cuclient.DefaultAPIVersion))
	runErr := root.ExecuteContext(t.Context())
	require.NoError(t, transport.complete())
	require.NoError(t, observer.failure)
	require.NoError(t, runErr)
	require.Empty(t, stderr.String())
	require.Zero(t, prompts)
	require.Equal(t, 1, opens)
	require.Equal(t, 1, closes)
	require.Equal(t, 1, credential.calls)
	require.Equal(t, 1, observer.calls)
	require.Equal(t, "invoice-v1", observer.request.AnalyzerID)
	require.Equal(t, cuclient.AnalysisModeAsync, observer.request.Mode)
	require.Empty(t, observer.request.Input.URL)
	require.Equal(t, "application/octet-stream", observer.request.Input.ContentType)
	require.Equal(t, []byte(fixture.Input.Body), observer.body)
	assertJSONEqual(t, fixture.Envelope, observer.response.JSON)
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	require.NoError(t, json.Unmarshal(fixture.Envelope, &envelope))
	assertJSONEqual(t, envelope.Result, observer.response.Result.JSON)
	_, err := observer.request.Input.Body.Read(make([]byte, 1))
	require.ErrorIs(t, err, os.ErrClosed)
	local, err := os.OpenRoot(directory)
	require.NoError(t, err)
	defer local.Close()
	saved, err := local.ReadFile("report.json")
	require.NoError(t, err)
	original, err := local.ReadFile(fixture.Input.Name)
	require.NoError(t, err)
	require.Equal(t, []byte(fixture.Input.Body), original)
	expected := normalizeRecordedReport(t, fixture.Report, fixture.Input.Name)
	require.Equal(t, expected, normalizeRecordedReport(t, saved, input))
	if format == "none" {
		require.Empty(t, stdout.String())
	} else {
		require.Equal(t, expected, normalizeRecordedReport(t, stdout.Bytes(), input))
	}
}

func TestAnalyzerTestRequiresSamples(t *testing.T) {
	for index, args := range analyzerNameForms("test", "invoice-v1") {
		for _, format := range []string{"default", "json", "none"} {
			t.Run(fmt.Sprintf("%d/%s", index, format), func(t *testing.T) {
				opens := 0
				root := newRootCommand(func(
					*cobra.Command, *azdext.ExtensionContext, config.Options,
				) (*serviceSession, error) {
					opens++
					return nil, errors.New("Missing samples opened a service session.")
				})
				var stdout, stderr bytes.Buffer
				root.SetOut(&stdout)
				root.SetErr(&stderr)
				root.SetArgs(append(slices.Clone(args), "-o", format, "--no-prompt"))
				err := root.ExecuteContext(t.Context())
				require.Error(t, err)
				local, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				require.Equal(t, "invalid_input_mode", local.Code)
				require.Equal(t, "Select inputs using positionals, --file, --source, or --url, "+
					"not a mixture.", local.Message)
				require.Zero(t, opens)
				require.Empty(t, stdout.String())
				require.Empty(t, stderr.String())
			})
		}
	}
}
