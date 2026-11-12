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
	"maps"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"azure.ai.content-understanding/internal/armclient"
	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/infra"
	"azure.ai.content-understanding/internal/testutil"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type infraCommandHost struct {
	snapshot       infraSnapshot
	capabilities   *infraCommandCapabilities
	calls          []string
	selected       string
	request        infra.PostRequest
	prompts        []generationPrompts
	prompt         func(infra.Choices) (infra.Choices, error)
	onSubscription func()
}

func infraCommandFixture(t *testing.T) *infraCommandHost {
	t.Helper()
	for _, name := range []string{"AZD_ENVIRONMENT", "AZD_NO_PROMPT", "AZD_CWD", "CU_DISABLE_AUTO_CONFIG"} {
		t.Setenv(name, "")
	}
	return &infraCommandHost{capabilities: &infraCommandCapabilities{}, snapshot: infraSnapshot{
		environment: "dev", section: map[string]json.RawMessage{}, values: map[string]string{
			"FOUNDRY_ENDPOINT": "https://foundry.services.ai.azure.com", "FOUNDRY_RESOURCE_NAME": "foundry",
			"CU_ENDPOINT":           "https://foundry.services.ai.azure.com",
			"AZURE_SUBSCRIPTION_ID": "sub", "AZURE_TENANT_ID": "tenant", "AZURE_RESOURCE_GROUP": "group",
			"CU_MODEL_SELECTION": "none",
		},
	}}
}
func (f *infraCommandHost) Subscription(context.Context, bool, string) (*azdext.Subscription, error) {
	f.calls = append(f.calls, "subscription")
	if f.onSubscription != nil {
		f.onSubscription()
	}
	return &azdext.Subscription{Id: "sub", TenantId: "tenant", Name: "Subscription"}, nil
}
func (f *infraCommandHost) Resolve(_ context.Context, _ *azdext.Subscription, resource string) (armclient.Account, error) {
	f.calls = append(f.calls, "resolve")
	return armclient.Account{
		ID:       "/subscriptions/sub/resourceGroups/existing/providers/Microsoft.CognitiveServices/accounts/foundry",
		Endpoint: "https://foundry.services.ai.azure.com/contentunderstanding", Region: "westus3"}, nil
}
func (f *infraCommandHost) Snapshot(_ context.Context, name string) (infraSnapshot, error) {
	f.calls = append(f.calls, "snapshot")
	f.selected = name
	return f.snapshot, nil
}
func (f *infraCommandHost) Capabilities(_ context.Context, _ infraSnapshot, request infra.PostRequest,
) (infra.PostCapabilities, infra.ModelCapabilities, error) {
	f.calls = append(f.calls, "capabilities")
	f.request = request
	return f.capabilities, f.capabilities, nil
}
func (f *infraCommandHost) Close() { f.calls = append(f.calls, "close") }
func (f *infraCommandHost) PromptGeneration(
	_ context.Context, choices infra.Choices, prompts generationPrompts,
) (infra.Choices, error) {
	f.prompts = append(f.prompts, prompts)
	if f.prompt != nil {
		return f.prompt(choices)
	}
	return choices, nil
}

type infraCommandCapabilities struct {
	calls []string
	fail  string
}

func (c *infraCommandCapabilities) record(value string) error {
	c.calls = append(c.calls, value)
	if c.fail == value {
		return errors.New("private-service-token-detail")
	}
	return nil
}
func (c *infraCommandCapabilities) SetupModels(context.Context) error  { return c.record("setup") }
func (c *infraCommandCapabilities) MarkComplete(context.Context) error { return c.record("complete") }
func (c *infraCommandCapabilities) HasConfiguration(context.Context) (bool, error) {
	return false, c.record("inspect")
}
func (c *infraCommandCapabilities) SetConfiguration(_ context.Context, key, value string) error {
	return c.record(key)
}
func (c *infraCommandCapabilities) ListDeployments(context.Context) ([]infra.Deployment, error) {
	return []infra.Deployment{}, c.record("list")
}
func (c *infraCommandCapabilities) ConfigureDefaults(context.Context, map[string]string) error {
	return c.record("defaults")
}
func (c *infraCommandCapabilities) Supported(context.Context) (cuclient.Analyzer, error) {
	return cuclient.Analyzer{JSON: []byte(`{"supportedModels":{"completion":["gpt-5.2"]}}`)}, c.record("supported")
}
func (c *infraCommandCapabilities) Catalog(context.Context) ([]infra.CatalogModel, error) {
	return []infra.CatalogModel{{Name: "gpt-5.2", Version: "v1", Format: "OpenAI",
			SKUs: []infra.ModelSKU{{Name: "Standard", Capacity: 1}}}},
		c.record("catalog")
}
func (c *infraCommandCapabilities) PromptModels(_ context.Context, models []infra.Model) ([]infra.Model, error) {
	return models, c.record("prompt")
}
func (c *infraCommandCapabilities) Deploy(context.Context, []infra.Model) error {
	return c.record("deploy")
}

func executeInfra(t *testing.T, host *infraCommandHost, args ...string) (string, string, error) {
	t.Helper()
	root := newRootCommandWithInfra(nil, func(*cobra.Command, *azdext.ExtensionContext) (infraHost, error) {
		host.calls = append(host.calls, "open")
		return host, nil
	})
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(args)
	err := root.ExecuteContext(t.Context())
	return stdout.String(), stderr.String(), err
}

func TestInfraCommandsGenerationReuseAndNoEndpointDependency(t *testing.T) {
	f := infraCommandFixture(t)
	t.Setenv("CU_ENDPOINT", "invalid-not-read")
	t.Setenv("CU_API_KEY", "private-not-read")
	target := filepath.Join(testutil.TempDir(t), "generated")
	stdout, stderr, err := executeInfra(t, f,
		"infra", "generate", "-d", target, "-e", "new-env", "--models", "none", "--no-prompt", "-o", "json")
	require.NoError(t, err)
	assert.Empty(t, stderr)
	var result infra.Generation
	require.NoError(t, json.Unmarshal([]byte(stdout), &result))
	assert.Equal(t, "new-env", result.Environment)
	assert.Equal(t, "eastus2", result.Location)
	assert.Equal(t, "sub", result.SubscriptionID)
	assert.Equal(t, []string{"open", "subscription", "close"}, f.calls)
	assert.Len(t, result.Files, 11)
	f.calls = nil
	stdout, _, err = executeInfra(t, f, "infra", "generate", "-d", target, "-e", "new-env", "--models", "none",
		"--foundry-endpoint", "foundry", "-o", "json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(stdout), &result))
	assert.True(t, result.ReusedExistingTemplate)
	assert.Equal(t, "westus3", result.Location)
	assert.Equal(t, "https://foundry.services.ai.azure.com", result.FoundryEndpoint)
	assert.Equal(t, "existing", result.FoundryResourceGroup)
	assert.False(t, result.AssignRoles)
	assert.Equal(t, []string{"open", "subscription", "resolve", "close"}, f.calls)
	for _, args := range [][]string{
		{"infra", "generate", "-e", "../escape"}, {"infra", "generate", "--location", "invalid"},
		{"infra", "generate", "--models", "none,other"},
		{"infra", "generate", "--foundry-prefix", "prefix", "--foundry-endpoint", "foundry"},
		{"infra", "generate", "--subscription", ""}, {"infra", "generate", "--api-key", "private"},
	} {
		f.calls = nil
		stdout, _, err := executeInfra(t, f, args...)
		require.Error(t, err)
		assert.Empty(t, stdout)
		assert.Empty(t, f.calls)
	}
}

func TestInfraExistingResourceRejectsExplicitRoleAssignmentBeforeHostAccess(t *testing.T) {
	for _, endpoint := range []string{
		"foundry", "https://foundry.services.ai.azure.com",
		"/subscriptions/sub/resourceGroups/group/providers/Microsoft.CognitiveServices/accounts/foundry", "",
	} {
		for _, flag := range []string{"--assign-roles", "--assign-roles=true", "--assign-roles=false"} {
			for _, existing := range []bool{false, true} {
				t.Run(endpoint+"/"+flag+fmt.Sprint("/existing=", existing), func(t *testing.T) {
					host := infraCommandFixture(t)
					target := filepath.Join(testutil.TempDir(t), "project")
					envFile := filepath.Join(target, ".azure", "dev", ".env")
					const saved = "AZD_ASSIGN_ROLES=\"false\"\nCUSTOM=\"retained\"\n"
					if existing {
						require.NoError(t, os.MkdirAll(filepath.Dir(envFile), 0o700))
						require.NoError(t, os.WriteFile(envFile, []byte(saved), 0o600))
					}
					stdout, stderr, err := executeInfra(t, host, "infra", "generate", "--output-dir", target,
						"--models", "none", "--no-prompt", "--foundry-endpoint", endpoint, flag)
					require.Error(t, err)
					local, ok := errors.AsType[*azdext.LocalError](err)
					require.True(t, ok)
					assert.Equal(t, azdext.LocalErrorCategoryValidation, local.Category)
					assert.Equal(t, "conflicting_role_assignment", local.Code)
					assert.Contains(t, local.Message, "--assign-roles")
					assert.Contains(t, local.Message, "--foundry-endpoint")
					assert.Contains(t, local.Suggestion, "administrator")
					assert.Empty(t, stdout)
					assert.Empty(t, stderr)
					assert.Empty(t, host.calls, "reject the flag conflict before authentication or resource discovery")
					if existing {
						body, err := os.ReadFile(envFile) // #nosec G304 -- Private test environment.
						require.NoError(t, err)
						assert.Equal(t, saved, string(body))
					} else {
						assert.NoDirExists(t, target)
					}
					assert.NoFileExists(t, filepath.Join(target, "azure.yaml"))
				})
			}
		}
	}
}

func TestInfraNewResourceRoleAssignmentOptions(t *testing.T) {
	for _, test := range []struct {
		name string
		flag string
		want bool
	}{
		{"enabled", "--assign-roles", true},
		{"explicitly disabled", "--assign-roles=false", false},
		{"disabled", "--no-assign-roles", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			host := infraCommandFixture(t)
			target := filepath.Join(testutil.TempDir(t), "project")
			stdout, stderr, err := executeInfra(t, host, "infra", "generate", "--output-dir", target,
				"--models", "none", "--no-prompt", "-o", "json", test.flag)
			require.NoError(t, err)
			assert.Empty(t, stderr)
			var result infra.Generation
			require.NoError(t, json.Unmarshal([]byte(stdout), &result))
			assert.Equal(t, test.want, result.AssignRoles)
			assert.Equal(t, []string{"open", "subscription", "close"}, host.calls)
			// #nosec G304 -- Reads only the generated test environment.
			body, err := os.ReadFile(filepath.Join(target, "infra", "main.parameters.json"))
			require.NoError(t, err)
			var parameters struct {
				Parameters map[string]json.RawMessage `json:"parameters"`
			}
			require.NoError(t, json.Unmarshal(body, &parameters))
			assert.JSONEq(t, `{"value":"${CU_ASSIGN_ROLES=false}"}`,
				string(parameters.Parameters["assignRolesToPrincipal"]))
			body, err = os.ReadFile(filepath.Join(target, ".azure/dev/.env")) // #nosec G304 -- Test environment.
			require.NoError(t, err)
			assert.Contains(t, string(body), fmt.Sprintf("CU_ASSIGN_ROLES=\"%t\"\n", test.want))
		})
	}
}

func TestInfraModelHookOfflineAndScopedDeployment(t *testing.T) {
	f := infraCommandFixture(t)
	output := filepath.Join(testutil.TempDir(t), "models.json")
	args := []string{"infra", "_models", "--resource-group", "group", "--account", "foundry",
		"--selection", "none", "--out", output, "-o", "none"}
	stdout, stderr, err := executeInfra(t, f, args...)
	require.NoError(t, err)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
	assert.Empty(t, f.calls)
	args[7] = "recommended"
	_, _, err = executeInfra(t, f, args...)
	require.NoError(t, err)
	assert.Equal(t, []string{"open", "snapshot", "capabilities", "close"}, f.calls)
	assert.Equal(t, []string{"supported", "catalog", "deploy", "defaults"}, f.capabilities.calls)
	f.calls = nil
	_, _, err = executeInfra(t, f, "infra", "_models", "--resource-group", "group", "--account", "different",
		"--selection", "recommended", "--out", output, "-e", "selected")
	require.Error(t, err)
	assert.Equal(t, "selected", f.selected)
	assert.NotContains(t, f.calls, "capabilities")
	f.calls = nil
	_, _, err = executeInfra(t, f, "infra", "_models", "--resource-group", "group", "--account", "foundry",
		"--selection", "prompt", "--out", output, "--no-prompt")
	require.Error(t, err)
	assert.Empty(t, f.calls)
}

func TestInfraPostHookResultsWarningsAndRequiredFailure(t *testing.T) {
	for _, fail := range []string{"", "setup", "endpoint", "inspect"} {
		f := infraCommandFixture(t)
		f.capabilities.fail = fail
		stdout, stderr, err := executeInfra(t, f, "infra", "_postprovision-v1", "-e", "selected", "-o", "json", "--debug")
		assert.Equal(t, "selected", f.selected)
		assert.Equal(t, "https://foundry.services.ai.azure.com", f.request.Endpoint)
		assert.NotContains(t, stdout+stderr, "private-service-token")
		switch fail {
		case "endpoint", "inspect":
			require.Error(t, err)
			assert.Empty(t, stdout)
		case "setup":
			require.NoError(t, err)
			assert.Contains(t, stderr, "Optional model setup failed")
			assert.Contains(t, stdout, `"modelSetup": "failed"`)
		default:
			require.NoError(t, err)
			assert.Empty(t, stderr)
			assert.Contains(t, stdout, `"succeeded": true`)
		}
	}
}

func TestInfraMetadataAndPostEnvironmentPrecedence(t *testing.T) {
	stdout, _, err := executeInfra(t, infraCommandFixture(t), "metadata")
	require.NoError(t, err)
	var metadata extensions.ExtensionCommandMetadata
	require.NoError(t, json.Unmarshal([]byte(stdout), &metadata))
	for _, path := range [][]string{{"infra", "generate"}, {"infra", "_models"}, {"infra", "_postprovision-v1"}} {
		command := extensions.ResolveCommand(&metadata, path)
		require.NotNil(t, command)
		require.NotNil(t, command.Launch)
		assert.True(t, command.Launch.NoAzdEnvironment)
	}
	f := infraCommandFixture(t)
	f.snapshot.values["CU_ENDPOINT"] = "https://other.services.ai.azure.com"
	f.snapshot.values["CU_MODEL_SETUP_COMPLETE"] = "true"
	f.snapshot.values["CU_CONFIG_SETUP_FORCE"] = "true"
	f.snapshot.values["CU_API_VERSION"] = "2026-06-01-preview"
	request, err := postRequest(f.snapshot)
	require.NoError(t, err)
	assert.Equal(t, "https://other.services.ai.azure.com", request.Endpoint)
	assert.Equal(t, cuclient.PreviewAPIVersion, request.APIVersion)
	assert.False(t, request.SetupComplete)
	assert.True(t, request.ForceConfiguration)
	f.snapshot.section["modelSetupComplete"] = json.RawMessage(`true`)
	request, err = postRequest(f.snapshot)
	require.NoError(t, err)
	assert.True(t, request.SetupComplete)
	f.snapshot.section["modelSetupComplete"] = json.RawMessage(`"true"`)
	_, err = postRequest(f.snapshot)
	require.Error(t, err)
}

func TestInfraPostConfigPrecedesLegacyValues(t *testing.T) {
	f := infraCommandFixture(t)
	f.snapshot.values["CU_MODEL_SELECTION"] = "recommended"
	f.snapshot.values["CU_CONFIG_SETUP_FORCE"] = "true"
	f.snapshot.values["CU_RESOURCE_NAME"] = "new-account"
	f.snapshot.section["modelSelection"] = json.RawMessage(`"none"`)
	f.snapshot.section["forceConfigurationSetup"] = json.RawMessage(`false`)
	f.snapshot.section["modelSetupComplete"] = json.RawMessage(`true`)
	request, err := postRequest(f.snapshot)
	require.NoError(t, err)
	assert.Equal(t, "none", request.Selection)
	assert.Equal(t, "new-account", request.Account)
	assert.False(t, request.ForceConfiguration)
	assert.True(t, request.SetupComplete)
	for _, field := range []string{"modelSelection", "forceConfigurationSetup"} {
		for _, raw := range []string{`null`, `{}`, `[]`, `""`, `1`} {
			original := f.snapshot.section[field]
			f.snapshot.section[field] = json.RawMessage(raw)
			_, err := postRequest(f.snapshot)
			require.Error(t, err, "%s=%s", field, raw)
			f.snapshot.section[field] = original
		}
	}
	f.snapshot.section["modelSelection"] = json.RawMessage(`"none,recommended"`)
	_, err = postRequest(f.snapshot)
	require.Error(t, err)
	f.snapshot.section["modelSelection"] = json.RawMessage(`"none"`)
	f.snapshot.values["CU_RESOURCE_NAME"] = ""
	_, err = postRequest(f.snapshot)
	require.Error(t, err, "an empty new output must not fall back to a stale legacy resource")
}

func TestInfraPostProvisionedOutputs(t *testing.T) {
	outputs := map[string]string{
		"_CU_PROVISIONED_ENDPOINT":        "https://provisioned.services.ai.azure.com",
		"_CU_PROVISIONED_RESOURCE_NAME":   "provisioned",
		"_CU_PROVISIONED_RESOURCE_GROUP":  "provisioned-group",
		"_CU_PROVISIONED_SUBSCRIPTION_ID": "provisioned-sub",
	}
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy=%t", legacy), func(t *testing.T) {
			f := infraCommandFixture(t)
			if !legacy {
				f.snapshot.values = map[string]string{}
			}
			maps.Copy(f.snapshot.values, outputs)
			request, err := postRequest(f.snapshot)
			require.NoError(t, err)
			assert.Equal(t, "https://provisioned.services.ai.azure.com", request.Endpoint)
			assert.Equal(t, "provisioned", request.Account)
			assert.Equal(t, "provisioned-group", request.ResourceGroup)
			assert.Equal(t, "provisioned-sub", request.Subscription)
			for key := range outputs {
				original := f.snapshot.values[key]
				f.snapshot.values[key] = ""
				_, err := postRequest(f.snapshot)
				require.Error(t, err, "%s must not fall back to stale legacy values", key)
				f.snapshot.values[key] = original
			}
			f.snapshot.values["_CU_PROVISIONED_ENDPOINT"] = "http://invalid.services.ai.azure.com"
			_, err = postRequest(f.snapshot)
			require.Error(t, err, "an invalid new endpoint must not fall back to the runtime endpoint")
		})
	}
}

type infraRPC struct {
	azdext.UnimplementedEnvironmentServiceServer
	azdext.UnimplementedAccountServiceServer
	mu                  sync.Mutex
	calls               []string
	values              []*azdext.KeyValue
	section             *azdext.GetConfigSectionResponse
	subscriptions       []*azdext.Subscription
	writes              map[string]json.RawMessage
	defaultSubscription *azdext.GetUserConfigResponse
	configErr           error
	subscriptionErr     error
	tenantResponse      *azdext.LookupTenantResponse
	tenantErr           error
	defaultEnvironment  string
	promptText          func(*azdext.PromptRequest) (*azdext.PromptResponse, error)
	selectOption        func(*azdext.SelectRequest) (*azdext.SelectResponse, error)
	confirm             func(*azdext.ConfirmRequest) (*azdext.ConfirmResponse, error)
	multiSelect         func(*azdext.MultiSelectRequest) (*azdext.MultiSelectResponse, error)
}

func (h *infraRPC) record(value string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, value)
}
func (h *infraRPC) Get(_ context.Context, request *azdext.GetEnvironmentRequest) (*azdext.EnvironmentResponse, error) {
	h.record("get:" + request.Name)
	return &azdext.EnvironmentResponse{Environment: &azdext.Environment{Name: request.Name}}, nil
}
func (h *infraRPC) GetCurrent(context.Context, *azdext.EmptyRequest) (*azdext.EnvironmentResponse, error) {
	h.record("current")
	return &azdext.EnvironmentResponse{Environment: &azdext.Environment{
		Name: firstInfra(h.defaultEnvironment, "default"),
	}}, nil
}
func (h *infraRPC) GetValues(
	_ context.Context, request *azdext.GetEnvironmentRequest,
) (*azdext.KeyValueListResponse, error) {
	h.record("values:" + request.Name)
	h.mu.Lock()
	defer h.mu.Unlock()
	return &azdext.KeyValueListResponse{KeyValues: h.values}, nil
}
func (h *infraRPC) GetConfigSection(_ context.Context, request *azdext.GetConfigSectionRequest,
) (*azdext.GetConfigSectionResponse, error) {
	h.record("config:" + request.EnvName + ":" + request.Path)
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.section, nil
}
func (h *infraRPC) SetConfig(_ context.Context, request *azdext.SetConfigRequest) (*azdext.EmptyResponse, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, "set:"+request.EnvName+":"+request.Path)
	h.writes[request.Path] = append(json.RawMessage{}, request.Value...)
	return &azdext.EmptyResponse{}, nil
}
func (h *infraRPC) SetValue(_ context.Context, request *azdext.SetEnvRequest) (*azdext.EmptyResponse, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, "env:"+request.EnvName+":"+request.Key+":"+request.Value)
	for _, pair := range h.values {
		if pair.Key == request.Key {
			pair.Value = request.Value
			return &azdext.EmptyResponse{}, nil
		}
	}
	h.values = append(h.values, &azdext.KeyValue{Key: request.Key, Value: request.Value})
	return &azdext.EmptyResponse{}, nil
}
func (h *infraRPC) ListSubscriptions(
	context.Context, *azdext.ListSubscriptionsRequest,
) (*azdext.ListSubscriptionsResponse, error) {
	h.record("subscriptions")
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subscriptionErr != nil {
		return nil, h.subscriptionErr
	}
	return &azdext.ListSubscriptionsResponse{Subscriptions: h.subscriptions}, nil
}
func (h *infraRPC) PromptSubscription(context.Context, *azdext.PromptSubscriptionRequest,
) (*azdext.PromptSubscriptionResponse, error) {
	h.record("prompt-subscription")
	h.mu.Lock()
	defer h.mu.Unlock()
	return &azdext.PromptSubscriptionResponse{Subscription: h.subscriptions[0]}, nil
}

func (h *infraRPC) LookupTenant(
	_ context.Context, request *azdext.LookupTenantRequest,
) (*azdext.LookupTenantResponse, error) {
	h.record("tenant:" + request.SubscriptionId)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.tenantErr != nil {
		return nil, h.tenantErr
	}
	if h.tenantResponse != nil {
		return h.tenantResponse, nil
	}
	return &azdext.LookupTenantResponse{TenantId: "tenant"}, nil
}

type infraPromptRPC struct {
	azdext.UnimplementedPromptServiceServer
	rpc *infraRPC
}

type infraUserConfigRPC struct {
	azdext.UnimplementedUserConfigServiceServer
	rpc *infraRPC
}

func (h *infraPromptRPC) Prompt(ctx context.Context, request *azdext.PromptRequest) (*azdext.PromptResponse, error) {
	h.rpc.record("prompt-text")
	if h.rpc.promptText == nil {
		return h.UnimplementedPromptServiceServer.Prompt(ctx, request)
	}
	return h.rpc.promptText(request)
}

func (h *infraPromptRPC) Select(ctx context.Context, request *azdext.SelectRequest) (*azdext.SelectResponse, error) {
	h.rpc.record("select")
	if h.rpc.selectOption == nil {
		return h.UnimplementedPromptServiceServer.Select(ctx, request)
	}
	return h.rpc.selectOption(request)
}

func (h *infraPromptRPC) Confirm(ctx context.Context, request *azdext.ConfirmRequest) (*azdext.ConfirmResponse, error) {
	h.rpc.record("confirm")
	if h.rpc.confirm == nil {
		return h.UnimplementedPromptServiceServer.Confirm(ctx, request)
	}
	return h.rpc.confirm(request)
}

func (h *infraPromptRPC) MultiSelect(
	ctx context.Context, request *azdext.MultiSelectRequest,
) (*azdext.MultiSelectResponse, error) {
	h.rpc.record("multi-select")
	if h.rpc.multiSelect == nil {
		return h.UnimplementedPromptServiceServer.MultiSelect(ctx, request)
	}
	return h.rpc.multiSelect(request)
}

func (h *infraUserConfigRPC) Get(
	_ context.Context, request *azdext.GetUserConfigRequest,
) (*azdext.GetUserConfigResponse, error) {
	h.rpc.mu.Lock()
	defer h.rpc.mu.Unlock()
	h.rpc.calls = append(h.rpc.calls, "user-config:"+request.Path)
	if h.rpc.configErr != nil {
		return nil, h.rpc.configErr
	}
	if h.rpc.defaultSubscription != nil {
		return h.rpc.defaultSubscription, nil
	}
	return &azdext.GetUserConfigResponse{}, nil
}

func (h *infraPromptRPC) PromptSubscription(
	ctx context.Context, request *azdext.PromptSubscriptionRequest,
) (*azdext.PromptSubscriptionResponse, error) {
	return h.rpc.PromptSubscription(ctx, request)
}
func infraRPCClient(t *testing.T, h *infraRPC) *azdInfraHost {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	azdext.RegisterEnvironmentServiceServer(server, h)
	azdext.RegisterAccountServiceServer(server, h)
	azdext.RegisterPromptServiceServer(server, &infraPromptRPC{rpc: h})
	azdext.RegisterUserConfigServiceServer(server, &infraUserConfigRPC{rpc: h})
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		err := <-done
		if err != nil {
			assert.ErrorIs(t, err, grpc.ErrServerStopped)
		}
	})
	t.Setenv("AZD_SERVER", listener.Addr().String())
	t.Setenv("AZD_ACCESS_TOKEN", "host-test-token")
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	cmd.SetOut(io.Discard)
	host, err := openInfraHost(cmd, &azdext.ExtensionContext{})
	require.NoError(t, err)
	t.Cleanup(host.Close)
	actual, ok := host.(*azdInfraHost)
	require.True(t, ok)
	return actual
}

func TestInfraHostConfiguredDefaultSubscription(t *testing.T) {
	for _, test := range []struct {
		name, raw, selected string
		subscriptions       []*azdext.Subscription
	}{
		{"configured without host default", `"second"`, "second", []*azdext.Subscription{
			{Id: "first", TenantId: "tenant"}, {Id: "second", TenantId: "tenant"},
		}},
		{"configured overrides host default", `"second"`, "second", []*azdext.Subscription{
			{Id: "first", TenantId: "tenant", IsDefault: true}, {Id: "second", TenantId: "tenant"},
		}},
		{"case-insensitive ID", `"SECOND"`, "second", []*azdext.Subscription{
			{Id: "first", TenantId: "tenant"}, {Id: "second", TenantId: "tenant"},
		}},
		{"inaccessible does not choose sole subscription", `"missing"`, "", []*azdext.Subscription{
			{Id: "first", TenantId: "tenant", IsDefault: true},
		}},
		{"invalid type", `123`, "", nil},
		{"null", `null`, "", nil},
		{"empty", `""`, "", nil},
		{"whitespace", `" second "`, "", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			rpc := &infraRPC{subscriptions: test.subscriptions,
				defaultSubscription: &azdext.GetUserConfigResponse{Found: true, Value: []byte(test.raw)}}
			host := infraRPCClient(t, rpc)
			subscription, err := host.Subscription(azdext.WithAccessToken(t.Context()), true, "")
			if test.selected == "" {
				require.Error(t, err)
				assert.Nil(t, subscription)
			} else {
				require.NoError(t, err)
				assert.Equal(t, test.selected, subscription.GetId())
			}
			rpc.mu.Lock()
			assert.Contains(t, rpc.calls, "user-config:defaults.subscription")
			assert.NotContains(t, rpc.calls, "prompt-subscription")
			rpc.mu.Unlock()
		})
	}
}

func TestInfraHostExplicitSubscription(t *testing.T) {
	const selectedID = "11111111-2222-3333-4444-555555555555"
	for _, test := range []struct {
		name, selector, selected, code string
		subscriptions                  []*azdext.Subscription
	}{
		{"ID", selectedID, selectedID, "", []*azdext.Subscription{
			{Id: "other", Name: "Other", TenantId: "tenant", IsDefault: true},
			{Id: selectedID, Name: "Target subscription", TenantId: "tenant"},
		}},
		{"name case insensitive", "target SUBSCRIPTION", selectedID, "", []*azdext.Subscription{
			{Id: selectedID, Name: "Target subscription", TenantId: "tenant"},
		}},
		{"ID case insensitive", "ABCDEFAB-2222-3333-4444-555555555555",
			"abcdefab-2222-3333-4444-555555555555", "", []*azdext.Subscription{
				{Id: "abcdefab-2222-3333-4444-555555555555", Name: "Target", TenantId: "tenant"},
			}},
		{"ID precedes name", selectedID, selectedID, "", []*azdext.Subscription{
			{Id: "other", Name: selectedID, TenantId: "tenant"},
			{Id: selectedID, Name: "Target", TenantId: "tenant"},
		}},
		{"duplicate name", "Target", "", "ambiguous_subscription", []*azdext.Subscription{
			{Id: "first", Name: "Target", TenantId: "tenant", IsDefault: true},
			{Id: "second", Name: "TARGET", TenantId: "tenant"},
		}},
		{"missing ignores sole default", "missing", "", "subscription_unavailable", []*azdext.Subscription{
			{Id: "other", Name: "Other", TenantId: "tenant", IsDefault: true},
		}},
		{"empty list", "missing", "", "subscription_unavailable", nil},
		{"missing tenant", "Target", "", "invalid_subscription", []*azdext.Subscription{
			{Id: selectedID, Name: "Target"},
		}},
		{"missing ID", "Target", "", "invalid_subscription", []*azdext.Subscription{
			{Name: "Target", TenantId: "tenant"},
		}},
	} {
		for _, noPrompt := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/noPrompt=%t", test.name, noPrompt), func(t *testing.T) {
				rpc := &infraRPC{subscriptions: test.subscriptions,
					configErr: errors.New("explicit selection must not read defaults")}
				host := infraRPCClient(t, rpc)
				subscription, err := host.Subscription(azdext.WithAccessToken(t.Context()), noPrompt, test.selector)
				if test.code == "" {
					require.NoError(t, err)
					assert.Equal(t, test.selected, subscription.GetId())
				} else {
					var failure *cuclient.Error
					require.ErrorAs(t, err, &failure)
					assert.Equal(t, test.code, failure.Code)
					assert.Nil(t, subscription)
				}
				rpc.mu.Lock()
				assert.Equal(t, []string{"subscriptions"}, rpc.calls)
				assert.Empty(t, rpc.writes)
				rpc.mu.Unlock()
			})
		}
	}
}

func TestInfraExplicitSubscriptionGeneration(t *testing.T) {
	for _, noPrompt := range []bool{false, true} {
		t.Run(fmt.Sprint("noPrompt=", noPrompt), func(t *testing.T) {
			_ = infraCommandFixture(t)
			rpc := &infraRPC{
				configErr: errors.New("explicit selection must not read defaults"),
				subscriptions: []*azdext.Subscription{
					{Id: "default-sub", Name: "Default", TenantId: "default-tenant", IsDefault: true},
					{Id: "selected-sub", Name: "Target subscription", TenantId: "selected-tenant",
						UserTenantId: "guest-access-tenant"},
				},
			}
			_ = infraRPCClient(t, rpc)
			directory := filepath.Join(testutil.TempDir(t), "provision")
			args := []string{"infra", "generate", "--subscription", "Target subscription", "--environment", "selected",
				"--location", "eastus2", "--foundry-prefix", "test", "--no-assign-roles",
				"--models", "none", "--output-dir", directory, "--output", "json"}
			if noPrompt {
				args = append(args, "--no-prompt")
			}
			stdout, stderr, err := executeRoot(t, args...)
			require.NoError(t, err)
			assert.Empty(t, stderr)
			var result infra.Generation
			require.NoError(t, json.Unmarshal([]byte(stdout), &result))
			assert.Equal(t, "selected-sub", result.SubscriptionID)
			assert.Equal(t, "Target subscription", result.SubscriptionName)
			assert.Equal(t, "selected-tenant", result.TenantID)
			// #nosec G304 -- Reads generated fixture state in the test's private directory.
			values, err := os.ReadFile(filepath.Join(directory, ".azure", "selected", ".env"))
			require.NoError(t, err)
			assert.Contains(t, string(values), `AZURE_SUBSCRIPTION_ID="selected-sub"`)
			assert.Contains(t, string(values), `AZURE_TENANT_ID="selected-tenant"`)
			rpc.mu.Lock()
			assert.Equal(t, []string{"subscriptions"}, rpc.calls)
			assert.Empty(t, rpc.writes)
			rpc.mu.Unlock()
		})
	}
}

func TestInfraInvalidSubscriptionBeforeHostAccess(t *testing.T) {
	for _, selector := range []string{"", " ", " Target", "Target ", "Target\nsubscription", "Target\x00"} {
		t.Run(fmt.Sprintf("%q", selector), func(t *testing.T) {
			host := infraCommandFixture(t)
			directory := testutil.TempDir(t)
			sentinel := filepath.Join(directory, "azure.yaml")
			require.NoError(t, os.WriteFile(sentinel, []byte("unchanged"), 0o600))
			stdout, stderr, err := executeInfra(t, host, "infra", "generate",
				"--subscription", selector, "--output-dir", directory, "--no-prompt")
			require.Error(t, err)
			assert.Empty(t, stdout)
			assert.Empty(t, stderr)
			assert.Empty(t, host.calls)
			// #nosec G304 -- Reads a test-owned sentinel.
			saved, err := os.ReadFile(sentinel)
			require.NoError(t, err)
			assert.Equal(t, "unchanged", string(saved))
			entries, err := os.ReadDir(directory)
			require.NoError(t, err)
			assert.Len(t, entries, 1)
		})
	}
}

func TestInfraExplicitSubscriptionFailuresDoNotGenerateFiles(t *testing.T) {
	for _, reason := range []string{"missing", "ambiguous", "host failure"} {
		t.Run(reason, func(t *testing.T) {
			_ = infraCommandFixture(t)
			rpc := &infraRPC{subscriptions: []*azdext.Subscription{
				{Id: "default-sub", Name: "Default", TenantId: "tenant", IsDefault: true},
			}}
			if reason == "ambiguous" {
				rpc.subscriptions = []*azdext.Subscription{
					{Id: "first", Name: "Target", TenantId: "tenant"},
					{Id: "second", Name: "Target", TenantId: "tenant"},
				}
			}
			if reason == "host failure" {
				rpc.subscriptionErr = errors.New("private-subscription-detail")
			}
			_ = infraRPCClient(t, rpc)
			directory := filepath.Join(testutil.TempDir(t), "provision")
			stdout, stderr, err := executeRoot(t, "infra", "generate",
				"--subscription", "Target", "--output-dir", directory, "--no-prompt")
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "private")
			assert.Empty(t, stdout)
			assert.Empty(t, stderr)
			_, err = os.Stat(directory)
			assert.ErrorIs(t, err, os.ErrNotExist)
			rpc.mu.Lock()
			assert.Equal(t, []string{"subscriptions"}, rpc.calls)
			assert.Empty(t, rpc.writes)
			rpc.mu.Unlock()
		})
	}
}

func TestInfraConfiguredDefaultGeneratesWithoutPromptsOrProvisioning(t *testing.T) {
	_ = infraCommandFixture(t)
	rpc := &infraRPC{
		defaultSubscription: &azdext.GetUserConfigResponse{Found: true, Value: []byte(`"configured-sub"`)},
		subscriptions: []*azdext.Subscription{
			{Id: "other-sub", TenantId: "other-tenant"},
			{Id: "configured-sub", Name: "Configured subscription", TenantId: "configured-tenant"},
		},
	}
	_ = infraRPCClient(t, rpc)
	directory := filepath.Join(testutil.TempDir(t), "provision")
	stdout, stderr, err := executeRoot(t, "infra", "generate", "--models", "none",
		"--output-dir", directory, "--no-prompt")
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.Contains(t, stdout, "provision")
	// #nosec G304 -- Reads the generated fixture in this test's private directory.
	values, err := os.ReadFile(filepath.Join(directory, ".azure", "dev", ".env"))
	require.NoError(t, err)
	assert.Contains(t, string(values), `AZURE_SUBSCRIPTION_ID="configured-sub"`)
	assert.Contains(t, string(values), `AZURE_TENANT_ID="configured-tenant"`)
	rpc.mu.Lock()
	assert.Equal(t, []string{"user-config:defaults.subscription", "subscriptions"}, rpc.calls)
	rpc.mu.Unlock()
}

func TestInfraSubscriptionFailuresDoNotGenerateFiles(t *testing.T) {
	for _, step := range []string{"configuration", "subscriptions"} {
		t.Run(step, func(t *testing.T) {
			_ = infraCommandFixture(t)
			rpc := &infraRPC{
				defaultSubscription: &azdext.GetUserConfigResponse{Found: true, Value: []byte(`"configured-sub"`)},
			}
			failure := errors.New("private-subscription-detail")
			if step == "configuration" {
				rpc.configErr = failure
			} else {
				rpc.subscriptionErr = failure
			}
			_ = infraRPCClient(t, rpc)
			directory := filepath.Join(testutil.TempDir(t), "provision")
			stdout, stderr, err := executeRoot(t, "infra", "generate", "--models", "none",
				"--output-dir", directory, "--no-prompt")
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "private")
			assert.Empty(t, stdout)
			assert.Empty(t, stderr)
			_, err = os.Stat(directory)
			assert.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestInfraResolveUsesUserAccessTenant(t *testing.T) {
	for _, test := range []struct{ name, userTenant, expectedTenant string }{
		{"selected subscription tenant", "guest-access-tenant", "guest-access-tenant"},
		{"tenant lookup when absent", "", "tenant"},
	} {
		t.Run(test.name, func(t *testing.T) {
			rpc := &infraRPC{}
			host := infraRPCClient(t, rpc)
			argsPath := installAuthenticationCLI(t, false)
			_, err := host.Resolve(azdext.WithAccessToken(t.Context()), &azdext.Subscription{
				Id: "sub", TenantId: "resource-tenant", UserTenantId: test.userTenant,
			}, "https://resource.services.ai.azure.com")
			require.Error(t, err, "the fake azd auth command fails before any ARM network request")
			// #nosec G304 -- Reads this test's private fake-azd argument log.
			args, err := os.ReadFile(argsPath)
			require.NoError(t, err)
			assert.Contains(t, string(args), test.expectedTenant)
			assert.NotContains(t, string(args), "resource-tenant")
			rpc.mu.Lock()
			if test.userTenant == "" {
				assert.Contains(t, rpc.calls, "tenant:sub")
			} else {
				assert.Empty(t, rpc.calls)
			}
			rpc.mu.Unlock()
		})
	}
}

func TestInfraResolveRejectsUnavailableAccessTenant(t *testing.T) {
	for _, test := range []struct {
		name     string
		response *azdext.LookupTenantResponse
		err      error
		want     string
	}{
		{name: "empty tenant", response: &azdext.LookupTenantResponse{}, want: "No Azure tenant"},
		{name: "failed lookup", err: errors.New("tenant lookup failed"), want: "tenant lookup failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			rpc := &infraRPC{tenantResponse: test.response, tenantErr: test.err}
			host := infraRPCClient(t, rpc)
			argsPath := installAuthenticationCLI(t, false)
			_, err := host.Resolve(azdext.WithAccessToken(t.Context()), &azdext.Subscription{
				Id: "sub", TenantId: "resource-tenant",
			}, "https://resource.services.ai.azure.com")
			require.ErrorContains(t, err, test.want)
			_, err = os.Stat(argsPath)
			assert.True(t, os.IsNotExist(err), "tenant lookup failure must not invoke azd auth")
		})
	}
}

func TestInfraPostprovisionUsesAccessTenantInsteadOfProvisionedTenant(t *testing.T) {
	rpc := &infraRPC{tenantResponse: &azdext.LookupTenantResponse{TenantId: "guest-access-tenant"}}
	host := infraRPCClient(t, rpc)
	argsPath := installAuthenticationCLI(t, false)
	snapshot := infraSnapshot{values: map[string]string{
		"_CU_PROVISIONED_ENDPOINT":        "https://resource.services.ai.azure.com",
		"_CU_PROVISIONED_RESOURCE_NAME":   "resource",
		"_CU_PROVISIONED_RESOURCE_GROUP":  "group",
		"_CU_PROVISIONED_SUBSCRIPTION_ID": "provisioned-sub",
		"_CU_PROVISIONED_TENANT_ID":       "resource-tenant",
		"AZURE_TENANT_ID":                 "stale-resource-tenant",
	}}
	request, err := postRequest(snapshot)
	require.NoError(t, err)
	ctx := azdext.WithAccessToken(t.Context())
	post, _, err := host.Capabilities(ctx, snapshot, request)
	require.NoError(t, err)
	capabilities, ok := post.(*infraCapabilities)
	require.True(t, ok)
	_, err = capabilities.client.GetAnalyzer(ctx, "prebuilt-layout")
	require.Error(t, err, "the fake azd auth command fails before any service request")
	// #nosec G304 -- Reads this test's private fake-azd argument log.
	args, err := os.ReadFile(argsPath)
	require.NoError(t, err)
	assert.Contains(t, string(args), "guest-access-tenant")
	assert.NotContains(t, string(args), "resource-tenant")
	rpc.mu.Lock()
	assert.Equal(t, []string{"tenant:provisioned-sub"}, rpc.calls)
	rpc.mu.Unlock()
}

func TestInfraPostprovisionRejectsUnavailableAccessTenant(t *testing.T) {
	for _, test := range []struct {
		name     string
		response *azdext.LookupTenantResponse
		err      error
	}{
		{name: "missing", response: &azdext.LookupTenantResponse{}},
		{name: "failure", err: errors.New("tenant lookup failed")},
	} {
		t.Run(test.name, func(t *testing.T) {
			rpc := &infraRPC{tenantResponse: test.response, tenantErr: test.err}
			host := infraRPCClient(t, rpc)
			post, models, err := host.Capabilities(azdext.WithAccessToken(t.Context()),
				infraSnapshot{values: map[string]string{"_CU_PROVISIONED_TENANT_ID": "resource-tenant"}},
				infra.PostRequest{Subscription: "sub"})
			require.Error(t, err)
			assert.Nil(t, post)
			assert.Nil(t, models)
		})
	}
}

func TestInfraPostprovisionDisabledKeepsOnlyProvisionedEndpoint(t *testing.T) {
	_ = infraCommandFixture(t)
	t.Setenv("CU_DISABLE_AUTO_CONFIG", "true")
	rpc := &infraRPC{
		section: &azdext.GetConfigSectionResponse{
			Found: true, Section: []byte(`{"modelSetupComplete":true}`),
		},
		writes: map[string]json.RawMessage{},
		values: []*azdext.KeyValue{
			{Key: "_CU_PROVISIONED_ENDPOINT", Value: "https://resource.services.ai.azure.com"},
			{Key: "_CU_PROVISIONED_RESOURCE_NAME", Value: "resource"},
			{Key: "_CU_PROVISIONED_RESOURCE_GROUP", Value: "group"},
			{Key: "_CU_PROVISIONED_SUBSCRIPTION_ID", Value: "sub"},
		},
	}
	_ = infraRPCClient(t, rpc)
	stdout, stderr, err := executeRoot(t, "infra", "_postprovision-v1", "-e", "dev", "--no-prompt", "-o", "json")
	require.NoError(t, err)
	assert.Empty(t, stderr)
	var result infra.PostResult
	require.NoError(t, json.Unmarshal([]byte(stdout), &result))
	assert.True(t, result.ConfigurationSetupDisabled)
	assert.False(t, result.EnvironmentConfigured)
	rpc.mu.Lock()
	assert.Empty(t, rpc.writes)
	for _, pair := range rpc.values {
		assert.NotEqual(t, "CU_ENDPOINT", pair.Key)
		assert.NotEqual(t, "CU_DEFAULT_ANALYZER", pair.Key)
	}
	rpc.mu.Unlock()
}

func TestInfraHostScopedRPCAndDottedModelNames(t *testing.T) {
	rpc := &infraRPC{section: &azdext.GetConfigSectionResponse{}, writes: map[string]json.RawMessage{},
		values:        []*azdext.KeyValue{{Key: "AZURE_SUBSCRIPTION_ID", Value: "selected-sub"}},
		subscriptions: []*azdext.Subscription{{Id: "sub", TenantId: "tenant", IsDefault: true}}}
	host := infraRPCClient(t, rpc)
	ctx := azdext.WithAccessToken(t.Context())
	snapshot, err := host.Snapshot(ctx, "selected")
	require.NoError(t, err)
	assert.Equal(t, "selected", snapshot.environment)
	assert.Equal(t, "selected-sub", snapshot.values["AZURE_SUBSCRIPTION_ID"])
	_, err = host.Subscription(ctx, true, "")
	require.NoError(t, err)
	_, err = host.Subscription(ctx, false, "")
	require.NoError(t, err)
	capabilities := &infraCapabilities{host: host, snapshot: snapshot}
	require.NoError(t, capabilities.SetConfiguration(ctx, "endpoint", "https://account.services.ai.azure.com"))
	require.NoError(t, capabilities.SetConfiguration(ctx, "default_analyzer", "prebuilt-layout"))
	require.NoError(t, capabilities.SetConfiguration(ctx, "model_deployments.gpt-5.2", "deployment"))
	require.NoError(t, capabilities.SetConfiguration(ctx, "model_deployments.text-embedding-3-large", "embedding"))
	require.NoError(t, capabilities.MarkComplete(ctx))
	rpc.mu.Lock()
	assert.JSONEq(t, `{"gpt-5.2":"deployment","text-embedding-3-large":"embedding"}`,
		string(rpc.writes["cu.modelDeployments"]))
	assert.JSONEq(t, `true`, string(rpc.writes["cu.modelSetupComplete"]))
	assert.NotContains(t, strings.Join(rpc.calls, " "), "get:default")
	assert.NotContains(t, rpc.calls, "current")
	assert.Contains(t, rpc.calls, "env:selected:CU_DEFAULT_ANALYZER:prebuilt-layout")
	assert.NotContains(t, rpc.writes, "cu.defaultAnalyzer")
	assert.Equal(t, []string{"get:selected", "values:selected", "config:selected:cu"}, rpc.calls[:3])
	rpc.mu.Unlock()
}

func TestInfraHostNoninteractiveDecisionsAndInvalidState(t *testing.T) {
	rpc := &infraRPC{section: &azdext.GetConfigSectionResponse{}, writes: map[string]json.RawMessage{}}
	host := infraRPCClient(t, rpc)
	ctx := azdext.WithAccessToken(t.Context())
	for _, subscriptions := range [][]*azdext.Subscription{
		{}, {{Id: "first", TenantId: "tenant"}, {Id: "second", TenantId: "tenant"}},
		{{Id: "first", TenantId: "tenant", IsDefault: true}, {Id: "second", TenantId: "tenant", IsDefault: true}},
		{{Id: "first"}},
	} {
		rpc.mu.Lock()
		rpc.subscriptions = subscriptions
		rpc.mu.Unlock()
		_, err := host.Subscription(ctx, true, "")
		require.Error(t, err)
	}
	rpc.mu.Lock()
	rpc.subscriptions = []*azdext.Subscription{{Id: "single", TenantId: "tenant"}}
	rpc.mu.Unlock()
	subscription, err := host.Subscription(ctx, true, "")
	require.NoError(t, err)
	assert.Equal(t, "single", subscription.Id)
	snapshot, err := host.Snapshot(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, "default", snapshot.environment)
	for _, section := range []string{"null", "[]", "invalid"} {
		rpc.mu.Lock()
		rpc.section = &azdext.GetConfigSectionResponse{Found: true, Section: []byte(section)}
		rpc.mu.Unlock()
		_, err := host.Snapshot(ctx, "selected")
		require.Error(t, err)
	}
	rpc.mu.Lock()
	rpc.section = &azdext.GetConfigSectionResponse{}
	rpc.values = []*azdext.KeyValue{{Key: "duplicate"}, {Key: "duplicate"}}
	rpc.mu.Unlock()
	_, err = host.Snapshot(ctx, "selected")
	require.Error(t, err)
	request := infra.PostRequest{Endpoint: "https://foundry.services.ai.azure.com", Account: "foundry", Subscription: "sub",
		ResourceGroup: "group", APIVersion: cuclient.DefaultAPIVersion, Selection: "none"}
	post, _, err := host.Capabilities(ctx, snapshot, request)
	require.NoError(t, err)
	actual, ok := post.(*infraCapabilities)
	require.True(t, ok)
	assert.Equal(t, "https://foundry.services.ai.azure.com", actual.request.Endpoint)
	require.NoError(t, actual.SetConfiguration(ctx, "default_analyzer", "prebuilt-layout"))
	actual.snapshot.section["modelDeployments"] = json.RawMessage(`[]`)
	require.Error(t, actual.SetConfiguration(ctx, "model_deployments.gpt-5.2", "deployment"))
	require.Error(t, actual.SetConfiguration(ctx, "unsupported", "value"))
	host.noPrompt = true
	_, err = actual.PromptModels(ctx, nil)
	require.Error(t, err)
	actual.request.Selection = "prompt"
	require.Error(t, actual.SetupModels(ctx))
	_, err = host.Resolve(ctx, subscription, "http://invalid.example")
	require.Error(t, err)
}
