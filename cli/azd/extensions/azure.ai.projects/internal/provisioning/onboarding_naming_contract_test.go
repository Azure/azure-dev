// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"strings"
	"testing"

	"azure.ai.projects/internal/onboarding"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBYONamingUsesEffectiveFoundryProjectParameter(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "environment name", true: "explicit template name"}[explicit], func(t *testing.T) {
			p := byoContractProvider(t)
			wantName, wantPart := p.foundryName, "contra"
			if explicit {
				wantName, wantPart = "Sample-Project_123", "sample"
				p.onDiskSource = &templateSource{
					armTemplate: p.armTemplate,
					parameters:  map[string]any{"foundryProjectName": map[string]any{"value": wantName}},
				}
			}
			var events []string
			p.byoPipeline = byoContractPipeline(&events, onboarding.Pass)
			var requestParameters map[string]struct {
				Value json.RawMessage `json:"value"`
			}
			requests := 0
			p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
				Transport: byoHTTPContract(func(request *http.Request) (*http.Response, error) {
					requests++
					require.Equal(t, http.MethodPut, request.Method)
					var body struct {
						Properties struct {
							Parameters map[string]struct {
								Value json.RawMessage `json:"value"`
							} `json:"parameters"`
						} `json:"properties"`
					}
					require.NoError(t, json.NewDecoder(request.Body).Decode(&body))
					requestParameters = body.Properties.Parameters
					return &http.Response{
						StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
						Body: io.NopCloser(strings.NewReader(`{"id":"` + request.URL.Path +
							`","properties":{"provisioningState":"Succeeded","outputs":{}}}`)),
						Request: request,
					}, nil
				}),
			}}
			_, err := p.Deploy(t.Context(), func(string) {})
			require.NoError(t, err)
			require.Equal(t, 1, requests)
			require.NotNil(t, p.byoPlan)
			owner := p.byoPlan.Owner
			assert.Equal(t, "sa"+wantPart+owner, p.byoPlan.Storage.Name)
			assert.Equal(t, "hosting-"+wantPart+owner, p.byoPlan.Hosting.Name)
			assert.Equal(t, "workload-"+wantPart+owner, p.byoPlan.Workload.Name)
			assert.True(t, strings.HasSuffix(p.byoSpec.ProjectID, "/projects/"+wantName))
			source, err := p.resolveTemplate(t.Context(), func(string) {})
			require.NoError(t, err)
			assert.Equal(t, wantName, byoParameterValue(source, "foundryProjectName"))
			raw, err := json.Marshal(byoParameterValue(source, "byoOnboarding"))
			require.NoError(t, err)
			var bindings map[string]any
			require.NoError(t, json.Unmarshal(raw, &bindings))
			var submitted map[string]any
			require.NoError(t, json.Unmarshal(requestParameters["byoOnboarding"].Value, &submitted))
			assert.Equal(t, bindings, submitted, "actual SDK request uses the exact naming plan")
			var submittedProject string
			require.NoError(t, json.Unmarshal(requestParameters["foundryProjectName"].Value, &submittedProject))
			assert.Equal(t, wantName, submittedProject)
			assert.Equal(t, p.byoPlan.Storage.Name, bindings["storageName"])
			assert.Equal(t, p.byoPlan.Hosting.Name, bindings["hostingIdentityName"])
			assert.Equal(t, p.byoPlan.Workload.Name, bindings["workloadIdentityName"])
			assert.Equal(t, owner, bindings["owner"])
			assert.Equal(t, "azd"+owner, bindings["registryName"])
			assert.Equal(t, "ais-"+owner, bindings["accountName"])
		})
	}
}

func TestBYONamingRejectsInvalidEffectiveProjectBeforePreflight(t *testing.T) {
	for _, value := range []any{"", "---中_", 42} {
		p := byoContractProvider(t)
		p.onDiskSource = &templateSource{
			armTemplate: p.armTemplate,
			parameters:  map[string]any{"foundryProjectName": map[string]any{"value": value}},
		}
		var events []string
		p.byoPipeline = byoContractPipeline(&events, onboarding.Pass)
		before := maps.Clone(p.synthResult.Parameters)
		err := p.planBYO(t.Context(), func(string) {}, false)
		require.ErrorContains(t, err, "foundryProjectName")
		assert.Empty(t, events, "reject invalid naming input before resource or authorization reads")
		assert.Equal(t, before, p.synthResult.Parameters)
		assert.Nil(t, p.byoPlan)
	}
}
