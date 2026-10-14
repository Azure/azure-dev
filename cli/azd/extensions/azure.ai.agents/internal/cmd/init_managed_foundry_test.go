// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/recordproxy"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestValidatePromptHarnessLocation(t *testing.T) {
	t.Parallel()
	for _, location := range []string{
		"japaneast", "swedencentral", "uksouth",
		"australiaeast", "westeurope", "germanywestcentral", " JapanEast ",
		"canadacentral", "westus2",
	} {
		require.NoError(t, validatePromptHarnessLocation(location, promptHarnessRegions))
	}
	for _, location := range []string{"eastus", "eastus2", "westus", ""} {
		err := validatePromptHarnessLocation(location, promptHarnessRegions)
		require.ErrorContains(t, err, "not supported for the GitHub Copilot harness")
		require.NoError(t, validatePromptHarnessLocation(location, nil), "plain prompt must remain unrestricted")
	}
}

func TestSelectPromptFoundryProject_Regions(t *testing.T) {
	const projectID = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acct/projects/"
	tests := []struct {
		name           string
		explicitID     bool
		restricted     bool
		location       string
		wantError      bool
		allUnsupported bool
	}{
		{name: "picker filters unsupported", restricted: true, location: "eastus"},
		{name: "picker excludes unknown", restricted: true},
		{name: "picker with no eligible projects", restricted: true, location: "eastus", allUnsupported: true},
		{name: "plain picker unchanged", location: "eastus"},
		{name: "explicit unsupported rejected", explicitID: true, restricted: true, location: "eastus", wantError: true},
		{name: "explicit unknown rejected", explicitID: true, restricted: true, wantError: true},
		{name: "explicit eastus2 rejected", explicitID: true, restricted: true, location: "eastus2", wantError: true},
		{name: "explicit supported accepted", explicitID: true, restricted: true, location: "japaneast"},
		{name: "plain explicit unchanged", explicitID: true, location: "eastus"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalTransport := recordproxy.Transport
			recordproxy.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				projectJSON := fmt.Sprintf(`{"id":%q,"name":"acct/first","location":%q}`, projectID+"first", tt.location)
				if !tt.explicitID {
					secondLocation := "japaneast"
					if tt.allUnsupported {
						secondLocation = "westus"
					}
					projectJSON = fmt.Sprintf(`{"value":[%s,{"id":%q,"location":%q}]}`,
						projectJSON, projectID+"supported", secondLocation)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {"application/json"}},
					Body:       io.NopCloser(strings.NewReader(projectJSON)),
					Request:    req,
				}, nil
			})
			t.Cleanup(func() { recordproxy.Transport = originalTransport })
			prompts := &helpersPromptServer{}
			client := newTestAzdClient(t, &testEnvironmentServiceServer{}, &testWorkflowServiceServer{}, prompts)
			var allowed []string
			if tt.restricted {
				allowed = promptHarnessRegions
			}
			id := ""
			if tt.explicitID {
				id = projectID + "first"
			}
			proj, err := selectPromptFoundryProject(t.Context(), client, responseTestCredential{},
				&azdext.AzureContext{Scope: &azdext.AzureScope{SubscriptionId: "sub"}}, "test", id, tt.explicitID, allowed)
			if tt.wantError {
				require.ErrorContains(t, err, "not supported for the GitHub Copilot harness")
				require.Nil(t, proj)
				return
			}
			require.NoError(t, err)
			if tt.allUnsupported {
				require.Nil(t, proj)
				require.EqualValues(t, 1, prompts.selectCalls.Load(), "must not prompt with an empty project list")
				return
			}
			if tt.restricted && !tt.explicitID {
				require.Equal(t, "supported", proj.ProjectName)
				require.Len(t, prompts.lastSelect.Options.Choices, 1)
			} else {
				require.Equal(t, "first", proj.ProjectName)
				if !tt.explicitID {
					require.Len(t, prompts.lastSelect.Options.Choices, 2)
				}
			}
		})
	}
}

type harnessLocationPromptServer struct {
	azdext.UnimplementedPromptServiceServer
	request *azdext.PromptLocationRequest
}

func (s *harnessLocationPromptServer) PromptLocation(
	_ context.Context, req *azdext.PromptLocationRequest,
) (*azdext.PromptLocationResponse, error) {
	s.request = req
	return &azdext.PromptLocationResponse{Location: &azdext.Location{Name: "japaneast"}}, nil
}

func TestEnsureLocationWithAllowedLocations_Harness(t *testing.T) {
	for _, location := range []string{"", "eastus", "eastus2", "japaneast"} {
		t.Run(location, func(t *testing.T) {
			const envName = "test"
			envServer := &testEnvironmentServiceServer{values: map[string]map[string]string{envName: {}}}
			prompts := &harnessLocationPromptServer{}
			client := newTestAzdClient(t, envServer, &testWorkflowServiceServer{}, prompts)
			azureContext := &azdext.AzureContext{Scope: &azdext.AzureScope{Location: location}}
			require.NoError(t, ensureLocationWithAllowedLocations(
				t.Context(), client, azureContext, envName, promptHarnessRegions,
			))
			require.Equal(t, "japaneast", envServer.values[envName]["AZURE_LOCATION"])
			require.Equal(t, "japaneast", envServer.values[envName]["AZURE_AI_DEPLOYMENTS_LOCATION"])
			if location == "japaneast" {
				require.Nil(t, prompts.request)
			} else {
				require.NotNil(t, prompts.request)
				require.Equal(t, promptHarnessRegions, prompts.request.AllowedLocations)
			}
		})
	}
}
