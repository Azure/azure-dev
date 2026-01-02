// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package registry_api

import (
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/version"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
)

// RegistryAgentManifestClient provides methods to interact with Azure ML registry agent manifests
type RegistryAgentManifestClient struct {
	baseEndpoint string
	pipeline     runtime.Pipeline
}

// NewRegistryAgentManifestClient creates a new instance of RegistryAgentManifestClient
func NewRegistryAgentManifestClient(registryName string, cred azcore.TokenCredential) *RegistryAgentManifestClient {
	baseEndpoint := fmt.Sprintf("https://int.api.azureml-test.ms/agent-asset/v1.0/registries/%s/agentManifests", registryName)

	userAgent := fmt.Sprintf("azd-ext-azure-ai-agents/%s", version.Version)

	clientOptions := &policy.ClientOptions{
		Logging: policy.LogOptions{
			AllowedHeaders: []string{azsdk.MsCorrelationIdHeader},
		},
		PerCallPolicies: []policy.Policy{
			runtime.NewBearerTokenPolicy(cred, []string{"https://ai.azure.com/.default"}, nil),
			azsdk.NewMsCorrelationPolicy(),
			azsdk.NewUserAgentPolicy(userAgent),
		},
	}

	pipeline := runtime.NewPipeline(
		"azure-ai-agents",
		"v1.0.0",
		runtime.PipelineOptions{},
		clientOptions,
	)

	return &RegistryAgentManifestClient{
		baseEndpoint: baseEndpoint,
		pipeline:     pipeline,
	}
}

// GetManifest retrieves a specific agent manifest from the registry
func (c *RegistryAgentManifestClient) GetManifest(ctx context.Context, manifestName string, manifestVersion string) (*Manifest, error) {
	targetEndpoint := fmt.Sprintf("%s/%s/versions/%s", c.baseEndpoint, manifestName, manifestVersion)

	req, err := runtime.NewRequest(ctx, http.MethodGet, targetEndpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	fmt.Println("Making HTTP request to retrieve manifest...")
	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK) {
		return nil, runtime.NewResponseError(resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var manifest Manifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, fmt.Errorf("failed to unmarshal manifest response: %w", err)
	}

	tools, err := HandleTools(&manifest)
	if err != nil {
		return nil, fmt.Errorf("failed to handle tools: %w", err)
	}

	manifest.Template.Tools = tools

	return &manifest, nil
}

func HandleTools(manifest *Manifest) ([]any, error) {
	tools := manifest.Template.Tools

	toolsBytes, err := json.Marshal(tools)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal tools: %w", err)
	}

	var toolsBase []interface{}
	if err := json.Unmarshal(toolsBytes, &toolsBase); err != nil {
		return nil, fmt.Errorf("failed to unmarshal tools: %w", err)
	}

	var result []any
	for _, tool := range toolsBase {
		toolBytes, err := json.Marshal(tool)
		if err != nil {
			continue
		}

		var toolBase agent_api.Tool
		if err := json.Unmarshal(toolBytes, &toolBase); err != nil {
			return nil, fmt.Errorf("failed to unmarshal to Tool: %w", err)
		}

		switch toolBase.Type {
		case agent_api.ToolTypeFunction:
			var functionTool agent_api.FunctionTool
			if err := json.Unmarshal(toolBytes, &functionTool); err != nil {
				return nil, fmt.Errorf("failed to unmarshal to FunctionTool: %w", err)
			}
			result = append(result, functionTool)
		case agent_api.ToolTypeFileSearch:
			var fileSearchTool agent_api.FileSearchTool
			if err := json.Unmarshal(toolBytes, &fileSearchTool); err != nil {
				return nil, fmt.Errorf("failed to unmarshal to FileSearchTool: %w", err)
			}
			result = append(result, fileSearchTool)
		case agent_api.ToolTypeComputerUsePreview:
			var computerUseTool agent_api.ComputerUsePreviewTool
			if err := json.Unmarshal(toolBytes, &computerUseTool); err != nil {
				return nil, fmt.Errorf("failed to unmarshal to ComputerUsePreviewTool: %w", err)
			}
			result = append(result, computerUseTool)
		case agent_api.ToolTypeWebSearchPreview:
			var webSearchTool agent_api.WebSearchPreviewTool
			if err := json.Unmarshal(toolBytes, &webSearchTool); err != nil {
				return nil, fmt.Errorf("failed to unmarshal to WebSearchPreviewTool: %w", err)
			}
			result = append(result, webSearchTool)
		case agent_api.ToolTypeMCP:
			var mcpTool agent_api.MCPTool
			if err := json.Unmarshal(toolBytes, &mcpTool); err != nil {
				return nil, fmt.Errorf("failed to unmarshal to MCPTool: %w", err)
			}

			mcpTool.Tool = toolBase
			result = append(result, mcpTool)
		case agent_api.ToolTypeCodeInterpreter:
			var codeInterpreterTool agent_api.CodeInterpreterTool
			if err := json.Unmarshal(toolBytes, &codeInterpreterTool); err != nil {
				return nil, fmt.Errorf("failed to unmarshal to CodeInterpreterTool: %w", err)
			}
			result = append(result, codeInterpreterTool)
		case agent_api.ToolTypeImageGeneration:
			var imageGenTool agent_api.ImageGenTool
			if err := json.Unmarshal(toolBytes, &imageGenTool); err != nil {
				return nil, fmt.Errorf("failed to unmarshal to ImageGenTool: %w", err)
			}
			result = append(result, imageGenTool)
		case agent_api.ToolTypeLocalShell:
			var localShellTool agent_api.LocalShellTool
			if err := json.Unmarshal(toolBytes, &localShellTool); err != nil {
				return nil, fmt.Errorf("failed to unmarshal to LocalShellTool: %w", err)
			}
			result = append(result, localShellTool)
		case agent_api.ToolTypeBingGrounding:
			var bingGroundingTool agent_api.BingGroundingAgentTool
			if err := json.Unmarshal(toolBytes, &bingGroundingTool); err != nil {
				return nil, fmt.Errorf("failed to unmarshal to BingGroundingAgentTool: %w", err)
			}
			result = append(result, bingGroundingTool)
		case agent_api.ToolTypeBrowserAutomationPreview:
			var browserAutomationTool agent_api.BrowserAutomationAgentTool
			if err := json.Unmarshal(toolBytes, &browserAutomationTool); err != nil {
				return nil, fmt.Errorf("failed to unmarshal to BrowserAutomationAgentTool: %w", err)
			}
			result = append(result, browserAutomationTool)
		case agent_api.ToolTypeFabricDataagentPreview:
			var fabricTool agent_api.MicrosoftFabricAgentTool
			if err := json.Unmarshal(toolBytes, &fabricTool); err != nil {
				return nil, fmt.Errorf("failed to unmarshal to MicrosoftFabricAgentTool: %w", err)
			}
			result = append(result, fabricTool)
		case agent_api.ToolTypeSharepointGroundingPreview:
			var sharepointTool agent_api.SharepointAgentTool
			if err := json.Unmarshal(toolBytes, &sharepointTool); err != nil {
				return nil, fmt.Errorf("failed to unmarshal to SharepointAgentTool: %w", err)
			}
			result = append(result, sharepointTool)
		case agent_api.ToolTypeAzureAISearch:
			var azureAISearchTool agent_api.AzureAISearchAgentTool
			if err := json.Unmarshal(toolBytes, &azureAISearchTool); err != nil {
				return nil, fmt.Errorf("failed to unmarshal to AzureAISearchAgentTool: %w", err)
			}
			result = append(result, azureAISearchTool)
		case agent_api.ToolTypeOpenAPI:
			var openApiTool agent_api.OpenApiAgentTool
			if err := json.Unmarshal(toolBytes, &openApiTool); err != nil {
				return nil, fmt.Errorf("failed to unmarshal to OpenApiAgentTool: %w", err)
			}
			result = append(result, openApiTool)
		case agent_api.ToolTypeBingCustomSearchPreview:
			var bingCustomSearchTool agent_api.BingCustomSearchAgentTool
			if err := json.Unmarshal(toolBytes, &bingCustomSearchTool); err != nil {
				return nil, fmt.Errorf("failed to unmarshal to BingCustomSearchAgentTool: %w", err)
			}
			result = append(result, bingCustomSearchTool)
		case agent_api.ToolTypeAzureFunction:
			var azureFunctionTool agent_api.AzureFunctionAgentTool
			if err := json.Unmarshal(toolBytes, &azureFunctionTool); err != nil {
				return nil, fmt.Errorf("failed to unmarshal to AzureFunctionAgentTool: %w", err)
			}
			result = append(result, azureFunctionTool)
		case agent_api.ToolTypeCaptureStructuredOutputs:
			var captureStructuredOutputsTool agent_api.CaptureStructuredOutputsTool
			if err := json.Unmarshal(toolBytes, &captureStructuredOutputsTool); err != nil {
				return nil, fmt.Errorf("failed to unmarshal to CaptureStructuredOutputsTool: %w", err)
			}
			result = append(result, captureStructuredOutputsTool)
		case agent_api.ToolTypeA2APreview:
			var a2aTool agent_api.A2ATool
			if err := json.Unmarshal(toolBytes, &a2aTool); err != nil {
				return nil, fmt.Errorf("failed to unmarshal to A2ATool: %w", err)
			}
			result = append(result, a2aTool)
		case agent_api.ToolTypeMemorySearch:
			var memorySearchTool agent_api.MemorySearchTool
			if err := json.Unmarshal(toolBytes, &memorySearchTool); err != nil {
				return nil, fmt.Errorf("failed to unmarshal to MemorySearchTool: %w", err)
			}
			result = append(result, memorySearchTool)
		default:
			return nil, fmt.Errorf("unsupported tool type: %s", toolBase.Type)
		}
	}

	return result, nil
}

// GetAllLatest retrieves all latest agent manifests from the specified registry
func (c *RegistryAgentManifestClient) GetAllLatest(ctx context.Context) ([]Manifest, error) {
	req, err := runtime.NewRequest(ctx, http.MethodGet, c.baseEndpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK) {
		return nil, runtime.NewResponseError(resp)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var manifestList ManifestList
	if err := json.Unmarshal(body, &manifestList); err != nil {
		return nil, fmt.Errorf("failed to unmarshal manifest list response: %w", err)
	}

	return manifestList.Value, nil
}
