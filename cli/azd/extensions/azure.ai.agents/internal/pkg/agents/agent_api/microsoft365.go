// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"azureaiagent/internal/version"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

// Microsoft365APIVersion is the API version for the Microsoft 365 packaging
// endpoints. These endpoints are versioned independently of the agent data-plane
// APIs and currently expose only "v1".
const Microsoft365APIVersion = "v1"

// TeamsAppPackageRequest is the body for the Microsoft 365 "zip" endpoint. The
// agent is resolved server-side from the agent name in the route, so the body
// only carries display metadata and the publish scope. Field casing matches the
// server contract (Microsoft365PublishRequestV3).
//
// For the simple activity-agent case the package is a custom engine agent backed
// by the agent's own instance identity, so PublishAsAutopilot and
// UseAgenticUserTemplate are false. PublishScope "Personal" produces a package
// intended for per-user sideload (no Teams admin required).
type TeamsAppPackageRequest struct {
	PublishAsAutopilot       bool   `json:"PublishAsAutopilot"`
	BotServiceArmID          string `json:"BotServiceArmId"`
	UseAgenticUserTemplate   bool   `json:"useAgenticUserTemplate"`
	PublishScope             string `json:"PublishScope"`
	AgentDisplayName         string `json:"AgentDisplayName"`
	AppVersion               string `json:"AppVersion"`
	ShortDescription         string `json:"ShortDescription"`
	FullDescription          string `json:"FullDescription"`
	DeveloperName            string `json:"DeveloperName"`
	DeveloperWebsiteURL      string `json:"DeveloperWebsiteUrl"`
	PrivacyURL               string `json:"PrivacyUrl"`
	TermsOfUseURL            string `json:"TermsOfUseUrl"`
	CanRespondWithoutMention bool   `json:"CanRespondWithoutMention"`
}

// DownloadTeamsAppPackage calls the Microsoft 365 "zip" endpoint and returns the
// bytes of a ready-to-sideload Teams app package (.zip) built by the service:
// manifest, icons, and a bot entry whose botId is the agent's instance identity.
// No manifest or icon assembly happens client-side.
//
// It is best-effort at the call site: the endpoint requires an APIM-routed user
// token, so the returned error is surfaced to the caller to decide whether to
// fall back to the manual packaging guide.
func (c *AgentClient) DownloadTeamsAppPackage(
	ctx context.Context,
	agentName string,
	request TeamsAppPackageRequest,
	apiVersion string,
) ([]byte, error) {
	u, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint URL: %w", err)
	}
	u.Path += fmt.Sprintf("/agents/%s/microsoft365/zip", agentName)

	query := u.Query()
	query.Set("api-version", apiVersion)
	u.RawQuery = query.Encode()

	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal Teams app package request: %w", err)
	}

	token, err := c.credential.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://ai.azure.com/.default"},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get auth token: %w", err)
	}

	requestCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	//nolint:gosec // request URL is built from trusted SDK endpoint + path components
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/zip")
	req.Header.Set("Foundry-Features", "HostedAgents=V1Preview,AgentEndpoints=V1Preview")
	req.Header.Set("User-Agent", fmt.Sprintf("azd-ext-azure-ai-agents/%s", version.Version))

	httpClient := &http.Client{}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("unexpected status code: %d — %s", resp.StatusCode, string(errBody))
	}

	zipBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Teams app package response: %w", err)
	}
	return zipBytes, nil
}
