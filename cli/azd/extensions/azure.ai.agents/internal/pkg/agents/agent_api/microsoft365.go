// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
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
//
// The request goes through the shared client pipeline (c.pipeline) so it inherits
// the same bearer-token, retry, and correlation policies as every other agent
// data-plane call; a transient 429/5xx is retried rather than immediately falling
// back to the manual guide. A per-call timeout bounds the total wait so a hung
// service can never block deploy.
func (c *AgentClient) DownloadTeamsAppPackage(
	ctx context.Context,
	agentName string,
	request TeamsAppPackageRequest,
	apiVersion string,
) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	url := fmt.Sprintf("%s/agents/%s/microsoft365/zip?api-version=%s", c.endpoint, agentName, apiVersion)

	req, err := runtime.NewRequest(ctx, http.MethodPost, url)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	if err := runtime.MarshalAsJSON(req, request); err != nil {
		return nil, fmt.Errorf("failed to marshal Teams app package request: %w", err)
	}
	req.Raw().Header.Set("Accept", "application/zip")
	req.Raw().Header.Set("Foundry-Features", "HostedAgents=V1Preview,AgentEndpoints=V1Preview")

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if !runtime.HasStatusCode(resp, http.StatusOK) {
		return nil, runtime.NewResponseError(resp)
	}

	zipBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Teams app package response: %w", err)
	}
	if len(zipBytes) == 0 {
		return nil, fmt.Errorf("Teams app package response was empty")
	}
	return zipBytes, nil
}

// TeamsAppPublishResult is the response of the Microsoft 365 "publish" endpoint.
// The V3 publish surface returns only the MOS title id and the generated Teams app
// id; the caller builds the install link from TitleID because shared-scope
// distribution is acquired through the MOS title before Teams can open the app id.
type TeamsAppPublishResult struct {
	// TitleID is the MOS catalog title id for the published app.
	TitleID string `json:"titleId"`
	// TeamsAppID is the Teams app (external) id of the published custom engine agent.
	TeamsAppID string `json:"teamsAppId"`
}

// PublishTeamsApp calls the Microsoft 365 "publish" endpoint. Unlike the "zip"
// endpoint (which returns the package to the caller), the service rebuilds the same
// package internally and publishes it to the Microsoft Organization Store (MOS)
// under the requested scope, returning the resulting title id and Teams app id.
//
// The request body is the same TeamsAppPackageRequest used by the "zip" endpoint;
// only PublishScope drives the publish behavior. The call goes through the shared
// client pipeline so it inherits the same bearer-token, retry, and correlation
// policies as every other agent data-plane call, and a per-call timeout bounds the
// total wait. Any non-2xx response is returned as an error (via runtime.ResponseError)
// so the caller can surface the failure — publishing is an explicit user action, not
// a best-effort side effect, so tenant/permission failures are reported, not swallowed.
func (c *AgentClient) PublishTeamsApp(
	ctx context.Context,
	agentName string,
	request TeamsAppPackageRequest,
	apiVersion string,
) (*TeamsAppPublishResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	url := fmt.Sprintf("%s/agents/%s/microsoft365/publish?api-version=%s", c.endpoint, agentName, apiVersion)

	req, err := runtime.NewRequest(ctx, http.MethodPost, url)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	if err := runtime.MarshalAsJSON(req, request); err != nil {
		return nil, fmt.Errorf("failed to marshal Teams app publish request: %w", err)
	}
	req.Raw().Header.Set("Foundry-Features", "HostedAgents=V1Preview,AgentEndpoints=V1Preview")

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
		return nil, fmt.Errorf("failed to read Teams app publish response: %w", err)
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("Teams app publish response was empty")
	}

	var result TeamsAppPublishResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse Teams app publish response: %w", err)
	}
	if result.TitleID == "" {
		return nil, fmt.Errorf("Teams app publish response was missing titleId")
	}
	if result.TeamsAppID == "" {
		return nil, fmt.Errorf("Teams app publish response was missing teamsAppId")
	}
	return &result, nil
}
