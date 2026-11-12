// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"azure.resources/internal/resources"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type wizardPromptServer struct {
	azdext.UnimplementedPromptServiceServer
	selectAnswers       []string
	selectRequests      []*azdext.SelectRequest
	textAnswers         []string
	multiAnswers        [][]string
	confirmAnswers      []bool
	promptErr           error
	invalidSelect       bool
	invalidMulti        bool
	subscription        *azdext.Subscription
	subscriptionRequest *azdext.PromptSubscriptionRequest
	location            string
	model               *azdext.AiModel
	deployment          *azdext.AiModelDeployment
	resource            *azdext.ResourceExtended
	modelRequest        *azdext.PromptAiModelRequest
	deployRequest       *azdext.PromptAiDeploymentRequest
	findRequest         *azdext.PromptSubscriptionResourceRequest
}

func (s *wizardPromptServer) Select(
	_ context.Context, request *azdext.SelectRequest,
) (*azdext.SelectResponse, error) {
	s.selectRequests = append(s.selectRequests, request)
	if s.promptErr != nil {
		return nil, s.promptErr
	}
	if s.invalidSelect {
		return &azdext.SelectResponse{Value: new(int32(1000))}, nil
	}
	if len(s.selectAnswers) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "unexpected select: %s", request.Options.Message)
	}
	answer := s.selectAnswers[0]
	s.selectAnswers = s.selectAnswers[1:]
	for i, choice := range request.Options.Choices {
		if choice.Value == answer {
			return &azdext.SelectResponse{Value: new(int32(i))}, nil
		}
	}
	return nil, status.Errorf(codes.InvalidArgument, "choice %s is unavailable", answer)
}

func (s *wizardPromptServer) Prompt(
	_ context.Context, request *azdext.PromptRequest,
) (*azdext.PromptResponse, error) {
	if s.promptErr != nil {
		return nil, s.promptErr
	}
	if len(s.textAnswers) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "unexpected text prompt: %s", request.Options.Message)
	}
	answer := s.textAnswers[0]
	s.textAnswers = s.textAnswers[1:]
	return &azdext.PromptResponse{Value: answer}, nil
}

func (s *wizardPromptServer) Confirm(
	_ context.Context, request *azdext.ConfirmRequest,
) (*azdext.ConfirmResponse, error) {
	if len(s.confirmAnswers) > 0 {
		answer := s.confirmAnswers[0]
		s.confirmAnswers = s.confirmAnswers[1:]
		return &azdext.ConfirmResponse{Value: new(answer)}, s.promptErr
	}
	return &azdext.ConfirmResponse{Value: request.Options.DefaultValue}, s.promptErr
}

func (s *wizardPromptServer) MultiSelect(
	_ context.Context, request *azdext.MultiSelectRequest,
) (*azdext.MultiSelectResponse, error) {
	if s.promptErr != nil {
		return nil, s.promptErr
	}
	if s.invalidMulti {
		return &azdext.MultiSelectResponse{
			Values: []*azdext.MultiSelectChoice{{Value: "not-an-option"}},
		}, nil
	}
	if len(s.multiAnswers) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "unexpected multi select: %s", request.Options.Message)
	}
	answers := s.multiAnswers[0]
	s.multiAnswers = s.multiAnswers[1:]
	response := &azdext.MultiSelectResponse{}
	for _, answer := range answers {
		response.Values = append(response.Values, &azdext.MultiSelectChoice{Value: answer})
	}
	return response, nil
}

func (s *wizardPromptServer) PromptSubscription(
	_ context.Context, request *azdext.PromptSubscriptionRequest,
) (*azdext.PromptSubscriptionResponse, error) {
	s.subscriptionRequest = request
	return &azdext.PromptSubscriptionResponse{Subscription: s.subscription}, s.promptErr
}

func (s *wizardPromptServer) PromptLocation(
	context.Context, *azdext.PromptLocationRequest,
) (*azdext.PromptLocationResponse, error) {
	return &azdext.PromptLocationResponse{Location: &azdext.Location{Name: s.location}}, s.promptErr
}

func (s *wizardPromptServer) PromptAiModel(
	_ context.Context, request *azdext.PromptAiModelRequest,
) (*azdext.PromptAiModelResponse, error) {
	s.modelRequest = request
	return &azdext.PromptAiModelResponse{Model: s.model}, s.promptErr
}

func (s *wizardPromptServer) PromptAiDeployment(
	_ context.Context, request *azdext.PromptAiDeploymentRequest,
) (*azdext.PromptAiDeploymentResponse, error) {
	s.deployRequest = request
	return &azdext.PromptAiDeploymentResponse{Deployment: s.deployment}, s.promptErr
}

func (s *wizardPromptServer) PromptSubscriptionResource(
	_ context.Context, request *azdext.PromptSubscriptionResourceRequest,
) (*azdext.PromptSubscriptionResourceResponse, error) {
	s.findRequest = request
	return &azdext.PromptSubscriptionResourceResponse{Resource: s.resource}, s.promptErr
}

type wizardEnvironmentServer struct {
	azdext.UnimplementedEnvironmentServiceServer
	values           map[string]string
	currentErr       error
	emptyEnvironment bool
	reads            []*azdext.GetEnvironmentRequest
	selectedErr      error
}

func (s *wizardEnvironmentServer) Get(
	_ context.Context, request *azdext.GetEnvironmentRequest,
) (*azdext.EnvironmentResponse, error) {
	return &azdext.EnvironmentResponse{Environment: &azdext.Environment{Name: request.Name}}, s.selectedErr
}

func (s *wizardEnvironmentServer) GetCurrent(
	context.Context, *azdext.EmptyRequest,
) (*azdext.EnvironmentResponse, error) {
	if s.currentErr != nil {
		return nil, s.currentErr
	}
	if s.emptyEnvironment {
		return &azdext.EnvironmentResponse{}, nil
	}
	return &azdext.EnvironmentResponse{Environment: &azdext.Environment{Name: "dev"}}, nil
}

func (s *wizardEnvironmentServer) GetValues(
	_ context.Context, request *azdext.GetEnvironmentRequest,
) (*azdext.KeyValueListResponse, error) {
	s.reads = append(s.reads, request)
	response := &azdext.KeyValueListResponse{}
	for key, value := range s.values {
		response.KeyValues = append(response.KeyValues, &azdext.KeyValue{Key: key, Value: value})
	}
	return response, nil
}

type wizardAccountServer struct {
	azdext.UnimplementedAccountServiceServer
}

func (s *wizardAccountServer) LookupTenant(
	context.Context, *azdext.LookupTenantRequest,
) (*azdext.LookupTenantResponse, error) {
	return &azdext.LookupTenantResponse{TenantId: "access-tenant"}, nil
}

func newWizardTestClient(t *testing.T, prompts *wizardPromptServer) *azdext.AzdClient {
	return newWizardTestClientWithEnvironment(t, prompts, map[string]string{})
}

func newWizardTestClientWithEnvironment(
	t *testing.T, prompts *wizardPromptServer, values map[string]string,
) *azdext.AzdClient {
	t.Helper()
	return newWizardTestClientWithEnvironmentServer(t, prompts, &wizardEnvironmentServer{values: values})
}

func newWizardTestClientWithEnvironmentServer(
	t *testing.T, prompts *wizardPromptServer, environment *wizardEnvironmentServer,
) *azdext.AzdClient {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	azdext.RegisterPromptServiceServer(server, prompts)
	azdext.RegisterEnvironmentServiceServer(server, environment)
	azdext.RegisterAccountServiceServer(server, &wizardAccountServer{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		<-finished
	})
	client, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(client.Close)
	return client
}

func TestWizardDatabaseCatalog(t *testing.T) {
	prompts := &wizardPromptServer{selectAnswers: []string{"MongoDB"}}
	wizard := &Wizard{Client: newWizardTestClient(t, prompts)}
	resourceType, err := wizard.selectResourceType(t.Context(), "Database")
	require.NoError(t, err)
	require.Equal(t, resources.Mongo, resourceType)
	require.Len(t, prompts.selectRequests, 1)
	var choices []string
	for _, choice := range prompts.selectRequests[0].Options.Choices {
		choices = append(choices, choice.Value)
	}
	require.Equal(t, []string{"CosmosDB", "MongoDB", "Redis"}, choices)
}

func TestWizardResourceCatalog(t *testing.T) {
	tests := []struct {
		name        string
		selections  []string
		text        []string
		multi       [][]string
		resource    string
		definition  resources.Definition
		expectVault bool
	}{
		{
			name: "mongo", selections: []string{"Database", "MongoDB"}, text: []string{"appdb"},
			resource: "appdb", definition: resources.Definition{Type: resources.Mongo}, expectVault: true,
		},
		{
			name: "cosmos", selections: []string{"Database", "CosmosDB"}, text: []string{"appdb"},
			resource: "appdb", definition: resources.Definition{Type: resources.Cosmos},
		},
		{
			name: "redis", selections: []string{"Database", "Redis"}, resource: "redis",
			definition: resources.Definition{Type: resources.Redis}, expectVault: true,
		},
		{
			name: "storage", selections: []string{"Storage account"}, text: []string{"files"}, multi: [][]string{{"Blobs"}},
			resource:   "storage",
			definition: resources.Definition{Type: resources.Storage, StorageContainers: []string{"files"}},
		},
		{
			name: "eventhubs", selections: []string{"Messaging", "Event Hubs"}, text: []string{"events"},
			resource: "event-hubs", definition: resources.Definition{Type: resources.EventHubs, Hubs: []string{"events"}},
		},
		{
			name: "servicebus", selections: []string{"Messaging", "Service Bus"}, text: []string{"work"},
			resource: "service-bus", definition: resources.Definition{Type: resources.ServiceBus, Queues: []string{"work"}},
		},
		{
			name: "keyvault", selections: []string{"Key Vault"}, resource: "vault",
			definition: resources.Definition{Type: resources.KeyVault},
		},
		{
			name: "search", selections: []string{"AI", "Azure AI Search"}, resource: "search",
			definition: resources.Definition{Type: resources.AISearch},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prompts := &wizardPromptServer{
				selectAnswers: test.selections, textAnswers: test.text,
				multiAnswers: append(test.multi, []string{"web"}),
			}
			original := resources.Config{"web": {Type: resources.ContainerApp, Port: 8080}}
			services := map[string]map[string]any{"web": {"project": "src/web", "config": map[string]any{"key": "value"}}}
			wizard := &Wizard{
				Client: newWizardTestClient(t, prompts), Resources: original, Services: services, Writer: &bytes.Buffer{},
			}
			result, err := wizard.Run(t.Context())
			require.NoError(t, err)
			assert.Equal(t, test.resource, result.Name)
			assert.Equal(t, test.definition, result.Resources[test.resource])
			assert.Equal(t, []string{test.resource}, result.Resources["web"].Uses)
			assert.Empty(t, result.Environment)
			assert.False(t, result.HasService)
			assert.Len(t, original, 1)
			assert.Empty(t, original["web"].Uses)
			if test.expectVault {
				assert.Equal(t, resources.KeyVault, result.Resources["vault"].Type)
			}
			nested, ok := result.Services["web"]["config"].(map[string]any)
			require.True(t, ok)
			nested["key"] = "changed"
			assert.Equal(t, map[string]any{"key": "value"}, services["web"]["config"])
		})
	}
}

func wizardAIPrompts() *wizardPromptServer {
	return &wizardPromptServer{
		subscription: &azdext.Subscription{Id: "subscription", TenantId: "resource-tenant", UserTenantId: "guest-tenant"},
		location:     "eastus",
		model:        &azdext.AiModel{Name: "model", Format: "OpenAI"},
		deployment: &azdext.AiModelDeployment{
			ModelName: "model", Format: "OpenAI", Version: "2026-01", Location: "eastus", Capacity: 20,
			Sku: &azdext.AiModelSku{Name: "Standard", UsageName: "model-standard"},
		},
	}
}

func TestWizardOpenAI(t *testing.T) {
	for _, kind := range []string{"Chat (GPT)", "Embeddings (Document search)"} {
		t.Run(kind, func(t *testing.T) {
			prompts := wizardAIPrompts()
			prompts.selectAnswers = []string{"AI", "Azure OpenAI model", kind}
			prompts.textAnswers = []string{"my-model"}
			wizard := &Wizard{Client: newWizardTestClient(t, prompts)}
			result, err := wizard.Run(t.Context())
			require.NoError(t, err)
			assert.Equal(t, resources.AIModelPropsModel{Name: "model", Version: "2026-01"},
				result.Resources["my-model"].Model)
			assert.Equal(t, map[string]string{
				"AZURE_SUBSCRIPTION_ID": "subscription", "AZURE_TENANT_ID": "guest-tenant", "AZURE_LOCATION": "eastus",
			}, result.Environment)
			require.NotNil(t, prompts.modelRequest)
			assert.Equal(t, "guest-tenant", prompts.modelRequest.AzureContext.Scope.TenantId)
			assert.Equal(t, []string{"eastus"}, prompts.modelRequest.Filter.Locations)
			assert.Equal(t, []string{"OpenAI"}, prompts.modelRequest.Filter.Formats)
			expectedCapability := "chat"
			if kind == "Embeddings (Document search)" {
				expectedCapability = "embeddings"
			}
			assert.Equal(t, []string{expectedCapability}, prompts.modelRequest.Filter.Capabilities)
			require.NotNil(t, prompts.deployRequest)
			assert.Equal(t, []string{"Standard"}, prompts.deployRequest.Options.Skus)
			assert.Equal(t, new(int32(20)), prompts.deployRequest.Options.Capacity)
			assert.True(t, prompts.deployRequest.UseDefaultCapacity)
		})
	}
}

func TestWizardFoundryModels(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "new project", true: "append model"}[existing], func(t *testing.T) {
			prompts := wizardAIPrompts()
			prompts.selectAnswers = []string{"AI", "Microsoft Foundry model"}
			prompts.model.Format = "Meta"
			prompts.deployment.Format = "Meta"
			prompts.deployment.Sku = &azdext.AiModelSku{Name: "GlobalStandard", UsageName: "model-global"}
			prompts.deployment.Capacity = 42
			original := resources.Config{}
			name := "ai-project"
			if existing {
				name = "custom-project"
				original[name] = resources.Definition{
					Type: resources.AIProject, Models: []resources.AiServicesModel{
						{Name: "old-model", Version: "1", Format: "Meta",
							Sku: resources.AiServicesModelSku{Name: "Standard", UsageName: "old", Capacity: 5}},
					},
				}
			}
			wizard := &Wizard{Client: newWizardTestClient(t, prompts), Resources: original}
			result, err := wizard.Run(t.Context())
			require.NoError(t, err)
			assert.Equal(t, name, result.Name)
			models := result.Resources[name].Models
			require.NotEmpty(t, models)
			assert.Equal(t, resources.AiServicesModel{
				Name: "model", Version: "2026-01", Format: "Meta",
				Sku: resources.AiServicesModelSku{Name: "GlobalStandard", UsageName: "model-global", Capacity: 42},
			}, models[len(models)-1])
			assert.Empty(t, prompts.modelRequest.Filter.Formats)
			assert.Empty(t, prompts.deployRequest.Options.Skus)
			assert.False(t, prompts.deployRequest.UseDefaultCapacity)
			if existing {
				assert.Len(t, models, 2)
				assert.Len(t, original[name].Models, 1)
			} else {
				assert.Empty(t, original)
			}
		})
	}
}

func TestWizardFoundryCatalogAcrossLocations(t *testing.T) {
	for _, location := range []string{"eastus", ""} {
		t.Run("environment location "+location, func(t *testing.T) {
			prompts := wizardAIPrompts()
			prompts.selectAnswers = []string{"AI", "Microsoft Foundry model"}
			// A location prompt would return an empty selection and fail this flow.
			prompts.location = ""
			prompts.deployment.Location = "westus"
			wizard := &Wizard{Client: newWizardTestClientWithEnvironment(t, prompts, map[string]string{
				"AZURE_LOCATION": location,
			})}
			result, err := wizard.Run(t.Context())
			require.NoError(t, err)
			assert.Equal(t, "model", result.Resources["ai-project"].Models[0].Name)
			require.NotNil(t, prompts.modelRequest)
			assert.Empty(t, prompts.modelRequest.Filter.Locations)
			require.NotNil(t, prompts.deployRequest)
			assert.Empty(t, prompts.deployRequest.Options.Locations)
			assert.NotContains(t, result.Environment, "AZURE_LOCATION")
		})
	}
}

func TestWizardFoundryRequiresSKUUsageName(t *testing.T) {
	for _, usageName := range []string{"", " \t"} {
		t.Run("usage name "+usageName, func(t *testing.T) {
			prompts := wizardAIPrompts()
			prompts.selectAnswers = []string{"AI", "Microsoft Foundry model"}
			prompts.deployment.Sku.UsageName = usageName
			wizard := &Wizard{Client: newWizardTestClient(t, prompts)}
			result, err := wizard.Run(t.Context())
			require.ErrorContains(t, err, "requires a SKU usage name")
			assert.Nil(t, result)
			assert.Nil(t, wizard.Resources)
		})
	}
}

func TestWizardAzureResourcesRequireEnvironment(t *testing.T) {
	for _, selection := range []string{"AI", "Existing resource"} {
		for _, missing := range []string{"not found", "empty name"} {
			t.Run(selection+"/"+missing, func(t *testing.T) {
				prompts := wizardAIPrompts()
				prompts.selectAnswers = []string{selection, "Microsoft Foundry model"}
				if selection == "Existing resource" {
					prompts.selectAnswers = []string{selection, "Storage account"}
				}
				environment := &wizardEnvironmentServer{emptyEnvironment: missing == "empty name"}
				if missing == "not found" {
					environment.currentErr = status.Error(codes.NotFound, "environment not found")
				}
				original := resources.Config{"web": {Type: resources.ContainerApp, Port: 8080}}
				wizard := &Wizard{
					Client: newWizardTestClientWithEnvironmentServer(t, prompts, environment), Resources: original,
				}
				result, err := wizard.Run(t.Context())
				require.ErrorContains(t, err, "run 'azd env new'")
				assert.Nil(t, result)
				assert.Nil(t, prompts.subscriptionRequest)
				assert.Nil(t, prompts.modelRequest)
				assert.Nil(t, prompts.deployRequest)
				assert.Nil(t, prompts.findRequest)
				assert.Equal(t, resources.Config{"web": {Type: resources.ContainerApp, Port: 8080}}, original)
			})
		}
	}
}

func TestWizardAIInvalidDeployment(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*wizardPromptServer)
	}{
		{"missing model", func(p *wizardPromptServer) { p.model = nil }},
		{"missing deployment", func(p *wizardPromptServer) { p.deployment = nil }},
		{"missing SKU", func(p *wizardPromptServer) { p.deployment.Sku = nil }},
		{"missing version", func(p *wizardPromptServer) { p.deployment.Version = "" }},
		{"missing capacity", func(p *wizardPromptServer) { p.deployment.Capacity = 0 }},
		{"unsupported capacity", func(p *wizardPromptServer) { p.deployment.Capacity = 30 }},
		{"wrong model", func(p *wizardPromptServer) { p.deployment.ModelName = "other" }},
		{"wrong location", func(p *wizardPromptServer) { p.deployment.Location = "westus" }},
		{"wrong format", func(p *wizardPromptServer) { p.deployment.Format = "Meta" }},
		{"unsupported SKU", func(p *wizardPromptServer) { p.deployment.Sku.Name = "GlobalStandard" }},
		{"missing subscription", func(p *wizardPromptServer) { p.subscription = nil }},
		{"missing location", func(p *wizardPromptServer) { p.location = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prompts := wizardAIPrompts()
			prompts.selectAnswers = []string{"AI", "Azure OpenAI model", "Chat (GPT)"}
			test.mutate(prompts)
			wizard := &Wizard{Client: newWizardTestClient(t, prompts)}
			result, err := wizard.Run(t.Context())
			require.Error(t, err)
			assert.Nil(t, result)
			assert.Nil(t, wizard.Resources)
		})
	}
}

func TestWizardExistingStorage(t *testing.T) {
	prompts := wizardAIPrompts()
	prompts.selectAnswers = []string{"Existing resource", "Storage account"}
	prompts.textAnswers = []string{"shared-storage"}
	prompts.multiAnswers = [][]string{{"web"}}
	prompts.resource = &azdext.ResourceExtended{
		Id:   "/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/shared",
		Name: "shared", Location: "eastus",
	}
	original := resources.Config{"web": {Type: resources.ContainerApp, Port: 8080}}
	wizard := &Wizard{Client: newWizardTestClient(t, prompts), Resources: original, EntryName: "data"}
	result, err := wizard.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, resources.Definition{Type: resources.Storage, Existing: true}, result.Resources["shared-storage"])
	assert.Equal(t, prompts.resource.Id, result.Environment[resourceIDKey(wizard.EntryName, "shared-storage")])
	files, err := resources.Generate(wizard.EntryName, result.Resources)
	require.NoError(t, err)
	assert.Contains(t, string(files["main.parameters.json"]), "${"+resourceIDKey(wizard.EntryName, "shared-storage")+"}")
	assert.Equal(t, "eastus", result.Environment["AZURE_LOCATION"])
	assert.Equal(t, "guest-tenant", prompts.findRequest.AzureContext.Scope.TenantId)
	assert.Equal(t, "Microsoft.Storage/storageAccounts", prompts.findRequest.Options.ResourceType)
	assert.False(t, *prompts.findRequest.Options.SelectOptions.AllowNewResource)
	assert.Empty(t, original["web"].Uses)
}

func TestWizardExistingCatalog(t *testing.T) {
	tests := []struct {
		selections   []string
		resourceType string
		discoverType string
		resourcePath string
		kind         string
	}{
		{
			[]string{"Database", "Redis"}, resources.Redis, "Microsoft.Cache/redis",
			"Microsoft.Cache/redis/resource", "",
		},
		{
			[]string{"Database", "MongoDB"}, resources.Mongo, "Microsoft.DocumentDB/databaseAccounts/mongodbDatabases",
			"Microsoft.DocumentDB/databaseAccounts/account/mongodbDatabases/resource", "",
		},
		{
			[]string{"Database", "CosmosDB"}, resources.Cosmos, "Microsoft.DocumentDB/databaseAccounts",
			"Microsoft.DocumentDB/databaseAccounts/resource", "",
		},
		{
			[]string{"AI", "Azure OpenAI model"}, resources.OpenAIModel, "Microsoft.CognitiveServices/accounts",
			"Microsoft.CognitiveServices/accounts/resource", "OpenAI",
		},
		{
			[]string{"AI", "Microsoft Foundry model"}, resources.AIProject, "Microsoft.CognitiveServices/accounts/projects",
			"Microsoft.CognitiveServices/accounts/account/projects/resource", "",
		},
		{
			[]string{"AI", "Azure AI Search"}, resources.AISearch, "Microsoft.Search/searchServices",
			"Microsoft.Search/searchServices/resource", "",
		},
		{
			[]string{"Messaging", "Event Hubs"}, resources.EventHubs, "Microsoft.EventHub/namespaces",
			"Microsoft.EventHub/namespaces/resource", "",
		},
		{
			[]string{"Messaging", "Service Bus"}, resources.ServiceBus, "Microsoft.ServiceBus/namespaces",
			"Microsoft.ServiceBus/namespaces/resource", "",
		},
		{
			[]string{"Storage account"}, resources.Storage, "Microsoft.Storage/storageAccounts",
			"Microsoft.Storage/storageAccounts/resource", "",
		},
		{
			[]string{"Key Vault"}, resources.KeyVault, "Microsoft.KeyVault/vaults",
			"Microsoft.KeyVault/vaults/resource", "",
		},
	}
	for _, test := range tests {
		t.Run(test.resourceType, func(t *testing.T) {
			prompts := wizardAIPrompts()
			prompts.selectAnswers = append([]string{"Existing resource"}, test.selections...)
			prompts.textAnswers = []string{"existing-resource"}
			id := "/subscriptions/subscription/resourceGroups/rg/providers/" + test.resourcePath
			prompts.resource = &azdext.ResourceExtended{Id: id}
			wizard := &Wizard{Client: newWizardTestClient(t, prompts)}
			result, err := wizard.Run(t.Context())
			require.NoError(t, err)
			assert.Equal(t, resources.Definition{Type: test.resourceType, Existing: true},
				result.Resources["existing-resource"])
			assert.Equal(t, id, result.Environment[resourceIDKey("", "existing-resource")])
			assert.Equal(t, test.discoverType, prompts.findRequest.Options.ResourceType)
			if test.kind != "" {
				assert.Equal(t, []string{test.kind}, prompts.findRequest.Options.Kinds)
			} else {
				assert.Empty(t, prompts.findRequest.Options.Kinds)
			}
		})
	}
}

func TestWizardExistingRejectsInvalidDiscovery(t *testing.T) {
	for _, id := range []string{
		"",
		"not-an-arm-id",
		"/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Web/sites/site",
		"/subscriptions/other/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/storage",
	} {
		t.Run(id, func(t *testing.T) {
			prompts := wizardAIPrompts()
			prompts.selectAnswers = []string{"Existing resource", "Storage account"}
			prompts.resource = &azdext.ResourceExtended{Id: id}
			wizard := &Wizard{Client: newWizardTestClient(t, prompts)}
			result, err := wizard.Run(t.Context())
			require.Error(t, err)
			assert.Nil(t, result)
		})
	}
}

func TestWizardResourceNameRetries(t *testing.T) {
	prompts := &wizardPromptServer{textAnswers: []string{"", "../escape", "existing", "service", "new-resource"}}
	var output bytes.Buffer
	wizard := &Wizard{
		Client: newWizardTestClient(t, prompts), Writer: &output,
		Resources: resources.Config{"existing": {Type: resources.Storage}},
		Services:  map[string]map[string]any{"service": {}},
	}
	name, err := wizard.resourceName(t.Context(), "")
	require.NoError(t, err)
	assert.Equal(t, "new-resource", name)
	assert.Contains(t, output.String(), "already in use")
}

func TestWizardPromptErrors(t *testing.T) {
	t.Run("invalid choice", func(t *testing.T) {
		wizard := &Wizard{Client: newWizardTestClient(t, &wizardPromptServer{invalidSelect: true})}
		_, err := wizard.Run(t.Context())
		require.ErrorContains(t, err, "invalid selection")
	})
	t.Run("unsupported multi value", func(t *testing.T) {
		wizard := &Wizard{Client: newWizardTestClient(t, &wizardPromptServer{invalidMulti: true})}
		_, err := wizard.multi(t.Context(), "resources", []string{"storage"})
		require.ErrorContains(t, err, "invalid selection")
	})
	t.Run("cancellation", func(t *testing.T) {
		wizard := &Wizard{Client: newWizardTestClient(t, &wizardPromptServer{
			promptErr: status.Error(codes.Canceled, "canceled"),
		})}
		_, err := wizard.Run(t.Context())
		require.Error(t, err)
		assert.Equal(t, codes.Canceled, status.Code(err))
	})
	t.Run("nil client", func(t *testing.T) {
		_, err := (&Wizard{}).Run(t.Context())
		require.ErrorContains(t, err, "host connection")
	})
}

func TestWizardStorageValidation(t *testing.T) {
	prompts := &wizardPromptServer{
		selectAnswers: []string{"Storage account"},
		multiAnswers:  [][]string{{}, {"Blobs"}},
		textAnswers:   []string{"AB", "a", "double--hyphen", "valid-container"},
	}
	wizard := &Wizard{Client: newWizardTestClient(t, prompts)}
	result, err := wizard.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []string{"valid-container"}, result.Resources["storage"].StorageContainers)
}

func TestWizardDependencyCollision(t *testing.T) {
	prompts := &wizardPromptServer{
		selectAnswers: []string{"Database", "Redis"},
	}
	original := resources.Config{"vault": {Type: resources.Storage}}
	wizard := &Wizard{Client: newWizardTestClient(t, prompts), Resources: original}
	_, err := wizard.Run(t.Context())
	require.ErrorContains(t, err, "requires a deployable Key Vault")
	assert.Len(t, original, 1)
}

func TestWizardUsesEnvironmentSubscription(t *testing.T) {
	prompts := wizardAIPrompts()
	prompts.selectAnswers = []string{"AI", "Microsoft Foundry model"}
	wizard := &Wizard{Client: newWizardTestClientWithEnvironment(t, prompts, map[string]string{
		"AZURE_SUBSCRIPTION_ID": "existing-subscription", "AZURE_LOCATION": "eastus",
	})}
	result, err := wizard.Run(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "existing-subscription", prompts.modelRequest.AzureContext.Scope.SubscriptionId)
	assert.Equal(t, "access-tenant", prompts.modelRequest.AzureContext.Scope.TenantId)
	assert.Equal(t, map[string]string{"AZURE_TENANT_ID": "access-tenant"}, result.Environment)
}

func TestWizardExistingAlreadyManaged(t *testing.T) {
	prompts := wizardAIPrompts()
	prompts.selectAnswers = []string{"Existing resource", "Storage account"}
	id := "/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/shared"
	prompts.resource = &azdext.ResourceExtended{Id: id}
	wizard := &Wizard{
		Client: newWizardTestClientWithEnvironment(t, prompts, map[string]string{
			resourceIDKey("", "shared"): strings.ToUpper(id),
		}),
		Resources: resources.Config{"shared": {Type: resources.Storage, Existing: true}},
	}
	_, err := wizard.Run(t.Context())
	require.ErrorContains(t, err, "already managed")
}

func TestWizardFoundryDuplicateDeployment(t *testing.T) {
	prompts := wizardAIPrompts()
	prompts.selectAnswers = []string{"AI", "Microsoft Foundry model"}
	original := resources.Config{"ai-project": {
		Type: resources.AIProject,
		Models: []resources.AiServicesModel{{
			Name: "model", Format: "OpenAI", Version: "2026-01",
			Sku: resources.AiServicesModelSku{Name: "Standard", UsageName: "model-standard", Capacity: 20},
		}},
	}}
	wizard := &Wizard{Client: newWizardTestClient(t, prompts), Resources: original}
	_, err := wizard.Run(t.Context())
	require.ErrorContains(t, err, "already configured")
	assert.Len(t, original["ai-project"].Models, 1)
}

func TestWizardFoundryRejectsAnotherVersionOfSameDeployment(t *testing.T) {
	prompts := wizardAIPrompts()
	prompts.selectAnswers = []string{"AI", "Microsoft Foundry model"}
	original := resources.Config{"ai-project": {
		Type: resources.AIProject,
		Models: []resources.AiServicesModel{{
			Name: "model", Format: "OpenAI", Version: "older",
			Sku: resources.AiServicesModelSku{Name: "GlobalStandard", UsageName: "model-global", Capacity: 10},
		}},
	}}
	wizard := &Wizard{Client: newWizardTestClient(t, prompts), Resources: original}
	_, err := wizard.Run(t.Context())
	require.ErrorContains(t, err, "already configured")
	assert.Len(t, original["ai-project"].Models, 1)
}

func TestWizardRejectsMissingSelectedEnvironmentBeforeAzureSelection(t *testing.T) {
	environment := &wizardEnvironmentServer{selectedErr: status.Error(codes.NotFound, "environment not found")}
	prompts := wizardAIPrompts()
	prompts.selectAnswers = []string{"AI", "Microsoft Foundry model"}
	wizard := &Wizard{
		Client: newWizardTestClientWithEnvironmentServer(t, prompts, environment), Environment: "missing",
	}
	_, err := wizard.Run(t.Context())
	require.ErrorContains(t, err, `getting environment "missing"`)
	require.Empty(t, environment.reads)
	require.Nil(t, prompts.subscriptionRequest)
	require.Nil(t, prompts.modelRequest)
}

func TestWizardReadsSelectedEnvironment(t *testing.T) {
	environment := &wizardEnvironmentServer{
		currentErr: errors.New("default environment must not be read"),
		values:     map[string]string{"AZURE_SUBSCRIPTION_ID": "selected-sub", "AZURE_LOCATION": "eastus"},
	}
	prompts := wizardAIPrompts()
	prompts.selectAnswers = []string{"AI", "Microsoft Foundry model"}
	wizard := &Wizard{
		Client: newWizardTestClientWithEnvironmentServer(t, prompts, environment), Environment: "staging",
	}
	_, err := wizard.Run(t.Context())
	require.NoError(t, err)
	require.Len(t, environment.reads, 1)
	require.Equal(t, "staging", environment.reads[0].Name)
	require.Equal(t, "selected-sub", prompts.modelRequest.AzureContext.Scope.SubscriptionId)
}

func TestWizardLinkEligibility(t *testing.T) {
	tests := []struct {
		host     string
		resource resources.Definition
		expected bool
	}{
		{resources.ContainerApp, resources.Definition{Type: resources.AppService}, false},
		{resources.AppService, resources.Definition{Type: resources.ContainerApp}, false},
		{resources.ContainerApp, resources.Definition{Type: resources.ContainerApp}, true},
		{resources.AppService, resources.Definition{Type: resources.Storage, Existing: true}, true},
		{resources.AppService, resources.Definition{Type: resources.Redis, Existing: true}, false},
		{resources.AppService, resources.Definition{Type: resources.Mongo, Existing: true}, false},
		{resources.ContainerApp, resources.Definition{Type: resources.Cosmos, Existing: true}, true},
		{resources.Storage, resources.Definition{Type: resources.Redis}, false},
	}
	for _, test := range tests {
		t.Run(test.host+"-"+test.resource.Type, func(t *testing.T) {
			assert.Equal(t, test.expected, canLink(test.host, test.resource))
		})
	}
}

func TestWizardResourceIDKey(t *testing.T) {
	assert.Equal(t, "AZURE_RESOURCES_DATA__RESOURCE_MY_RESOURCE_ID", resourceIDKey("data", "my--resource"))
	assert.NotEqual(t, resourceIDKey("data", "storage"), resourceIDKey("app", "storage"))
}
