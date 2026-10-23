// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"azure.ai.content-understanding/internal/armclient"
	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/cuclient"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type copyWireCredential struct{ scopes []string }

func (c *copyWireCredential) GetToken(
	_ context.Context, options policy.TokenRequestOptions,
) (azcore.AccessToken, error) {
	c.scopes = append(c.scopes, options.Scopes...)
	return azcore.AccessToken{Token: "private-wire-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type copyWireTransport func(*http.Request) (*http.Response, error)

func (t copyWireTransport) Do(request *http.Request) (*http.Response, error) { return t(request) }

func TestAnalyzerCopyCommandThroughARMAndCUPipelines(t *testing.T) {
	for _, version := range []string{cuclient.DefaultAPIVersion, cuclient.PreviewAPIVersion} {
		for _, failed := range []bool{false, true} {
			t.Run(version+map[bool]string{false: "/ready", true: "/failed"}[failed], func(t *testing.T) {
				_ = newAnalyzerFixture(t)
				credential := &copyWireCredential{}
				sourceID := "/subscriptions/source-sub/resourceGroups/source-group" +
					"/providers/Microsoft.CognitiveServices/accounts/source"
				targetID := "/subscriptions/target-sub/resourceGroups/target-group" +
					"/providers/Microsoft.CognitiveServices/accounts/target"
				var calls []string
				transport := copyWireTransport(func(request *http.Request) (*http.Response, error) {
					action := request.Method + " " + request.URL.Host + request.URL.Path
					calls = append(calls, action)
					assert.Equal(t, "Bearer private-wire-token", request.Header.Get("Authorization"))
					result := &http.Response{StatusCode: 200, Header: http.Header{}, Request: request}
					var body string
					if request.URL.Host == "management.azure.com" {
						assert.Equal(t, "2024-10-01", request.URL.Query().Get("api-version"))
						name, id := "source", sourceID
						if request.URL.Path == targetID {
							name, id = "target", targetID
						}
						body = `{"id":"` + id + `","name":"` + name + `","location":"westus",` +
							`"properties":{"endpoint":"https://` + name + `.services.ai.azure.com"}}`
					} else {
						assert.Equal(t, version, request.URL.Query().Get("api-version"))
						assert.NotEmpty(t, request.Header.Get("x-ms-client-request-id"))
						assert.Empty(t, request.Header.Get("Ocp-Apim-Subscription-Key"))
						switch action {
						case "GET source.services.ai.azure.com/contentunderstanding/analyzers/source":
							body = `{"analyzerId":"source","status":"ready",` +
								`"config":{"contentCategories":{"custom":{"analyzerId":"dependency"}}}}`
						case "GET target.services.ai.azure.com/contentunderstanding/analyzers/dependency":
							body = `{"analyzerId":"dependency","status":"ready"}`
						case "GET target.services.ai.azure.com/contentunderstanding/analyzers/destination":
							result.StatusCode = 404
							body = `{"error":{"code":"NotFound","message":"private backend secret"}}`
						case "POST source.services.ai.azure.com/contentunderstanding/analyzers/" +
							"source:grantCopyAuthorization":
							var grant map[string]string
							require.NoError(t, json.NewDecoder(request.Body).Decode(&grant))
							assert.Equal(t, map[string]string{
								"targetAzureResourceId": targetID, "targetRegion": "westus"}, grant)
							body = `{"targetAzureResourceId":"` + targetID +
								`","targetRegion":"westus","expiresAt":"2099-01-01T00:00:00Z"}`
						case "POST target.services.ai.azure.com/contentunderstanding/analyzers/destination:copy":
							var copyBody map[string]string
							require.NoError(t, json.NewDecoder(request.Body).Decode(&copyBody))
							assert.Equal(t, map[string]string{
								"sourceAnalyzerId": "source", "sourceAzureResourceId": sourceID,
								"sourceRegion": "westus"}, copyBody)
							result.StatusCode = 201
							result.Header.Set("Operation-Location",
								"https://target.services.ai.azure.com/contentunderstanding/analyzers/"+
									"destination/operations/copy-operation")
							body = `{"analyzerId":"destination","status":"creating"}`
						case "GET target.services.ai.azure.com/contentunderstanding/analyzers/" +
							"destination/operations/copy-operation":
							status := "ready"
							if failed {
								status = "failed"
							}
							body = `{"id":"copy-operation","status":"Succeeded",` +
								`"result":{"analyzerId":"destination","status":"` + status +
								`","warnings":[{"code":"Notice","message":"visible warning"}],"unknown":9007199254740993}}`
						default:
							t.Errorf("unexpected copy pipeline request: %s", action)
							result.StatusCode = 500
						}
					}
					result.Body = io.NopCloser(strings.NewReader(body))
					return result, nil
				})
				resolver, err := armclient.NewResolver(credential, transport)
				require.NoError(t, err)
				clientFor := func(endpoint string) (cuclient.Client, error) {
					return cuclient.NewRESTTransport(endpoint, credential, &cuclient.TransportOptions{
						APIVersion: version, Transport: transport,
					})
				}
				var stdout, stderr bytes.Buffer
				root := newRootCommand(func(
					_ *cobra.Command, _ *azdext.ExtensionContext, _ config.Options,
				) (*serviceSession, error) {
					client, err := clientFor("https://source.services.ai.azure.com")
					require.NoError(t, err)
					return &serviceSession{client: client, close: func() {},
						resolveAccount: func(
							ctx context.Context, resource, subscription, group string,
						) (armclient.Account, error) {
							return resolver.Resolve(ctx, resource, subscription, group, config.ResourceScope{})
						}, clientForEndpoint: clientFor}, nil
				})
				root.SetOut(&stdout)
				root.SetErr(&stderr)
				root.SetArgs([]string{"analyzer", "copy", "source", "destination",
					"--source-resource", sourceID, "--destination-resource", targetID, "--api-version", version})
				err = root.ExecuteContext(t.Context())
				if failed {
					require.Error(t, err)
					assert.Contains(t, err.Error(), "destination")
					assert.Empty(t, stdout.String())
				} else {
					require.NoError(t, err)
					assert.Contains(t, stdout.String(), "9007199254740993")
					assert.Contains(t, stdout.String(), "visible warning")
				}
				assert.NotContains(t, stdout.String()+stderr.String(), "private")
				assert.Contains(t, credential.scopes, cloud.AzurePublic.Services[cloud.ResourceManager].Audience+"/.default")
				assert.Contains(t, credential.scopes, "https://cognitiveservices.azure.com/.default")
				assert.Equal(t, []string{
					"GET management.azure.com" + sourceID, "GET management.azure.com" + targetID,
					"GET source.services.ai.azure.com/contentunderstanding/analyzers/source",
					"GET target.services.ai.azure.com/contentunderstanding/analyzers/dependency",
					"GET target.services.ai.azure.com/contentunderstanding/analyzers/destination",
					"POST source.services.ai.azure.com/contentunderstanding/analyzers/source:grantCopyAuthorization",
					"POST target.services.ai.azure.com/contentunderstanding/analyzers/destination:copy",
					"GET target.services.ai.azure.com/contentunderstanding/analyzers/destination/operations/copy-operation",
				}, calls)
			})
		}
	}
}
