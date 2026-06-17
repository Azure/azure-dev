// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSynthesize(t *testing.T) {
	tests := []struct {
		name        string
		yaml        string
		serviceName string

		wantErr        error
		wantDeployLen  int
		wantIncludeAcr bool
		// wantDeployName0, if non-empty, asserts the name of the first deployment.
		wantDeployName0 string
	}{
		{
			name: "greenfield hosted agent with docker",
			yaml: `
name: my-foundry-agent
services:
  my-project:
    host: azure.ai.agent
    deployments:
      - name: gpt-4.1-mini
        model:
          format: OpenAI
          name: gpt-4.1-mini
          version: "2025-04-14"
        sku:
          capacity: 10
          name: GlobalStandard
    agents:
      - name: my-agent
        kind: hosted
        project: src/my-agent
        docker:
          path: Dockerfile
          remoteBuild: true
`,
			serviceName:     "my-project",
			wantDeployLen:   1,
			wantIncludeAcr:  true,
			wantDeployName0: "gpt-4.1-mini",
		},
		{
			name: "greenfield hosted agent runtime-only (no docker)",
			yaml: `
name: my-foundry-agent
services:
  my-project:
    host: azure.ai.agent
    deployments:
      - name: gpt-4.1-mini
        model:
          format: OpenAI
          name: gpt-4.1-mini
          version: "2025-04-14"
        sku:
          capacity: 10
          name: GlobalStandard
    agents:
      - name: my-agent
        kind: hosted
        project: src/my-agent
        runtime:
          stack: python
          version: "3.12"
`,
			serviceName:    "my-project",
			wantDeployLen:  1,
			wantIncludeAcr: false,
		},
		{
			name: "prompt-only agent (no project/runtime/docker)",
			yaml: `
services:
  my-project:
    host: azure.ai.agent
    deployments:
      - name: gpt-4.1-mini
        model:
          format: OpenAI
          name: gpt-4.1-mini
          version: "2025-04-14"
        sku:
          capacity: 10
          name: GlobalStandard
    agents:
      - name: triage-agent
        kind: prompt
        instructions: route the user
`,
			serviceName:    "my-project",
			wantDeployLen:  1,
			wantIncludeAcr: false,
		},
		{
			name: "mixed: one runtime agent and one docker agent => ACR on",
			yaml: `
services:
  my-project:
    host: azure.ai.agent
    deployments:
      - name: gpt-4.1
        model:
          format: OpenAI
          name: gpt-4.1
          version: "2025-04-14"
        sku:
          capacity: 50
          name: GlobalStandard
    agents:
      - name: support-agent
        kind: hosted
        project: src/support-agent
        runtime: {stack: python, version: "3.12"}
      - name: research-agent
        kind: hosted
        project: src/research-agent
        docker: {path: Dockerfile, remoteBuild: true}
`,
			serviceName:    "my-project",
			wantDeployLen:  1,
			wantIncludeAcr: true,
		},
		{
			name: "no deployments declared => empty array, not nil",
			yaml: `
services:
  my-project:
    host: azure.ai.agent
    agents:
      - name: prompt-agent
        kind: prompt
        instructions: hi
`,
			serviceName:    "my-project",
			wantDeployLen:  0,
			wantIncludeAcr: false,
		},
		{
			name: "ignores connections/toolboxes/skills (deploy-time concerns)",
			yaml: `
services:
  my-project:
    host: azure.ai.agent
    deployments:
      - name: gpt-4.1-mini
        model: {format: OpenAI, name: gpt-4.1-mini, version: "2025-04-14"}
        sku: {capacity: 10, name: GlobalStandard}
    connections:
      - name: github-mcp-conn
        category: CustomKeys
        target: https://api.githubcopilot.com/mcp
        authType: CustomKeys
    toolboxes:
      - name: t1
        tools: [{type: web_search}]
    skills:
      - name: s1
        instructions: hi
    routines:
      - name: r1
        agent: prompt-agent
        trigger: {type: schedule, cron: "0 8 * * *"}
    agents:
      - name: prompt-agent
        kind: prompt
        instructions: hi
`,
			serviceName:    "my-project",
			wantDeployLen:  1,
			wantIncludeAcr: false,
		},
		{
			name: "brownfield: endpoint set => ErrEndpointBrownfield",
			yaml: `
services:
  my-project:
    host: azure.ai.agent
    endpoint: https://existing.services.ai.azure.com/api/projects/p1
    deployments:
      - name: gpt-4.1-mini
        model: {format: OpenAI, name: gpt-4.1-mini, version: "2025-04-14"}
        sku: {capacity: 10, name: GlobalStandard}
`,
			serviceName: "my-project",
			wantErr:     ErrEndpointBrownfield,
		},
		{
			name: "service not found",
			yaml: `
services:
  my-project:
    host: azure.ai.agent
`,
			serviceName: "nope",
			wantErr:     ErrServiceNotFound,
		},
		{
			name: "wrong host treated as not found",
			yaml: `
services:
  webapp:
    host: containerapp
    project: src/web
`,
			serviceName: "webapp",
			wantErr:     ErrServiceNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := Synthesize(Input{
				RawAzureYAML:  []byte(tt.yaml),
				ServiceName:   tt.serviceName,
				AcceptedHosts: []string{"azure.ai.agent"},
			})

			if tt.wantErr != nil {
				require.Error(t, err)
				assert.True(t, errors.Is(err, tt.wantErr), "got %v, want %v", err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, res)

			deployments, ok := res.Parameters["deployments"].([]Deployment)
			require.True(t, ok, "deployments param should be []Deployment, got %T", res.Parameters["deployments"])
			assert.Len(t, deployments, tt.wantDeployLen)
			if tt.wantDeployName0 != "" {
				require.NotEmpty(t, deployments)
				assert.Equal(t, tt.wantDeployName0, deployments[0].Name)
			}

			includeAcr, ok := res.Parameters["includeAcr"].(bool)
			require.True(t, ok, "includeAcr param should be bool")
			assert.Equal(t, tt.wantIncludeAcr, includeAcr)
		})
	}
}

func TestSynthesize_InputValidation(t *testing.T) {
	tests := []struct {
		name string
		in   Input
		want string
	}{
		{
			name: "empty yaml",
			in:   Input{ServiceName: "x"},
			want: "RawAzureYAML is empty",
		},
		{
			name: "empty service name",
			in:   Input{RawAzureYAML: []byte("services:\n  x:\n    host: azure.ai.agent\n")},
			want: "ServiceName is empty",
		},
		{
			name: "malformed yaml",
			in: Input{
				RawAzureYAML: []byte("services: [this is not a map"),
				ServiceName:  "x",
			},
			want: "parse azure.yaml",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Synthesize(tt.in)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestTemplatesFS_Embedded(t *testing.T) {
	fs := TemplatesFS()

	wantFiles := []string{
		"templates/main.bicep",
		"templates/main.arm.json",
		"templates/abbreviations.json",
		"templates/modules/acr.bicep",
	}
	for _, p := range wantFiles {
		t.Run(p, func(t *testing.T) {
			data, err := fs.ReadFile(p)
			require.NoError(t, err)
			assert.NotEmpty(t, data, "%s should not be empty", p)
		})
	}
}

func TestARMTemplate_IsValidJSONWithExpectedShape(t *testing.T) {
	data, err := ARMTemplate()
	require.NoError(t, err)
	require.NotEmpty(t, data)

	var arm map[string]any
	require.NoError(t, json.Unmarshal(data, &arm), "ARM template must be valid JSON")

	// Sanity-check the ARM document is what we expect.
	assert.Contains(t, arm, "$schema")
	assert.Contains(t, arm, "resources")
	assert.Contains(t, arm, "parameters")

	// The template is subscription-scoped so `azd provision --preview` can run
	// what-if without creating the resource group first.
	schema, _ := arm["$schema"].(string)
	assert.Contains(t, schema, "subscriptionDeploymentTemplate",
		"main.bicep must target subscription scope")

	// resourceGroupName is the parameter that drives the resource group the
	// template creates; the provider supplies it at provision time.
	params, ok := arm["parameters"].(map[string]any)
	require.True(t, ok, "parameters must be an object")
	assert.Contains(t, params, "resourceGroupName")
}
