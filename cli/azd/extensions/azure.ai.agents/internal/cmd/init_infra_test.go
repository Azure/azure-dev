// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// validFoundryAzureYAML returns an azure.yaml payload exercising the
// synthesizer's two derived parameters: deployments and includeAcr.
// Container deployment via the `docker:` block forces includeAcr=true.
const validFoundryAzureYAML = `name: my-project
metadata:
  template: azure.ai.agents
infra:
  provider: microsoft.foundry
services:
  my-foundry:
    host: azure.ai.project
    deployments:
      - name: gpt-4-1-mini
        model:
          name: gpt-4.1-mini
          format: OpenAI
          version: "2024-07-18"
        sku:
          name: GlobalStandard
          capacity: 50
    agents:
      - name: my-agent
        docker:
          path: src/my-agent
`

func TestEjectInfra_RefusesWhenAzureYamlMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	err := ejectInfra(dir, "bicep")
	require.Error(t, err)

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "expected structured azdext.LocalError, got %T: %v", err, err)
	assert.Equal(t, exterrors.CodeInfraEjectAzureYamlMissing, localErr.Code)
	assert.Contains(t, localErr.Message, "azure.yaml not found")
	assert.NotEmpty(t, localErr.Suggestion)

	// Refusal must not produce ./infra/.
	_, statErr := os.Stat(filepath.Join(dir, "infra"))
	assert.True(t, os.IsNotExist(statErr), "infra/ must not be created on refusal")
}

func TestEjectInfra_RefusesWhenInfraExists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), validFoundryAzureYAML)
	// Pre-create infra/ -- contents don't matter, even an empty dir refuses.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "infra"), 0o750))

	err := ejectInfra(dir, "bicep")
	require.Error(t, err)

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "expected structured azdext.LocalError, got %T", err)
	assert.Equal(t, exterrors.CodeInfraEjectExists, localErr.Code)
	assert.Contains(t, localErr.Message, "./infra/")
	assert.Contains(t, localErr.Suggestion, "delete the infra directory")

	// Pre-existing infra/ must not be wiped by the refusal.
	info, err := os.Stat(filepath.Join(dir, "infra"))
	require.NoError(t, err, "pre-existing infra/ must survive refusal")
	assert.True(t, info.IsDir())
}

func TestEjectInfra_RefusesWhenNoFoundryService(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		yaml string
	}{
		{
			name: "non-foundry services only",
			yaml: `name: my-project
services:
  webapp:
    host: containerapp
    project: src/web
`,
		},
		{
			name: "no services block at all",
			yaml: `name: my-project
infra:
  provider: microsoft.foundry
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			mustWriteFile(t, filepath.Join(dir, "azure.yaml"), tt.yaml)

			err := ejectInfra(dir, "bicep")
			require.Error(t, err)

			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok, "expected structured azdext.LocalError, got %T", err)
			assert.Equal(t, exterrors.CodeInfraEjectNoFoundryService, localErr.Code)
			assert.Contains(t, localErr.Message, "nothing to eject")

			_, statErr := os.Stat(filepath.Join(dir, "infra"))
			assert.True(t, os.IsNotExist(statErr), "infra/ must not be created on refusal")
		})
	}
}

func TestEjectInfra_RefusesWhenMultipleFoundryServices(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), `name: my-project
services:
  agent-a:
    host: azure.ai.project
  agent-b:
    host: azure.ai.project
`)

	err := ejectInfra(dir, "bicep")
	require.Error(t, err)

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, exterrors.CodeInfraEjectMultipleFoundryServices, localErr.Code)
	assert.Contains(t, localErr.Message, "multiple services")
	// Deterministic ordering check: matches are sorted before formatting.
	assert.Contains(t, localErr.Message, "[agent-a agent-b]")
}

func TestEjectInfra_RefusesWhenBrownfieldEndpoint(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), `name: my-project
services:
  ai-project:
    host: azure.ai.project
    endpoint: https://acct.services.ai.azure.com/api/projects/p1
`)

	err := ejectInfra(dir, "bicep")
	require.Error(t, err)

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, exterrors.CodeInfraEjectBrownfieldUnsupported, localErr.Code)
	assert.Contains(t, localErr.Message, "endpoint:")
}

func TestEjectInfra_HappyPath_WritesExpectedFiles(t *testing.T) {
	// Intentionally NOT parallel: this test captures os.Stdout, and running
	// it concurrently with other stdout-capturing tests in the same package
	// would race over the global file descriptor.
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), validFoundryAzureYAML)

	stdout := withCapturedStdout(t, func() {
		err := ejectInfra(dir, "bicep")
		require.NoError(t, err)
	})

	// Every embedded template under templates/ (except main.arm.json and the
	// dead-in-a-greenfield-eject brownfield.bicep/brownfield.arm.json) should
	// be on disk under ./infra/, plus the synthesized main.parameters.json.
	expected := []string{
		filepath.Join("infra", "main.bicep"),
		filepath.Join("infra", "abbreviations.json"),
		filepath.Join("infra", "modules", "acr.bicep"),
		filepath.Join("infra", "modules", "connections.bicep"),
		filepath.Join("infra", "modules", "network.bicep"),
		filepath.Join("infra", "modules", "subnet.bicep"),
		filepath.Join("infra", "modules", "private-endpoint-dns.bicep"),
		filepath.Join("infra", "main.parameters.json"),
	}
	for _, rel := range expected {
		path := filepath.Join(dir, rel)
		info, err := os.Stat(path)
		require.NoError(t, err, "expected file %s", rel)
		assert.Greater(t, info.Size(), int64(0), "file %s should not be empty", rel)
	}

	// main.arm.json is deliberately excluded.
	_, err := os.Stat(filepath.Join(dir, "infra", "main.arm.json"))
	assert.True(t, os.IsNotExist(err),
		"main.arm.json should be excluded from the ejected tree (it would be stale "+
			"the moment the user edits main.bicep)")

	// brownfield.bicep/brownfield.arm.json are excluded too: unreachable in a
	// greenfield eject (see TestEjectInfra_RefusesWhenBrownfieldEndpoint).
	for _, rel := range []string{
		filepath.Join("infra", "brownfield.bicep"),
		filepath.Join("infra", "brownfield.arm.json"),
	} {
		_, err := os.Stat(filepath.Join(dir, rel))
		assert.True(t, os.IsNotExist(err),
			"%s should be excluded from the ejected tree (unused in a greenfield eject)", rel)
	}

	// Spec's success block elements.
	assert.Contains(t, stdout, "Generating infrastructure files from azure.yaml")
	assert.Contains(t, stdout, "infra/main.bicep")
	assert.Contains(t, stdout, "infra/modules/acr.bicep")
	assert.Contains(t, stdout, "infra/main.parameters.json")
	assert.Contains(t, stdout, "Future provisions will read from ./infra/")
	assert.Contains(t, stdout, "Next steps:")
	assert.Contains(t, stdout, "azd provision")

	// azure.yaml must not be mutated by eject (spec is explicit on this).
	got, err := os.ReadFile(filepath.Join(dir, "azure.yaml")) //nolint:gosec // G304: test file path from t.TempDir()
	require.NoError(t, err)
	assert.Equal(t, validFoundryAzureYAML, string(got),
		"azure.yaml must not be mutated by eject")
}

func TestEjectInfra_HappyPath_ParametersFileShape(t *testing.T) {
	// See TestEjectInfra_HappyPath_WritesExpectedFiles for why this is not parallel.
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), validFoundryAzureYAML)

	withCapturedStdout(t, func() {
		require.NoError(t, ejectInfra(dir, "bicep"))
	})

	raw, err := os.ReadFile(filepath.Join(dir, "infra", "main.parameters.json")) //nolint:gosec // G304: test file path from t.TempDir()
	require.NoError(t, err)

	var doc struct {
		Schema         string `json:"$schema"`
		ContentVersion string `json:"contentVersion"`
		Parameters     map[string]struct {
			Value any `json:"value"`
		} `json:"parameters"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc),
		"main.parameters.json must be valid JSON")

	assert.Contains(t, doc.Schema, "deploymentParameters.json",
		"$schema must point at the ARM parameters schema")
	assert.Equal(t, "1.0.0.0", doc.ContentVersion)

	// Synthesizer derives exactly these two from the test YAML: includeAcr
	// because of the docker: block, and a single deployment entry.
	require.Contains(t, doc.Parameters, "includeAcr")
	assert.Equal(t, true, doc.Parameters["includeAcr"].Value)

	require.Contains(t, doc.Parameters, "deployments")
	deps, ok := doc.Parameters["deployments"].Value.([]any)
	require.True(t, ok, "deployments should be an array, got %T",
		doc.Parameters["deployments"].Value)
	require.Len(t, deps, 1)

	// Deploy-time-only params that we intentionally omit so the file isn't
	// stale the moment the user runs `azd env new`.
	for _, k := range []string{
		"location", "foundryProjectName", "resourceTokenSalt",
		"principalId", "tags",
	} {
		assert.NotContains(t, doc.Parameters, k,
			"%s is supplied at provision time and must not be hard-coded in the ejected file", k)
	}
}

func TestEjectInfra_HappyPath_NoDockerOmitsAcrParam(t *testing.T) {
	// See TestEjectInfra_HappyPath_WritesExpectedFiles for why this is not parallel.
	dir := t.TempDir()
	// No docker: block -> includeAcr should be false in the params file
	// but the acr.bicep module is still written (the template files are a
	// static set; whether ACR is provisioned is a parameter decision).
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), `name: my-project
services:
  my-foundry:
    host: azure.ai.project
    deployments: []
    agents:
      - name: my-agent
        image: registry.io/myorg/myagent:latest
`)

	withCapturedStdout(t, func() {
		require.NoError(t, ejectInfra(dir, "bicep"))
	})

	// acr.bicep is still in the ejected tree -- the template is static.
	_, err := os.Stat(filepath.Join(dir, "infra", "modules", "acr.bicep"))
	assert.NoError(t, err, "acr.bicep module is part of the static template set")

	raw, err := os.ReadFile(filepath.Join(dir, "infra", "main.parameters.json")) //nolint:gosec // G304: test file path from t.TempDir()
	require.NoError(t, err)
	var doc struct {
		Parameters map[string]struct {
			Value any `json:"value"`
		} `json:"parameters"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))
	assert.Equal(t, false, doc.Parameters["includeAcr"].Value)
}

func TestEjectInfra_EjectsConnectionServices(t *testing.T) {
	// See TestEjectInfra_HappyPath_WritesExpectedFiles for why this is not parallel.
	// Connection metadata and credentials are ejected separately.
	// Bicep keeps credential values in a secure object parameter.
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), `name: my-project
services:
  my-foundry:
    host: azure.ai.project
    deployments: []
  search-conn:
    host: azure.ai.connection
    uses: [my-foundry]
    category: CognitiveSearch
    target: https://my-search.search.windows.net
    authType: ApiKey
    credentials:
      key: ${SEARCH_API_KEY}
  mcp-conn:
    host: azure.ai.connection
    uses: [my-foundry]
    category: RemoteTool
    target: https://mcp.example.com
    authType: CustomKeys
    credentials:
      keys:
        x-api-key: ${MCP_KEY}
`)

	withCapturedStdout(t, func() {
		require.NoError(t, ejectInfra(dir, "bicep"))
	})

	// The connections module is part of the ejected tree.
	_, err := os.Stat(filepath.Join(dir, "infra", "modules", "connections.bicep"))
	assert.NoError(t, err, "connections.bicep module must be ejected")

	raw, err := os.ReadFile(filepath.Join(dir, "infra", "main.parameters.json")) //nolint:gosec // G304: test file path from t.TempDir()
	require.NoError(t, err)
	var doc struct {
		Parameters map[string]struct {
			Value any `json:"value"`
		} `json:"parameters"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))

	require.Contains(t, doc.Parameters, "connections")
	conns, ok := doc.Parameters["connections"].Value.([]any)
	require.True(t, ok, "connections should be an array, got %T", doc.Parameters["connections"].Value)
	require.Len(t, conns, 2)

	conn, ok := conns[1].(map[string]any)
	require.True(t, ok, "connection entry should be an object, got %T", conns[0])
	assert.Equal(t, "search-conn", conn["name"])
	assert.Equal(t, "CognitiveSearch", conn["category"])
	assert.Equal(t, "ApiKey", conn["authType"])

	assert.NotContains(t, conn, "credentials")

	// Nested CustomKeys credentials must remain an object so Terraform's
	// optional(any) value can preserve mixed connection credential shapes.
	mcpConn, ok := conns[0].(map[string]any)
	require.True(t, ok, "connection entry should be an object, got %T", conns[0])
	assert.Equal(t, "mcp-conn", mcpConn["name"])
	assert.NotContains(t, mcpConn, "credentials")

	secureCreds, ok := doc.Parameters["connectionCredentials"].Value.(map[string]any)
	require.True(t, ok, "connectionCredentials should be an object")
	searchCreds := secureCreds["search-conn"].(map[string]any)
	assert.Equal(t, "${SEARCH_API_KEY}", searchCreds["key"])
	mcpCreds := secureCreds["mcp-conn"].(map[string]any)
	keys := mcpCreds["keys"].(map[string]any)
	assert.Equal(t, "${MCP_KEY}", keys["x-api-key"])
}

func TestEjectInfra_PreservesNetworkVarRefs(t *testing.T) {
	// See TestEjectInfra_HappyPath_WritesExpectedFiles for why this is not parallel.
	// Eject must keep ${VAR} references verbatim in main.parameters.json so the
	// ejected tree stays environment-portable; the on-disk provision flow
	// resolves them from the azd environment at provision time.
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), `name: my-project
services:
  my-foundry:
    host: azure.ai.project
    network:
      peSubnet: {vnet: "${AZURE_VNET_ID}", name: pe-subnet}
      dns:
        resourceGroup: rg-dns
        subscription: "${AZURE_DNS_SUBSCRIPTION_ID}"
    deployments: []
    agents:
      - name: my-agent
        image: registry.io/myorg/myagent:latest
`)

	withCapturedStdout(t, func() {
		require.NoError(t, ejectInfra(dir, "bicep"))
	})

	raw, err := os.ReadFile(filepath.Join(dir, "infra", "main.parameters.json")) //nolint:gosec // G304: test file path from t.TempDir()
	require.NoError(t, err)
	var doc struct {
		Parameters map[string]struct {
			Value any `json:"value"`
		} `json:"parameters"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))

	assert.Equal(t, "${AZURE_VNET_ID}", doc.Parameters["vnetId"].Value,
		"vnet id ${VAR} must be preserved for provision-time resolution")
	assert.Equal(t, "${AZURE_DNS_SUBSCRIPTION_ID}", doc.Parameters["dnsZonesSubscription"].Value,
		"dns subscription ${VAR} must be preserved for provision-time resolution")
	assert.Equal(t, true, doc.Parameters["enableNetworkIsolation"].Value)

	// Managed egress (no agentSubnet): the full param set must thread through.
	assert.Equal(t, true, doc.Parameters["useManagedEgress"].Value,
		"omitting agentSubnet selects managed egress")
	assert.Equal(t, false, doc.Parameters["createAgentSubnet"].Value,
		"managed egress creates no agent subnet")
	assert.Equal(t, "pe-subnet", doc.Parameters["peSubnetName"].Value)
	assert.Equal(t, false, doc.Parameters["createPESubnet"].Value,
		"peSubnet without prefix references an existing subnet")
	assert.Equal(t, "rg-dns", doc.Parameters["dnsZonesResourceGroup"].Value,
		"dns.resourceGroup selects reference mode")
}

// TestEjectInfra_Bicep_NetworkParamsComplete_Byo ejects a BYO-egress service
// (agentSubnet + peSubnet, both with prefixes) and asserts the complete network
// parameter set lands in main.parameters.json. This is the Bicep eject path's
// end-to-end contract: every value the synthesizer derives from network: must
// reach the ejected parameters file so a later `azd provision` reproduces the
// declared topology.
func TestEjectInfra_Bicep_NetworkParamsComplete_Byo(t *testing.T) {
	// Not parallel: shares the stdout-capture rationale of the other eject tests.
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), `name: my-project
infra:
  provider: microsoft.foundry
services:
  my-foundry:
    host: azure.ai.project
    network:
      agentSubnet:
        vnet: "${AZURE_VNET_ID}"
        name: agent-subnet
        prefix: 192.168.10.0/24
      peSubnet:
        vnet: "${AZURE_VNET_ID}"
        name: pe-subnet
        prefix: 192.168.11.0/24
    deployments: []
    agents:
      - name: my-agent
        image: registry.io/myorg/myagent:latest
`)

	withCapturedStdout(t, func() {
		require.NoError(t, ejectInfra(dir, "bicep"))
	})

	raw, err := os.ReadFile(filepath.Join(dir, "infra", "main.parameters.json")) //nolint:gosec // G304: test file path from t.TempDir()
	require.NoError(t, err)
	var doc struct {
		Parameters map[string]struct {
			Value any `json:"value"`
		} `json:"parameters"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))

	// Ingress + egress are both private; agentSubnet present => BYO egress.
	assert.Equal(t, true, doc.Parameters["enableNetworkIsolation"].Value)
	assert.Equal(t, false, doc.Parameters["useManagedEgress"].Value,
		"agentSubnet present selects BYO egress")
	assert.Equal(t, "${AZURE_VNET_ID}", doc.Parameters["vnetId"].Value,
		"vnet id ${VAR} must be preserved for provision-time resolution")

	// Agent (egress) subnet: prefix set => create.
	assert.Equal(t, "agent-subnet", doc.Parameters["agentSubnetName"].Value)
	assert.Equal(t, "192.168.10.0/24", doc.Parameters["agentSubnetPrefix"].Value)
	assert.Equal(t, true, doc.Parameters["createAgentSubnet"].Value,
		"agentSubnet with a prefix is created")

	// PE (ingress) subnet: prefix set => create.
	assert.Equal(t, "pe-subnet", doc.Parameters["peSubnetName"].Value)
	assert.Equal(t, "192.168.11.0/24", doc.Parameters["peSubnetPrefix"].Value)
	assert.Equal(t, true, doc.Parameters["createPESubnet"].Value,
		"peSubnet with a prefix is created")

	// BYO egress has no managed-network knobs.
	assert.Equal(t, "", doc.Parameters["managedIsolationMode"].Value,
		"isolationMode is managed-egress only")
}

func TestEjectInfra_RefusesWhenInfraIsAFile(t *testing.T) {
	t.Parallel()
	// Pre-existing `infra` as a regular file (not a directory) hits the
	// same "already exists" refusal as a pre-existing directory. os.Stat
	// can't tell the caller's intent apart, and overwriting a user file
	// silently would violate "no implicit destruction of user-owned files".
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), validFoundryAzureYAML)
	mustWriteFile(t, filepath.Join(dir, "infra"), "this is a file, not a dir")

	err := ejectInfra(dir, "bicep")
	require.Error(t, err)

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, exterrors.CodeInfraEjectExists, localErr.Code,
		"a pre-existing file at ./infra is reported as an exists conflict, "+
			"not silently overwritten")

	// User's file must survive the refusal.
	got, err := os.ReadFile(filepath.Join(dir, "infra")) //nolint:gosec // G304: test file path from t.TempDir()
	require.NoError(t, err)
	assert.Equal(t, "this is a file, not a dir", string(got))
}

func TestValidateStandaloneEjectArgs(t *testing.T) {
	// The standalone-eject branch in init.go runs after positional-arg
	// resolution, so by the time validateStandaloneEjectArgs is called
	// flags.manifestPointer / flags.src may have been set by
	// applyPositionalArg even if the user never passed a `-m` or `--src`.
	// Either way: any of args, manifestPointer, or src being set means
	// init-driving input that standalone eject cannot honor.
	tests := []struct {
		name      string
		args      []string
		manifest  string
		src       string
		image     string
		wantError bool
	}{
		{name: "no extras: ok", args: nil, manifest: "", src: "", wantError: false},
		{name: "positional arg: refuse", args: []string{"./foo"}, wantError: true},
		{name: "manifest flag: refuse", manifest: "./agent.yaml", wantError: true},
		{name: "src flag: refuse", src: "./src/agent", wantError: true},
		{name: "image flag: refuse", image: "myacr.azurecr.io/agent:1", wantError: true},
		{
			name:      "all three set: refuse",
			args:      []string{"./pos"},
			manifest:  "./agent.yaml",
			src:       "./src",
			wantError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			flags := &initFlags{
				manifestPointer: tt.manifest,
				src:             tt.src,
				image:           tt.image,
			}
			err := validateStandaloneEjectArgs(tt.args, flags)
			if !tt.wantError {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok, "expected *azdext.LocalError, got %T", err)
			assert.Equal(t, exterrors.CodeInfraEjectConflictingArguments, localErr.Code)
			assert.Equal(t, azdext.LocalErrorCategoryValidation, localErr.Category,
				"the conflict is bad-user-input, classified Validation")
			// Suggestion must point at both ways out: drop the arg, or drop --infra.
			assert.Contains(t, localErr.Suggestion, "drop the extra argument")
			assert.Contains(t, localErr.Suggestion, "remove --infra")
		})
	}
}

func TestParseInfraProvider(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "bicep", in: "bicep", want: "bicep"},
		{name: "terraform", in: "terraform", want: "terraform"},
		{name: "uppercase terraform", in: "TERRAFORM", want: "terraform"},
		{name: "mixed case bicep", in: "Bicep", want: "bicep"},
		{name: "whitespace trimmed", in: "  terraform  ", want: "terraform"},
		{name: "unknown value", in: "pulumi", wantErr: true},
		{name: "arm not supported", in: "arm", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseInfraProvider(tt.in)
			if tt.wantErr {
				require.Error(t, err)
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok, "expected *azdext.LocalError, got %T", err)
				assert.Equal(t, exterrors.CodeInvalidParameter, localErr.Code)
				assert.Contains(t, localErr.Suggestion, "--infra=bicep")
				assert.Contains(t, localErr.Suggestion, "--infra=terraform")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestEjectInfraAfterInit_ResolvesParentProject(t *testing.T) {
	t.Setenv("AZD_EXEC_PROJECT_DIR", "")
	projectRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projectRoot, "azure.yaml"), []byte(`name: test
services:
  ai-project:
    host: azure.ai.project
`), 0600))
	nestedDir := filepath.Join(projectRoot, "src", "agent")
	require.NoError(t, os.MkdirAll(nestedDir, 0750))
	t.Chdir(nestedDir)

	require.NoError(t, ejectInfraAfterInit("bicep"))

	assert.FileExists(t, filepath.Join(projectRoot, "infra", "main.bicep"))
	assert.NoDirExists(t, filepath.Join(nestedDir, "infra"))
}

func TestEjectInfraAfterInit_NoProject(t *testing.T) {
	t.Setenv("AZD_EXEC_PROJECT_DIR", "")
	t.Chdir(t.TempDir())

	assert.NoError(t, ejectInfraAfterInit("bicep"))
}

func TestEjectInfra_Terraform_HappyPath_WritesExpectedFiles(t *testing.T) {
	// Not parallel: captures os.Stdout (see TestEjectInfra_HappyPath_WritesExpectedFiles).
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), validFoundryAzureYAML)

	stdout := withCapturedStdout(t, func() {
		require.NoError(t, ejectInfra(dir, "terraform"))
	})

	// Every embedded .tf under templates/terraform/ should be on disk under
	// ./infra/, plus the generated main.tfvars.json. Bicep artifacts must NOT
	// be present.
	expected := []string{
		filepath.Join("infra", "provider.tf"),
		filepath.Join("infra", "variables.tf"),
		filepath.Join("infra", "main.tf"),
		filepath.Join("infra", "acr.tf"),
		filepath.Join("infra", "connections.tf"),
		filepath.Join("infra", "outputs.tf"),
		filepath.Join("infra", "main.tfvars.json"),
	}
	for _, rel := range expected {
		info, err := os.Stat(filepath.Join(dir, rel))
		require.NoError(t, err, "expected file %s", rel)
		assert.Greater(t, info.Size(), int64(0), "file %s should not be empty", rel)
	}

	// Bicep outputs must not leak onto the Terraform path.
	for _, rel := range []string{
		filepath.Join("infra", "main.bicep"),
		filepath.Join("infra", "main.parameters.json"),
		filepath.Join("infra", "modules", "acr.bicep"),
	} {
		_, err := os.Stat(filepath.Join(dir, rel))
		assert.True(t, os.IsNotExist(err), "%s must not be written on the terraform path", rel)
	}

	// Summary mentions the created files and the azure.yaml provider stamp.
	assert.Contains(t, stdout, "Generating infrastructure files from azure.yaml")
	assert.Contains(t, stdout, "infra/main.tf")
	assert.Contains(t, stdout, "infra/main.tfvars.json")
	assert.Contains(t, stdout, "infra.provider: terraform")
	assert.Contains(t, stdout, "azd provision")

	// This fixture has a docker: agent, so acr.tf is present and the generated
	// outputs.tf must reference the registry resources (not empty strings).
	outputs, err := os.ReadFile(filepath.Join(dir, "infra", "outputs.tf")) //nolint:gosec // G304: test path from t.TempDir()
	require.NoError(t, err)
	assert.Contains(t, string(outputs), "azurerm_container_registry.this.login_server",
		"docker fixture => ACR outputs reference the registry")
}

func TestEjectInfra_Terraform_StampsProviderInAzureYaml(t *testing.T) {
	// Not parallel: captures os.Stdout.
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), validFoundryAzureYAML)

	withCapturedStdout(t, func() {
		require.NoError(t, ejectInfra(dir, "terraform"))
	})

	raw, err := os.ReadFile(filepath.Join(dir, "azure.yaml")) //nolint:gosec // G304: test file path from t.TempDir()
	require.NoError(t, err)

	var doc struct {
		Infra struct {
			Provider string `yaml:"provider"`
			Path     string `yaml:"path"`
		} `yaml:"infra"`
		Services map[string]any `yaml:"services"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &doc))

	// The Terraform path is the one place eject mutates azure.yaml: the
	// provider must flip from microsoft.foundry to terraform so azd-core's
	// built-in provider handles provisioning.
	assert.Equal(t, "terraform", doc.Infra.Provider)
	assert.Empty(t, doc.Infra.Path, "starter infra.path must be dropped")
	// The rest of azure.yaml (services) must survive the edit.
	require.Contains(t, doc.Services, "my-foundry")
}

func TestEjectInfra_Terraform_TfvarsShape(t *testing.T) {
	// Not parallel: captures os.Stdout.
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), validFoundryAzureYAML)

	withCapturedStdout(t, func() {
		require.NoError(t, ejectInfra(dir, "terraform"))
	})

	raw, err := os.ReadFile(filepath.Join(dir, "infra", "main.tfvars.json")) //nolint:gosec // G304: test file path from t.TempDir()
	require.NoError(t, err)

	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc), "main.tfvars.json must be valid JSON")

	// Static keys carry ${...} placeholders azd resolves at provision time.
	assert.Equal(t, "${AZURE_LOCATION}", doc["location"])
	assert.Equal(t, "${AZURE_RESOURCE_GROUP}", doc["resource_group_name"])
	assert.Equal(t, "${AZURE_AI_PROJECT_NAME}", doc["foundry_project_name"])
	assert.Equal(t, "${AZURE_SUBSCRIPTION_ID}", doc["subscription_id"])
	assert.Equal(t, "${AZURE_PRINCIPAL_ID}", doc["principal_id"])

	// include_acr is NOT written to tfvars; the ACR decision is the presence of
	// acr.tf at eject time, not a Terraform variable.
	assert.NotContains(t, doc, "include_acr",
		"include_acr must not be emitted to main.tfvars.json")

	// deployments is the synthesizer-derived value carried into tfvars.
	deps, ok := doc["deployments"].([]any)
	require.True(t, ok, "deployments should be an array, got %T", doc["deployments"])
	require.Len(t, deps, 1)

	// connections is always present too (empty here: the fixture declares
	// none), so a project with no host: azure.ai.connection services still
	// gets a well-typed empty list rather than a missing key.
	conns, ok := doc["connections"].([]any)
	require.True(t, ok, "connections should be an array, got %T", doc["connections"])
	assert.Empty(t, conns)
	assert.NotContains(t, doc, "connectionCredentials")
}

func TestEjectInfra_Terraform_EjectsConnectionServices(t *testing.T) {
	// Not parallel: captures os.Stdout.
	// A host: azure.ai.connection service must be synthesized into the
	// connections tfvars value, connections.tf must be part of the ejected
	// tree, and any ${VAR} in credentials kept verbatim (environment-portable
	// -- azd's Terraform provider substitutes ${...} at provision time).
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), `name: my-project
services:
  my-foundry:
    host: azure.ai.project
    deployments: []
  search-conn:
    host: azure.ai.connection
    uses: [my-foundry]
    category: CognitiveSearch
    target: https://my-search.search.windows.net
    authType: ApiKey
    credentials:
      key: ${SEARCH_API_KEY}
`)

	withCapturedStdout(t, func() {
		require.NoError(t, ejectInfra(dir, "terraform"))
	})

	// connections.tf is part of the ejected tree.
	_, err := os.Stat(filepath.Join(dir, "infra", "connections.tf"))
	assert.NoError(t, err, "connections.tf must be ejected")

	raw, err := os.ReadFile(filepath.Join(dir, "infra", "main.tfvars.json")) //nolint:gosec // G304: test file path from t.TempDir()
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))

	conns, ok := doc["connections"].([]any)
	require.True(t, ok, "connections should be an array, got %T", doc["connections"])
	require.Len(t, conns, 1)

	conn, ok := conns[0].(map[string]any)
	require.True(t, ok, "connection entry should be an object, got %T", conns[0])
	assert.Equal(t, "search-conn", conn["name"])
	assert.Equal(t, "CognitiveSearch", conn["category"])
	assert.Equal(t, "ApiKey", conn["authType"])

	// ${VAR} in credentials must be preserved verbatim on the eject path.
	creds, ok := conn["credentials"].(map[string]any)
	require.True(t, ok, "credentials should be an object, got %T", conn["credentials"])
	assert.Equal(t, "${SEARCH_API_KEY}", creds["key"])
	assert.NotContains(t, doc, "connectionCredentials")

	// outputs.tf always carries the connection-names output, unconditional on
	// includeAcr (unlike the ACR outputs).
	outputs, err := os.ReadFile(filepath.Join(dir, "infra", "outputs.tf")) //nolint:gosec // G304: test path from t.TempDir()
	require.NoError(t, err)
	assert.Contains(t, string(outputs), "AZURE_AI_PROJECT_CONNECTION_NAMES")
}

func TestEjectInfra_Terraform_NoDockerOmitsAcr(t *testing.T) {
	// Not parallel: captures os.Stdout.
	dir := t.TempDir()
	// image-only agent (no docker:) -> no ACR.
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), `name: my-project
services:
  my-foundry:
    host: azure.ai.project
    deployments: []
    agents:
      - name: my-agent
        image: registry.io/myorg/myagent:latest
`)

	withCapturedStdout(t, func() {
		require.NoError(t, ejectInfra(dir, "terraform"))
	})

	// acr.tf must NOT be written when no agent uses docker:.
	_, err := os.Stat(filepath.Join(dir, "infra", "acr.tf"))
	assert.True(t, os.IsNotExist(err), "acr.tf must be omitted when no agent uses docker:")

	// outputs.tf must not contain any ACR output at all when ACR is not used --
	// no resource references and no empty-string placeholders.
	outputs, err := os.ReadFile(filepath.Join(dir, "infra", "outputs.tf")) //nolint:gosec // G304: test path from t.TempDir()
	require.NoError(t, err)
	assert.NotContains(t, string(outputs), "azurerm_container_registry",
		"no ACR resource references when acr.tf is omitted")
	assert.NotContains(t, string(outputs), "azapi_resource.acr_connection",
		"no ACR connection reference when acr.tf is omitted")
	assert.NotContains(t, string(outputs), "AZURE_CONTAINER_REGISTRY_ENDPOINT",
		"ACR outputs must be omitted entirely, not emitted as empty strings")
	assert.NotContains(t, string(outputs), "AZURE_CONTAINER_REGISTRY_RESOURCE_ID")
	assert.NotContains(t, string(outputs), "AZURE_AI_PROJECT_ACR_CONNECTION_NAME")
	// The non-ACR outputs are still present.
	assert.Contains(t, string(outputs), "AZURE_RESOURCE_GROUP")
	assert.Contains(t, string(outputs), "FOUNDRY_PROJECT_ENDPOINT")

	// main.tf must not carry any ACR leftovers (e.g. container_registry_name).
	main, err := os.ReadFile(filepath.Join(dir, "infra", "main.tf")) //nolint:gosec // G304: test path from t.TempDir()
	require.NoError(t, err)
	assert.NotContains(t, string(main), "container_registry",
		"main.tf must have no ACR references when ACR is not used")

	// include_acr is not emitted to tfvars either.
	raw, err := os.ReadFile(filepath.Join(dir, "infra", "main.tfvars.json")) //nolint:gosec // G304: test file path from t.TempDir()
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))
	assert.NotContains(t, doc, "include_acr")
}

func TestEjectInfra_Terraform_RefusesWhenInfraExists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), validFoundryAzureYAML)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "infra"), 0o750))

	err := ejectInfra(dir, "terraform")
	require.Error(t, err)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "expected structured azdext.LocalError, got %T", err)
	assert.Equal(t, exterrors.CodeInfraEjectExists, localErr.Code)

	// The refusal must fire before azure.yaml is touched: provider stays foundry.
	raw, err := os.ReadFile(filepath.Join(dir, "azure.yaml")) //nolint:gosec // G304: test file path from t.TempDir()
	require.NoError(t, err)
	assert.Contains(t, string(raw), "provider: microsoft.foundry",
		"azure.yaml must not be stamped when eject refuses")
}

func TestEjectInfra_Terraform_RefusesWhenNetworkDeclared(t *testing.T) {
	t.Parallel()
	// Private networking is Bicep-only: the Terraform module has no VNet / PE /
	// DNS / networkInjections resources. Ejecting it for a network: service would
	// silently drop the config and provision a public account. Eject must refuse
	// rather than emit an insecure template.
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "azure.yaml"), `name: my-project
infra:
  provider: microsoft.foundry
services:
  my-foundry:
    host: azure.ai.project
    network:
      peSubnet: {vnet: "${AZURE_VNET_ID}", name: pe-subnet}
    deployments: []
    agents:
      - name: my-agent
        image: registry.io/myorg/myagent:latest
`)

	err := ejectInfra(dir, "terraform")
	require.Error(t, err)
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "expected structured azdext.LocalError, got %T", err)
	assert.Equal(t, exterrors.CodeInfraEjectNetworkUnsupported, localErr.Code)

	// The refusal must fire before any files land or azure.yaml is stamped.
	_, statErr := os.Stat(filepath.Join(dir, "infra"))
	assert.True(t, os.IsNotExist(statErr), "infra/ must not be written when eject refuses")
	raw, err := os.ReadFile(filepath.Join(dir, "azure.yaml")) //nolint:gosec // G304: test file path from t.TempDir()
	require.NoError(t, err)
	assert.Contains(t, string(raw), "provider: microsoft.foundry",
		"azure.yaml must not be stamped when eject refuses")
}
