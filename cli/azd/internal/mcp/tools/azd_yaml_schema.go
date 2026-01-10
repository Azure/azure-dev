// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"
)

// NewAzdYamlSchemaTool creates a new azd yaml schema tool
func NewAzdYamlSchemaTool() server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(
			"validate_azure_yaml",
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithDescription(
				`Validates an azure.yaml against the official azure.yaml JSON schema and returns the results.`,
			),
			mcp.WithString("path",
				mcp.Description("Path to the azure.yaml file"),
				mcp.Required(),
			),
		),
		Handler: HandleAzdYamlSchema,
	}
}

func HandleAzdYamlSchema(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	azureYamlPath, err := request.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	f, err := os.Open(azureYamlPath)
	if err != nil {
		return errorResult("azure.yaml not found: " + err.Error()), nil
	}
	defer f.Close()

	var yamlData interface{}
	yamlBytes, err := io.ReadAll(f)
	if err != nil {
		return errorResult("Failed to read azure.yaml: " + err.Error()), nil
	}
	if err := yaml.Unmarshal(yamlBytes, &yamlData); err != nil {
		return errorResult("Failed to unmarshal azure.yaml: " + err.Error()), nil
	}
	jsonBytes, err := json.Marshal(yamlData)
	if err != nil {
		return errorResult("Failed to marshal azure.yaml to JSON: " + err.Error()), nil
	}

	var jsonObj interface{}
	if err := json.Unmarshal(jsonBytes, &jsonObj); err != nil {
		return errorResult("Failed to unmarshal JSON: " + err.Error()), nil
	}

	// Attempt to validate against stable and alpha schemas
	schemas := []struct {
		url    string
		result string
	}{
		{
			"https://raw.githubusercontent.com/Azure/azure-dev/refs/heads/main/schemas/v1.0/azure.yaml.json",
			"azure.yaml is valid against the stable schema.",
		},
		{
			"https://raw.githubusercontent.com/Azure/azure-dev/refs/heads/main/schemas/alpha/azure.yaml.json",
			"azure.yaml is valid against the alpha schema.",
		},
	}

	loader := jsonschema.SchemeURLLoader{
		"file":  jsonschema.FileLoader{},
		"https": newHttpsUrlLoader(),
	}

	var validationErr error

	for _, s := range schemas {
		compiler := jsonschema.NewCompiler()
		compiler.UseLoader(loader)

		schema, err := compiler.Compile(s.url)
		if err == nil {
			if err := schema.Validate(jsonObj); err == nil {
				return mcp.NewToolResultText(s.result), nil
			} else {
				validationErr = err
			}
		}
	}

	if validationErr != nil {
		return errorResult(validationErr.Error()), nil
	}

	return errorResult("an error occurred while validating azure.yaml"), nil
}

func errorResult(msg string) *mcp.CallToolResult {
	resp := ErrorResponse{Error: true, Message: msg}
	jsonResp, _ := json.MarshalIndent(resp, "", "  ")
	return mcp.NewToolResultText(string(jsonResp))
}

type httpsUrlLoader http.Client

func (l *httpsUrlLoader) Load(url string) (any, error) {
	client := (*http.Client)(l)
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%s returned status code %d", url, resp.StatusCode)
	}
	defer resp.Body.Close()

	return jsonschema.UnmarshalJSON(resp.Body)
}

func newHttpsUrlLoader() *httpsUrlLoader {
	httpLoader := httpsUrlLoader(http.Client{
		Timeout: 15 * time.Second,
	})

	return &httpLoader
}
