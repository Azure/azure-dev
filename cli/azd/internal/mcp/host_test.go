// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package mcp

import (
	"context"
	"reflect"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// simpleSession is a minimal implementation of server.ClientSession for testing
type simpleSession struct{}

func (s *simpleSession) Initialize()                                         {}
func (s *simpleSession) Initialized() bool                                   { return true }
func (s *simpleSession) NotificationChannel() chan<- mcp.JSONRPCNotification { return nil }
func (s *simpleSession) SessionID() string                                   { return "test-session" }

func TestNewMcpHost(t *testing.T) {
	tests := []struct {
		name          string
		options       []McpHostOption
		expectedEmpty bool
	}{
		{
			name:          "creates empty host with no options",
			options:       []McpHostOption{},
			expectedEmpty: true,
		},
		{
			name: "creates host with servers",
			options: []McpHostOption{
				WithServers(map[string]*ServerConfig{
					"test-server": {
						Type:    "stdio",
						Command: "test-command",
					},
				}),
			},
			expectedEmpty: false,
		},
		{
			name: "creates host with capabilities",
			options: []McpHostOption{
				WithCapabilities(Capabilities{
					Sampling:    NewProxySamplingHandler(),
					Elicitation: NewProxyElicitationHandler(),
				}),
			},
			expectedEmpty: true, // No servers, so servers map is empty
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host := NewMcpHost(tt.options...)

			require.NotNil(t, host)
			assert.NotNil(t, host.servers)
			assert.NotNil(t, host.clients)

			if tt.expectedEmpty {
				assert.Len(t, host.servers, 0)
			} else {
				assert.Greater(t, len(host.servers), 0)
			}
		})
	}
}

func TestWithServers(t *testing.T) {
	tests := []struct {
		name           string
		servers        map[string]*ServerConfig
		expectedCount  int
		expectedExists string
	}{
		{
			name:           "nil servers map",
			servers:        nil,
			expectedCount:  0,
			expectedExists: "",
		},
		{
			name:           "empty servers map",
			servers:        map[string]*ServerConfig{},
			expectedCount:  0,
			expectedExists: "",
		},
		{
			name: "single server",
			servers: map[string]*ServerConfig{
				"test-server": {
					Type:    "stdio",
					Command: "test-cmd",
				},
			},
			expectedCount:  1,
			expectedExists: "test-server",
		},
		{
			name: "multiple servers",
			servers: map[string]*ServerConfig{
				"server1": {Type: "stdio", Command: "cmd1"},
				"server2": {Type: "http", Url: "http://example.com"},
			},
			expectedCount:  2,
			expectedExists: "server1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host := NewMcpHost(WithServers(tt.servers))

			assert.Len(t, host.servers, tt.expectedCount)
			if tt.expectedExists != "" {
				assert.Contains(t, host.servers, tt.expectedExists)
			}
		})
	}
}

func TestWithCapabilities(t *testing.T) {
	t.Run("sets capabilities correctly", func(t *testing.T) {
		sampling := NewProxySamplingHandler()
		elicitation := NewProxyElicitationHandler()
		capabilities := Capabilities{
			Sampling:    sampling,
			Elicitation: elicitation,
		}

		host := NewMcpHost(WithCapabilities(capabilities))

		assert.Equal(t, sampling, host.capabilities.Sampling)
		assert.Equal(t, elicitation, host.capabilities.Elicitation)
	})

	t.Run("sets proxy handler host reference for sampling", func(t *testing.T) {
		proxySampling := NewProxySamplingHandler().(*ProxySamplingHandler)
		capabilities := Capabilities{
			Sampling: proxySampling,
		}

		host := NewMcpHost(WithCapabilities(capabilities))

		assert.Equal(t, host, proxySampling.host)
	})

	t.Run("sets proxy handler host reference for elicitation", func(t *testing.T) {
		proxyElicitation := NewProxyElicitationHandler().(*ProxyElicitationHandler)
		capabilities := Capabilities{
			Elicitation: proxyElicitation,
		}

		host := NewMcpHost(WithCapabilities(capabilities))

		assert.Equal(t, host, proxyElicitation.host)
	})

	t.Run("handles proxy handlers correctly", func(t *testing.T) {
		// This test ensures that proxy handlers are handled correctly
		sampling := NewProxySamplingHandler()
		elicitation := NewProxyElicitationHandler()
		capabilities := Capabilities{
			Sampling:    sampling,
			Elicitation: elicitation,
		}

		// Should not panic
		host := NewMcpHost(WithCapabilities(capabilities))

		assert.Equal(t, sampling, host.capabilities.Sampling)
		assert.Equal(t, elicitation, host.capabilities.Elicitation)
	})
}

func TestMcpHost_Servers(t *testing.T) {
	tests := []struct {
		name            string
		serverConfigs   map[string]*ServerConfig
		expectedServers []string
	}{
		{
			name:            "no servers",
			serverConfigs:   map[string]*ServerConfig{},
			expectedServers: []string{},
		},
		{
			name: "single server",
			serverConfigs: map[string]*ServerConfig{
				"test-server": {Type: "stdio", Command: "test"},
			},
			expectedServers: []string{"test-server"},
		},
		{
			name: "multiple servers",
			serverConfigs: map[string]*ServerConfig{
				"server1": {Type: "stdio", Command: "cmd1"},
				"server2": {Type: "http", Url: "http://example.com"},
				"server3": {Type: "stdio", Command: "cmd3"},
			},
			expectedServers: []string{"server1", "server2", "server3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host := NewMcpHost(WithServers(tt.serverConfigs))
			servers := host.Servers()

			assert.Len(t, servers, len(tt.expectedServers))
			for _, expectedServer := range tt.expectedServers {
				assert.Contains(t, servers, expectedServer)
			}
		})
	}
}

func TestMcpHost_SetSession(t *testing.T) {
	host := NewMcpHost()
	session := &simpleSession{}

	assert.Nil(t, host.session)

	host.SetSession(session)

	assert.Equal(t, session, host.session)
}

func TestMcpHost_SetProxyServer(t *testing.T) {
	host := NewMcpHost()

	// Create a minimal mock MCP server
	mockServer := &server.MCPServer{}

	assert.Nil(t, host.proxyServer)

	host.SetProxyServer(mockServer)

	assert.Equal(t, mockServer, host.proxyServer)
}

func TestMcpHost_ServerTools_NoClient(t *testing.T) {
	host := NewMcpHost(WithServers(map[string]*ServerConfig{
		"test-server": {Type: "stdio", Command: "test"},
	}))

	ctx := context.Background()
	tools, err := host.ServerTools(ctx, "test-server")

	assert.Error(t, err)
	assert.Nil(t, tools)
	assert.Contains(t, err.Error(), "no MCP client found for server test-server")
}

func TestMcpHost_ServerTools_NonexistentServer(t *testing.T) {
	host := NewMcpHost()

	ctx := context.Background()
	tools, err := host.ServerTools(ctx, "nonexistent-server")

	assert.Error(t, err)
	assert.Nil(t, tools)
	assert.Contains(t, err.Error(), "no MCP client found for server nonexistent-server")
}

func TestMcpHost_AllTools_NoServers(t *testing.T) {
	host := NewMcpHost()

	ctx := context.Background()
	tools, err := host.AllTools(ctx)

	assert.NoError(t, err)
	assert.Empty(t, tools)
}

func TestMcpHost_AllTools_WithServersButNoClients(t *testing.T) {
	host := NewMcpHost(WithServers(map[string]*ServerConfig{
		"server1": {Type: "stdio", Command: "test1"},
		"server2": {Type: "http", Url: "http://example.com"},
	}))

	ctx := context.Background()
	tools, err := host.AllTools(ctx)

	// Should not return error even if individual servers fail
	assert.NoError(t, err)
	assert.Empty(t, tools)
}

func TestMcpHost_Stop_NoClients(t *testing.T) {
	host := NewMcpHost()

	err := host.Stop()

	assert.NoError(t, err)
}

func TestMcpHost_Hooks(t *testing.T) {
	host := NewMcpHost()

	hooks := host.Hooks()

	require.NotNil(t, hooks)
	require.NotNil(t, hooks.OnRegisterSession)
	assert.Len(t, hooks.OnRegisterSession, 1)
}

func TestMcpHost_Hooks_OnRegisterSession(t *testing.T) {
	host := NewMcpHost()
	session := &simpleSession{}

	hooks := host.Hooks()
	hookFunc := hooks.OnRegisterSession[0]

	// Verify session is initially nil
	assert.Nil(t, host.session)

	// Call the hook function
	ctx := context.Background()
	hookFunc(ctx, session)

	// Verify session is now set
	assert.Equal(t, session, host.session)
}

func TestMcpHost_Hooks_OnRegisterSession_NilSession(t *testing.T) {
	host := NewMcpHost()

	hooks := host.Hooks()
	hookFunc := hooks.OnRegisterSession[0]

	// Call the hook function with nil session
	ctx := context.Background()
	hookFunc(ctx, nil)

	// Session should remain nil
	assert.Nil(t, host.session)
}

func TestCreateProxyTool(t *testing.T) {
	// Create a mock MCP client
	mockClient := &MockMcpClient{}

	// Create a test MCP tool
	mcpTool := mcp.Tool{
		Name:        "original-tool",
		Description: "Test tool description",
		Annotations: mcp.ToolAnnotation{
			ReadOnlyHint:    new(true),
			IdempotentHint:  new(false),
			DestructiveHint: new(false),
			OpenWorldHint:   new(true),
		},
	}

	// Create proxy tool
	proxyTool := createProxyTool("prefix_original-tool", mcpTool, mockClient)

	// Verify the proxy tool properties
	assert.Equal(t, "prefix_original-tool", proxyTool.Tool.Name)
	assert.Equal(t, "Test tool description", proxyTool.Tool.Description)
	assert.NotNil(t, proxyTool.Handler)

	// Verify annotations are copied correctly
	assert.Equal(t, true, *proxyTool.Tool.Annotations.ReadOnlyHint)
	assert.Equal(t, false, *proxyTool.Tool.Annotations.IdempotentHint)
	assert.Equal(t, false, *proxyTool.Tool.Annotations.DestructiveHint)
	assert.Equal(t, true, *proxyTool.Tool.Annotations.OpenWorldHint)
}

func TestCreateProxyTool_NoAnnotations(t *testing.T) {
	mockClient := &MockMcpClient{}

	// Create a test MCP tool without any annotations set (all nil)
	mcpTool := mcp.Tool{
		Name:        "simple-tool",
		Description: "Simple test tool",
		Annotations: mcp.ToolAnnotation{
			ReadOnlyHint:    nil,
			IdempotentHint:  nil,
			DestructiveHint: nil,
			OpenWorldHint:   nil,
		},
	}

	// Create proxy tool
	proxyTool := createProxyTool("prefix_simple-tool", mcpTool, mockClient)

	// Verify the proxy tool properties
	assert.Equal(t, "prefix_simple-tool", proxyTool.Tool.Name)
	assert.Equal(t, "Simple test tool", proxyTool.Tool.Description)
	assert.NotNil(t, proxyTool.Handler)

	// Since the original tool has nil annotations, the createProxyTool function
	// should not add any specific annotation options, but mcp.NewTool might
	// still set some defaults. We just verify the basic structure is correct.
	assert.NotNil(t, proxyTool.Tool)
	assert.Equal(t, "prefix_simple-tool", proxyTool.Tool.Name)
}

func TestCreateProxyTool_HandlerForwardsCall(t *testing.T) {
	mockClient := &MockMcpClient{}
	expectedResult := mcp.NewToolResultText("test result")

	// Set up expectations for the mock
	expectedRequest := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "original-tool", // This should be the original name, not the prefixed one
		},
	}
	mockClient.On("CallTool", mock.Anything, expectedRequest).Return(expectedResult, nil)

	mcpTool := mcp.Tool{
		Name:        "original-tool",
		Description: "Test tool",
	}

	proxyTool := createProxyTool("prefix_original-tool", mcpTool, mockClient)

	// Create a test request with the prefixed name
	request := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "prefix_original-tool",
		},
	}

	ctx := context.Background()
	result, err := proxyTool.Handler(ctx, request)

	// Verify the call was successful
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "test result", result.Content[0].(mcp.TextContent).Text)

	// Verify that the mock expectations were met
	mockClient.AssertExpectations(t)
}

func TestCreateProxyTool_HandlerForwardsError(t *testing.T) {
	mockClient := &MockMcpClient{}
	expectedError := assert.AnError

	// Set up expectations for the mock to return an error
	expectedRequest := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "original-tool",
		},
	}
	mockClient.On("CallTool", mock.Anything, expectedRequest).Return((*mcp.CallToolResult)(nil), expectedError)

	mcpTool := mcp.Tool{
		Name:        "original-tool",
		Description: "Test tool",
	}

	proxyTool := createProxyTool("prefix_original-tool", mcpTool, mockClient)

	// Create a test request with the prefixed name
	request := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "prefix_original-tool",
		},
	}

	ctx := context.Background()
	result, err := proxyTool.Handler(ctx, request)

	// Verify the error was forwarded
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Equal(t, expectedError, err)

	// Verify that the mock expectations were met
	mockClient.AssertExpectations(t)
}

func TestCreateProxyTool_HandlerCallsExactlyOnce(t *testing.T) {
	mockClient := &MockMcpClient{}
	expectedResult := mcp.NewToolResultText("test result")

	// Set up expectations for exactly one call
	expectedRequest := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "original-tool",
		},
	}
	mockClient.On("CallTool", mock.Anything, expectedRequest).Return(expectedResult, nil).Once()

	mcpTool := mcp.Tool{
		Name:        "original-tool",
		Description: "Test tool",
	}

	proxyTool := createProxyTool("prefix_original-tool", mcpTool, mockClient)

	// Create a test request
	request := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "prefix_original-tool",
		},
	}

	ctx := context.Background()

	// Call the handler
	result, err := proxyTool.Handler(ctx, request)

	// Verify the call was successful
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Verify that the mock expectations were met (exactly one call)
	mockClient.AssertExpectations(t)
}

// MockMcpClient is a testify mock implementation of client.MCPClient for testing
type MockMcpClient struct {
	mock.Mock
}

// Verify that MockMcpClient implements client.MCPClient
var _ client.MCPClient = (*MockMcpClient)(nil)

func (m *MockMcpClient) CallTool(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := m.Called(ctx, request)
	return args.Get(0).(*mcp.CallToolResult), args.Error(1)
}

func (m *MockMcpClient) Initialize(ctx context.Context, request mcp.InitializeRequest) (*mcp.InitializeResult, error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*mcp.InitializeResult), args.Error(1)
}

func (m *MockMcpClient) Ping(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockMcpClient) ListResourcesByPage(
	ctx context.Context,
	request mcp.ListResourcesRequest,
) (*mcp.ListResourcesResult, error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*mcp.ListResourcesResult), args.Error(1)
}

func (m *MockMcpClient) ListResources(
	ctx context.Context,
	request mcp.ListResourcesRequest,
) (*mcp.ListResourcesResult, error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*mcp.ListResourcesResult), args.Error(1)
}

func (m *MockMcpClient) ListResourceTemplatesByPage(
	ctx context.Context,
	request mcp.ListResourceTemplatesRequest,
) (*mcp.ListResourceTemplatesResult, error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*mcp.ListResourceTemplatesResult), args.Error(1)
}

func (m *MockMcpClient) ListResourceTemplates(
	ctx context.Context,
	request mcp.ListResourceTemplatesRequest,
) (*mcp.ListResourceTemplatesResult, error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*mcp.ListResourceTemplatesResult), args.Error(1)
}

func (m *MockMcpClient) ReadResource(ctx context.Context, request mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*mcp.ReadResourceResult), args.Error(1)
}

func (m *MockMcpClient) Subscribe(ctx context.Context, request mcp.SubscribeRequest) error {
	args := m.Called(ctx, request)
	return args.Error(0)
}

func (m *MockMcpClient) Unsubscribe(ctx context.Context, request mcp.UnsubscribeRequest) error {
	args := m.Called(ctx, request)
	return args.Error(0)
}

func (m *MockMcpClient) ListPromptsByPage(
	ctx context.Context,
	request mcp.ListPromptsRequest,
) (*mcp.ListPromptsResult, error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*mcp.ListPromptsResult), args.Error(1)
}

func (m *MockMcpClient) ListPrompts(ctx context.Context, request mcp.ListPromptsRequest) (*mcp.ListPromptsResult, error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*mcp.ListPromptsResult), args.Error(1)
}

func (m *MockMcpClient) GetPrompt(ctx context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*mcp.GetPromptResult), args.Error(1)
}

func (m *MockMcpClient) ListToolsByPage(ctx context.Context, request mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*mcp.ListToolsResult), args.Error(1)
}

func (m *MockMcpClient) ListTools(ctx context.Context, request mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*mcp.ListToolsResult), args.Error(1)
}

func (m *MockMcpClient) SetLevel(ctx context.Context, request mcp.SetLevelRequest) error {
	args := m.Called(ctx, request)
	return args.Error(0)
}

func (m *MockMcpClient) Complete(ctx context.Context, request mcp.CompleteRequest) (*mcp.CompleteResult, error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*mcp.CompleteResult), args.Error(1)
}

func (m *MockMcpClient) Close() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockMcpClient) OnNotification(handler func(notification mcp.JSONRPCNotification)) {
	m.Called(handler)
}

func TestMcpHostOptions_MultipleOptions(t *testing.T) {
	servers := map[string]*ServerConfig{
		"test-server": {Type: "stdio", Command: "test"},
	}
	capabilities := Capabilities{
		Sampling: NewProxySamplingHandler(),
	}

	host := NewMcpHost(
		WithServers(servers),
		WithCapabilities(capabilities),
	)

	assert.Len(t, host.servers, 1)
	assert.Contains(t, host.servers, "test-server")
	assert.NotNil(t, host.capabilities.Sampling)
}

func TestMcpHost_InitialState(t *testing.T) {
	host := NewMcpHost()

	// Verify initial state
	assert.NotNil(t, host.servers)
	assert.NotNil(t, host.clients)
	assert.Empty(t, host.servers)
	assert.Empty(t, host.clients)
	assert.Nil(t, host.proxyServer)
	assert.Nil(t, host.session)

	// Verify capabilities structure exists but handlers are nil
	assert.Nil(t, host.capabilities.Sampling)
	assert.Nil(t, host.capabilities.Elicitation)
}

func TestMcpHost_ServersImmutability(t *testing.T) {
	originalServers := map[string]*ServerConfig{
		"server1": {Type: "stdio", Command: "test1"},
	}

	host := NewMcpHost(WithServers(originalServers))

	// Get servers list
	serversList := host.Servers()

	// Modify the returned slice (shouldn't affect internal state)
	if len(serversList) > 0 {
		serversList[0] = "modified"
	}

	// Verify internal state is unchanged
	newServersList := host.Servers()
	assert.Contains(t, newServersList, "server1")
	assert.NotContains(t, newServersList, "modified")
}

// Test that reflects the Go code organization patterns from the instructions
func TestMcpHost_StructureFollowsStandards(t *testing.T) {
	// Verify that McpHost has the expected structure as defined
	hostType := reflect.TypeFor[McpHost]()

	// Check that all expected fields exist
	expectedFields := []string{"proxyServer", "servers", "capabilities", "clients", "session"}

	for _, fieldName := range expectedFields {
		_, found := hostType.FieldByName(fieldName)
		assert.True(t, found, "Expected field %s not found in McpHost struct", fieldName)
	}
}

// newTestClient creates a fully initialized in-process MCP client for testing.
// The client is backed by a real in-process MCPServer, avoiding network I/O.
func newTestClient(t *testing.T, tools ...server.ServerTool) *client.Client {
	t.Helper()

	mcpServer := server.NewMCPServer("test-server", "1.0.0")
	if len(tools) > 0 {
		mcpServer.AddTools(tools...)
	}

	inProcessClient, err := client.NewInProcessClient(mcpServer)
	require.NoError(t, err)
	t.Cleanup(func() { _ = inProcessClient.Close() })

	err = inProcessClient.Start(t.Context())
	require.NoError(t, err)

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{
		Name:    "test-client",
		Version: "1.0.0",
	}
	_, err = inProcessClient.Initialize(t.Context(), initReq)
	require.NoError(t, err)

	return inProcessClient
}

// dummyToolHandler is a no-op tool handler used in tests.
func dummyToolHandler(
	_ context.Context, _ mcp.CallToolRequest,
) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText("ok"), nil
}

func TestMcpHost_Start_EmptyServers(t *testing.T) {
	host := NewMcpHost()

	err := host.Start(t.Context())

	require.NoError(t, err)
	assert.Empty(t, host.clients)
}

func TestMcpHost_Start_UnsupportedServerType(t *testing.T) {
	host := NewMcpHost(WithServers(map[string]*ServerConfig{
		"grpc-server": {Type: "grpc", Command: "some-command"},
	}))

	err := host.Start(t.Context())

	require.NoError(t, err)
	assert.Empty(t, host.clients,
		"unsupported type should not create a client")
}

func TestMcpHost_Start_StdioNonExistentCommand(t *testing.T) {
	host := NewMcpHost(WithServers(map[string]*ServerConfig{
		"bad-stdio": {
			Type:    "stdio",
			Command: "nonexistent-command-abc123xyz",
			Args:    []string{"--flag"},
			Env:     []string{"KEY=VAL"},
		},
	}))

	err := host.Start(t.Context())

	require.NoError(t, err)
	assert.Empty(t, host.clients,
		"failed transport start should not create a client")
}

func TestMcpHost_Start_HttpInvalidUrl(t *testing.T) {
	host := NewMcpHost(WithServers(map[string]*ServerConfig{
		"bad-http": {
			Type: "http",
			Url:  "://not-a-valid-url",
		},
	}))

	err := host.Start(t.Context())

	require.NoError(t, err)
	assert.Empty(t, host.clients,
		"invalid URL should not create a client")
}

func TestMcpHost_Start_EmptyTypeUsesHttp(t *testing.T) {
	// An empty Type falls into the "http" case per the switch.
	host := NewMcpHost(WithServers(map[string]*ServerConfig{
		"default-type": {
			Type: "",
			Url:  "://also-bad",
		},
	}))

	err := host.Start(t.Context())

	require.NoError(t, err)
	assert.Empty(t, host.clients,
		"empty type with bad URL should not create a client")
}

func TestMcpHost_Start_MixedServerTypes(t *testing.T) {
	host := NewMcpHost(WithServers(map[string]*ServerConfig{
		"unsupported": {Type: "grpc"},
		"bad-stdio": {
			Type:    "stdio",
			Command: "nonexistent-cmd-xyz789",
		},
		"bad-http": {Type: "http", Url: "://invalid"},
	}))

	err := host.Start(t.Context())

	require.NoError(t, err)
	assert.Empty(t, host.clients)
}

func TestMcpHost_ServerTools_WithClient(t *testing.T) {
	testTool := server.ServerTool{
		Tool: mcp.Tool{
			Name:        "my-tool",
			Description: "A test tool",
		},
		Handler: dummyToolHandler,
	}

	testClient := newTestClient(t, testTool)

	host := NewMcpHost(WithServers(map[string]*ServerConfig{
		"svc": {Type: "stdio", Command: "dummy"},
	}))
	host.clients["svc"] = testClient

	tools, err := host.ServerTools(t.Context(), "svc")

	require.NoError(t, err)
	require.Len(t, tools, 1)
	assert.Equal(t, "svc_my-tool", tools[0].Tool.Name)
	assert.Equal(t, "A test tool", tools[0].Tool.Description)
	assert.NotNil(t, tools[0].Handler)
}

func TestMcpHost_ServerTools_MultipleTools(t *testing.T) {
	toolA := server.ServerTool{
		Tool:    mcp.Tool{Name: "tool-a", Description: "Tool A"},
		Handler: dummyToolHandler,
	}
	toolB := server.ServerTool{
		Tool:    mcp.Tool{Name: "tool-b", Description: "Tool B"},
		Handler: dummyToolHandler,
	}

	testClient := newTestClient(t, toolA, toolB)

	host := NewMcpHost(WithServers(map[string]*ServerConfig{
		"svc": {Type: "stdio", Command: "dummy"},
	}))
	host.clients["svc"] = testClient

	result, err := host.ServerTools(t.Context(), "svc")

	require.NoError(t, err)
	require.Len(t, result, 2)

	names := []string{result[0].Tool.Name, result[1].Tool.Name}
	assert.Contains(t, names, "svc_tool-a")
	assert.Contains(t, names, "svc_tool-b")
}

func TestMcpHost_ServerTools_ProxyHandlerForwardsToOriginal(t *testing.T) {
	// Verify the proxy tool handler calls through to the
	// backing client with the original (un-prefixed) tool name.
	testTool := server.ServerTool{
		Tool: mcp.Tool{Name: "echo", Description: "Echo tool"},
		Handler: func(
			_ context.Context, req mcp.CallToolRequest,
		) (*mcp.CallToolResult, error) {
			argsMap, _ := req.Params.Arguments.(map[string]any)
			text, _ := argsMap["text"].(string)
			return mcp.NewToolResultText(text), nil
		},
	}

	testClient := newTestClient(t, testTool)

	host := NewMcpHost(WithServers(map[string]*ServerConfig{
		"ext": {Type: "stdio", Command: "dummy"},
	}))
	host.clients["ext"] = testClient

	tools, err := host.ServerTools(t.Context(), "ext")
	require.NoError(t, err)
	require.Len(t, tools, 1)

	// Invoke the proxy handler with a simple argument map.
	req := mcp.CallToolRequest{}
	req.Params.Name = "ext_echo"
	args := map[string]any{"text": "hello"}
	req.Params.Arguments = args

	result, err := tools[0].Handler(t.Context(), req)

	require.NoError(t, err)
	require.Len(t, result.Content, 1)
	assert.Equal(t, "hello",
		result.Content[0].(mcp.TextContent).Text)
}

func TestMcpHost_AllTools_WithClients(t *testing.T) {
	toolAlpha := server.ServerTool{
		Tool:    mcp.Tool{Name: "alpha", Description: "Alpha"},
		Handler: dummyToolHandler,
	}
	toolBeta := server.ServerTool{
		Tool:    mcp.Tool{Name: "beta", Description: "Beta"},
		Handler: dummyToolHandler,
	}

	client1 := newTestClient(t, toolAlpha)
	client2 := newTestClient(t, toolBeta)

	host := NewMcpHost(WithServers(map[string]*ServerConfig{
		"svc1": {Type: "stdio", Command: "dummy"},
		"svc2": {Type: "stdio", Command: "dummy"},
	}))
	host.clients["svc1"] = client1
	host.clients["svc2"] = client2

	allTools, err := host.AllTools(t.Context())

	require.NoError(t, err)
	assert.Len(t, allTools, 2)
}

func TestMcpHost_AllTools_MixedClientResults(t *testing.T) {
	// One server has a working client; another has no client.
	// AllTools should return tools from the working client and
	// silently skip the server with no client.
	tool := server.ServerTool{
		Tool:    mcp.Tool{Name: "good-tool", Description: "Good"},
		Handler: dummyToolHandler,
	}

	goodClient := newTestClient(t, tool)

	host := NewMcpHost(WithServers(map[string]*ServerConfig{
		"good": {Type: "stdio", Command: "dummy"},
		"bad":  {Type: "stdio", Command: "dummy"},
	}))
	host.clients["good"] = goodClient
	// "bad" server has no client entry → ServerTools returns error,
	// AllTools logs and continues.

	allTools, err := host.AllTools(t.Context())

	require.NoError(t, err)
	assert.Len(t, allTools, 1)
	assert.Equal(t, "good_good-tool", allTools[0].Tool.Name)
}

func TestMcpHost_Stop_WithClient(t *testing.T) {
	mcpServer := server.NewMCPServer("test-server", "1.0.0")
	inProcessClient, err := client.NewInProcessClient(mcpServer)
	require.NoError(t, err)

	host := NewMcpHost()
	host.clients["test"] = inProcessClient

	err = host.Stop()

	require.NoError(t, err)
}

func TestMcpHost_Stop_MultipleClients(t *testing.T) {
	srv1 := server.NewMCPServer("server1", "1.0.0")
	c1, err := client.NewInProcessClient(srv1)
	require.NoError(t, err)

	srv2 := server.NewMCPServer("server2", "1.0.0")
	c2, err := client.NewInProcessClient(srv2)
	require.NoError(t, err)

	host := NewMcpHost()
	host.clients["svc1"] = c1
	host.clients["svc2"] = c2

	err = host.Stop()

	require.NoError(t, err)
}

func TestMcpHost_Start_WithCapabilities(t *testing.T) {
	// Verify Start propagates capabilities to client options.
	// The server won't connect, but we exercise the capability
	// check branches inside Start.
	host := NewMcpHost(
		WithServers(map[string]*ServerConfig{
			"bad": {
				Type:    "stdio",
				Command: "nonexistent-cap-test-cmd",
			},
		}),
		WithCapabilities(Capabilities{
			Sampling:    NewProxySamplingHandler(),
			Elicitation: NewProxyElicitationHandler(),
		}),
	)

	err := host.Start(t.Context())

	require.NoError(t, err)
	assert.Empty(t, host.clients)
}
