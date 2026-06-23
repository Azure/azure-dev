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
			name: "brownfield: endpoint + network => network ignored, still ErrEndpointBrownfield",
			yaml: `
services:
  my-project:
    host: azure.ai.agent
    endpoint: https://existing.services.ai.azure.com/api/projects/p1
    network:
      peSubnet: {vnet: /subscriptions/s/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/v, name: pe}
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

func TestSynthesize_NetworkPreserveVarRefs(t *testing.T) {
	// Eject path: ${VAR} references must pass through verbatim (and skip the
	// format checks that cannot run on an unexpanded placeholder), so the
	// ejected main.parameters.json stays environment-portable.
	yaml := `
services:
  my-project:
    host: azure.ai.agent
    network:
      peSubnet: {vnet: "${AZURE_VNET_ID}", name: pe-subnet}
      dns:
        resourceGroup: rg-dns
        subscription: "${AZURE_DNS_SUBSCRIPTION_ID}"
`
	res, err := Synthesize(Input{
		RawAzureYAML:    []byte(yaml),
		ServiceName:     "my-project",
		AcceptedHosts:   []string{"azure.ai.agent"},
		PreserveVarRefs: true,
	})
	require.NoError(t, err, "unset ${VAR} must not fail on the eject path")
	require.NotNil(t, res)
	assert.Equal(t, "${AZURE_VNET_ID}", res.Parameters["vnetId"])
	assert.Equal(t, "${AZURE_DNS_SUBSCRIPTION_ID}", res.Parameters["dnsZonesSubscription"])
	assert.Equal(t, "rg-dns", res.Parameters["dnsZonesResourceGroup"])
}

func TestSynthesize_NetworkPreserveVarRefs_StillValidatesConcrete(t *testing.T) {
	// PreserveVarRefs only skips checks for unexpanded placeholders; a
	// concrete-but-malformed value still fails on the eject path.
	yaml := `
services:
  my-project:
    host: azure.ai.agent
    network:
      peSubnet: {vnet: not-an-arm-id, name: pe-subnet}
`
	_, err := Synthesize(Input{
		RawAzureYAML:    []byte(yaml),
		ServiceName:     "my-project",
		AcceptedHosts:   []string{"azure.ai.agent"},
		PreserveVarRefs: true,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a well-formed")
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
		"templates/modules/network.bicep",
		"templates/modules/subnet.bicep",
		"templates/modules/private-endpoint-dns.bicep",
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

	// Network isolation parameters must exist so the synthesizer's network
	// param set is accepted by ARM (extra params would fail the deployment).
	for _, p := range []string{
		"enableNetworkIsolation", "useManagedEgress", "vnetId",
		"agentSubnetName", "agentSubnetPrefix", "createAgentSubnet",
		"peSubnetName", "peSubnetPrefix", "createPESubnet",
		"managedIsolationMode", "dnsZonesResourceGroup", "dnsZonesSubscription",
	} {
		assert.Contains(t, params, p, "network param %q must be declared in the ARM template", p)
	}

	// The old mode-enum param must be gone; egress is driven by useManagedEgress.
	assert.NotContains(t, params, "networkMode",
		"networkMode param was replaced by useManagedEgress")

	// Secure-by-default lock: the account data plane must be private whenever
	// network isolation is on. The compiled template must gate public access on
	// enableNetworkIsolation (not on egress mode), so a network-bound account is
	// never left public. This is the regression guard for the data-plane fix.
	text := string(data)
	wantDisable := `"disablePublicDataPlaneAccess": "[parameters('enableNetworkIsolation')]"`
	wantPublic := `"publicNetworkAccess": "[if(variables('disablePublicDataPlaneAccess'), 'Disabled', 'Enabled')]"`
	assert.Contains(t, text, wantDisable,
		"public data-plane access must be disabled for every network-isolated account")
	assert.Contains(t, text, wantPublic,
		"account publicNetworkAccess must follow disablePublicDataPlaneAccess")

	// Egress injection shape: byo injects into the customer subnet
	// (useMicrosoftManagedNetwork=false), managed uses the Microsoft-managed
	// network (useMicrosoftManagedNetwork=true). Both branches must survive
	// compilation so the account gets the right networkInjections per mode.
	assert.Contains(t, text, "'useMicrosoftManagedNetwork', false()",
		"byo egress must inject the agent subnet (useMicrosoftManagedNetwork=false)")
	assert.Contains(t, text, "'useMicrosoftManagedNetwork', true()",
		"managed egress must use the Microsoft-managed network (useMicrosoftManagedNetwork=true)")
	assert.Contains(t, text, `"networkInjections": "[variables('agentNetworkInjections')]"`,
		"account must carry the computed networkInjections")

	// isolationMode must be wired to the V2 managed network child resource
	// (regression guard: it was previously a no-op echoed only to output).
	assert.Contains(t, text, `"type": "Microsoft.CognitiveServices/accounts/managedNetworks"`,
		"managed isolationMode must provision a managedNetworks child resource")
	assert.Contains(t, text, `"isolationMode": "[parameters('managedIsolationMode')]"`,
		"managedNetworks isolationMode must come from the managedIsolationMode param")
}

func TestSynthesize_Network(t *testing.T) {
	t.Setenv("AZURE_VNET_ID",
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/"+
			"providers/Microsoft.Network/virtualNetworks/my-vnet")

	const validVNet = "/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/rg/" +
		"providers/Microsoft.Network/virtualNetworks/my-vnet"

	tests := []struct {
		name     string
		yaml     string
		wantMode string
		check    func(t *testing.T, p map[string]any)
	}{
		{
			name: "no network block => public account, isolation off",
			yaml: `
services:
  my-project:
    host: azure.ai.agent
    deployments:
      - name: gpt-4.1-mini
        model: {format: OpenAI, name: gpt-4.1-mini, version: "2025-04-14"}
        sku: {capacity: 10, name: GlobalStandard}
`,
			wantMode: NetworkModeNone,
			check: func(t *testing.T, p map[string]any) {
				assert.Equal(t, false, p["enableNetworkIsolation"])
				assert.Equal(t, false, p["useManagedEgress"])
			},
		},
		{
			name: "byo egress (agentSubnet present) with explicit subnets => create both",
			yaml: `
services:
  my-project:
    host: azure.ai.agent
    network:
      agentSubnet: {vnet: ` + validVNet + `, name: agent-subnet, prefix: 192.168.0.0/24}
      peSubnet: {vnet: ` + validVNet + `, name: pe-subnet, prefix: 192.168.1.0/24}
      dns:
        resourceGroup: rg-private-dns
        subscription: 22222222-2222-2222-2222-222222222222
`,
			wantMode: NetworkModeByo,
			check: func(t *testing.T, p map[string]any) {
				assert.Equal(t, true, p["enableNetworkIsolation"])
				assert.Equal(t, false, p["useManagedEgress"])
				assert.Equal(t, validVNet, p["vnetId"])
				assert.Equal(t, "agent-subnet", p["agentSubnetName"])
				assert.Equal(t, "192.168.0.0/24", p["agentSubnetPrefix"])
				assert.Equal(t, true, p["createAgentSubnet"])
				assert.Equal(t, true, p["createPESubnet"])
				assert.Equal(t, "rg-private-dns", p["dnsZonesResourceGroup"])
				assert.Equal(t, "22222222-2222-2222-2222-222222222222", p["dnsZonesSubscription"])
			},
		},
		{
			name: "subnet without prefix => reference (create=false)",
			yaml: `
services:
  my-project:
    host: azure.ai.agent
    network:
      agentSubnet: {vnet: ` + validVNet + `, name: existing-agent}
      peSubnet: {vnet: ` + validVNet + `, name: pe-subnet, prefix: 192.168.1.0/24}
`,
			wantMode: NetworkModeByo,
			check: func(t *testing.T, p map[string]any) {
				assert.Equal(t, "existing-agent", p["agentSubnetName"])
				assert.Equal(t, false, p["createAgentSubnet"])
				assert.Equal(t, "pe-subnet", p["peSubnetName"])
				assert.Equal(t, true, p["createPESubnet"])
			},
		},
		{
			name: "subnet vnet from ${VAR}",
			yaml: `
services:
  my-project:
    host: azure.ai.agent
    network:
      peSubnet: {vnet: "${AZURE_VNET_ID}", name: pe-subnet}
`,
			wantMode: NetworkModeManaged,
			check: func(t *testing.T, p map[string]any) {
				assert.Contains(t, p["vnetId"], "/virtualNetworks/my-vnet")
			},
		},
		{
			name: "managed egress (agentSubnet absent) with isolation",
			yaml: `
services:
  my-project:
    host: azure.ai.agent
    network:
      isolationMode: AllowOnlyApprovedOutbound
      peSubnet: {vnet: ` + validVNet + `, name: pe-subnet, prefix: 192.168.1.0/24}
`,
			wantMode: NetworkModeManaged,
			check: func(t *testing.T, p map[string]any) {
				assert.Equal(t, true, p["enableNetworkIsolation"])
				assert.Equal(t, true, p["useManagedEgress"])
				assert.Equal(t, false, p["createAgentSubnet"])
				assert.Equal(t, "AllowOnlyApprovedOutbound", p["managedIsolationMode"])
			},
		},
		{
			name: "dns subscription normalized from /subscriptions/<guid>",
			yaml: `
services:
  my-project:
    host: azure.ai.agent
    network:
      peSubnet: {vnet: ` + validVNet + `, name: pe-subnet}
      dns:
        resourceGroup: rg-dns
        subscription: /subscriptions/33333333-3333-3333-3333-333333333333
`,
			wantMode: NetworkModeManaged,
			check: func(t *testing.T, p map[string]any) {
				assert.Equal(t, "33333333-3333-3333-3333-333333333333", p["dnsZonesSubscription"])
			},
		},
		{
			name: "managed egress, isolationMode unset => empty managedIsolationMode",
			yaml: `
services:
  my-project:
    host: azure.ai.agent
    network:
      peSubnet: {vnet: ` + validVNet + `, name: pe-subnet, prefix: 192.168.1.0/24}
`,
			wantMode: NetworkModeManaged,
			check: func(t *testing.T, p map[string]any) {
				assert.Equal(t, true, p["useManagedEgress"])
				assert.Equal(t, "", p["managedIsolationMode"])
				assert.Equal(t, true, p["createPESubnet"])
			},
		},
		{
			name: "managed egress, AllowInternetOutbound with referenced peSubnet",
			yaml: `
services:
  my-project:
    host: azure.ai.agent
    network:
      isolationMode: AllowInternetOutbound
      peSubnet: {vnet: ` + validVNet + `, name: existing-pe}
`,
			wantMode: NetworkModeManaged,
			check: func(t *testing.T, p map[string]any) {
				assert.Equal(t, true, p["useManagedEgress"])
				assert.Equal(t, "AllowInternetOutbound", p["managedIsolationMode"])
				assert.Equal(t, "existing-pe", p["peSubnetName"])
				assert.Equal(t, false, p["createPESubnet"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := Synthesize(Input{
				RawAzureYAML:  []byte(tt.yaml),
				ServiceName:   "my-project",
				AcceptedHosts: []string{"azure.ai.agent"},
			})
			require.NoError(t, err)
			require.NotNil(t, res)
			assert.Equal(t, tt.wantMode, res.NetworkMode)
			if tt.check != nil {
				tt.check(t, res.Parameters)
			}
		})
	}
}

func TestSynthesize_NetworkValidationErrors(t *testing.T) {
	const validVNet = "/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/rg/" +
		"providers/Microsoft.Network/virtualNetworks/my-vnet"
	const validVNet2 = "/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/rg/" +
		"providers/Microsoft.Network/virtualNetworks/other-vnet"

	tests := []struct {
		name    string
		yaml    string
		wantSub string
	}{
		{
			name: "network present but peSubnet missing",
			yaml: `
services:
  my-project:
    host: azure.ai.agent
    network:
      isolationMode: AllowInternetOutbound
`,
			wantSub: "private networking requires peSubnet",
		},
		{
			name: "isolationMode with agentSubnet present",
			yaml: `
services:
  my-project:
    host: azure.ai.agent
    network:
      isolationMode: AllowInternetOutbound
      agentSubnet: {vnet: ` + validVNet + `, name: a, prefix: 192.168.0.0/24}
      peSubnet: {vnet: ` + validVNet + `, name: pe, prefix: 192.168.1.0/24}
`,
			wantSub: "only valid for managed egress",
		},
		{
			name: "agentSubnet and peSubnet in different vnets",
			yaml: `
services:
  my-project:
    host: azure.ai.agent
    network:
      agentSubnet: {vnet: ` + validVNet + `, name: a, prefix: 192.168.0.0/24}
      peSubnet: {vnet: ` + validVNet2 + `, name: pe, prefix: 192.168.1.0/24}
`,
			wantSub: "same virtual network",
		},
		{
			name: "agentSubnet and peSubnet share a name in one vnet",
			yaml: `
services:
  my-project:
    host: azure.ai.agent
    network:
      agentSubnet: {vnet: ` + validVNet + `, name: shared, prefix: 192.168.0.0/24}
      peSubnet: {vnet: ` + validVNet + `, name: shared, prefix: 192.168.1.0/24}
`,
			wantSub: "agentSubnet.name and peSubnet.name must differ",
		},
		{
			name: "subnet missing vnet",
			yaml: `
services:
  my-project:
    host: azure.ai.agent
    network:
      peSubnet: {name: pe}
`,
			wantSub: "peSubnet.vnet: required",
		},
		{
			name: "subnet missing name",
			yaml: `
services:
  my-project:
    host: azure.ai.agent
    network:
      peSubnet: {vnet: ` + validVNet + `}
`,
			wantSub: "peSubnet.name: required",
		},
		{
			name: "malformed vnet id",
			yaml: `
services:
  my-project:
    host: azure.ai.agent
    network:
      peSubnet: {vnet: not-an-arm-id, name: pe}
`,
			wantSub: "not a well-formed",
		},
		{
			name: "subnet invalid cidr",
			yaml: `
services:
  my-project:
    host: azure.ai.agent
    network:
      peSubnet: {vnet: ` + validVNet + `, name: pe, prefix: not-a-cidr}
`,
			wantSub: "not a valid CIDR",
		},
		{
			name: "unresolved var",
			yaml: `
services:
  my-project:
    host: azure.ai.agent
    network:
      peSubnet: {vnet: "${DEFINITELY_NOT_SET_VAR_XYZ}", name: pe}
`,
			wantSub: "unresolved environment variable",
		},
		{
			name: "bad managed isolation mode",
			yaml: `
services:
  my-project:
    host: azure.ai.agent
    network:
      isolationMode: Wide
      peSubnet: {vnet: ` + validVNet + `, name: pe}
`,
			wantSub: "isolationMode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Synthesize(Input{
				RawAzureYAML:  []byte(tt.yaml),
				ServiceName:   "my-project",
				AcceptedHosts: []string{"azure.ai.agent"},
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantSub)
			// Errors carry the service-scoped field path.
			assert.Contains(t, err.Error(), "services.my-project.network")
		})
	}
}
