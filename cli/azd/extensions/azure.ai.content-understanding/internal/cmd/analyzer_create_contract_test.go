// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/cuclient"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

type createRequestObserver struct {
	cuclient.Client
	requests []cuclient.CreateAnalyzerRequest
	results  []json.RawMessage
}

func (o *createRequestObserver) CreateAnalyzer(
	ctx context.Context, request cuclient.CreateAnalyzerRequest,
) (cuclient.AnalyzerResponse, error) {
	o.requests = append(o.requests, request)
	result, err := o.Client.CreateAnalyzer(ctx, request)
	if err != nil {
		return result, err
	}
	raw, err := analyzerJSON(result.Analyzer)
	if err != nil {
		return cuclient.AnalyzerResponse{}, err
	}
	o.results = append(o.results, raw)
	return result, nil
}

func TestAnalyzerCreateContracts(t *testing.T) {
	var fixture struct {
		Name     string          `json:"name"`
		Schema   json.RawMessage `json:"schema"`
		Response json.RawMessage `json:"response"`
	}
	loadCommandFixture(t, loadParityInventory(t).CoreCreateBinding, &fixture)
	for index, args := range analyzerNameForms("create", fixture.Name) {
		for _, format := range []string{"default", "json", "none"} {
			t.Run(fmt.Sprintf("%d/%s", index, format), func(t *testing.T) {
				_ = newDefaultsFixture(t)
				directory := filepath.Join(t.TempDir(), "schema folder with spaces")
				require.NoError(t, os.Mkdir(directory, 0o700))
				schemaPath := filepath.Join(directory, "schema.json")
				require.NoError(t, os.WriteFile(schemaPath, fixture.Schema, 0o600))
				var step recordedInteraction
				step.Request.Method = "PUT"
				step.Request.URL = "https://sanitized.services.ai.azure.com/contentunderstanding/analyzers/" +
					fixture.Name + "?api-version=" + cuclient.DefaultAPIVersion + "&allowReplace=false"
				step.Request.Headers = map[string]string{"Content-Type": "application/json", "Accept": "application/json"}
				step.Request.BodyMode = "json"
				step.Request.Body = fixture.Schema
				step.Response.Status = 201
				step.Response.Headers = map[string]string{"Content-Type": "application/json"}
				step.Response.Body = fixture.Response
				transport := &recordedTransport{fixture: recordedFixture{Interactions: []recordedInteraction{step}}}
				credential := &recordedCredential{}
				observer := &createRequestObserver{}
				opens, closes, prompts := 0, 0, 0
				root := newRootCommand(func(
					cmd *cobra.Command, _ *azdext.ExtensionContext, options config.Options,
				) (*serviceSession, error) {
					opens++
					selected, err := cmd.Flags().GetString("schema")
					require.NoError(t, err)
					require.Equal(t, schemaPath, selected)
					require.True(t, filepath.IsAbs(selected))
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
							return false, errors.New("Unexpected create prompt.")
						},
					}, nil
				})
				var stdout, stderr bytes.Buffer
				root.SetOut(&stdout)
				root.SetErr(&stderr)
				schemaFlag := "--schema"
				if format == "json" {
					schemaFlag = "-s"
				}
				root.SetArgs(append(slices.Clone(args), schemaFlag, schemaPath, "-o", format, "--no-prompt",
					"--endpoint", "https://sanitized.services.ai.azure.com", "--api-version", cuclient.DefaultAPIVersion))
				require.NoError(t, root.ExecuteContext(t.Context()))
				require.Empty(t, stderr.String())
				require.Zero(t, prompts)
				require.Equal(t, 1, opens)
				require.Equal(t, 1, closes)
				require.Equal(t, 1, credential.calls)
				require.NoError(t, transport.complete())
				require.Len(t, observer.requests, 1)
				require.Len(t, observer.results, 1)
				require.Equal(t, fixture.Name, observer.requests[0].AnalyzerID)
				assertJSONEqual(t, fixture.Schema, observer.requests[0].Definition)
				assertJSONEqual(t, fixture.Response, observer.results[0])
				if format == "none" {
					require.Empty(t, stdout.String())
				} else {
					assertJSONEqual(t, fixture.Response, json.RawMessage(stdout.Bytes()))
				}
			})
		}
	}
}

func TestAnalyzerCreateRejectsInvalidIDBeforeReadingSchema(t *testing.T) {
	for index, args := range analyzerNameForms("create", "invoice-v1") {
		for _, format := range []string{"default", "json", "none"} {
			t.Run(fmt.Sprintf("%d/%s", index, format), func(t *testing.T) {
				_ = newDefaultsFixture(t)
				opens := 0
				root := newRootCommand(func(
					*cobra.Command, *azdext.ExtensionContext, config.Options,
				) (*serviceSession, error) {
					opens++
					return nil, errors.New("Invalid create ID opened a service session.")
				})
				var stdout, stderr bytes.Buffer
				root.SetOut(&stdout)
				root.SetErr(&stderr)
				schemaPath := filepath.Join(t.TempDir(), "schema.json")
				root.SetArgs(append(slices.Clone(args), "--schema", schemaPath, "-o", format, "--no-prompt"))
				err := root.ExecuteContext(t.Context())
				local, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				require.Equal(t, "invalid_analyzer_schema", local.Code)
				require.Equal(t, "Invalid analyzer schema at analyzerId: "+
					"Custom analyzer IDs require 1-64 ASCII letters, numbers, or underscores.", local.Message)
				require.Zero(t, opens)
				require.NoFileExists(t, schemaPath)
				require.Empty(t, stdout.String())
				require.Empty(t, stderr.String())
			})
		}
	}
}
