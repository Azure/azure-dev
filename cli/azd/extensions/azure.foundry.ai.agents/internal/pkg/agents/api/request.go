// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/extensions/azure.foundry.ai.agents/internal/pkg/agents"
)

// CreateAgentResponse represents the response from creating an agent
type CreateAgentResponse struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Status  string `json:"status"`
}

// CreateAgent creates an agent using the Azure AI Agent API
func CreateAgent(
	ctx context.Context,
	apiVersion string,
	request *agents.CreateAgentRequest,
	cred azcore.TokenCredential,
	env map[string]string) (*CreateAgentResponse, error) {
	// Get Azure token
	authToken, err := getAzureToken(ctx, cred)
	if err != nil {
		return nil, fmt.Errorf("failed to get Azure credentials: %w", err)
	}

	// Get endpoint from environment variable
	endpoint := env["AZURE_AI_PROJECT_ENDPOINT"]
	if endpoint == "" {
		return nil, fmt.Errorf("AZURE_AI_PROJECT_ENDPOINT environment variable is not set")
	}

	// Construct the full URL
	agentName := request.Name
	url := fmt.Sprintf("%s/agents/%s/versions?api-version=%s", endpoint, agentName, apiVersion)

	// Convert request to JSON
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Debug output to stderr
	fmt.Fprintf(os.Stderr, "Definition object: %+v\n", request.Definition)
	if promptDef, ok := request.Definition.(agents.PromptAgentDefinition); ok {
		fmt.Fprintf(os.Stderr, "Definition model_name: %s\n", promptDef.ModelName)
	} else if hostedDef, ok := request.Definition.(agents.HostedAgentDefinition); ok {
		fmt.Fprintf(os.Stderr, "Definition image: %s\n", hostedDef.Image)
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set headers
	httpReq.Header.Set("Authorization", "Bearer "+authToken)
	httpReq.Header.Set("Content-Type", "application/json")

	// Print request details to stderr
	fmt.Fprintf(os.Stderr, "Creating agent '%s' (ID: %s) at %s\n", request.Name, agentName, url)

	// Pretty print the payload to stderr
	var prettyPayload interface{}
	if err := json.Unmarshal(payload, &prettyPayload); err == nil {
		prettyJSON, _ := json.MarshalIndent(prettyPayload, "", "  ")
		fmt.Fprintf(os.Stderr, "Payload:\n%s\n", string(prettyJSON))
	} else {
		fmt.Fprintf(os.Stderr, "Payload: %s\n", string(payload))
	}

	// Make the HTTP request
	client := &http.Client{}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check response status
	if resp.StatusCode == 200 || resp.StatusCode == 201 {
		fmt.Fprintln(os.Stderr, "Agent created successfully!")
		fmt.Fprintln(os.Stderr, "Response:")

		// Parse the JSON response into CreateAgentResponse struct
		var agentResponse CreateAgentResponse
		if err := json.Unmarshal(body, &agentResponse); err != nil {
			return nil, fmt.Errorf("failed to parse response JSON: %w", err)
		}

		// Pretty print JSON response to stderr for user info
		var jsonResponse interface{}
		if err := json.Unmarshal(body, &jsonResponse); err == nil {
			prettyJSON, _ := json.MarshalIndent(jsonResponse, "", "  ")
			fmt.Fprintln(os.Stderr, string(prettyJSON))
		} else {
			fmt.Fprintln(os.Stderr, string(body))
		}

		// Output only the raw JSON to stdout for script consumption
		fmt.Print(string(body))
		return &agentResponse, nil
	} else {
		return nil, fmt.Errorf("failed to create agent. Status code: %d, Response: %s", resp.StatusCode, string(body))
	}
}

// getAzureToken gets an Azure access token for AI services
func getAzureToken(ctx context.Context, cred azcore.TokenCredential) (string, error) {
	// Get token for Azure AI services
	token, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://ai.azure.com/.default"},
	})
	if err != nil {
		return "", fmt.Errorf("failed to get token: %w", err)
	}

	return token.Token, nil
}

// StartAgentContainerRequest represents the request model for starting an agent container
type StartAgentContainerRequest struct {
	MinReplicas *int `json:"min_replicas,omitempty"`
	MaxReplicas *int `json:"max_replicas,omitempty"`
}

// AgentContainerObject represents the details of the container of a specific version of an agent
type AgentContainerObject struct {
	Object       string    `json:"object"`
	Status       string    `json:"status"`
	MaxReplicas  *int      `json:"max_replicas,omitempty"`
	MinReplicas  *int      `json:"min_replicas,omitempty"`
	ErrorMessage *string   `json:"error_message,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// AgentContainerOperationError represents the error details of a container operation
type AgentContainerOperationError struct {
	Code    string `json:"code"`
	Type    string `json:"type"`
	Message string `json:"message"`
}

// AgentContainerOperationObject represents the container operation for a specific version of an agent
type AgentContainerOperationObject struct {
	ID             string                        `json:"id"`
	AgentID        string                        `json:"agent_id"`
	AgentVersionID string                        `json:"agent_version_id"`
	Status         string                        `json:"status"`
	Error          *AgentContainerOperationError `json:"error,omitempty"`
	Container      *AgentContainerObject         `json:"container,omitempty"`
}

// AcceptedAgentContainerOperation represents the response for starting an agent container operation
type AcceptedAgentContainerOperation struct {
	OperationLocation string                        `json:"-"` // This comes from the header
	Body              AgentContainerOperationObject `json:",inline"`
}

// ContainerStatus represents the status of a container (legacy, kept for backward compatibility)
type ContainerStatus struct {
	Status        string                 `json:"status"`
	ReadyReplicas int                    `json:"ready_replicas"`
	TotalReplicas int                    `json:"total_replicas"`
	Details       map[string]interface{} `json:"details,omitempty"`
}

// StartAgentContainer starts a container for a specific version of a hosted agent
func StartAgentContainer(
	ctx context.Context,
	apiVersion,
	agentName,
	agentVersion string,
	minReplicas,
	maxReplicas *int,
	env map[string]string,
	cred azcore.TokenCredential) (*AcceptedAgentContainerOperation, error) {
	// Get Azure token
	authToken, err := getAzureToken(ctx, cred)
	if err != nil {
		return nil, fmt.Errorf("failed to get Azure credentials: %w", err)
	}

	// Get endpoint from environment variable
	endpoint := env["AZURE_AI_PROJECT_ENDPOINT"]
	if endpoint == "" {
		return nil, fmt.Errorf("AZURE_AI_PROJECT_ENDPOINT environment variable is not set")
	}

	// Construct the full URL for starting container
	url := fmt.Sprintf("%s/agents/%s/versions/%s/containers/default:start?api-version=%s",
		endpoint,
		agentName,
		agentVersion,
		apiVersion)

	// Create request payload
	request := StartAgentContainerRequest{
		MinReplicas: minReplicas,
		MaxReplicas: maxReplicas,
	}

	// Convert request to JSON
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal start container request: %w", err)
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set headers
	httpReq.Header.Set("Authorization", "Bearer "+authToken)
	httpReq.Header.Set("Content-Type", "application/json")

	// Print request details to stderr
	fmt.Fprintf(os.Stderr, "Starting container for agent '%s' version '%s' at %s\n", agentName, agentVersion, url)

	// Pretty print the payload to stderr
	var prettyPayload interface{}
	if err := json.Unmarshal(payload, &prettyPayload); err == nil {
		prettyJSON, _ := json.MarshalIndent(prettyPayload, "", "  ")
		fmt.Fprintf(os.Stderr, "Start Container Payload:\n%s\n", string(prettyJSON))
	} else {
		fmt.Fprintf(os.Stderr, "Start Container Payload: %s\n", string(payload))
	}

	// Make the HTTP request
	client := &http.Client{}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check response status (202 Accepted for long-running operation)
	if resp.StatusCode == 202 {
		fmt.Fprintln(os.Stderr, "Agent container start operation initiated successfully!")

		// Parse the operation response
		var operation AgentContainerOperationObject
		if err := json.Unmarshal(body, &operation); err != nil {
			return nil, fmt.Errorf("failed to parse operation response: %w", err)
		}

		// Get the Operation-Location header if present
		operationLocation := resp.Header.Get("Operation-Location")

		result := &AcceptedAgentContainerOperation{
			OperationLocation: operationLocation,
			Body:              operation,
		}

		fmt.Fprintln(os.Stderr, "Start Container Response:")

		// Pretty print JSON response to stderr for user info
		prettyJSON, _ := json.MarshalIndent(operation, "", "  ")
		fmt.Fprintln(os.Stderr, string(prettyJSON))

		if operationLocation != "" {
			fmt.Fprintf(os.Stderr, "Operation-Location: %s\n", operationLocation)
		}

		// Output only the raw JSON to stdout for script consumption
		fmt.Print(string(body))
		return result, nil
	} else {
		return nil, fmt.Errorf(
			"failed to start agent container. Status code: %d, Response: %s", resp.StatusCode, string(body))
	}
}

// WaitForOperationComplete waits for an operation to complete
func WaitForOperationComplete(
	ctx context.Context,
	apiVersion,
	agentName,
	operationID string,
	maxWaitTime time.Duration,
	env map[string]string,
	cred azcore.TokenCredential) (*AgentContainerOperationObject, error) {
	fmt.Fprintf(os.Stderr, "Waiting for operation %s to complete (max wait time: %v)...\n", operationID, maxWaitTime)

	startTime := time.Now()
	ticker := time.NewTicker(10 * time.Second) // Poll every 10 seconds
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(maxWaitTime):
			return nil, fmt.Errorf("timeout waiting for operation to complete after %v", maxWaitTime)
		case <-ticker.C:
			operation, err := CheckOperationStatus(ctx, apiVersion, agentName, operationID, env, cred)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error checking operation status: %v\n", err)
				continue
			}

			elapsed := time.Since(startTime)
			fmt.Fprintf(os.Stderr, "Operation status: %s - elapsed: %v\n",
				operation.Status, elapsed.Truncate(time.Second))

			// Check if operation completed successfully
			if operation.Status == "Succeeded" {
				fmt.Fprintf(os.Stderr, "Operation completed successfully!\n")
				if operation.Container != nil {
					fmt.Fprintf(os.Stderr, "Container status: %s\n", operation.Container.Status)
				}
				return operation, nil
			}

			// Check for error states
			if operation.Status == "Failed" {
				errorMsg := "operation failed"
				if operation.Error != nil {
					errorMsg = fmt.Sprintf("operation failed: %s - %s", operation.Error.Code, operation.Error.Message)
				}
				return nil, fmt.Errorf("%s", errorMsg)
			}

			// Continue polling for InProgress and NotStarted states
		}
	}
}

// CheckOperationStatus checks the status of a container operation
func CheckOperationStatus(
	ctx context.Context,
	apiVersion,
	agentName,
	operationID string,
	env map[string]string,
	cred azcore.TokenCredential) (*AgentContainerOperationObject, error) {
	// Get Azure token
	authToken, err := getAzureToken(ctx, cred)
	if err != nil {
		return nil, fmt.Errorf("failed to get Azure credentials: %w", err)
	}

	// Get endpoint from environment variable
	endpoint := env["AZURE_AI_PROJECT_ENDPOINT"]
	if endpoint == "" {
		return nil, fmt.Errorf("AZURE_AI_PROJECT_ENDPOINT environment variable is not set")
	}

	// Construct the full URL for checking operation status
	url := fmt.Sprintf("%s/agents/%s/operations/%s?api-version=%s", endpoint, agentName, operationID, apiVersion)

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set headers
	httpReq.Header.Set("Authorization", "Bearer "+authToken)

	// Make the HTTP request
	client := &http.Client{}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check response status
	if resp.StatusCode == 200 {
		var operation AgentContainerOperationObject
		if err := json.Unmarshal(body, &operation); err != nil {
			return nil, fmt.Errorf("failed to parse operation status response: %w", err)
		}
		return &operation, nil
	} else {
		return nil, fmt.Errorf(
			"failed to get operation status. Status code: %d, Response: %s", resp.StatusCode, string(body))
	}
}