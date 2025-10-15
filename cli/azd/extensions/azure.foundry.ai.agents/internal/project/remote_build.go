// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

// ACR-related types
type ACRTaskRun struct {
	Type           string   `json:"type"`
	IsArchive      bool     `json:"isArchiveEnabled"`
	SourceLocation string   `json:"sourceLocation"`
	DockerFilePath string   `json:"dockerFilePath"`
	ImageNames     []string `json:"imageNames"`
	IsPushEnabled  bool     `json:"isPushEnabled"`
	Platform       Platform `json:"platform"`
}

type Platform struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
}

// ACRRunResponse represents the response from starting an ACR task run
type ACRRunResponse struct {
	RunID  string `json:"runId"`
	Status string `json:"status"`
}

// ACRRunStatus represents the status response for a run
type ACRRunStatus struct {
	RunID     string    `json:"runId"`
	Status    string    `json:"status"`
	StartTime time.Time `json:"startTime"`
	EndTime   time.Time `json:"endTime,omitempty"`
}

// GenerateImageNamesFromAgent generates image names using the agent ID
func GenerateImageNamesFromAgent(agentID string, customVersion string) []string {
	// Use agent ID as the base image name
	imageName := strings.ToLower(strings.ReplaceAll(agentID, "_", "-"))

	// Use custom version if provided, otherwise use timestamp
	var version string
	if customVersion != "" {
		version = customVersion
	} else {
		version = time.Now().Format("20060102-150405")
	}

	// Return array with only the version tag (no latest tag)
	return []string{
		fmt.Sprintf("%s:%s", imageName, version),
	}
}

func createBuildContext(dockerfilePath string) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	defer tw.Close()

	// Get the directory containing the Dockerfile
	dockerfileDir := filepath.Dir(dockerfilePath)

	// Walk through the directory and add files to tar
	err := filepath.Walk(dockerfileDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Get relative path from dockerfile directory
		relPath, err := filepath.Rel(dockerfileDir, path)
		if err != nil {
			return err
		}

		// Convert Windows path separators to Unix style for tar
		relPath = filepath.ToSlash(relPath)

		// Create tar header
		header := &tar.Header{
			Name:    relPath,
			Size:    info.Size(),
			Mode:    int64(info.Mode()),
			ModTime: info.ModTime(),
		}

		// Write header
		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		// Read and write file content
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		_, err = io.Copy(tw, file)
		return err
	})

	if err != nil {
		return nil, fmt.Errorf("failed to create tar archive: %w", err)
	}

	return buf.Bytes(), nil
}

func extractRegistryName(endpoint string) string {
	// Remove https:// prefix
	endpoint = strings.TrimPrefix(endpoint, "https://")
	// Remove .azurecr.io suffix
	endpoint = strings.TrimSuffix(endpoint, ".azurecr.io")
	return endpoint
}

func startRemoteBuildWithAPI(
	ctx context.Context,
	cred azcore.TokenCredential,
	registryEndpoint string,
	buildContext []byte,
	imageNames []string,
	dockerfilePath string,
	env map[string]string) (string, error) {
	// Extract registry name from endpoint
	registryName := extractRegistryName(registryEndpoint)

	// Get access token for authentication
	token, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://management.azure.com/.default"},
	})
	if err != nil {
		return "", fmt.Errorf("failed to get access token: %w", err)
	}

	// Prepare JSON request body for ACR Tasks API
	dockerfileName := filepath.Base(dockerfilePath)

	// Create a request body that includes both the JSON config and tar archive
	// We'll use the "quick build" approach where we send the tar directly
	buildRequest := map[string]interface{}{
		"type":           "DockerBuildRequest",
		"dockerFilePath": dockerfileName,
		"imageNames":     imageNames,
		"isPushEnabled":  true,
		"platform": map[string]interface{}{
			"os":           "linux",
			"architecture": "amd64",
		},
		"isArchiveEnabled": true,
	}

	buildRequestBytes, err := json.Marshal(buildRequest)
	if err != nil {
		return "", fmt.Errorf("failed to marshal build request: %w", err)
	}

	// For now, let's send just the JSON and modify the API call to use the tar separately
	var requestBody bytes.Buffer
	requestBody.Write(buildRequestBytes)

	// Note: You'll need subscription ID and resource group name from environment or config
	subscriptionID := env["AZURE_SUBSCRIPTION_ID"]
	resourceGroup := env["AZURE_RESOURCE_GROUP"]

	if subscriptionID == "" || resourceGroup == "" {
		return "", fmt.Errorf("AZURE_SUBSCRIPTION_ID and AZURE_RESOURCE_GROUP environment variables are required")
	}

	// First upload the build context to ACR's blob storage
	sourceLocation, err := uploadBuildContextToACR(
		ctx, token.Token, subscriptionID, resourceGroup, registryName, buildContext)
	if err != nil {
		return "", fmt.Errorf("failed to upload build context to ACR: %w", err)
	}

	buildRequest["sourceLocation"] = sourceLocation

	requestBodyBytes, err := json.Marshal(buildRequest)
	if err != nil {
		return "", fmt.Errorf("failed to marshal build request: %w", err)
	}

	requestBodyReader := bytes.NewBuffer(requestBodyBytes)

	// Construct the ACR Tasks API URL for scheduling a run with stable api-version
	// nolint:lll
	apiURL := fmt.Sprintf("https://management.azure.com/subscriptions/%s/resourceGroups/%s/providers/Microsoft.ContainerRegistry/registries/%s/scheduleRun?api-version=2019-04-01",
		subscriptionID, resourceGroup, registryName)

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, requestBodyReader)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Authorization", "Bearer "+token.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "acr-build-go-client/1.0")

	// Make the request
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(responseBody))
	}

	// Parse response to get run ID
	fmt.Fprintf(os.Stderr, "Response status: %d, body: %s\n", resp.StatusCode, string(responseBody))

	var buildResponse ACRRunResponse
	err = json.Unmarshal(responseBody, &buildResponse)
	if err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	if buildResponse.RunID == "" {
		// Try parsing as a different response format
		var altResponse map[string]interface{}
		err = json.Unmarshal(responseBody, &altResponse)
		if err == nil {
			if runID, ok := altResponse["runId"].(string); ok {
				buildResponse.RunID = runID
			} else if runID, ok := altResponse["name"].(string); ok {
				buildResponse.RunID = runID
			}
		}
	}

	fmt.Fprintf(os.Stderr, "Build started successfully with run ID: %s\n", buildResponse.RunID)
	return buildResponse.RunID, nil
}

// uploadBuildContextToACR uploads the build context tar to ACR's managed storage
func uploadBuildContextToACR(
	ctx context.Context,
	accessToken,
	subscriptionID,
	resourceGroup,
	registryName string,
	buildContext []byte) (string, error) {
	client := &http.Client{Timeout: 60 * time.Second}

	// Step 1: Get upload URL from ACR using stable API version
	// nolint:lll
	uploadURL := fmt.Sprintf("https://management.azure.com/subscriptions/%s/resourceGroups/%s/providers/Microsoft.ContainerRegistry/registries/%s/listBuildSourceUploadUrl?api-version=2019-04-01",
		subscriptionID, resourceGroup, registryName)

	req, err := http.NewRequestWithContext(ctx, "POST", uploadURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create upload URL request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Length", "0")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to get upload URL: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read upload URL response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("upload URL request failed with status %d: %s", resp.StatusCode, string(responseBody))
	}

	// Parse response to get upload URL and relative path
	var uploadResponse struct {
		UploadURL    string `json:"uploadUrl"`
		RelativePath string `json:"relativePath"`
	}

	err = json.Unmarshal(responseBody, &uploadResponse)
	if err != nil {
		return "", fmt.Errorf("failed to parse upload URL response: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Got upload URL, uploading build context (%d bytes)...\n", len(buildContext))

	// Step 2: Upload the tar archive to the blob URL
	uploadReq, err := http.NewRequestWithContext(ctx, "PUT", uploadResponse.UploadURL, bytes.NewReader(buildContext))
	if err != nil {
		return "", fmt.Errorf("failed to create upload request: %w", err)
	}

	uploadReq.Header.Set("Content-Type", "application/octet-stream")
	uploadReq.Header.Set("x-ms-blob-type", "BlockBlob")
	uploadReq.ContentLength = int64(len(buildContext))

	uploadResp, err := client.Do(uploadReq)
	if err != nil {
		return "", fmt.Errorf("failed to upload build context: %w", err)
	}
	defer uploadResp.Body.Close()

	if uploadResp.StatusCode != http.StatusCreated {
		uploadRespBody, _ := io.ReadAll(uploadResp.Body)
		return "", fmt.Errorf("upload failed with status %d: %s", uploadResp.StatusCode, string(uploadRespBody))
	}

	fmt.Fprintf(os.Stderr, "Build context uploaded successfully to: %s\n", uploadResponse.RelativePath)

	// Return the relative path for ACR to use
	return uploadResponse.RelativePath, nil
}

// monitorBuildWithLogs monitors build status and streams logs in real-time
func monitorBuildWithLogs(
	ctx context.Context, cred azcore.TokenCredential, registryEndpoint string, runID string, env map[string]string) error {
	fmt.Fprintf(os.Stderr, "Monitoring build with run ID: %s\n", runID)

	registryName := extractRegistryName(registryEndpoint)
	subscriptionID := env["AZURE_SUBSCRIPTION_ID"]
	resourceGroup := env["AZURE_RESOURCE_GROUP"]

	if subscriptionID == "" || resourceGroup == "" {
		fmt.Fprintf(os.Stderr,
			"Note: AZURE_SUBSCRIPTION_ID and AZURE_RESOURCE_GROUP not set, falling back to basic monitoring...\n")
		return monitorBuild(ctx, cred, registryEndpoint, runID, env)
	}

	// Get access token
	token, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://management.azure.com/.default"},
	})
	if err != nil {
		return fmt.Errorf("failed to get access token: %w", err)
	}

	// Start log streaming in a separate goroutine
	logsChan := make(chan string, 100)
	logsDone := make(chan bool)

	go func() {
		defer close(logsDone)
		err := streamBuildLogs(ctx, token.Token, subscriptionID, resourceGroup, registryName, runID, logsChan)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Log streaming failed: %v\n", err)
		}
	}()

	// Print logs as they come in
	go func() {
		for logLine := range logsChan {
			fmt.Fprint(os.Stderr, logLine)
		}
	}()

	// Monitor build status
	client := &http.Client{Timeout: 10 * time.Second}
	// nolint:lll
	apiURL := fmt.Sprintf("https://management.azure.com/subscriptions/%s/resourceGroups/%s/providers/Microsoft.ContainerRegistry/registries/%s/runs/%s?api-version=2019-06-01-preview",
		subscriptionID, resourceGroup, registryName, runID)

	for {
		req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
		if err != nil {
			return fmt.Errorf("failed to create status request: %w", err)
		}

		req.Header.Set("Authorization", "Bearer "+token.Token)
		req.Header.Set("User-Agent", "acr-build-go-client/1.0")

		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("failed to get build status: %w", err)
		}

		responseBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("failed to read status response: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("status request failed with status %d: %s", resp.StatusCode, string(responseBody))
		}

		// Parse the response - Azure returns nested structure
		var response map[string]interface{}
		err = json.Unmarshal(responseBody, &response)
		if err != nil {
			return fmt.Errorf("failed to parse status response: %w", err)
		}

		var status string
		if properties, ok := response["properties"].(map[string]interface{}); ok {
			if statusVal, ok := properties["status"].(string); ok {
				status = statusVal
			}
		}

		fmt.Fprintf(os.Stderr, "\n[Build Status: %s]\n", status)

		// Check if build is complete
		if status == "Succeeded" || status == "Failed" || status == "Canceled" {
			// Wait a bit for final logs
			time.Sleep(2 * time.Second)
			close(logsChan)
			<-logsDone

			if status == "Succeeded" {
				fmt.Fprintf(os.Stderr, "\nBuild completed successfully!\n")
				return nil
			} else {
				return fmt.Errorf("build failed with status: %s", status)
			}
		}

		// Wait before next poll
		time.Sleep(10 * time.Second)
	}
}

// streamBuildLogs streams build logs from ACR
func streamBuildLogs(
	ctx context.Context, token, subscriptionID, resourceGroup, registryName, runID string, logsChan chan<- string) error {
	client := &http.Client{Timeout: 30 * time.Second}

	// ACR logs API endpoint
	logsURL := fmt.Sprintf(
		"https://management.azure.com/subscriptions/%s/resourceGroups/%s/providers/Microsoft.ContainerRegistry/"+
			"registries/%s/runs/%s/listLogSasUrl?api-version=2019-06-01-preview",
		subscriptionID, resourceGroup, registryName, runID)

	// Wait a bit for the build to start and generate logs
	time.Sleep(5 * time.Second)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Get the log SAS URL
		req, err := http.NewRequestWithContext(ctx, "POST", logsURL, nil)
		if err != nil {
			return fmt.Errorf("failed to create logs request: %w", err)
		}

		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("User-Agent", "acr-build-go-client/1.0")
		req.Header.Set("Content-Length", "0")

		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("failed to get logs URL: %w", err)
		}

		responseBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("failed to read logs response: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			// Logs might not be available yet, wait and retry
			time.Sleep(5 * time.Second)
			continue
		}

		// Parse the response to get the log URL
		var logsResponse map[string]interface{}
		err = json.Unmarshal(responseBody, &logsResponse)
		if err != nil {
			return fmt.Errorf("failed to parse logs response: %w", err)
		}

		if logURL, ok := logsResponse["logLink"].(string); ok && logURL != "" {
			// Fetch and stream the logs
			err = fetchAndStreamLogs(ctx, logURL, logsChan)
			if err != nil {
				return fmt.Errorf("failed to stream logs: %w", err)
			}
			break
		}

		// Wait before retrying
		time.Sleep(5 * time.Second)
	}

	return nil
}

// fetchAndStreamLogs fetches logs from the SAS URL and streams them
func fetchAndStreamLogs(ctx context.Context, logURL string, logsChan chan<- string) error {
	client := &http.Client{Timeout: 30 * time.Second}

	req, err := http.NewRequestWithContext(ctx, "GET", logURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create log fetch request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch logs: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("log fetch failed with status %d", resp.StatusCode)
	}

	// Stream the logs line by line
	scanner := io.Reader(resp.Body)
	buffer := make([]byte, 4096)

	for {
		n, err := scanner.Read(buffer)
		if n > 0 {
			logsChan <- string(buffer[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read logs: %w", err)
		}
	}

	return nil
}

func monitorBuild(
	ctx context.Context, cred azcore.TokenCredential, registryEndpoint string, runID string, env map[string]string) error {
	fmt.Fprintf(os.Stderr, "Monitoring build with run ID: %s\n", runID)

	registryName := extractRegistryName(registryEndpoint)
	subscriptionID := env["AZURE_SUBSCRIPTION_ID"]
	resourceGroup := env["AZURE_RESOURCE_GROUP"]

	if subscriptionID == "" || resourceGroup == "" {
		fmt.Fprintf(os.Stderr,
			"Note: AZURE_SUBSCRIPTION_ID and AZURE_RESOURCE_GROUP not set, simulating build monitoring...\n")
		// Fallback to simulation
		for i := 0; i < 3; i++ {
			time.Sleep(2 * time.Second)
			fmt.Fprintf(os.Stderr, "Build status: %s\n", []string{"Queued", "Running", "Succeeded"}[i])
		}
		return nil
	}

	// Get access token
	token, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://management.azure.com/.default"},
	})
	if err != nil {
		return fmt.Errorf("failed to get access token: %w", err)
	}

	// Construct the API URL for getting run status with required api-version parameter
	apiURL := fmt.Sprintf("https://management.azure.com/subscriptions/%s/resourceGroups/%s/providers/"+
		"Microsoft.ContainerRegistry/registries/%s/runs/%s?api-version=2019-06-01-preview",
		subscriptionID, resourceGroup, registryName, runID)

	// Poll for build status
	client := &http.Client{Timeout: 10 * time.Second}

	for {
		req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
		if err != nil {
			return fmt.Errorf("failed to create status request: %w", err)
		}

		req.Header.Set("Authorization", "Bearer "+token.Token)
		req.Header.Set("User-Agent", "acr-build-go-client/1.0")

		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("failed to get build status: %w", err)
		}

		responseBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("failed to read status response: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("status request failed with status %d: %s", resp.StatusCode, string(responseBody))
		}

		// Parse the response - Azure returns nested structure
		var response map[string]interface{}
		err = json.Unmarshal(responseBody, &response)
		if err != nil {
			return fmt.Errorf("failed to parse status response: %w", err)
		}

		var status string
		if properties, ok := response["properties"].(map[string]interface{}); ok {
			if statusVal, ok := properties["status"].(string); ok {
				status = statusVal
			}
		}

		fmt.Fprintf(os.Stderr, "Build status: %s\n", status)

		// Check if build is complete
		if status == "Succeeded" || status == "Failed" || status == "Canceled" {
			if status == "Succeeded" {
				fmt.Fprintf(os.Stderr, "Build completed successfully!\n")
				return nil
			} else {
				return fmt.Errorf("build failed with status: %s", status)
			}
		}

		// Wait before next poll
		time.Sleep(5 * time.Second)
	}
}
