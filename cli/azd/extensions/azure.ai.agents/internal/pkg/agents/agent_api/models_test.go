// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCreateAgentRequest_RoundTrip(t *testing.T) {
	t.Parallel()

	original := CreateAgentRequest{
		Name: "test-agent",
		CreateAgentVersionRequest: CreateAgentVersionRequest{
			Description: new("A test agent"),
			Metadata:    map[string]string{"env": "test"},
			Definition: HostedAgentDefinition{
				AgentDefinition: AgentDefinition{
					Kind:      AgentKindHosted,
					RaiConfig: &RaiConfig{RaiPolicyName: "default"},
				},
				CPU:    "1",
				Memory: "2Gi",
			},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Verify JSON tag names
	s := string(data)
	for _, field := range []string{`"name"`, `"description"`, `"metadata"`, `"definition"`} {
		if !strings.Contains(s, field) {
			t.Errorf("expected JSON to contain %s, got: %s", field, s)
		}
	}

	var got CreateAgentRequest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Name != original.Name {
		t.Errorf("Name = %q, want %q", got.Name, original.Name)
	}
	if got.Description == nil || *got.Description != *original.Description {
		t.Errorf("Description mismatch")
	}
	if got.Metadata["env"] != "test" {
		t.Errorf("Metadata[env] = %q, want %q", got.Metadata["env"], "test")
	}
}

func TestAgentObject_RoundTrip(t *testing.T) {
	t.Parallel()

	original := AgentObject{
		Object: "agent",
		ID:     "agent-123",
		Name:   "my-agent",
		Versions: struct {
			Latest AgentVersionObject `json:"latest"`
		}{
			Latest: AgentVersionObject{
				Object:      "agent_version",
				ID:          "ver-1",
				Name:        "my-agent",
				Version:     "1",
				Description: new("version one"),
				Metadata:    map[string]string{"release": "stable"},
				CreatedAt:   1700000000,
			},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	s := string(data)
	for _, field := range []string{`"object"`, `"id"`, `"name"`, `"versions"`, `"latest"`} {
		if !strings.Contains(s, field) {
			t.Errorf("expected JSON to contain %s", field)
		}
	}

	var got AgentObject
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.ID != original.ID {
		t.Errorf("ID = %q, want %q", got.ID, original.ID)
	}
	if got.Versions.Latest.Version != "1" {
		t.Errorf("Latest.Version = %q, want %q", got.Versions.Latest.Version, "1")
	}
	if got.Versions.Latest.CreatedAt != 1700000000 {
		t.Errorf("Latest.CreatedAt = %d, want %d", got.Versions.Latest.CreatedAt, int64(1700000000))
	}
}

func TestHostedAgentDefinition_RoundTrip(t *testing.T) {
	t.Parallel()

	original := HostedAgentDefinition{
		AgentDefinition: AgentDefinition{Kind: AgentKindHosted},
		ProtocolVersions: []ProtocolVersionRecord{
			{Protocol: AgentProtocolResponses, Version: "2024-07-01"},
		},
		CPU:                  "1.0",
		Memory:               "2Gi",
		EnvironmentVariables: map[string]string{"LOG_LEVEL": "debug"},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	s := string(data)
	for _, field := range []string{
		`"container_protocol_versions"`, `"cpu"`, `"memory"`, `"environment_variables"`,
	} {
		if !strings.Contains(s, field) {
			t.Errorf("expected JSON to contain %s", field)
		}
	}

	var got HostedAgentDefinition
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Kind != AgentKindHosted {
		t.Errorf("Kind = %q, want %q", got.Kind, AgentKindHosted)
	}
	if len(got.ProtocolVersions) != 1 || got.ProtocolVersions[0].Version != "2024-07-01" {
		t.Error("ProtocolVersions mismatch")
	}
	if got.EnvironmentVariables["LOG_LEVEL"] != "debug" {
		t.Error("EnvironmentVariables mismatch")
	}
}

func TestHostedAgentDefinition_ContainerImage_RoundTrip(t *testing.T) {
	t.Parallel()

	original := HostedAgentDefinition{
		AgentDefinition: AgentDefinition{Kind: AgentKindHosted},
		ProtocolVersions: []ProtocolVersionRecord{
			{Protocol: AgentProtocolActivityProtocol, Version: "1.0"},
		},
		CPU:    "0.5",
		Memory: "1Gi",
		Image:  "myregistry.azurecr.io/agent:latest",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	s := string(data)
	if !strings.Contains(s, `"image"`) {
		t.Error("expected JSON to contain \"image\"")
	}
	if !strings.Contains(s, `"container_protocol_versions"`) {
		t.Error("expected JSON to contain \"container_protocol_versions\"")
	}

	var got HostedAgentDefinition
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Image != original.Image {
		t.Errorf("Image = %q, want %q", got.Image, original.Image)
	}
	if got.CPU != "0.5" {
		t.Errorf("CPU = %q, want %q", got.CPU, "0.5")
	}
}

func TestAgentVersionObject_RoundTrip(t *testing.T) {
	t.Parallel()

	original := AgentVersionObject{
		Object:      "agent_version",
		ID:          "ver-abc",
		Name:        "my-agent",
		Version:     "3",
		Description: new("third version"),
		Metadata:    map[string]string{"stage": "prod"},
		CreatedAt:   1710000000,
		Status:      "active",
		InstanceIdentity: &AgentIdentityInfo{
			PrincipalID: "inst-principal-id",
			ClientID:    "inst-client-id",
		},
		Blueprint: &BlueprintInfo{
			PrincipalID: "bp-principal-id",
			ClientID:    "bp-client-id",
		},
		BlueprintReference: &BlueprintReference{
			Type:        "ManagedAgentIdentityBlueprint",
			BlueprintID: "my-agent-abc12",
		},
		AgentGUID: "abc12345-0000-1111-2222-333344445555",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	s := string(data)
	for _, field := range []string{
		`"object"`, `"id"`, `"version"`, `"created_at"`,
		`"status"`, `"instance_identity"`, `"blueprint"`,
		`"blueprint_reference"`, `"agent_guid"`,
	} {
		if !strings.Contains(s, field) {
			t.Errorf("expected JSON to contain %s", field)
		}
	}

	var got AgentVersionObject
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Version != "3" {
		t.Errorf("Version = %q, want %q", got.Version, "3")
	}
	if got.CreatedAt != 1710000000 {
		t.Errorf("CreatedAt = %d, want %d", got.CreatedAt, int64(1710000000))
	}
	if got.Metadata["stage"] != "prod" {
		t.Errorf("Metadata[stage] = %q, want %q", got.Metadata["stage"], "prod")
	}
	if got.Status != "active" {
		t.Errorf("Status = %q, want %q", got.Status, "active")
	}
	if got.AgentGUID != "abc12345-0000-1111-2222-333344445555" {
		t.Errorf("AgentGUID = %q, want %q", got.AgentGUID, "abc12345-0000-1111-2222-333344445555")
	}
	if got.InstanceIdentity == nil || got.InstanceIdentity.PrincipalID != "inst-principal-id" {
		t.Errorf("InstanceIdentity.PrincipalID mismatch")
	}
	if got.Blueprint == nil || got.Blueprint.PrincipalID != "bp-principal-id" {
		t.Errorf("Blueprint.PrincipalID mismatch")
	}
	if got.BlueprintReference == nil || got.BlueprintReference.BlueprintID != "my-agent-abc12" {
		t.Errorf("BlueprintReference.BlueprintID mismatch")
	}
}

func TestDeleteAgentResponse_RoundTrip(t *testing.T) {
	t.Parallel()

	original := DeleteAgentResponse{
		Object:  "agent",
		Name:    "old-agent",
		Deleted: true,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got DeleteAgentResponse
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Name != "old-agent" {
		t.Errorf("Name = %q, want %q", got.Name, "old-agent")
	}
	if !got.Deleted {
		t.Error("Deleted = false, want true")
	}
}

func TestDeleteAgentVersionResponse_RoundTrip(t *testing.T) {
	t.Parallel()

	original := DeleteAgentVersionResponse{
		Object:  "agent_version",
		Name:    "my-agent",
		Version: "2",
		Deleted: true,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	s := string(data)
	if !strings.Contains(s, `"version"`) {
		t.Error("expected JSON to contain \"version\"")
	}

	var got DeleteAgentVersionResponse
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Version != "2" {
		t.Errorf("Version = %q, want %q", got.Version, "2")
	}
	if !got.Deleted {
		t.Error("Deleted = false, want true")
	}
}

func TestAgentEventHandlerRequest_RoundTrip(t *testing.T) {
	t.Parallel()

	original := AgentEventHandlerRequest{
		Name:       "eval-handler",
		Metadata:   map[string]string{"purpose": "eval"},
		EventTypes: []AgentEventType{AgentEventTypeResponseCompleted},
		Filter: &AgentEventHandlerFilter{
			AgentVersions: []string{"v1", "v2"},
		},
		Destination: AgentEventHandlerDestination{
			Type: AgentEventHandlerDestinationTypeEvals,
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	s := string(data)
	for _, field := range []string{`"event_types"`, `"filter"`, `"destination"`, `"agent_versions"`} {
		if !strings.Contains(s, field) {
			t.Errorf("expected JSON to contain %s", field)
		}
	}

	var got AgentEventHandlerRequest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Name != "eval-handler" {
		t.Errorf("Name = %q, want %q", got.Name, "eval-handler")
	}
	if len(got.EventTypes) != 1 || got.EventTypes[0] != AgentEventTypeResponseCompleted {
		t.Error("EventTypes mismatch")
	}
	if got.Filter == nil || len(got.Filter.AgentVersions) != 2 {
		t.Error("Filter.AgentVersions mismatch")
	}
}

func TestAgentEventHandlerObject_RoundTrip(t *testing.T) {
	t.Parallel()

	original := AgentEventHandlerObject{
		Object:     "event_handler",
		ID:         "eh-1",
		Name:       "my-handler",
		Metadata:   map[string]string{"team": "platform"},
		CreatedAt:  1720000000,
		EventTypes: []AgentEventType{AgentEventTypeResponseCompleted},
		Destination: AgentEventHandlerDestination{
			Type: AgentEventHandlerDestinationTypeEvals,
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got AgentEventHandlerObject
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.ID != "eh-1" {
		t.Errorf("ID = %q, want %q", got.ID, "eh-1")
	}
	if got.CreatedAt != 1720000000 {
		t.Errorf("CreatedAt = %d, want %d", got.CreatedAt, int64(1720000000))
	}
}

func TestFunctionTool_RoundTrip(t *testing.T) {
	t.Parallel()

	original := FunctionTool{
		Tool:        Tool{Type: ToolTypeFunction},
		Name:        "get_weather",
		Description: new("Gets weather data"),
		Parameters:  map[string]any{"type": "object"},
		Strict:      new(true),
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	s := string(data)
	for _, field := range []string{`"type"`, `"name"`, `"description"`, `"parameters"`, `"strict"`} {
		if !strings.Contains(s, field) {
			t.Errorf("expected JSON to contain %s", field)
		}
	}

	var got FunctionTool
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Type != ToolTypeFunction {
		t.Errorf("Type = %q, want %q", got.Type, ToolTypeFunction)
	}
	if got.Name != "get_weather" {
		t.Errorf("Name = %q, want %q", got.Name, "get_weather")
	}
	if got.Strict == nil || !*got.Strict {
		t.Error("Strict mismatch")
	}
}

func TestMCPTool_RoundTrip(t *testing.T) {
	t.Parallel()

	original := MCPTool{
		Tool:                Tool{Type: ToolTypeMCP},
		ServerLabel:         "my-server",
		ServerURL:           "https://mcp.example.com",
		Headers:             map[string]string{"Authorization": "Bearer tok"},
		ProjectConnectionID: new("conn-abc"),
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	s := string(data)
	for _, field := range []string{`"server_label"`, `"server_url"`, `"project_connection_id"`} {
		if !strings.Contains(s, field) {
			t.Errorf("expected JSON to contain %s", field)
		}
	}

	var got MCPTool
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.ServerLabel != "my-server" {
		t.Errorf("ServerLabel = %q, want %q", got.ServerLabel, "my-server")
	}
	if got.ServerURL != "https://mcp.example.com" {
		t.Errorf("ServerURL = %q, want %q", got.ServerURL, "https://mcp.example.com")
	}
	if got.ProjectConnectionID == nil || *got.ProjectConnectionID != "conn-abc" {
		t.Error("ProjectConnectionID mismatch")
	}
}

func TestFileSearchTool_RoundTrip(t *testing.T) {
	t.Parallel()

	original := FileSearchTool{
		Tool:           Tool{Type: ToolTypeFileSearch},
		VectorStoreIds: []string{"vs-1", "vs-2"},
		MaxNumResults:  new(int32(10)),
		RankingOptions: &RankingOptions{
			Ranker:         new("auto"),
			ScoreThreshold: new(float32(0.8)),
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	s := string(data)
	for _, field := range []string{
		`"vector_store_ids"`, `"max_num_results"`, `"ranking_options"`,
		`"ranker"`, `"score_threshold"`,
	} {
		if !strings.Contains(s, field) {
			t.Errorf("expected JSON to contain %s", field)
		}
	}

	var got FileSearchTool
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(got.VectorStoreIds) != 2 {
		t.Errorf("VectorStoreIds length = %d, want 2", len(got.VectorStoreIds))
	}
	if got.MaxNumResults == nil || *got.MaxNumResults != 10 {
		t.Error("MaxNumResults mismatch")
	}
	if got.RankingOptions == nil || got.RankingOptions.Ranker == nil || *got.RankingOptions.Ranker != "auto" {
		t.Error("RankingOptions.Ranker mismatch")
	}
}

func TestWebSearchPreviewTool_RoundTrip(t *testing.T) {
	t.Parallel()

	original := WebSearchPreviewTool{
		Tool:              Tool{Type: ToolTypeWebSearchPreview},
		SearchContextSize: new("medium"),
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	s := string(data)
	if !strings.Contains(s, `"search_context_size"`) {
		t.Error("expected JSON to contain \"search_context_size\"")
	}

	var got WebSearchPreviewTool
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Type != ToolTypeWebSearchPreview {
		t.Errorf("Type = %q, want %q", got.Type, ToolTypeWebSearchPreview)
	}
	if got.SearchContextSize == nil || *got.SearchContextSize != "medium" {
		t.Error("SearchContextSize mismatch")
	}
}

func TestCodeInterpreterTool_RoundTrip(t *testing.T) {
	t.Parallel()

	original := CodeInterpreterTool{
		Tool:      Tool{Type: ToolTypeCodeInterpreter},
		Container: "container-id-123",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	s := string(data)
	if !strings.Contains(s, `"container"`) {
		t.Error("expected JSON to contain \"container\"")
	}

	var got CodeInterpreterTool
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Type != ToolTypeCodeInterpreter {
		t.Errorf("Type = %q, want %q", got.Type, ToolTypeCodeInterpreter)
	}
	// Container is `any`, so after round-trip it comes back as string
	if got.Container != "container-id-123" {
		t.Errorf("Container = %v, want %q", got.Container, "container-id-123")
	}
}

func TestBingGroundingAgentTool_RoundTrip(t *testing.T) {
	t.Parallel()

	original := BingGroundingAgentTool{
		Tool: Tool{Type: ToolTypeBingGrounding},
		BingGrounding: BingGroundingSearchToolParameters{
			ProjectConnections: ToolProjectConnectionList{
				ProjectConnections: []ToolProjectConnection{{ID: "conn-1"}},
			},
			SearchConfigurations: []BingGroundingSearchConfiguration{
				{
					ProjectConnectionID: "conn-1",
					Market:              new("en-US"),
				},
			},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	s := string(data)
	if !strings.Contains(s, `"bing_grounding"`) {
		t.Error("expected JSON to contain \"bing_grounding\"")
	}
	if !strings.Contains(s, `"project_connections"`) {
		t.Error("expected JSON to contain \"project_connections\"")
	}

	var got BingGroundingAgentTool
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(got.BingGrounding.ProjectConnections.ProjectConnections) != 1 {
		t.Error("ProjectConnections length mismatch")
	}
	if len(got.BingGrounding.SearchConfigurations) != 1 {
		t.Error("SearchConfigurations length mismatch")
	}
}

func TestOpenApiAgentTool_RoundTrip(t *testing.T) {
	t.Parallel()

	original := OpenApiAgentTool{
		Tool: Tool{Type: ToolTypeOpenAPI},
		OpenAPI: OpenApiFunctionDefinition{
			Name:        "petstore",
			Description: new("Pet store API"),
			Spec:        map[string]any{"openapi": "3.0.0"},
			Auth: OpenApiAuthDetails{
				Type: OpenApiAuthTypeAnonymous,
			},
			DefaultParams: []string{"api_version=v1"},
			Functions: []OpenApiFunction{
				{
					Name:        "listPets",
					Description: new("List all pets"),
					Parameters:  map[string]any{"type": "object"},
				},
			},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	s := string(data)
	if !strings.Contains(s, `"openapi"`) {
		t.Error("expected JSON to contain \"openapi\"")
	}

	var got OpenApiAgentTool
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.OpenAPI.Name != "petstore" {
		t.Errorf("OpenAPI.Name = %q, want %q", got.OpenAPI.Name, "petstore")
	}
	if got.OpenAPI.Auth.Type != OpenApiAuthTypeAnonymous {
		t.Errorf("Auth.Type = %q, want %q", got.OpenAPI.Auth.Type, OpenApiAuthTypeAnonymous)
	}
	if len(got.OpenAPI.Functions) != 1 {
		t.Errorf("Functions length = %d, want 1", len(got.OpenAPI.Functions))
	}
}

func TestSessionFileInfo_RoundTrip(t *testing.T) {
	t.Parallel()

	original := SessionFileInfo{
		Name:         "data.csv",
		Path:         "/workspace/data.csv",
		IsDirectory:  false,
		Size:         2048,
		Mode:         0644,
		LastModified: new(FlexibleTimestamp("2024-06-15T10:30:00Z")),
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	s := string(data)
	for _, field := range []string{`"name"`, `"path"`, `"is_dir"`, `"size"`, `"mode"`, `"modified_time"`} {
		if !strings.Contains(s, field) {
			t.Errorf("expected JSON to contain %s", field)
		}
	}

	var got SessionFileInfo
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Name != "data.csv" {
		t.Errorf("Name = %q, want %q", got.Name, "data.csv")
	}
	if got.IsDirectory {
		t.Error("IsDirectory = true, want false")
	}
	if got.Size != 2048 {
		t.Errorf("Size = %d, want %d", got.Size, int64(2048))
	}
	if got.LastModified == nil || got.LastModified.String() != "2024-06-15T10:30:00Z" {
		t.Error("LastModified mismatch")
	}
}

func TestFlexibleTimestamp_UnmarshalString(t *testing.T) {
	t.Parallel()

	raw := `{"name":"f.txt","path":"/f.txt","is_dir":false,"modified_time":"2025-03-01T12:00:00Z"}`
	var got SessionFileInfo
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal string timestamp: %v", err)
	}
	if got.LastModified == nil || got.LastModified.String() != "2025-03-01T12:00:00Z" {
		t.Errorf("LastModified = %v, want 2025-03-01T12:00:00Z", got.LastModified)
	}
}

func TestFlexibleTimestamp_UnmarshalNumber(t *testing.T) {
	t.Parallel()

	// 1700000000 == 2023-11-14T22:13:20Z
	raw := `{"name":"f.txt","path":"/f.txt","is_dir":false,"modified_time":1700000000}`
	var got SessionFileInfo
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal numeric timestamp: %v", err)
	}
	if got.LastModified == nil ||
		got.LastModified.String() != "2023-11-14T22:13:20Z" {
		t.Errorf(
			"LastModified = %v, want 2023-11-14T22:13:20Z",
			got.LastModified,
		)
	}

	// Verify round-trip: re-marshalling produces an RFC3339 string.
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal numeric timestamp: %v", err)
	}
	if !strings.Contains(
		string(data),
		`"modified_time":"2023-11-14T22:13:20Z"`,
	) {
		t.Errorf(
			"marshalled JSON = %s, want RFC3339 string",
			string(data),
		)
	}
}

func TestFlexibleTimestamp_UnmarshalNumberInEntries(t *testing.T) {
	t.Parallel()

	raw := `{"path":"/","entries":[{"name":"a","path":"/a","is_dir":false,"modified_time":1700000000}]}`
	var got SessionFileList
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal entries with numeric timestamp: %v", err)
	}
	if len(got.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got.Entries))
	}
	if got.Entries[0].LastModified == nil ||
		got.Entries[0].LastModified.String() != "2023-11-14T22:13:20Z" {
		t.Errorf(
			"entry LastModified = %v, want 2023-11-14T22:13:20Z",
			got.Entries[0].LastModified,
		)
	}
}

func TestFlexibleTimestamp_UnmarshalMilliseconds(t *testing.T) {
	t.Parallel()

	// 1700000000123 ms == 2023-11-14T22:13:20.123Z
	raw := `{"name":"f.txt","path":"/f.txt","is_dir":false,"modified_time":1700000000123}`
	var got SessionFileInfo
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal millisecond timestamp: %v", err)
	}
	want := "2023-11-14T22:13:20.123Z"
	if got.LastModified == nil ||
		got.LastModified.String() != want {
		t.Errorf(
			"LastModified = %v, want %s",
			got.LastModified, want,
		)
	}

	// Verify round-trip preserves millisecond precision.
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal millisecond timestamp: %v", err)
	}
	wantJSON := `"modified_time":"2023-11-14T22:13:20.123Z"`
	if !strings.Contains(string(data), wantJSON) {
		t.Errorf(
			"marshalled JSON = %s, want %s",
			string(data), wantJSON,
		)
	}
}

func TestSessionFileList_RoundTrip(t *testing.T) {
	t.Parallel()

	original := SessionFileList{
		Path: "/workspace",
		Entries: []SessionFileInfo{
			{Name: "file1.txt", Path: "/workspace/file1.txt", IsDirectory: false, Size: 100},
			{Name: "subdir", Path: "/workspace/subdir", IsDirectory: true},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	s := string(data)
	if !strings.Contains(s, `"entries"`) {
		t.Error("expected JSON to contain \"entries\"")
	}

	var got SessionFileList
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Path != "/workspace" {
		t.Errorf("Path = %q, want %q", got.Path, "/workspace")
	}
	if len(got.Entries) != 2 {
		t.Fatalf("Entries length = %d, want 2", len(got.Entries))
	}
	if !got.Entries[1].IsDirectory {
		t.Error("Entries[1].IsDirectory = false, want true")
	}
}

func TestEvalsDestination_RoundTrip(t *testing.T) {
	t.Parallel()

	original := EvalsDestination{
		AgentEventHandlerDestination: AgentEventHandlerDestination{
			Type: AgentEventHandlerDestinationTypeEvals,
		},
		EvalID:        "eval-123",
		MaxHourlyRuns: new(int32(10)),
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	s := string(data)
	for _, field := range []string{`"eval_id"`, `"max_hourly_runs"`} {
		if !strings.Contains(s, field) {
			t.Errorf("expected JSON to contain %s", field)
		}
	}

	var got EvalsDestination
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.EvalID != "eval-123" {
		t.Errorf("EvalID = %q, want %q", got.EvalID, "eval-123")
	}
	if got.MaxHourlyRuns == nil || *got.MaxHourlyRuns != 10 {
		t.Error("MaxHourlyRuns mismatch")
	}
}

func TestWorkflowDefinition_RoundTrip(t *testing.T) {
	t.Parallel()

	original := WorkflowDefinition{
		AgentDefinition: AgentDefinition{Kind: AgentKindWorkflow},
		Trigger:         map[string]any{"type": "schedule", "cron": "0 * * * *"},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	s := string(data)
	if !strings.Contains(s, `"trigger"`) {
		t.Error("expected JSON to contain \"trigger\"")
	}

	var got WorkflowDefinition
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Kind != AgentKindWorkflow {
		t.Errorf("Kind = %q, want %q", got.Kind, AgentKindWorkflow)
	}
	if got.Trigger["type"] != "schedule" {
		t.Errorf("Trigger[type] = %v, want %q", got.Trigger["type"], "schedule")
	}
}

func TestCommonListObjectProperties_RoundTrip(t *testing.T) {
	t.Parallel()

	original := AgentList{
		Data: []AgentObject{
			{Object: "agent", ID: "a1", Name: "agent-one"},
		},
		CommonListObjectProperties: CommonListObjectProperties{
			Object:  "list",
			FirstID: "a1",
			LastID:  "a1",
			HasMore: false,
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	s := string(data)
	for _, field := range []string{`"first_id"`, `"last_id"`, `"has_more"`} {
		if !strings.Contains(s, field) {
			t.Errorf("expected JSON to contain %s", field)
		}
	}

	var got AgentList
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(got.Data) != 1 || got.Data[0].ID != "a1" {
		t.Error("Data mismatch")
	}
	if got.HasMore {
		t.Error("HasMore = true, want false")
	}
}

func TestAgentEndpoint_RoundTrip(t *testing.T) {
	t.Parallel()

	original := AgentEndpoint{
		Protocols: []AgentProtocol{AgentProtocolResponses, AgentProtocolA2A},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	s := string(data)
	for _, field := range []string{`"protocols"`, `"responses"`, `"a2a"`} {
		if !strings.Contains(s, field) {
			t.Errorf("expected JSON to contain %s, got: %s", field, s)
		}
	}

	var got AgentEndpoint
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(got.Protocols) != 2 {
		t.Fatalf("Protocols length = %d, want 2", len(got.Protocols))
	}
	if got.Protocols[0] != AgentProtocolResponses {
		t.Errorf("Protocols[0] = %q, want %q", got.Protocols[0], AgentProtocolResponses)
	}
	if got.Protocols[1] != AgentProtocolA2A {
		t.Errorf("Protocols[1] = %q, want %q", got.Protocols[1], AgentProtocolA2A)
	}
}

func TestAgentCard_RoundTrip(t *testing.T) {
	t.Parallel()

	original := AgentCard{
		Description: "test a2a agent",
		Version:     new("1.0"),
		Skills: []AgentCardSkill{
			{
				ID:          "skill1",
				Name:        "greet",
				Description: "provides a greeting to the user",
				Tags:        []string{"greeting", "hello"},
				Examples:    []string{"Say hello", "Greet the user"},
			},
			{
				ID:          "skill2",
				Name:        "farewell",
				Description: "says goodbye",
			},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	s := string(data)
	for _, field := range []string{
		`"description"`, `"version"`, `"skills"`,
		`"id"`, `"name"`, `"tags"`, `"examples"`,
	} {
		if !strings.Contains(s, field) {
			t.Errorf("expected JSON to contain %s, got: %s", field, s)
		}
	}

	var got AgentCard
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Description != "test a2a agent" {
		t.Errorf("Description = %q, want %q", got.Description, "test a2a agent")
	}
	if got.Version == nil || *got.Version != "1.0" {
		t.Error("Version mismatch")
	}
	if len(got.Skills) != 2 {
		t.Fatalf("Skills length = %d, want 2", len(got.Skills))
	}
	if got.Skills[0].ID != "skill1" {
		t.Errorf("Skills[0].ID = %q, want %q", got.Skills[0].ID, "skill1")
	}
	if len(got.Skills[0].Tags) != 2 {
		t.Errorf("Skills[0].Tags length = %d, want 2", len(got.Skills[0].Tags))
	}
	if len(got.Skills[0].Examples) != 2 {
		t.Errorf("Skills[0].Examples length = %d, want 2", len(got.Skills[0].Examples))
	}
	// Second skill has no tags/examples — verify they are omitted/empty.
	if len(got.Skills[1].Tags) != 0 {
		t.Errorf("Skills[1].Tags length = %d, want 0", len(got.Skills[1].Tags))
	}
}

func TestAgentCard_NoVersion(t *testing.T) {
	t.Parallel()

	original := AgentCard{
		Description: "agent without version",
		Skills: []AgentCardSkill{
			{ID: "s1", Name: "do-stuff", Description: "does stuff"},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if strings.Contains(string(data), `"version"`) {
		t.Error("version should be omitted when nil")
	}

	var got AgentCard
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Version != nil {
		t.Errorf("Version = %v, want nil", got.Version)
	}
}

func TestCreateAgentRequest_WithEndpointAndCard(t *testing.T) {
	t.Parallel()

	original := CreateAgentRequest{
		Name: "a2a-agent",
		AgentEndpoint: &AgentEndpoint{
			Protocols: []AgentProtocol{AgentProtocolResponses, AgentProtocolA2A},
		},
		AgentCard: &AgentCard{
			Description: "test a2a agent",
			Skills: []AgentCardSkill{
				{ID: "skill1", Name: "greet", Description: "provides a greeting"},
			},
		},
		CreateAgentVersionRequest: CreateAgentVersionRequest{
			Description: new("An A2A agent"),
			Definition: HostedAgentDefinition{
				AgentDefinition: AgentDefinition{Kind: AgentKindHosted},
			},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	s := string(data)
	for _, field := range []string{`"agent_endpoint"`, `"agent_card"`, `"a2a"`} {
		if !strings.Contains(s, field) {
			t.Errorf("expected JSON to contain %s, got: %s", field, s)
		}
	}

	var got CreateAgentRequest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.AgentEndpoint == nil {
		t.Fatal("AgentEndpoint is nil")
	}
	if len(got.AgentEndpoint.Protocols) != 2 {
		t.Fatalf("AgentEndpoint.Protocols length = %d, want 2", len(got.AgentEndpoint.Protocols))
	}
	if got.AgentCard == nil {
		t.Fatal("AgentCard is nil")
	}
	if got.AgentCard.Description != "test a2a agent" {
		t.Errorf("AgentCard.Description = %q, want %q", got.AgentCard.Description, "test a2a agent")
	}
}

func TestIsInvocable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		protocol AgentProtocol
		want     bool
	}{
		{AgentProtocolResponses, true},
		{AgentProtocolInvocations, true},
		{AgentProtocolA2A, false},
		{AgentProtocolActivityProtocol, false},
		{AgentProtocol("unknown"), false},
	}

	for _, tt := range tests {
		t.Run(string(tt.protocol), func(t *testing.T) {
			t.Parallel()
			if got := tt.protocol.IsInvocable(); got != tt.want {
				t.Errorf("IsInvocable() = %v, want %v", got, tt.want)
			}
		})
	}
}
