// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
// cspell:ignore envkey

package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"azure.ai.connections/internal/exterrors"
	"azure.ai.connections/internal/pkg/envkey"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	identityProjectID = "/subscriptions/staging-sub/resourceGroups/staging-rg/providers/" +
		"Microsoft.CognitiveServices/accounts/arm-account/projects/project-one"
	identityEndpoint = "https://custom-dns.services.ai.azure.com/api/projects/project-one"
)

func TestConnectionContextCustomDomainCRUD(t *testing.T) {
	for _, tt := range []struct {
		name               string
		projectID          string
		persistedEndpoint  string
		flagEndpoint       string
		advertisedEndpoint string
		existing           bool
		wantMetadataCalls  int
		wantDiscoveryCalls int
	}{
		{name: "first connection with persisted ARM ID", projectID: identityProjectID, wantMetadataCalls: 1},
		{name: "existing connection discovery", existing: true, wantDiscoveryCalls: 1},
		{
			name: "invalid persisted ID discovery", projectID: "not-an-arm-resource-id",
			existing: true, wantDiscoveryCalls: 1,
		},
		{
			name: "unrelated account discovery", projectID: strings.ReplaceAll(identityProjectID, "arm-account", "other"),
			advertisedEndpoint: "https://other.services.ai.azure.com/api/projects/project-one",
			existing:           true, wantMetadataCalls: 1, wantDiscoveryCalls: 1,
		},
		{
			name: "same names in unrelated resource group", projectID: strings.ReplaceAll(identityProjectID, "staging-rg", "other"),
			advertisedEndpoint: "https://other.services.ai.azure.com/api/projects/project-one",
			existing:           true, wantMetadataCalls: 1, wantDiscoveryCalls: 1,
		},
		{
			name: "different project discovery", projectID: strings.ReplaceAll(identityProjectID, "project-one", "other"),
			existing: true, wantDiscoveryCalls: 1,
		},
		{
			name: "explicit endpoint overrides persisted endpoint", projectID: identityProjectID,
			persistedEndpoint: connectionContextProductionEndpoint, flagEndpoint: identityEndpoint,
			wantMetadataCalls: 1,
		},
		{
			name:              "explicit endpoint overrides unrelated environment identity",
			projectID:         strings.ReplaceAll(identityProjectID, "arm-account", "other"),
			persistedEndpoint: "https://other.services.ai.azure.com/api/projects/project-one",
			flagEndpoint:      identityEndpoint, advertisedEndpoint: "https://other.services.ai.azure.com/api/projects/project-one",
			existing: true, wantMetadataCalls: 1, wantDiscoveryCalls: 1,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			persistedEndpoint := tt.persistedEndpoint
			if persistedEndpoint == "" {
				persistedEndpoint = identityEndpoint
			}
			advertisedEndpoint := tt.advertisedEndpoint
			if advertisedEndpoint == "" {
				advertisedEndpoint = identityEndpoint
			}
			staging := map[string]string{
				"FOUNDRY_PROJECT_ENDPOINT": persistedEndpoint,
				"AZURE_AI_PROJECT_ID":      tt.projectID,
				"AZURE_SUBSCRIPTION_ID":    "staging-sub",
			}
			fixture := newConnectionContextFixture(t, staging, nil)
			var metadataCalls, discoveryCalls int
			var requests []string
			var stored rawConnectionBody
			exists := false
			connectionPath := identityProjectID + "/connections/endpoint-smoke"
			connectionJSON := func() string {
				t.Helper()
				body, err := json.Marshal(map[string]any{
					"id": connectionPath, "name": "endpoint-smoke", "properties": stored.Properties,
				})
				require.NoError(t, err)
				return string(body)
			}
			transport := deleteReadinessTransport(func(request *http.Request) (*http.Response, error) {
				requests = append(requests, request.Method+" "+request.URL.Host+request.URL.Path)
				assert.Equal(t, "https", request.URL.Scheme)
				switch {
				case request.Method == http.MethodGet && request.URL.Host == "custom-dns.services.ai.azure.com":
					discoveryCalls++
					assert.Equal(t, "/api/projects/project-one/connections", request.URL.Path)
					assert.Equal(t, "2025-11-15-preview", request.URL.Query().Get("api-version"))
					body := `{"value":[]}`
					if tt.existing {
						body = `{"value":[{"id":"` + identityProjectID +
							`/connections/existing","name":"existing","type":"RemoteTool","target":"https://example.test"}]}`
					}
					return identityResponse(request, http.StatusOK, body), nil
				case request.Method == http.MethodGet && request.URL.Path == tt.projectID:
					metadataCalls++
					assert.Equal(t, "management.azure.com", request.URL.Host)
					assert.Equal(t, "2025-06-01", request.URL.Query().Get("api-version"))
					return identityResponse(request, http.StatusOK,
						`{"properties":{"endpoints":{"AI Foundry API":"`+advertisedEndpoint+`"}}}`), nil
				case request.URL.Path == identityProjectID+"/connections" && request.Method == http.MethodGet:
					assert.Equal(t, "management.azure.com", request.URL.Host)
					assert.True(t, exists)
					return identityResponse(request, http.StatusOK, `{"value":[`+connectionJSON()+`]}`), nil
				case request.URL.Path == connectionPath:
					assert.Equal(t, "management.azure.com", request.URL.Host)
					switch request.Method {
					case http.MethodPut:
						require.NoError(t, json.NewDecoder(request.Body).Decode(&stored))
						exists = true
						return identityResponse(request, http.StatusOK, connectionJSON()), nil
					case http.MethodGet:
						assert.True(t, exists)
						return identityResponse(request, http.StatusOK, connectionJSON()), nil
					case http.MethodDelete:
						assert.True(t, exists)
						exists = false
						return identityResponse(request, http.StatusNoContent, ""), nil
					}
				}
				return nil, fmt.Errorf("unexpected identity test request: %s %s", request.Method, request.URL.Path)
			})
			connCtx, err := resolveConnectionContextWithClients(
				fixture.ctx, tt.flagEndpoint, "staging",
				func(tenant string) (azcore.TokenCredential, error) {
					assert.Equal(t, connectionContextStagingTenant, tenant)
					return deleteReadinessCredential{}, nil
				},
				identityClientOptions(transport),
			)
			require.NoError(t, err)
			require.NotNil(t, connCtx)
			assert.Equal(t, tt.wantMetadataCalls, metadataCalls)
			assert.Equal(t, tt.wantDiscoveryCalls, discoveryCalls)
			assert.Equal(t, "staging-sub", connCtx.sub)
			assert.Equal(t, "staging-rg", connCtx.rg)
			assert.Equal(t, "arm-account", connCtx.account)
			assert.Equal(t, "project-one", connCtx.project)
			assert.Equal(t, identityEndpoint, connCtx.endpoint)

			body, err := buildConnectionBody("remote-tool", "https://example.test", "none", "", nil, nil, "", "")
			require.NoError(t, err)
			created, err := connCtx.armClient.Create(
				fixture.ctx, connCtx.rg, connCtx.account, connCtx.project, "endpoint-smoke",
				&armcognitiveservices.ProjectConnectionsClientCreateOptions{Connection: body},
			)
			require.NoError(t, err)
			assert.Equal(t, connectionPath, deref(created.ID))
			assert.Equal(t, "https://example.test", deref(created.Properties.GetConnectionPropertiesV2().Target))

			shown, err := connCtx.armClient.Get(
				fixture.ctx, connCtx.rg, connCtx.account, connCtx.project, "endpoint-smoke", nil)
			require.NoError(t, err)
			assert.Equal(t, connectionPath, deref(shown.ID))
			pager := connCtx.armClient.NewListPager(connCtx.rg, connCtx.account, connCtx.project, nil)
			page, err := pager.NextPage(fixture.ctx)
			require.NoError(t, err)
			require.Len(t, page.Value, 1)
			assert.Equal(t, connectionPath, deref(page.Value[0].ID))

			updatedBody, err := buildConnectionBody(
				"remote-tool", "https://example.test/updated", "none", "", nil, nil, "", "")
			require.NoError(t, err)
			updated, err := connCtx.armClient.Create(
				fixture.ctx, connCtx.rg, connCtx.account, connCtx.project, "endpoint-smoke",
				&armcognitiveservices.ProjectConnectionsClientCreateOptions{Connection: updatedBody},
			)
			require.NoError(t, err)
			assert.Equal(t, "https://example.test/updated", deref(updated.Properties.GetConnectionPropertiesV2().Target))

			rawProperties := rawConnectionProperties{
				AuthType: "UserEntraToken", Category: "RemoteTool", Target: "https://example.test/raw",
				Audience: "https://ai.azure.com",
			}
			require.NoError(t, rawCreateConnection(fixture.ctx, connCtx, "endpoint-smoke", rawProperties))
			rawResult, err := rawGetConnection(fixture.ctx, connCtx, "endpoint-smoke")
			require.NoError(t, err)
			assert.Equal(t, &rawProperties, rawResult)
			_, err = connCtx.armClient.Delete(
				fixture.ctx, connCtx.rg, connCtx.account, connCtx.project, "endpoint-smoke", nil)
			require.NoError(t, err)
			assert.False(t, exists)
			assert.Equal(t, []string{
				"PUT management.azure.com" + connectionPath,
				"GET management.azure.com" + connectionPath,
				"GET management.azure.com" + identityProjectID + "/connections",
				"PUT management.azure.com" + connectionPath,
				"PUT management.azure.com" + connectionPath,
				"GET management.azure.com" + connectionPath,
				"DELETE management.azure.com" + connectionPath,
			}, requests[metadataCalls+discoveryCalls:])
			fixture.environment.mu.Lock()
			defer fixture.environment.mu.Unlock()
			assert.Empty(t, fixture.environment.client.setRequests)
			assert.Equal(t, staging, fixture.environment.client.values["staging"])
			assert.Equal(t, fixture.production, fixture.environment.client.values["production"])
		})
	}
}

func TestConnectionContextRejectsConflictingEndpointSelection(t *testing.T) {
	for _, tt := range []struct {
		name              string
		projectID         string
		persistedEndpoint string
		flagEndpoint      string
	}{
		{name: "selected environment has another account", projectID: strings.ReplaceAll(identityProjectID, "arm-account", "other")},
		{name: "selected environment has another project", projectID: strings.ReplaceAll(identityProjectID, "project-one", "other")},
		{name: "selected environment has another scope", projectID: strings.ReplaceAll(identityProjectID, "staging-rg", "other")},
		{
			name: "explicit endpoint conflicts with environment identity", projectID: identityProjectID,
			persistedEndpoint: "https://other.services.ai.azure.com/api/projects/project-one", flagEndpoint: identityEndpoint,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			staging := map[string]string{
				"FOUNDRY_PROJECT_ENDPOINT":                 identityEndpoint,
				"AZURE_AI_PROJECT_ID":                      tt.projectID,
				"AZURE_SUBSCRIPTION_ID":                    "staging-sub",
				envkey.ConnectionProjectEndpoint("search"): identityEndpoint,
			}
			if tt.persistedEndpoint != "" {
				staging["FOUNDRY_PROJECT_ENDPOINT"] = tt.persistedEndpoint
			}
			fixture := newConnectionContextFixture(t, staging, nil)
			var requests []string
			options := identityClientOptions(deleteReadinessTransport(func(request *http.Request) (*http.Response, error) {
				requests = append(requests, request.Method)
				assert.Equal(t, http.MethodGet, request.Method)
				if request.URL.Host == "management.azure.com" {
					assert.Equal(t, tt.projectID, request.URL.Path)
					return identityResponse(request, http.StatusOK,
							`{"properties":{"endpoints":{"AI Foundry API":"https://other.services.ai.azure.com/api/projects/project-one"}}}`),
						nil
				}
				assert.Equal(t, "custom-dns.services.ai.azure.com", request.URL.Host)
				assert.Equal(t, "/api/projects/project-one/connections", request.URL.Path)
				return identityResponse(request, http.StatusOK, `{"value":[]}`), nil
			}))
			resolved, err := resolveConnectionContextWithClients(fixture.ctx, tt.flagEndpoint, "staging",
				func(tenant string) (azcore.TokenCredential, error) {
					assert.Equal(t, connectionContextStagingTenant, tenant)
					return deleteReadinessCredential{}, nil
				}, options)
			assert.Nil(t, resolved)
			requireConnectionValidationError(t, err, exterrors.CodeConflictingArguments, "AZURE_AI_PROJECT_ID")
			assert.NotEmpty(t, requests)
			fixture.environment.mu.Lock()
			defer fixture.environment.mu.Unlock()
			assert.Empty(t, fixture.environment.client.setRequests)
			assert.Equal(t, staging, fixture.environment.client.values["staging"])
			assert.Equal(t, fixture.production, fixture.environment.client.values["production"])
		})
	}
}

func identityResponse(request *http.Request, statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Status:     fmt.Sprintf("%d %s", statusCode, http.StatusText(statusCode)),
		Request:    request,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func identityClientOptions(transport policy.Transporter) *arm.ClientOptions {
	return &arm.ClientOptions{
		ClientOptions: policy.ClientOptions{
			Transport: transport, Retry: policy.RetryOptions{MaxRetries: -1},
		},
		DisableRPRegistration: true,
	}
}
