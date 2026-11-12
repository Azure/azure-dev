// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/recordproxy"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestValidateInitLocation(t *testing.T) {
	t.Parallel()
	allowed, err := parseAgentRegionsManifest(embeddedHarnessAgentRegionsJSON)
	require.NoError(t, err)
	require.NotEmpty(t, allowed)
	for _, location := range []string{
		"australiaeast", "brazilsouth", "canadacentral", "canadaeast", "centralus",
		"eastus", "francecentral", "germanywestcentral", "italynorth", "japaneast", " JapanEast ",
		"koreacentral", "northcentralus", "norwayeast", "polandcentral",
		"southeastasia", "southindia", "spaincentral", "swedencentral",
		"uksouth", "westeurope", "westus", "westus3",
	} {
		require.NoError(t, validateInitLocation(t.Context(), location, allowed))
	}
	for _, location := range []string{
		"eastus2", "eastus2euap", "japanwest", "southafricanorth", "southcentralus", "switzerlandnorth", "westus2", "",
	} {
		err := validateInitLocation(t.Context(), location, allowed)
		require.ErrorContains(t, err, "not supported for this agent setup")
		require.NoError(t, validateInitLocation(t.Context(), location, nil), "plain prompt must remain unrestricted")
	}
}

func TestInternalHarnessRegions(t *testing.T) {
	internal, err := parseAgentRegionsManifest(embeddedInternalHarnessRegionsJSON)
	require.NoError(t, err)
	resetAgentRegionsCache(t, &internalHarnessRegionsCache, internal)
	allowed, err := parseAgentRegionsManifest(embeddedHarnessAgentRegionsJSON)
	require.NoError(t, err)
	for _, harness := range []string{"microsoft_copilot_preview", "github_copilot_preview", "", "other_harness"} {
		t.Run(harness, func(t *testing.T) {
			ctx := withHarnessRegionPolicy(t.Context(), harness)
			for _, region := range []string{" EastUS2EUAP ", " WestUS2 "} {
				err := validateInitLocation(ctx, region, allowed)
				if harness == "microsoft_copilot_preview" || harness == "github_copilot_preview" {
					require.NoError(t, err)
				} else {
					require.Error(t, err, "internal regions must not broaden other restricted init flows")
				}
			}
			err = validateInitLocation(ctx, "unsupported", allowed)
			require.Error(t, err)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			for _, region := range internal {
				require.NotContains(t, localErr.Message, region)
				require.NotContains(t, localErr.Suggestion, region)
				require.NotContains(t, allowed, region)
			}
		})
	}
}

func TestSelectPromptFoundryProject_Regions(t *testing.T) {
	internal, err := parseAgentRegionsManifest(embeddedInternalHarnessRegionsJSON)
	require.NoError(t, err)
	resetAgentRegionsCache(t, &internalHarnessRegionsCache, internal)
	const projectID = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acct/projects/"
	tests := []struct {
		name           string
		explicitID     bool
		restricted     bool
		location       string
		wantError      bool
		allUnsupported bool
	}{
		{name: "picker filters unsupported", restricted: true, location: "eastus2"},
		{name: "picker includes internal", restricted: true, location: "eastus2euap"},
		{name: "picker includes westus2 internal", restricted: true, location: "westus2"},
		{name: "picker excludes unknown", restricted: true},
		{name: "picker with no eligible projects", restricted: true, location: "eastus2", allUnsupported: true},
		{name: "plain picker unchanged", location: "eastus2"},
		{name: "explicit unsupported rejected", explicitID: true, restricted: true, location: "japanwest", wantError: true},
		{name: "explicit unknown rejected", explicitID: true, restricted: true, wantError: true},
		{name: "explicit eastus2 rejected", explicitID: true, restricted: true, location: "eastus2", wantError: true},
		{name: "explicit westus accepted", explicitID: true, restricted: true, location: "westus"},
		{name: "explicit centralus accepted", explicitID: true, restricted: true, location: "centralus"},
		{name: "explicit eastus accepted", explicitID: true, restricted: true, location: "eastus"},
		{name: "explicit westus3 accepted", explicitID: true, restricted: true, location: "westus3"},
		{name: "explicit internal accepted", explicitID: true, restricted: true, location: "eastus2euap"},
		{name: "explicit westus2 internal accepted", explicitID: true, restricted: true, location: "westus2"},
		{name: "explicit eastus2-euap rejected", explicitID: true, restricted: true, location: "eastus2-euap", wantError: true},
		{name: "explicit supported accepted", explicitID: true, restricted: true, location: "japaneast"},
		{name: "plain explicit unchanged", explicitID: true, location: "eastus2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalTransport := recordproxy.Transport
			recordproxy.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				projectJSON := fmt.Sprintf(`{"id":%q,"name":"acct/first","location":%q}`, projectID+"first", tt.location)
				if !tt.explicitID {
					secondLocation := "japaneast"
					if tt.allUnsupported {
						secondLocation = "japanwest"
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
			ctx := t.Context()
			if tt.restricted {
				ctx = withHarnessRegionPolicy(ctx, "microsoft_copilot_preview")
				var err error
				allowed, err = parseAgentRegionsManifest(embeddedHarnessAgentRegionsJSON)
				require.NoError(t, err)
			}
			id := ""
			if tt.explicitID {
				id = projectID + "first"
			}
			proj, err := selectPromptFoundryProject(ctx, client, responseTestCredential{},
				&azdext.AzureContext{Scope: &azdext.AzureScope{SubscriptionId: "sub"}}, "test", id, tt.explicitID, allowed)
			if tt.wantError {
				require.ErrorContains(t, err, "not supported for this agent setup")
				require.Nil(t, proj)
				proj, err = selectFoundryProject(t.Context(), client, responseTestCredential{},
					&azdext.AzureContext{Scope: &azdext.AzureScope{SubscriptionId: "sub"}},
					"test", "sub", id, "", true, false, true, allowed)
				require.ErrorContains(t, err, "not supported for this agent setup")
				require.Nil(t, proj)
				return
			}
			require.NoError(t, err)
			if tt.location == "eastus2euap" || tt.location == "westus2" {
				promptSelection := prompts.lastSelect
				sharedProject, err := selectFoundryProject(ctx, client, responseTestCredential{},
					&azdext.AzureContext{Scope: &azdext.AzureScope{SubscriptionId: "sub"}},
					"test", "sub", id, "", true, false, true, allowed)
				require.NoError(t, err)
				require.NotNil(t, sharedProject)
				if tt.explicitID {
					require.Equal(t, "first", sharedProject.ProjectName)
				} else {
					require.Equal(t, "first", sharedProject.ProjectName)
					require.Len(t, prompts.lastSelect.Options.Choices, 3, "internal and public projects plus create-new are offered")
				}
				prompts.lastSelect = promptSelection
			}
			if tt.allUnsupported {
				require.Nil(t, proj)
				require.EqualValues(t, 1, prompts.selectCalls.Load(), "must not prompt with an empty project list")
				proj, err = selectFoundryProject(t.Context(), client, responseTestCredential{},
					&azdext.AzureContext{Scope: &azdext.AzureScope{SubscriptionId: "sub"}},
					"test", "sub", "", "", true, false, true, allowed)
				require.NoError(t, err)
				require.Nil(t, proj)
				require.EqualValues(t, 1, prompts.selectCalls.Load(), "shared selector must not add a Select call")
				return
			}
			if tt.restricted && !tt.explicitID && tt.location != "eastus2euap" && tt.location != "westus2" {
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
	internal, err := parseAgentRegionsManifest(embeddedInternalHarnessRegionsJSON)
	require.NoError(t, err)
	resetAgentRegionsCache(t, &internalHarnessRegionsCache, internal)
	allowed, err := parseAgentRegionsManifest(embeddedHarnessAgentRegionsJSON)
	require.NoError(t, err)
	for _, location := range []string{"", "eastus2", "japanwest", "eastus2-euap", "japaneast", "eastus2euap", "westus2"} {
		t.Run(location, func(t *testing.T) {
			const envName = "test"
			envServer := &testEnvironmentServiceServer{values: map[string]map[string]string{envName: {}}}
			prompts := &harnessLocationPromptServer{}
			client := newTestAzdClient(t, envServer, &testWorkflowServiceServer{}, prompts)
			azureContext := &azdext.AzureContext{Scope: &azdext.AzureScope{Location: location}}
			require.NoError(t, ensureLocationWithAllowedLocations(
				withHarnessRegionPolicy(t.Context(), "microsoft_copilot_preview"), client, azureContext, envName, allowed,
			))
			expectedLocation := "japaneast"
			if location == "eastus2euap" || location == "westus2" {
				expectedLocation = location
			}
			require.Equal(t, expectedLocation, envServer.values[envName]["AZURE_LOCATION"])
			require.Equal(t, expectedLocation, envServer.values[envName]["AZURE_AI_DEPLOYMENTS_LOCATION"])
			if location == "japaneast" || location == "eastus2euap" || location == "westus2" {
				require.Nil(t, prompts.request)
			} else {
				require.NotNil(t, prompts.request)
				require.Equal(t, allowed, prompts.request.AllowedLocations)
				require.NotContains(t, prompts.request.AllowedLocations, "eastus2euap")
				require.NotContains(t, prompts.request.AllowedLocations, "westus2")
			}
		})
	}
}

func TestInspectAzureYamlCopilotHarness(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"inline", "$ref"} {
		for _, harness := range []string{"microsoft_copilot_preview", "github_copilot_preview", ""} {
			t.Run(source+"/"+harness, func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				definition := "kind: prompt\n"
				if harness != "" {
					definition += "harness:\n  type: " + harness + "\n"
				}
				content := "services:\n  prompt:\n    host: azure.ai.agent\n    " + strings.ReplaceAll(
					strings.TrimSuffix(definition, "\n"), "\n", "\n    ") + "\n"
				if source == "$ref" {
					require.NoError(t, os.WriteFile(filepath.Join(root, "agent.yaml"), []byte(definition), 0o600))
					content = "services:\n  prompt:\n    host: azure.ai.agent\n    $ref: ./agent.yaml\n"
				}
				require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(content), 0o600))
				require.NoError(t, validateStagedAzureYaml(root, filepath.Join(root, "azure.yaml")))
				info, err := inspectAzureYaml([]byte(content), root)
				require.NoError(t, err)
				require.True(t, info.promptOnly())
				require.Equal(t, harness, info.promptHarness)
			})
		}
	}
}

func TestInspectAzureYamlRetainsRestrictedHarnessAcrossServices(t *testing.T) {
	t.Parallel()
	content := []byte(`services:
  copilot:
    host: azure.ai.agent
    kind: prompt
    harness:
      type: microsoft_copilot_preview
  other:
    host: azure.ai.agent
    kind: prompt
    harness:
      type: other
  plain:
    host: azure.ai.agent
    kind: prompt
`)
	for range 100 {
		info, err := inspectAzureYaml(content, "")
		require.NoError(t, err)
		require.True(t, info.promptOnly())
		require.Equal(t, "microsoft_copilot_preview", info.promptHarness)
	}
}
