// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build ghCopilot

package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/openai"
)

// GitHubCopilotModelConfig holds configuration settings for GitHub Copilot models
type GitHubCopilotModelConfig struct {
	Model    string `json:"model"`
	Endpoint string `json:"endpoint"`
	Token    string `json:"token"`
}

// GitHubCopilotModelProvider creates GitHub Copilot models from user configuration
type GitHubCopilotModelProvider struct {
	userConfigManager config.UserConfigManager
	console           input.Console
}

// NewGitHubCopilotModelProvider creates a new GitHub Copilot model provider
func NewGitHubCopilotModelProvider(userConfigManager config.UserConfigManager, console input.Console) ModelProvider {
	return &GitHubCopilotModelProvider{
		userConfigManager: userConfigManager,
		console:           console,
	}
}

const (
	githubCopilotApi  = "https://api.githubcopilot.com"
	tokenCachePath    = "gh-cp"
	ghTokenFileName   = "gh"
	ghCopilotFileName = "cp"
	scope             = "read:user"
)

// copilotIntegrationID is set at compile time using -ldflags
// This file is only included when built with -tags ghCopilot
// Example: go build -tags ghCopilot -ldflags "-X github.com/azure/azure-dev/cli/azd/pkg/llm.copilotIntegrationID=azd-cli -X github.com/azure/azure-dev/cli/azd/pkg/llm.clientID=Iv1.b507a08c87ecfe98"
var copilotIntegrationID = mustSetCopilotIntegrationID

// clientID is set at compile time using -ldflags
// This must be provided along with copilotIntegrationID when using -tags ghCopilot
var clientID = mustSetClientID

// mustSetCopilotIntegrationID is a placeholder that will cause a compile error
// if copilotIntegrationID is not overridden via ldflags
// The ldflags will replace this entire variable, so this value should never be used
const mustSetCopilotIntegrationID = "COPILOT_INTEGRATION_ID_NOT_SET_VIA_LDFLAGS_BUILD_WILL_FAIL"

// mustSetClientID is a placeholder that will cause a compile error
// if clientID is not overridden via ldflags
// The ldflags will replace this entire variable, so this value should never be used
const mustSetClientID = "CLIENT_ID_NOT_SET_VIA_LDFLAGS_BUILD_WILL_FAIL"

func init() {
	// This check ensures that if someone tries to use this without proper ldflags,
	// it will fail immediately with a clear error message
	// This is effectively a "compile-time" check from a developer experience perspective
	// because the program fails immediately on startup

	integrationIDMissing := copilotIntegrationID == mustSetCopilotIntegrationID
	clientIDMissing := clientID == mustSetClientID

	if integrationIDMissing || clientIDMissing {
		var missingParams []string
		if integrationIDMissing {
			missingParams = append(missingParams, "copilotIntegrationID")
		}
		if clientIDMissing {
			missingParams = append(missingParams, "clientID")
		}

		log.Fatalf("\n"+
			"===============================================================================\n"+
			"BUILD ERROR: GitHub Copilot parameters not set during compilation!\n"+
			"===============================================================================\n"+
			"Missing parameters: %s\n"+
			"\n"+
			"When using -tags ghCopilot, you MUST provide both parameters via ldflags:\n"+
			"\n"+
			"With environment variables (recommended):\n"+
			"  export COPILOT_INTEGRATION_ID=\"your-integration-id\"\n"+
			"  export COPILOT_CLIENT_ID=\"your-client-id\"\n"+
			"  go build -tags ghCopilot -ldflags \"-X github.com/azure/azure-dev/cli/azd/pkg/llm.copilotIntegrationID=$COPILOT_INTEGRATION_ID -X github.com/azure/azure-dev/cli/azd/pkg/llm.clientID=$COPILOT_CLIENT_ID\"\n"+
			"\n"+
			"Or with direct values:\n"+
			"  go build -tags ghCopilot -ldflags \"-X github.com/azure/azure-dev/cli/azd/pkg/llm.copilotIntegrationID=your-integration-id -X github.com/azure/azure-dev/cli/azd/pkg/llm.clientID=your-client-id\"\n"+
			"===============================================================================",
			strings.Join(missingParams, ", "))
	}
}

// CreateModelContainer creates a model container for GitHub Copilot with configuration
// loaded from user settings. It validates required fields and applies optional parameters
// like temperature and max tokens before creating the GitHub Copilot client.
func (p *GitHubCopilotModelProvider) CreateModelContainer(
	ctx context.Context, opts ...ModelOption) (*ModelContainer, error) {
	// GitHub Copilot integration is enabled - copilotIntegrationID is set at compile time

	modelContainer := &ModelContainer{
		Type:    LlmTypeGhCp,
		IsLocal: false,
		Url:     githubCopilotApi,
	}

	for _, opt := range opts {
		opt(modelContainer)
	}

	tokenData, err := copilotToken(ctx, p.console)
	if err != nil {
		return nil, err
	}

	ghCpModel, err := openai.New(
		openai.WithToken(tokenData.Token),
		openai.WithBaseURL(githubCopilotApi),
		openai.WithAPIType(openai.APITypeOpenAI), // GitHub Copilot uses the OpenAI API type
		openai.WithModel("gpt-4"),
		openai.WithHTTPClient(&httpClient{}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM: %w", err)
	}

	callOptions := []llms.CallOption{}
	ghCpModel.CallbacksHandler = modelContainer.logger
	callOptions = append(callOptions, llms.WithTemperature(1.0))
	modelContainer.Model = newModelWithCallOptions(ghCpModel, callOptions...)

	return modelContainer, nil
}

type httpClient struct {
}

func (c *httpClient) Do(req *http.Request) (*http.Response, error) {
	req.Header.Set("copilot-integration-id", copilotIntegrationID)
	return http.DefaultClient.Do(req)
}

// loadGitHubToken loads the saved GitHub access token
func loadGitHubToken() (string, error) {
	configDir, err := config.GetUserConfigDir()
	if err != nil {
		return "", err
	}
	tokenFile := filepath.Join(configDir, tokenCachePath, ghTokenFileName)
	var token string
	err = loadFromFile(tokenFile, &token)
	return token, err
}

// loadCopilotToken loads the saved Copilot session token
func loadCopilotToken() (tokenData, error) {
	configDir, err := config.GetUserConfigDir()
	if err != nil {
		return tokenData{}, err
	}
	tokenFile := filepath.Join(configDir, tokenCachePath, ghCopilotFileName)
	var tokenData tokenData
	err = loadFromFile(tokenFile, &tokenData)
	return tokenData, err
}

// TokenData represents the structure of GitHub Token
type tokenData struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
}

// saveAsFile saves content as file in the user config directory
func saveAsFile(content any, name string) error {
	configDir, err := config.GetUserConfigDir()
	if err != nil {
		return err
	}
	tokenFile := filepath.Join(configDir, tokenCachePath, name)
	return saveToFile(tokenFile, content)
}

// saveToFile saves data to a file
func saveToFile(filePath string, data interface{}) error {
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, osutil.PermissionDirectory); err != nil {
		return err
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return err
	}

	return os.WriteFile(filePath, jsonData, 0600)
}

// loadFromFile loads JSON data from a file into the provided data structure
func loadFromFile(filePath string, data interface{}) error {
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return err
	}

	jsonData, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	return json.Unmarshal(jsonData, data)
}

// copilotToken ensures a valid Copilot token is available, performing authentication if necessary
func copilotToken(ctx context.Context, console input.Console) (*tokenData, error) {
	// Try to load existing GitHub token
	githubToken, err := loadGitHubToken()

	// If no GitHub token, perform device flow
	if err != nil {
		githubToken, err = deviceCodeFlow(ctx, console)
		if err != nil {
			return nil, fmt.Errorf("failed to authenticate with GitHub: %w", err)
		}

		// Save the GitHub token
		if err := saveAsFile(githubToken, ghTokenFileName); err != nil {
			// not a fatal error if saving fails
			log.Println("Warning: failed to save GitHub token:", err)
		}
	}

	// Try to load existing Copilot token
	copilotToken, err := loadCopilotToken()

	// If token exists and not expired, return it
	if err == nil && !isTokenExpired(copilotToken.ExpiresAt) {
		return &copilotToken, nil
	}

	// Token is missing or expired, get a new one
	newToken, err := newCopilotToken(githubToken)
	if err != nil {
		// If Copilot token request fails, GitHub token might be expired
		if strings.Contains(err.Error(), "status 401") || strings.Contains(err.Error(), "status 403") {
			// Clear the expired GitHub token
			cPath, err := config.GetUserConfigDir()
			if err != nil {
				return nil, fmt.Errorf("failed to get user config directory: %w", err)
			}
			os.Remove(filepath.Join(cPath, tokenCachePath, ghTokenFileName))

			// Get a new GitHub token
			githubToken, err = deviceCodeFlow(ctx, console)
			if err != nil {
				return nil, fmt.Errorf("failed to re-authenticate with GitHub: %w", err)
			}

			// Save the new GitHub token
			if err := saveAsFile(githubToken, ghTokenFileName); err != nil {
				log.Printf("Warning: failed to save GitHub token: %v\n", err)
			}

			// Try getting Copilot token again with new GitHub token
			newToken, err = newCopilotToken(githubToken)
			if err != nil {
				return nil, fmt.Errorf("failed to get Copilot token after re-authentication: %w", err)
			}
		} else {
			return nil, fmt.Errorf("failed to get Copilot token: %w", err)
		}
	}

	// Save the new Copilot token
	if err := saveAsFile(*newToken, ghCopilotFileName); err != nil {
		log.Printf("Warning: failed to save Copilot token: %v\n", err)
	}

	return newToken, nil
}

// deviceCodeFlow performs the GitHub device code authentication flow
func deviceCodeFlow(ctx context.Context, console input.Console) (string, error) {
	// Step 1: Request device and user codes
	data := url.Values{}
	data.Set("client_id", clientID)
	data.Set("scope", scope)

	resp, err := http.Post("https://github.com/login/device/code",
		"application/x-www-form-urlencoded",
		strings.NewReader(data.Encode()))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	// Parse the form-encoded response
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return "", err
	}

	deviceCode := values.Get("device_code")
	userCode := values.Get("user_code")
	verificationURI := values.Get("verification_uri")
	intervalStr := values.Get("interval")
	intervalSec, _ := strconv.Atoi(intervalStr)

	console.Message(ctx, fmt.Sprintf("\nGo to %s and enter code: %s\n", verificationURI, userCode))
	console.ShowSpinner(ctx, "Waiting for GitHub authorization...", input.Step)
	defer console.StopSpinner(ctx, "", input.Step)

	// Step 2: Poll for access token
	for {
		time.Sleep(time.Duration(intervalSec) * time.Second)

		tokenData := url.Values{}
		tokenData.Set("client_id", clientID)
		tokenData.Set("device_code", deviceCode)
		tokenData.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")

		resp, err := http.Post("https://github.com/login/oauth/access_token",
			"application/x-www-form-urlencoded",
			strings.NewReader(tokenData.Encode()))
		if err != nil {
			return "", err
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		// Parse the form-encoded token response
		tokenValues, err := url.ParseQuery(string(body))
		if err != nil {
			log.Printf("Error parsing token response: %v\n", err)
			continue
		}

		if token := tokenValues.Get("access_token"); token != "" {
			log.Println("GitHub authentication successful!")
			return token, nil
		}

		if errDesc := tokenValues.Get("error"); errDesc != "" {
			if errDesc == "authorization_pending" {
				continue
			}
			if errDesc == "expired_token" || errDesc == "access_denied" {
				return "", fmt.Errorf("authorization failed: %s", errDesc)
			}
		}
	}
}

// isTokenExpired checks if the token is expired (with 5 minute buffer)
// The buffer helps avoid using a token that is about to expire
func isTokenExpired(expiresAt int64) bool {
	if expiresAt == 0 {
		return true
	}
	now := time.Now().Unix()
	buffer := int64(300) // 5 minutes buffer
	return now >= (expiresAt - buffer)
}

// newCopilotToken gets a Copilot session token using the GitHub token
func newCopilotToken(githubToken string) (*tokenData, error) {
	client := &http.Client{}
	req, err := http.NewRequest("GET", "https://api.github.com/copilot_internal/v2/token", nil)
	if err != nil {
		return nil, err
	}

	// Set headers to mimic an approved Copilot client
	req.Header.Set("Authorization", "token "+githubToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Azd/1.17.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("copilot API error (status %d): %s", resp.StatusCode, string(body))
	}

	var copilotResp map[string]interface{}
	json.Unmarshal(body, &copilotResp)

	token, tokenOk := copilotResp["token"].(string)
	expiresAt, expiresOk := copilotResp["expires_at"].(float64)

	if !tokenOk || token == "" {
		return nil, fmt.Errorf("no token in response: %s", string(body))
	}

	tokenData := &tokenData{
		Token:     token,
		ExpiresAt: int64(expiresAt),
	}

	if expiresOk {
		fmt.Printf("✅ Copilot token expires at: %s\n", time.Unix(int64(expiresAt), 0).Format(time.RFC3339))
	}

	return tokenData, nil
}
